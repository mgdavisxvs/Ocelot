package tracker

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ── Typed configuration newtypes (F-C2) ──────────────────────────────────────
// These newtypes give semantic meaning to bare integer config values, making
// incorrect unit assignments a type-level error when used in typed contexts.
// The FileConfig struct keeps int fields for backward-compatible file parsing;
// callers that need semantic safety use these types explicitly.

// Seconds is a duration expressed as whole seconds (non-negative).
type Seconds int

// ToDuration converts a Seconds value to a time.Duration.
func (s Seconds) ToDuration() time.Duration { return time.Duration(s) * time.Second }

// Port is a valid TCP/UDP port number (1–65535).
type Port uint16

// Valid returns true when the port is in the usable range.
func (p Port) Valid() bool { return p >= 1 && p <= 65535 }

// Megabytes is a size expressed in mebibytes (MiB), used for buffer and limit fields.
type Megabytes int

// Bytes returns the size in bytes.
func (m Megabytes) Bytes() int64 { return int64(m) * 1024 * 1024 }

// FileConfig holds all values parsed from ocelot.conf.
type FileConfig struct {
	ListenPort        int
	MaxConnections    int
	MaxMiddlemen      int
	MaxReadBuffer     int
	ConnectionTimeout int
	KeepaliveTimeout  int
	AnnounceInterval  int
	MaxRequestSize    int
	NumWantLimit      int
	RequestLogSize    int
	SitePassword      string
	ReportPassword    string
	PeersTimeout      int
	DelReasonLifetime int
	ReapPeersInterval int
	ScheduleInterval  int
	Readonly          bool
	DBDir             string
	GazelleURL        string // optional Gazelle callback endpoint
	MetricsPort       string // Prometheus metrics listen address, e.g. ":9090"

	// VirtualServer subsystem fields
	VSEnabled        bool
	VSPort           string // VS HTTP API listen address, e.g. ":9091"
	VSAdminKey       string // bearer token for VS API auth
	VSDBPath         string // path to vs.db (default data/db/vs.db)
	VSReconcileEvery int    // reconciler tick interval in seconds (default 15)

	// Optional / advanced fields
	OTelEndpoint         string // OpenTelemetry collector endpoint
	BatchBufferCap       int    // capacity of the async DB write queue
	RateLimitRPS         int    // per-IP rate limit (requests/sec); 0 = disabled
	RateLimitBurst       int    // burst allowance for the rate limiter
	MarkovAPIURL         string // Markov engine HTTP API base URL
	FreeleechPollSec     int    // freeleech candidate poll interval (seconds)
	FreeleechNotifyHours int    // hours ahead to notify freeleech window
	RedisAddr            string // Redis address for optional dual-write backend

	// TLS
	TLSCertFile string
	TLSKeyFile  string
	TLSAutoTLS  bool
	TLSDomain   string
	TLSCacheDir string
}

// DefaultFileConfig returns conservative defaults matching ocelot.conf.dist.
func DefaultFileConfig() *FileConfig {
	return &FileConfig{
		ListenPort:        34000,
		MaxConnections:    128,
		MaxMiddlemen:      20000,
		MaxReadBuffer:     4096,
		ConnectionTimeout: 10,
		KeepaliveTimeout:  0,
		AnnounceInterval:  1800,
		MaxRequestSize:    4096,
		NumWantLimit:      50,
		RequestLogSize:    500,
		SitePassword:      "00000000000000000000000000000000",
		ReportPassword:    "00000000000000000000000000000000",
		PeersTimeout:      7200,
		DelReasonLifetime: 86400,
		ReapPeersInterval: 1800,
		ScheduleInterval:  3,
		Readonly:          false,
		DBDir:             "./data/db",
		MetricsPort:       ":9090",
		VSEnabled:         false,
		VSPort:            ":9091",
		VSAdminKey:        "",
		VSDBPath:          "data/db/vs.db",
		VSReconcileEvery:  15,
		BatchBufferCap:    1000,
		RateLimitRPS:      100,
		RateLimitBurst:    200,
	}
}

// Validate checks that all numeric fields fall within safe operating ranges.
// Call after parsing to catch mis-typed or out-of-range config values before
// the server starts. Returns the first violation found.
func (fc *FileConfig) Validate() error {
	if !Port(fc.ListenPort).Valid() {
		return fmt.Errorf("listen_port %d is not in range 1–65535", fc.ListenPort)
	}
	if fc.AnnounceInterval <= 0 {
		return fmt.Errorf("announce_interval must be > 0 seconds, got %d", fc.AnnounceInterval)
	}
	if fc.PeersTimeout <= 0 {
		return fmt.Errorf("peers_timeout must be > 0 seconds, got %d", fc.PeersTimeout)
	}
	if fc.MaxConnections <= 0 {
		return fmt.Errorf("max_connections must be > 0, got %d", fc.MaxConnections)
	}
	if fc.NumWantLimit <= 0 || fc.NumWantLimit > 500 {
		return fmt.Errorf("numwant_limit must be 1–500, got %d", fc.NumWantLimit)
	}
	if fc.RateLimitRPS < 0 {
		return fmt.Errorf("rate_limit_rps must be >= 0, got %d", fc.RateLimitRPS)
	}
	if fc.BatchBufferCap < 0 {
		return fmt.Errorf("batch_buffer_cap must be >= 0, got %d", fc.BatchBufferCap)
	}
	return nil
}

