package ml

import (
	"testing"
	"time"
)

// ── AnomalyDetector ───────────────────────────────────────────────────────────

func cleanBehavior() *PeerBehavior {
	return &PeerBehavior{
		Uploaded:        1_000_000_000, // 1 GB
		Downloaded:      800_000_000,   // 800 MB
		UploadSpeed:     500_000,       // 500 KB/s — well under 1 Gbps
		TorrentSize:     2_000_000_000, // 2 GB
		AnnounceCount:   5,
		PortHistory:     []uint16{6881},
		ConnectionTimes: []time.Time{time.Now().Add(-10 * time.Minute), time.Now()},
		FirstSeen:       time.Now().Add(-2 * time.Hour),
	}
}

func TestDetectAnomaly_CleanBehavior(t *testing.T) {
	ad := NewAnomalyDetector()
	b := cleanBehavior()
	blocked, reason := ad.DetectAnomaly(b)
	if blocked {
		t.Errorf("expected clean behavior to pass, got blocked=%v reason=%q", blocked, reason)
	}
}

func TestDetectAnomaly_ImpossibleUploadSpeed(t *testing.T) {
	ad := NewAnomalyDetector()
	b := cleanBehavior()
	b.UploadSpeed = 2e9 // 2 Gbps — above 1 Gbps threshold
	blocked, reason := ad.DetectAnomaly(b)
	if !blocked {
		t.Fatal("expected impossible_upload_speed to be blocked")
	}
	if reason != "impossible_upload_speed" {
		t.Errorf("reason = %q, want \"impossible_upload_speed\"", reason)
	}
}

func TestDetectAnomaly_RatioCheating(t *testing.T) {
	ad := NewAnomalyDetector()
	b := cleanBehavior()
	b.Uploaded = 200_000_000_000   // 200 GB
	b.Downloaded = 500_000         // 500 KB — ratio ≈ 0.0000025 < 0.01
	blocked, reason := ad.DetectAnomaly(b)
	if !blocked {
		t.Fatal("expected ratio_cheating to be blocked")
	}
	if reason != "ratio_cheating" {
		t.Errorf("reason = %q, want \"ratio_cheating\"", reason)
	}
}

func TestDetectAnomaly_RatioCheating_BelowThreshold(t *testing.T) {
	// Uploaded just under 100 GB — ratio check should NOT fire.
	ad := NewAnomalyDetector()
	b := cleanBehavior()
	b.Uploaded = 99_000_000_000 // 99 GB — below 100 GB gate
	b.Downloaded = 100_000
	blocked, reason := ad.DetectAnomaly(b)
	// Only upload speed matters here; ratio check requires Uploaded > 100e9.
	if blocked && reason == "ratio_cheating" {
		t.Errorf("ratio_cheating should not fire below 100 GB uploaded threshold")
	}
}

func TestDetectAnomaly_DDoSPattern(t *testing.T) {
	ad := NewAnomalyDetector()
	b := cleanBehavior()
	b.AnnounceCount = 200                    // 200 announces
	b.FirstSeen = time.Now().Add(-30 * time.Minute) // in 0.5 hours → rate = 400/h > 100
	blocked, reason := ad.DetectAnomaly(b)
	if !blocked {
		t.Fatal("expected ddos_pattern to be blocked")
	}
	if reason != "ddos_pattern" {
		t.Errorf("reason = %q, want \"ddos_pattern\"", reason)
	}
}

func TestDetectAnomaly_PortScanning(t *testing.T) {
	ad := NewAnomalyDetector()
	b := cleanBehavior()
	b.PortHistory = make([]uint16, 11) // 11 ports > maxPortChanges(10)
	for i := range b.PortHistory {
		b.PortHistory[i] = uint16(6881 + i)
	}
	blocked, reason := ad.DetectAnomaly(b)
	if !blocked {
		t.Fatal("expected port_scanning to be blocked")
	}
	if reason != "port_scanning" {
		t.Errorf("reason = %q, want \"port_scanning\"", reason)
	}
}

func TestDetectAnomaly_RapidReconnect(t *testing.T) {
	ad := NewAnomalyDetector()
	b := cleanBehavior()
	now := time.Now()
	// Two connections 10 seconds apart — well under 60s threshold.
	b.ConnectionTimes = []time.Time{
		now.Add(-10 * time.Second),
		now,
	}
	blocked, reason := ad.DetectAnomaly(b)
	if !blocked {
		t.Fatal("expected rapid_reconnect to be blocked")
	}
	if reason != "rapid_reconnect" {
		t.Errorf("reason = %q, want \"rapid_reconnect\"", reason)
	}
}

