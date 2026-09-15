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
	}
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
		ListenAddr:       fmt.Sprintf(":%d", fc.ListenPort),
		AnnounceInterval: fc.AnnounceInterval,
		PeersTimeout:     fc.PeersTimeout,
		MaxMiddlemen:     fc.MaxMiddlemen,
		NumWantLimit:     fc.NumWantLimit,
		KeepaliveTimeout: time.Duration(fc.KeepaliveTimeout) * time.Second,
		SitePassword:     fc.SitePassword,
		ReportPassword:   fc.ReportPassword,
		ReadTimeout:      readTimeout,
		WriteTimeout:     readTimeout,
		MetricsPort:      fc.MetricsPort,
	}
}

func parseIntVal(s string, def int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
