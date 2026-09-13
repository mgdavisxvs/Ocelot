package ml

import (
	"testing"
	"time"
)

func TestAnomalyDetectorCreation(t *testing.T) {
	ad := NewAnomalyDetector()

	if ad == nil {
		t.Fatal("NewAnomalyDetector returned nil")
	}

	// Verify sensible defaults
	if ad.maxUploadSpeed <= 0 {
		t.Error("Expected positive maxUploadSpeed")
	}

	if ad.maxAnnounceRate <= 0 {
		t.Error("Expected positive maxAnnounceRate")
	}
}

func TestDetectImpossibleUploadSpeed(t *testing.T) {
	ad := NewAnomalyDetector()

	behavior := &PeerBehavior{
		UploadSpeed: 2e9, // 2 Gbps - impossibly high
		Uploaded:    1e9,
		Downloaded:  1e9,
	}

	isAnomaly, anomalyType := ad.DetectAnomaly(behavior)

	if !isAnomaly {
		t.Error("Expected anomaly detection for impossible upload speed")
	}

	if anomalyType != "impossible_upload_speed" {
		t.Errorf("Expected anomaly type 'impossible_upload_speed', got '%s'", anomalyType)
	}
}

func TestDetectRatioCheating(t *testing.T) {
	ad := NewAnomalyDetector()

	behavior := &PeerBehavior{
		Uploaded:    200e9, // 200 GB uploaded
		Downloaded:  500e3, // 500 KB downloaded
		UploadSpeed: 1e6,
	}

	isAnomaly, anomalyType := ad.DetectAnomaly(behavior)

	if !isAnomaly {
		t.Error("Expected anomaly detection for ratio cheating")
	}

	if anomalyType != "ratio_cheating" {
		t.Errorf("Expected anomaly type 'ratio_cheating', got '%s'", anomalyType)
	}
}

func TestDetectDDoSPattern(t *testing.T) {
	ad := NewAnomalyDetector()

	now := time.Now()
	behavior := &PeerBehavior{
		AnnounceCount: 200, // 200 announces in 1 hour
		FirstSeen:     now.Add(-1 * time.Hour),
		Uploaded:      1e6,
		Downloaded:    1e6,
	}

	isAnomaly, anomalyType := ad.DetectAnomaly(behavior)

	if !isAnomaly {
		t.Error("Expected anomaly detection for DDoS pattern")
	}

	if anomalyType != "ddos_pattern" {
		t.Errorf("Expected anomaly type 'ddos_pattern', got '%s'", anomalyType)
	}
}

func TestDetectPortScanning(t *testing.T) {
	ad := NewAnomalyDetector()

	// Create port history with many different ports
	ports := make([]uint16, 15)
	for i := range ports {
		ports[i] = uint16(6881 + i)
	}

	behavior := &PeerBehavior{
		PortHistory: ports,
		Uploaded:    1e6,
		Downloaded:  1e6,
	}

	isAnomaly, anomalyType := ad.DetectAnomaly(behavior)

	if !isAnomaly {
		t.Error("Expected anomaly detection for port scanning")
	}

	if anomalyType != "port_scanning" {
		t.Errorf("Expected anomaly type 'port_scanning', got '%s'", anomalyType)
	}
}

func TestDetectRapidReconnect(t *testing.T) {
	ad := NewAnomalyDetector()

	now := time.Now()
	behavior := &PeerBehavior{
		ConnectionTimes: []time.Time{
			now.Add(-90 * time.Second), // 90 seconds ago
			now.Add(-30 * time.Second), // 30 seconds ago - only 60s apart but within threshold
		},
		Uploaded:    1e6,
		Downloaded:  1e6,
		TorrentSize: 10e9, // Add torrent size to avoid impossible_download check
	}

	// Actually, the threshold is 60 seconds, so 60 seconds apart should trigger it
	// Let me use 30 seconds apart to be safe
	behavior.ConnectionTimes = []time.Time{
		now.Add(-60 * time.Second),
		now.Add(-20 * time.Second), // 40 seconds apart - rapid reconnect
	}

	isAnomaly, anomalyType := ad.DetectAnomaly(behavior)

	if !isAnomaly {
		t.Error("Expected anomaly detection for rapid reconnect")
	}

	if anomalyType != "rapid_reconnect" {
		t.Errorf("Expected anomaly type 'rapid_reconnect', got '%s'", anomalyType)
	}
}

func TestDetectImpossibleDownload(t *testing.T) {
	ad := NewAnomalyDetector()

	behavior := &PeerBehavior{
		Downloaded:  300e9, // 300 GB downloaded
		TorrentSize: 100e9, // 100 GB torrent
		Uploaded:    1e6,
	}

	isAnomaly, anomalyType := ad.DetectAnomaly(behavior)

	if !isAnomaly {
		t.Error("Expected anomaly detection for impossible download")
	}

	if anomalyType != "impossible_download" {
		t.Errorf("Expected anomaly type 'impossible_download', got '%s'", anomalyType)
	}
}

