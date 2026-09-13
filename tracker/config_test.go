package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ocelot.conf")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write config fixture: %v", err)
	}
	return path
}

func TestLoadConfigMissingFileUsesDefaults(t *testing.T) {
	config, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.conf"))
	if err != nil {
		t.Fatalf("a missing config should not be an error: %v", err)
	}

	defaults := DefaultConfig()
	if config.ListenAddr != defaults.ListenAddr {
		t.Errorf("ListenAddr = %q, want %q", config.ListenAddr, defaults.ListenAddr)
	}
	if config.NumWantLimit != defaults.NumWantLimit {
		t.Errorf("NumWantLimit = %d, want %d", config.NumWantLimit, defaults.NumWantLimit)
	}
}

func TestLoadConfigParsesDistributedFormat(t *testing.T) {
	// The shape shipped as ocelot.conf.dist, including legacy MySQL keys that
	// must be ignored rather than rejected.
	path := writeConfig(t, `# Ocelot config file
# Lines starting with a # are ignored

listen_port         = 8080
max_middlemen       = 5000
keepalive_timeout   = 30
connection_timeout  = 15

announce_interval   = 900
numwant_limit       = 25

mysql_host          =
mysql_username      =

site_password       = 0123456789abcdef0123456789abcdef
report_password     = fedcba9876543210fedcba9876543210

peers_timeout       = 3600
reap_peers_interval = 600
`)

	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if config.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", config.ListenAddr)
	}
	if config.MaxMiddlemen != 5000 {
		t.Errorf("MaxMiddlemen = %d, want 5000", config.MaxMiddlemen)
	}
	if config.KeepaliveTimeout != 30*time.Second {
		t.Errorf("KeepaliveTimeout = %v, want 30s", config.KeepaliveTimeout)
	}
	if config.ReadTimeout != 15*time.Second || config.WriteTimeout != 15*time.Second {
		t.Errorf("connection_timeout should set both read (%v) and write (%v) to 15s",
			config.ReadTimeout, config.WriteTimeout)
	}
	if config.AnnounceInterval != 900 {
		t.Errorf("AnnounceInterval = %d, want 900", config.AnnounceInterval)
	}
	if config.NumWantLimit != 25 {
		t.Errorf("NumWantLimit = %d, want 25", config.NumWantLimit)
	}
	if config.PeersTimeout != 3600 {
		t.Errorf("PeersTimeout = %d, want 3600", config.PeersTimeout)
	}
	if config.ReapInterval != 600 {
		t.Errorf("ReapInterval = %d, want 600", config.ReapInterval)
	}
	if config.SitePassword != "0123456789abcdef0123456789abcdef" {
		t.Errorf("SitePassword = %q", config.SitePassword)
	}
}

func TestLoadConfigIgnoresCommentsAndBlankLines(t *testing.T) {
	path := writeConfig(t, `
# a comment
   # an indented comment

numwant_limit = 10

`)

	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if config.NumWantLimit != 10 {
		t.Errorf("NumWantLimit = %d, want 10", config.NumWantLimit)
	}
}

func TestLoadConfigKeepsHashInsideValues(t *testing.T) {
	// ocelot.conf.dist states a '#' elsewhere on a line is an ordinary
	// character, so it must not be stripped from a password.
	path := writeConfig(t, "site_password = abc#def\n")

	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if config.SitePassword != "abc#def" {
		t.Errorf("SitePassword = %q, want %q", config.SitePassword, "abc#def")
	}
}

func TestLoadConfigRejectsMalformedLine(t *testing.T) {
	path := writeConfig(t, "numwant_limit 50\n")

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected an error for a line without '='")
	}
	if !strings.Contains(err.Error(), "key = value") {
		t.Errorf("error = %q, want it to explain the expected format", err)
	}
}

func TestLoadConfigRejectsNonNumericValue(t *testing.T) {
	path := writeConfig(t, "numwant_limit = many\n")

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected an error for a non-numeric integer setting")
	}
	if !strings.Contains(err.Error(), "numwant_limit") {
		t.Errorf("error = %q, want it to name the offending key", err)
	}
}

