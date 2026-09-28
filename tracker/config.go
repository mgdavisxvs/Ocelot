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
	GazelleURL           string // optional Gazelle callback endpoint
	MarkovAPIURL         string // optional Markov engine HTTP API base URL, e.g. "http://127.0.0.1:9090"
	FreeleechPollSec     int    // how often (seconds) to poll /freeleech; 0 = disabled
	FreeleechNotifyHours int    // freeleech duration (hours) passed to NotifyFreeleech
	MetricsPort          string // Prometheus metrics listen address, e.g. ":9090"
	// Rate limiting (0 = disabled)
	RateLimitRPS   int
	RateLimitBurst int
	// Async write queue capacity for BufferedDB (0 = default 4096)
	BatchBufferCap int
	// TLS — leave CertFile empty to disable TLS.
	TLSCertFile string
	TLSKeyFile  string
	TLSAutoTLS  bool
	TLSDomain   string
	TLSCacheDir string
	// Admin REST API (JWT-protected)
	AdminAPIPort string
	JWTSecret    string
	// OTel OTLP endpoint, e.g. "localhost:4318" (empty = tracing disabled)
	OTelEndpoint string
	// RedisAddr is the Redis server address for optional dual-write / caching
	// e.g. "localhost:6379" (empty = Redis disabled)
	RedisAddr string
	// UC use-case config fields
	LiveAnnounceInterval  int     // announce interval override for live streams (default 10)
	LivePeersTimeout      int     // peer timeout for live streams (default 45)
	MinReplicas           int     // minimum replica count for preservation monitor (default 3)
	SLACheckInterval      int     // SLA monitor check interval in seconds (default 60)
	SLAMaxAnnounceSec     int     // maximum seconds between announces before SLA alert (default 300)
	BackupRetentionDays   int     // days to keep backup torrents (default 30)
	DataRetentionDays     int     // days to keep tick data torrents (default 90)
	CIArtifactTTL         int     // CI artifact TTL in seconds (default 14400)
	RolloutDwellSeconds   int     // default dwell seconds between rollout stages (default 300)
	RolloutAnomalyGate    float64 // default anomaly rate gate for rollouts (default 0.05)
	CheatCorruptThreshold float64 // corrupt ratio above which a game peer is banned (default 0.1)
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
		ScheduleInterval:  300, // 5-minute WAL checkpoint interval
		Readonly:          false,
		DBDir:                "./data/db",
		FreeleechPollSec:     300,
		FreeleechNotifyHours: 24,
		MetricsPort:          ":9090",
		RateLimitRPS:      100,
		RateLimitBurst:    200,
		BatchBufferCap:    4096,
		LiveAnnounceInterval:  10,
		LivePeersTimeout:      45,
		MinReplicas:           3,
		SLACheckInterval:      60,
		SLAMaxAnnounceSec:     300,
		BackupRetentionDays:   30,
		DataRetentionDays:     90,
		CIArtifactTTL:         14400,
		RolloutDwellSeconds:   300,
		RolloutAnomalyGate:    0.05,
		CheatCorruptThreshold: 0.10,
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
		case "markov_api_url":
			cfg.MarkovAPIURL = val
		case "freeleech_poll_sec":
			cfg.FreeleechPollSec = parseIntVal(val, cfg.FreeleechPollSec)
		case "freeleech_notify_hours":
			cfg.FreeleechNotifyHours = parseIntVal(val, cfg.FreeleechNotifyHours)
		case "metrics_port":
			cfg.MetricsPort = val
		case "rate_limit_rps":
			cfg.RateLimitRPS = parseIntVal(val, cfg.RateLimitRPS)
		case "rate_limit_burst":
			cfg.RateLimitBurst = parseIntVal(val, cfg.RateLimitBurst)
		case "batch_buffer_cap":
			cfg.BatchBufferCap = parseIntVal(val, cfg.BatchBufferCap)
		case "tls_cert_file":
			cfg.TLSCertFile = val
		case "tls_key_file":
			cfg.TLSKeyFile = val
		case "tls_auto":
			cfg.TLSAutoTLS = val == "true"
		case "tls_domain":
			cfg.TLSDomain = val
		case "tls_cache_dir":
			cfg.TLSCacheDir = val
		case "admin_api_port":
			cfg.AdminAPIPort = val
		case "jwt_secret":
			cfg.JWTSecret = val
		case "otel_endpoint":
			cfg.OTelEndpoint = val
		case "redis_addr":
			cfg.RedisAddr = val
		case "live_announce_interval":
			cfg.LiveAnnounceInterval = parseIntVal(val, cfg.LiveAnnounceInterval)
		case "live_peers_timeout":
			cfg.LivePeersTimeout = parseIntVal(val, cfg.LivePeersTimeout)
		case "min_replicas":
			cfg.MinReplicas = parseIntVal(val, cfg.MinReplicas)
		case "sla_check_interval":
			cfg.SLACheckInterval = parseIntVal(val, cfg.SLACheckInterval)
		case "sla_max_announce_sec":
			cfg.SLAMaxAnnounceSec = parseIntVal(val, cfg.SLAMaxAnnounceSec)
		case "backup_retention_days":
			cfg.BackupRetentionDays = parseIntVal(val, cfg.BackupRetentionDays)
		case "data_retention_days":
			cfg.DataRetentionDays = parseIntVal(val, cfg.DataRetentionDays)
		case "ci_artifact_ttl":
			cfg.CIArtifactTTL = parseIntVal(val, cfg.CIArtifactTTL)
		case "rollout_dwell_seconds":
			cfg.RolloutDwellSeconds = parseIntVal(val, cfg.RolloutDwellSeconds)
		case "rollout_anomaly_gate":
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.RolloutAnomalyGate = v
			}
		case "cheat_corrupt_threshold":
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.CheatCorruptThreshold = v
			}
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
		ListenAddr:           fmt.Sprintf(":%d", fc.ListenPort),
		AnnounceInterval:     fc.AnnounceInterval,
		PeersTimeout:         fc.PeersTimeout,
		MaxMiddlemen:         fc.MaxMiddlemen,
		NumWantLimit:         fc.NumWantLimit,
		KeepaliveTimeout:     time.Duration(fc.KeepaliveTimeout) * time.Second,
		SitePassword:         fc.SitePassword,
		ReportPassword:       fc.ReportPassword,
		ReadTimeout:          readTimeout,
		WriteTimeout:         readTimeout,
		ScheduleInterval:     fc.ScheduleInterval,
		ReapPeersInterval:    fc.ReapPeersInterval,
		GazelleURL:           fc.GazelleURL,
		MarkovAPIURL:         fc.MarkovAPIURL,
		MetricsPort:          fc.MetricsPort,
		RateLimitRPS:         fc.RateLimitRPS,
		RateLimitBurst:       fc.RateLimitBurst,
		BatchBufferCap:       fc.BatchBufferCap,
		FreeleechPollSec:     fc.FreeleechPollSec,
		FreeleechNotifyHours: fc.FreeleechNotifyHours,
		MaxReadBuffer:        fc.MaxReadBuffer,
		MaxRequestSize:       fc.MaxRequestSize,
		RequestLogSize:       fc.RequestLogSize,
		DelReasonLifetime:    fc.DelReasonLifetime,
		Readonly:             fc.Readonly,
		AdminAPIPort:         fc.AdminAPIPort,
		JWTSecret:            []byte(fc.JWTSecret),
		OTelEndpoint:         fc.OTelEndpoint,
		MaxConnections:       fc.MaxConnections,
		RedisAddr:            fc.RedisAddr,
		LiveAnnounceInterval:  fc.LiveAnnounceInterval,
		LivePeersTimeout:      fc.LivePeersTimeout,
		MinReplicas:           fc.MinReplicas,
		SLACheckInterval:      fc.SLACheckInterval,
		SLAMaxAnnounceSec:     fc.SLAMaxAnnounceSec,
		BackupRetentionDays:   fc.BackupRetentionDays,
		DataRetentionDays:     fc.DataRetentionDays,
		CIArtifactTTL:         fc.CIArtifactTTL,
		RolloutDwellSeconds:   fc.RolloutDwellSeconds,
		RolloutAnomalyGate:    fc.RolloutAnomalyGate,
		CheatCorruptThreshold: fc.CheatCorruptThreshold,
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