func TestNormalBehavior(t *testing.T) {
	ad := NewAnomalyDetector()

	now := time.Now()
	behavior := &PeerBehavior{
		Uploaded:      10e9,  // 10 GB
		Downloaded:    5e9,   // 5 GB
		UploadSpeed:   5e6,   // 5 MB/s
		TorrentSize:   20e9,  // 20 GB torrent
		AnnounceCount: 10,    // 10 announces
		FirstSeen:     now.Add(-2 * time.Hour),
		PortHistory:   []uint16{6881},
		ConnectionTimes: []time.Time{
			now.Add(-2 * time.Hour),
			now.Add(-30 * time.Minute),
		},
	}

	isAnomaly, anomalyType := ad.DetectAnomaly(behavior)

	if isAnomaly {
		t.Errorf("Expected no anomaly for normal behavior, got '%s'", anomalyType)
	}
}

func TestClientAnomalyDetectorCreation(t *testing.T) {
	cad := NewClientAnomalyDetector()

	if cad == nil {
		t.Fatal("NewClientAnomalyDetector returned nil")
	}
}

func TestDetectMaliciousClient(t *testing.T) {
	cad := NewClientAnomalyDetector()

	tests := []struct {
		name      string
		userAgent string
		expectAnom bool
	}{
		{
			name:       "BitSpirit",
			userAgent:  "BitSpirit/3.6.0.550",
			expectAnom: true,
		},
		{
			name:       "BitComet",
			userAgent:  "BitComet/1.37",
			expectAnom: true,
		},
		{
			name:       "XunLei",
			userAgent:  "XunLei/5.9.9.1234",
			expectAnom: true,
		},
		{
			name:       "Thunder",
			userAgent:  "Thunder/2.7.9.478",
			expectAnom: true,
		},
		{
			name:       "qBittorrent (legitimate)",
			userAgent:  "qBittorrent/4.3.9",
			expectAnom: false,
		},
		{
			name:       "Transmission (legitimate)",
			userAgent:  "Transmission/3.00",
			expectAnom: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isAnomaly, anomalyType := cad.DetectClientAnomaly("12345678901234567890", tt.userAgent)

			if isAnomaly != tt.expectAnom {
				if tt.expectAnom {
					t.Errorf("Expected anomaly detection for %s", tt.name)
				} else {
					t.Errorf("Expected no anomaly for %s, got '%s'", tt.name, anomalyType)
				}
			}

			if tt.expectAnom && anomalyType != "known_malicious_client" {
				t.Errorf("Expected anomaly type 'known_malicious_client', got '%s'", anomalyType)
			}
		})
	}
}

func TestDetectInvalidClientIDLength(t *testing.T) {
	cad := NewClientAnomalyDetector()

	// Client ID too short
	isAnomaly, anomalyType := cad.DetectClientAnomaly("short", "qBittorrent/4.3.9")

	if !isAnomaly {
		t.Error("Expected anomaly detection for invalid client ID length")
	}

	if anomalyType != "invalid_client_id_length" {
		t.Errorf("Expected anomaly type 'invalid_client_id_length', got '%s'", anomalyType)
	}
}

func TestDetectSuspiciousClientIDPattern(t *testing.T) {
	cad := NewClientAnomalyDetector()

	tests := []struct {
		name       string
		clientID   string
		expectAnom bool
	}{
		{
			name:       "All zeros",
			clientID:   "00000000000000000000",
			expectAnom: true,
		},
		{
			name:       "All same character",
			clientID:   "AAAAAAAAAAAAAAAAAAAA",
			expectAnom: true,
		},
		{
			name:       "Normal client ID",
			clientID:   "qB34567890abcdefghij",
			expectAnom: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isAnomaly, anomalyType := cad.DetectClientAnomaly(tt.clientID, "qBittorrent/4.3.9")

			if isAnomaly != tt.expectAnom {
				if tt.expectAnom {
					t.Errorf("Expected anomaly detection for %s", tt.name)
				} else {
					t.Errorf("Expected no anomaly for %s, got '%s'", tt.name, anomalyType)
				}
			}

			if tt.expectAnom && anomalyType != "suspicious_client_id_pattern" {
				t.Errorf("Expected anomaly type 'suspicious_client_id_pattern', got '%s'", anomalyType)
			}
		})
	}
}

func TestIsRepeatingChar(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"", false},
		{"A", true},
		{"AAA", true},
		{"000000", true},
		{"ABCD", false},
		{"A1A1", false},
	}

	for _, tt := range tests {
		result := isRepeatingChar(tt.input)

		if result != tt.expected {
			t.Errorf("isRepeatingChar(%q) = %v, expected %v", tt.input, result, tt.expected)
		}
	}
}