func TestLoadConfigRejectsBadPort(t *testing.T) {
	for _, value := range []string{"0", "70000", "http"} {
		t.Run(value, func(t *testing.T) {
			path := writeConfig(t, "listen_port = "+value+"\n")
			if _, err := LoadConfig(path); err == nil {
				t.Errorf("expected listen_port = %q to be rejected", value)
			}
		})
	}
}

func TestLoadConfigEnvOverridesFile(t *testing.T) {
	path := writeConfig(t, `site_password = from-file
numwant_limit = 10
`)

	t.Setenv("OCELOT_SITE_PASSWORD", "from-env")
	t.Setenv("OCELOT_NUMWANT_LIMIT", "77")

	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if config.SitePassword != "from-env" {
		t.Errorf("SitePassword = %q, want the environment value", config.SitePassword)
	}
	if config.NumWantLimit != 77 {
		t.Errorf("NumWantLimit = %d, want 77", config.NumWantLimit)
	}
}

func TestLoadConfigEnvAppliesWithoutFile(t *testing.T) {
	t.Setenv("OCELOT_LISTEN_PORT", "9999")

	config, err := LoadConfig(filepath.Join(t.TempDir(), "absent.conf"))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if config.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want :9999", config.ListenAddr)
	}
}

func TestConfigValidateRejectsUnusableValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"zero numwant", func(c *Config) { c.NumWantLimit = 0 }, "numwant_limit"},
		{"zero announce interval", func(c *Config) { c.AnnounceInterval = 0 }, "announce_interval"},
		{"zero peers timeout", func(c *Config) { c.PeersTimeout = 0 }, "peers_timeout"},
		{"zero max middlemen", func(c *Config) { c.MaxMiddlemen = 0 }, "max_middlemen"},
		{"zero reap interval", func(c *Config) { c.ReapInterval = 0 }, "reap_peers_interval"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			tt.mutate(config)

			err := config.Validate()
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestConfigValidateRejectsReapSlowerThanTimeout(t *testing.T) {
	// Sweeping less often than the timeout lets dead peers linger for up to
	// two full timeouts.
	config := DefaultConfig()
	config.PeersTimeout = 600
	config.ReapInterval = 1800

	err := config.Validate()
	if err == nil {
		t.Fatal("expected reap_peers_interval > peers_timeout to be rejected")
	}
	if !strings.Contains(err.Error(), "reap_peers_interval") {
		t.Errorf("error = %q", err)
	}
}

func TestConfigValidateAcceptsDefaults(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("the built-in defaults should validate: %v", err)
	}
}

func TestInsecureWarningsFlagPlaceholderPasswords(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantWarn bool
	}{
		{"empty", "", true},
		{"old hardcoded default", "changeme", true},
		{"dist placeholder", "00000000000000000000000000000000", true},
		{"real secret", "b7f3a1c9e5d24680b7f3a1c9e5d24680", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			config.SitePassword = tt.password
			config.ReportPassword = "b7f3a1c9e5d24680b7f3a1c9e5d24680"

			warnings := config.InsecureWarnings()
			got := len(warnings) > 0

			if got != tt.wantWarn {
				t.Errorf("InsecureWarnings() = %v, want a warning: %v", warnings, tt.wantWarn)
			}
			if tt.wantWarn && !strings.Contains(warnings[0], "site_password") {
				t.Errorf("warning should name site_password, got %q", warnings[0])
			}
		})
	}
}

func TestInsecureWarningsSilentOnGoodConfig(t *testing.T) {
	config := DefaultConfig()
	config.SitePassword = "b7f3a1c9e5d24680b7f3a1c9e5d24680"
	config.ReportPassword = "0f1e2d3c4b5a69780f1e2d3c4b5a6978"

	if warnings := config.InsecureWarnings(); len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestDefaultConfigReapIsFasterThanTimeout(t *testing.T) {
	config := DefaultConfig()

	if config.ReapInterval >= config.PeersTimeout {
		t.Errorf("reap interval %d should be well under peers timeout %d",
			config.ReapInterval, config.PeersTimeout)
	}
}