// ParseFlags processes CLI arguments and returns the config file path.
// Supports the same -c flag as the original C++ binary.
func ParseFlags() string {
	path := flag.String("c", "ocelot.conf", "path to config file")
	flag.Parse()
	return *path
}

// ParseConfigFile reads an ocelot.conf-style file (key = value, # comments).
// Missing or unparseable values fall back to DefaultFileConfig.
func ParseConfigFile(path string) (*FileConfig, error) {
	cfg := DefaultFileConfig()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // use defaults when file is absent
		}
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "listen_port":
			cfg.ListenPort = parseIntVal(val, cfg.ListenPort)
		case "max_connections":
			cfg.MaxConnections = parseIntVal(val, cfg.MaxConnections)
		case "max_middlemen":
			cfg.MaxMiddlemen = parseIntVal(val, cfg.MaxMiddlemen)
		case "max_read_buffer":
			cfg.MaxReadBuffer = parseIntVal(val, cfg.MaxReadBuffer)
		case "connection_timeout":
			cfg.ConnectionTimeout = parseIntVal(val, cfg.ConnectionTimeout)
		case "keepalive_timeout":
			cfg.KeepaliveTimeout = parseIntVal(val, cfg.KeepaliveTimeout)
		case "announce_interval":
			cfg.AnnounceInterval = parseIntVal(val, cfg.AnnounceInterval)
		case "max_request_size":
			cfg.MaxRequestSize = parseIntVal(val, cfg.MaxRequestSize)
		case "numwant_limit":
			cfg.NumWantLimit = parseIntVal(val, cfg.NumWantLimit)
		case "request_log_size":
			cfg.RequestLogSize = parseIntVal(val, cfg.RequestLogSize)
		case "site_password":
			cfg.SitePassword = val
		case "report_password":
			cfg.ReportPassword = val
		case "peers_timeout":
			cfg.PeersTimeout = parseIntVal(val, cfg.PeersTimeout)
		case "del_reason_lifetime":
			cfg.DelReasonLifetime = parseIntVal(val, cfg.DelReasonLifetime)
		case "reap_peers_interval":
			cfg.ReapPeersInterval = parseIntVal(val, cfg.ReapPeersInterval)
		case "schedule_interval":
			cfg.ScheduleInterval = parseIntVal(val, cfg.ScheduleInterval)
		case "readonly":
			cfg.Readonly = val == "true"
		case "db_dir":
			cfg.DBDir = val
		case "gazelle_url":
			cfg.GazelleURL = val
		case "metrics_port":
			cfg.MetricsPort = val
		case "vs_enabled":
			cfg.VSEnabled = val == "true"
		case "vs_port":
			cfg.VSPort = val
		case "vs_admin_key":
			cfg.VSAdminKey = val
		case "vs_db_path":
			cfg.VSDBPath = val
		case "vs_reconcile_every":
			cfg.VSReconcileEvery = parseIntVal(val, cfg.VSReconcileEvery)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return cfg, nil
}

// ToTrackerConfig converts a FileConfig to the runtime Config used by Server.
func (fc *FileConfig) ToTrackerConfig() *Config {
	readTimeout := time.Duration(fc.ConnectionTimeout) * time.Second
	return &Config{
		ListenAddr:        fmt.Sprintf(":%d", fc.ListenPort),
		AnnounceInterval:  fc.AnnounceInterval,
		PeersTimeout:      fc.PeersTimeout,
		MaxMiddlemen:      fc.MaxMiddlemen,
		MaxConnections:    fc.MaxConnections,
		MaxReadBuffer:     fc.MaxReadBuffer,
		NumWantLimit:      fc.NumWantLimit,
		KeepaliveTimeout:  time.Duration(fc.KeepaliveTimeout) * time.Second,
		SitePassword:      fc.SitePassword,
		ReportPassword:    fc.ReportPassword,
		ReadTimeout:       readTimeout,
		WriteTimeout:      readTimeout,
		MetricsPort:       fc.MetricsPort,
		GazelleURL:        fc.GazelleURL,
		ScheduleInterval:  fc.ScheduleInterval,
		ReapPeersInterval: fc.ReapPeersInterval,
		DelReasonLifetime: fc.DelReasonLifetime,
		Readonly:          fc.Readonly,

		// Extended fields
		OTelEndpoint:         fc.OTelEndpoint,
		BatchBufferCap:       fc.BatchBufferCap,
		RateLimitRPS:         fc.RateLimitRPS,
		RateLimitBurst:       fc.RateLimitBurst,
		MarkovAPIURL:         fc.MarkovAPIURL,
		FreeleechPollSec:     fc.FreeleechPollSec,
		FreeleechNotifyHours: fc.FreeleechNotifyHours,
		RedisAddr:            fc.RedisAddr,
		TLS: TLSConfig{
			CertFile: fc.TLSCertFile,
			KeyFile:  fc.TLSKeyFile,
			AutoTLS:  fc.TLSAutoTLS,
			Domain:   fc.TLSDomain,
			CacheDir: fc.TLSCacheDir,
		},
	}
}

func parseIntVal(s string, def int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
