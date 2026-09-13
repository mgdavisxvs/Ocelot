package tracker

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultConfig returns the built-in configuration. Callers should layer a
// config file and environment variables on top with LoadConfig.
func DefaultConfig() *Config {
	return &Config{
		ListenAddr:       ":34000",
		AnnounceInterval: 1800,
		PeersTimeout:     7200,
		ReapInterval:     1800,
		MaxMiddlemen:     20000,
		NumWantLimit:     50,
		KeepaliveTimeout: 0,
		ReadTimeout:      30 * time.Second,
		WriteTimeout:     30 * time.Second,

		MetricsAddr:        ":9090",
		TLSAddr:            ":34443",
		RateLimitRPS:       5,
		RateLimitBurst:     20,
		AuditRetentionDays: 90,
	}
}

// placeholderPasswords are the values shipped in ocelot.conf.dist and in the
// old hardcoded defaults. Running with any of them leaves the admin API open.
var placeholderPasswords = map[string]bool{
	"":                                 true,
	"changeme":                         true,
	"00000000000000000000000000000000": true,
}

// LoadConfig builds a configuration from defaults, then the file at path if it
// exists, then OCELOT_-prefixed environment variables. A missing file is not an
// error: it leaves the defaults in place.
//
// The file format matches the original ocelot.conf: "key = value" lines, with
// '#' starting a comment only at the beginning of a line.
func LoadConfig(path string) (*Config, error) {
	config := DefaultConfig()

	settings, err := readConfigFile(path)
	if err != nil {
		return nil, err
	}

	applyEnvOverrides(settings)

	if err := applySettings(config, settings); err != nil {
		return nil, err
	}

	return config, nil
}

// readConfigFile parses path into a key/value map. A missing file yields an
// empty map so that defaults and environment variables still apply.
func readConfigFile(path string) (map[string]string, error) {
	settings := make(map[string]string)

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return settings, nil
		}
		return nil, fmt.Errorf("failed to open config %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		key, value, found := strings.Cut(text, "=")
		if !found {
			return nil, fmt.Errorf("%s:%d: expected 'key = value', got %q", path, line, text)
		}

		settings[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}

	return settings, nil
}

// configKeys are every recognised setting. Anything else in the file is
// ignored, which keeps old MySQL-era keys from breaking startup.
var configKeys = []string{
	"listen_port",
	"announce_interval",
	"peers_timeout",
	"reap_peers_interval",
	"max_middlemen",
	"numwant_limit",
	"keepalive_timeout",
	"connection_timeout",
	"site_password",
	"report_password",
	"metrics_addr",
	"rate_limit_rps",
	"rate_limit_burst",
	"audit_retention_days",
	"tls_cert_file",
	"tls_key_file",
	"tls_addr",
}

// applyEnvOverrides lets OCELOT_SITE_PASSWORD override site_password, and so
// on, so deployments can set secrets without writing a config file.
func applyEnvOverrides(settings map[string]string) {
	for _, key := range configKeys {
		if value, ok := os.LookupEnv("OCELOT_" + strings.ToUpper(key)); ok {
			settings[key] = value
		}
	}
}

func applySettings(config *Config, settings map[string]string) error {
	ints := map[string]*int{
		"announce_interval":    &config.AnnounceInterval,
		"peers_timeout":        &config.PeersTimeout,
		"reap_peers_interval":  &config.ReapInterval,
		"max_middlemen":        &config.MaxMiddlemen,
		"numwant_limit":        &config.NumWantLimit,
		"rate_limit_rps":       &config.RateLimitRPS,
		"rate_limit_burst":     &config.RateLimitBurst,
		"audit_retention_days": &config.AuditRetentionDays,
	}

	for key, target := range ints {
		value, ok := settings[key]
		if !ok {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("config %s: expected an integer, got %q", key, value)
		}
		*target = parsed
	}

	if value, ok := settings["listen_port"]; ok {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("config listen_port: expected a port in 1-65535, got %q", value)
		}
		config.ListenAddr = fmt.Sprintf(":%d", port)
	}

	seconds := map[string]*time.Duration{
		"keepalive_timeout": &config.KeepaliveTimeout,
	}
	for key, target := range seconds {
		value, ok := settings[key]
		if !ok {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("config %s: expected seconds, got %q", key, value)
		}
		*target = time.Duration(parsed) * time.Second
	}

	// connection_timeout covers both directions in the original config.
	if value, ok := settings["connection_timeout"]; ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("config connection_timeout: expected seconds, got %q", value)
		}
		config.ReadTimeout = time.Duration(parsed) * time.Second
		config.WriteTimeout = time.Duration(parsed) * time.Second
	}

	strs := map[string]*string{
		"site_password":   &config.SitePassword,
		"report_password": &config.ReportPassword,
		"metrics_addr":    &config.MetricsAddr,
		"tls_cert_file":   &config.TLSCertFile,
		"tls_key_file":    &config.TLSKeyFile,
		"tls_addr":        &config.TLSAddr,
	}
	for key, target := range strs {
		if value, ok := settings[key]; ok {
			*target = value
		}
	}

	return config.Validate()
}

// Validate rejects configurations that would start an insecure or unusable
// tracker. It returns an error only for values that cannot work; unsafe but
// functional values are reported by InsecureWarnings.
func (c *Config) Validate() error {
	if c.NumWantLimit <= 0 {
		return fmt.Errorf("config numwant_limit must be positive, got %d", c.NumWantLimit)
	}
	if c.AnnounceInterval <= 0 {
		return fmt.Errorf("config announce_interval must be positive, got %d", c.AnnounceInterval)
	}
	if c.PeersTimeout <= 0 {
		return fmt.Errorf("config peers_timeout must be positive, got %d", c.PeersTimeout)
	}
	if c.MaxMiddlemen <= 0 {
		return fmt.Errorf("config max_middlemen must be positive, got %d", c.MaxMiddlemen)
	}

	// A reap interval at or above the timeout lets dead peers linger for up to
	// two full timeouts before collection.
	if c.ReapInterval <= 0 {
		return fmt.Errorf("config reap_peers_interval must be positive, got %d", c.ReapInterval)
	}
	if c.ReapInterval > c.PeersTimeout {
		return fmt.Errorf("config reap_peers_interval (%d) must not exceed peers_timeout (%d)",
			c.ReapInterval, c.PeersTimeout)
	}

	// A half-configured keypair is always a mistake: it silently serves
	// plaintext only.
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return fmt.Errorf("config: tls_cert_file and tls_key_file must be set together")
	}
	if c.TLSEnabled() && c.TLSAddr == "" {
		return fmt.Errorf("config: tls_addr is required when TLS is enabled")
	}

	return nil
}

// InsecureWarnings lists settings that are usable but unsafe in production, so
// the caller can refuse to start or log loudly.
func (c *Config) InsecureWarnings() []string {
	var warnings []string

	if placeholderPasswords[c.SitePassword] {
		warnings = append(warnings,
			"site_password is unset or still the placeholder: the admin API "+
				"(/update, /stats, /torrents, /peers, /whitelist) is effectively open")
	}
	if placeholderPasswords[c.ReportPassword] {
		warnings = append(warnings, "report_password is unset or still the placeholder")
	}
	if !c.TLSEnabled() {
		warnings = append(warnings,
			"TLS is not configured: passkeys travel in the request path, so every "+
				"announce sends a credential in cleartext unless TLS terminates upstream")
	}

	return warnings
}