func TestDetectAnomaly_ImpossibleDownload(t *testing.T) {
	ad := NewAnomalyDetector()
	b := cleanBehavior()
	b.TorrentSize = 1_000_000_000   // 1 GB
	b.Downloaded = 3_000_000_000    // 3 GB — exceeds 2×TorrentSize
	blocked, reason := ad.DetectAnomaly(b)
	if !blocked {
		t.Fatal("expected impossible_download to be blocked")
	}
	if reason != "impossible_download" {
		t.Errorf("reason = %q, want \"impossible_download\"", reason)
	}
}

// ── ClientAnomalyDetector ─────────────────────────────────────────────────────

func TestDetectClientAnomaly_CleanClient(t *testing.T) {
	cad := NewClientAnomalyDetector()
	// Standard qBittorrent peer ID: 8-char prefix + 12-char random = exactly 20 bytes.
	blocked, reason := cad.DetectClientAnomaly("-qB4400-abcdefghijkl", "qBittorrent/4.4.0")
	if blocked {
		t.Errorf("expected clean client to pass, got blocked=%v reason=%q", blocked, reason)
	}
}

func TestDetectClientAnomaly_KnownMaliciousClient_BitSpirit(t *testing.T) {
	cad := NewClientAnomalyDetector()
	blocked, reason := cad.DetectClientAnomaly("-BS0001-abcdefghijklmnopqrst", "BitSpirit/3.7")
	if !blocked {
		t.Fatal("expected known_malicious_client for BitSpirit user agent")
	}
	if reason != "known_malicious_client" {
		t.Errorf("reason = %q, want \"known_malicious_client\"", reason)
	}
}

func TestDetectClientAnomaly_KnownMaliciousClient_XunLei(t *testing.T) {
	cad := NewClientAnomalyDetector()
	blocked, reason := cad.DetectClientAnomaly("-XL0001-abcdefghijklmnopqrst", "XunLei/7.9")
	if !blocked {
		t.Fatal("expected known_malicious_client for XunLei user agent")
	}
	if reason != "known_malicious_client" {
		t.Errorf("reason = %q, want \"known_malicious_client\"", reason)
	}
}

func TestDetectClientAnomaly_InvalidLength_Short(t *testing.T) {
	cad := NewClientAnomalyDetector()
	blocked, reason := cad.DetectClientAnomaly("tooshort", "qBittorrent/4.4.0")
	if !blocked {
		t.Fatal("expected invalid_client_id_length for short peer ID")
	}
	if reason != "invalid_client_id_length" {
		t.Errorf("reason = %q, want \"invalid_client_id_length\"", reason)
	}
}

func TestDetectClientAnomaly_InvalidLength_Long(t *testing.T) {
	cad := NewClientAnomalyDetector()
	blocked, reason := cad.DetectClientAnomaly("this-peer-id-is-way-too-long-for-bittorrent", "qBittorrent/4.4.0")
	if !blocked {
		t.Fatal("expected invalid_client_id_length for long peer ID")
	}
	if reason != "invalid_client_id_length" {
		t.Errorf("reason = %q, want \"invalid_client_id_length\"", reason)
	}
}

func TestDetectClientAnomaly_SuspiciousPattern_AllSameByte(t *testing.T) {
	cad := NewClientAnomalyDetector()
	// Exactly 20 bytes, all identical — suspicious_client_id_pattern.
	peerID := string(make([]byte, 20)) // all zero bytes
	blocked, reason := cad.DetectClientAnomaly(peerID, "qBittorrent/4.4.0")
	if !blocked {
		t.Fatal("expected suspicious_client_id_pattern for all-zero peer ID")
	}
	if reason != "suspicious_client_id_pattern" {
		t.Errorf("reason = %q, want \"suspicious_client_id_pattern\"", reason)
	}
}

func TestDetectClientAnomaly_SuspiciousPattern_AllSameChar(t *testing.T) {
	cad := NewClientAnomalyDetector()
	peerID := string([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	blocked, reason := cad.DetectClientAnomaly(peerID, "qBittorrent/4.4.0")
	if !blocked {
		t.Fatal("expected suspicious_client_id_pattern for all-0xFF peer ID")
	}
	if reason != "suspicious_client_id_pattern" {
		t.Errorf("reason = %q, want \"suspicious_client_id_pattern\"", reason)
	}
}
