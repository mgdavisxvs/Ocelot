package tracker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConf(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ocelot.conf")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestParseConfigFile_Defaults_WhenMissing(t *testing.T) {
	cfg, err := ParseConfigFile("/nonexistent/ocelot.conf")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	def := DefaultFileConfig()
	if cfg.ListenPort != def.ListenPort {
		t.Errorf("ListenPort = %d, want %d", cfg.ListenPort, def.ListenPort)
	}
	if cfg.AnnounceInterval != def.AnnounceInterval {
		t.Errorf("AnnounceInterval = %d, want %d", cfg.AnnounceInterval, def.AnnounceInterval)
	}
}

func TestParseConfigFile_BasicKeys(t *testing.T) {
	path := writeConf(t, `
listen_port = 9999
announce_interval = 3600
peers_timeout = 14400
numwant_limit = 25
site_password = secretpassword1234567890123456
report_password = reportpassword123456789012345
db_dir = /var/ocelot/db
gazelle_url = http://gazelle.example.com/callback.php
`)
	cfg, err := ParseConfigFile(path)
	if err != nil {
		t.Fatalf("ParseConfigFile: %v", err)
	}
	if cfg.ListenPort != 9999 {
		t.Errorf("ListenPort = %d", cfg.ListenPort)
	}
	if cfg.AnnounceInterval != 3600 {
		t.Errorf("AnnounceInterval = %d", cfg.AnnounceInterval)
	}
	if cfg.PeersTimeout != 14400 {
		t.Errorf("PeersTimeout = %d", cfg.PeersTimeout)
	}
	if cfg.NumWantLimit != 25 {
		t.Errorf("NumWantLimit = %d", cfg.NumWantLimit)
	}
	if cfg.SitePassword != "secretpassword1234567890123456" {
		t.Errorf("SitePassword = %q", cfg.SitePassword)
	}
	if cfg.DBDir != "/var/ocelot/db" {
		t.Errorf("DBDir = %q", cfg.DBDir)
	}
	if cfg.GazelleURL != "http://gazelle.example.com/callback.php" {
		t.Errorf("GazelleURL = %q", cfg.GazelleURL)
	}
}

func TestParseConfigFile_Comments_AndBlanks(t *testing.T) {
	path := writeConf(t, `
# This is a comment
listen_port = 12345

# Another comment
announce_interval = 900
`)
	cfg, err := ParseConfigFile(path)
	if err != nil {
		t.Fatalf("ParseConfigFile: %v", err)
	}
	if cfg.ListenPort != 12345 {
		t.Errorf("ListenPort = %d", cfg.ListenPort)
	}
	if cfg.AnnounceInterval != 900 {
		t.Errorf("AnnounceInterval = %d", cfg.AnnounceInterval)
	}
}

func TestParseConfigFile_InvalidInt_UsesDefault(t *testing.T) {
	path := writeConf(t, `listen_port = notanumber`)
	cfg, err := ParseConfigFile(path)
	if err != nil {
		t.Fatalf("ParseConfigFile: %v", err)
	}
	if cfg.ListenPort != DefaultFileConfig().ListenPort {
		t.Errorf("bad int should fall back to default, got %d", cfg.ListenPort)
	}
}

func TestParseConfigFile_UnknownKey_Ignored(t *testing.T) {
	path := writeConf(t, `
listen_port = 5000
totally_unknown_key = whatever
`)
	cfg, err := ParseConfigFile(path)
	if err != nil {
		t.Fatalf("ParseConfigFile: %v", err)
	}
	if cfg.ListenPort != 5000 {
		t.Errorf("ListenPort = %d", cfg.ListenPort)
	}
}

func TestParseConfigFile_Readonly(t *testing.T) {
	path := writeConf(t, `readonly = true`)
	cfg, err := ParseConfigFile(path)
	if err != nil {
		t.Fatalf("ParseConfigFile: %v", err)
	}
	if !cfg.Readonly {
		t.Error("Readonly should be true")
	}

	path2 := writeConf(t, `readonly = false`)
	cfg2, _ := ParseConfigFile(path2)
	if cfg2.Readonly {
		t.Error("Readonly should be false")
	}
}

func TestToTrackerConfig(t *testing.T) {
	fc := &FileConfig{
		ListenPort:        34000,
		AnnounceInterval:  1800,
		PeersTimeout:      7200,
		MaxMiddlemen:      20000,
		NumWantLimit:      50,
		KeepaliveTimeout:  60,
		SitePassword:      "sitepass",
		ReportPassword:    "reportpass",
		ConnectionTimeout: 30,
		ScheduleInterval:  3,
	}
	cfg := fc.ToTrackerConfig()

	if cfg.ListenAddr != ":34000" {
		t.Errorf("ListenAddr = %q, want \":34000\"", cfg.ListenAddr)
	}
	if cfg.AnnounceInterval != 1800 {
		t.Errorf("AnnounceInterval = %d", cfg.AnnounceInterval)
	}
	if cfg.KeepaliveTimeout != 60*time.Second {
		t.Errorf("KeepaliveTimeout = %v", cfg.KeepaliveTimeout)
	}
	if cfg.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v", cfg.ReadTimeout)
	}
	if cfg.SitePassword != "sitepass" {
		t.Errorf("SitePassword = %q", cfg.SitePassword)
	}
}

func TestParseConfigFile_RemainingKeys(t *testing.T) {
	path := writeConf(t, `
max_connections = 200
max_middlemen = 50
max_read_buffer = 8192
connection_timeout = 30
keepalive_timeout = 60
max_request_size = 4096
request_log_size = 1000
report_password = reportpass1234567890123456789
del_reason_lifetime = 86400
reap_peers_interval = 120
schedule_interval = 10
metrics_port = 9090
`)
	cfg, err := ParseConfigFile(path)
	if err != nil {
		t.Fatalf("ParseConfigFile: %v", err)
	}
	if cfg.MaxConnections != 200 {
		t.Errorf("MaxConnections = %d, want 200", cfg.MaxConnections)
	}
	if cfg.MaxMiddlemen != 50 {
		t.Errorf("MaxMiddlemen = %d, want 50", cfg.MaxMiddlemen)
	}
	if cfg.MaxReadBuffer != 8192 {
		t.Errorf("MaxReadBuffer = %d, want 8192", cfg.MaxReadBuffer)
	}
	if cfg.ConnectionTimeout != 30 {
		t.Errorf("ConnectionTimeout = %d, want 30", cfg.ConnectionTimeout)
	}
	if cfg.KeepaliveTimeout != 60 {
		t.Errorf("KeepaliveTimeout = %d, want 60", cfg.KeepaliveTimeout)
	}
	if cfg.MaxRequestSize != 4096 {
		t.Errorf("MaxRequestSize = %d, want 4096", cfg.MaxRequestSize)
	}
	if cfg.RequestLogSize != 1000 {
		t.Errorf("RequestLogSize = %d, want 1000", cfg.RequestLogSize)
	}
	if cfg.ReportPassword != "reportpass1234567890123456789" {
		t.Errorf("ReportPassword = %q", cfg.ReportPassword)
	}
	if cfg.DelReasonLifetime != 86400 {
		t.Errorf("DelReasonLifetime = %d, want 86400", cfg.DelReasonLifetime)
	}
	if cfg.ReapPeersInterval != 120 {
		t.Errorf("ReapPeersInterval = %d, want 120", cfg.ReapPeersInterval)
	}
	if cfg.ScheduleInterval != 10 {
		t.Errorf("ScheduleInterval = %d, want 10", cfg.ScheduleInterval)
	}
	if cfg.MetricsPort != "9090" {
		t.Errorf("MetricsPort = %q, want \"9090\"", cfg.MetricsPort)
	}
}

func TestDefaultFileConfig_Sensible(t *testing.T) {
	d := DefaultFileConfig()
	if d.ListenPort <= 0 || d.ListenPort > 65535 {
		t.Errorf("ListenPort = %d out of valid range", d.ListenPort)
	}
	if d.PeersTimeout <= d.AnnounceInterval {
		t.Errorf("PeersTimeout (%d) should be > AnnounceInterval (%d)",
			d.PeersTimeout, d.AnnounceInterval)
	}
	if d.NumWantLimit <= 0 {
		t.Errorf("NumWantLimit = %d, must be positive", d.NumWantLimit)
	}
	if d.DBDir == "" {
		t.Error("DBDir must not be empty")
	}
}
