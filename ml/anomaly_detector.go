package ml

import (
	"strings"
	"time"
)

// AnomalyDetector detects suspicious peer behavior
type AnomalyDetector struct {
	// Thresholds for anomaly detection
	maxUploadSpeed    float64       // 1 Gbps
	maxAnnounceRate   int           // 100/hour
	maxPortChanges    int           // 10 different ports
	minDownloadRatio  float64       // 0.01 (1%)
	rapidReconnectSec int           // 60 seconds
}

// NewAnomalyDetector creates a new anomaly detector
func NewAnomalyDetector() *AnomalyDetector {
	return &AnomalyDetector{
		maxUploadSpeed:    1e9,  // 1 Gbps
		maxAnnounceRate:   100,  // 100/hour
		maxPortChanges:    10,   // 10 ports
		minDownloadRatio:  0.01, // 1%
		rapidReconnectSec: 60,   // 60 seconds
	}
}

// ThresholdConfig carries externally-supplied threshold overrides.
// A zero value for any numeric field means "keep the current value".
type ThresholdConfig struct {
	MaxUploadSpeed    float64 // bytes/sec; 0 = no change
	MaxAnnounceRate   int     // announces/hour; 0 = no change
	MaxPortChanges    int     // unique ports; 0 = no change
	MinDownloadRatio  float64 // fraction; 0 = no change
	RapidReconnectSec int     // seconds; 0 = no change
}

// Thresholds returns a snapshot of the current threshold configuration.
func (ad *AnomalyDetector) Thresholds() ThresholdConfig {
	return ThresholdConfig{
		MaxUploadSpeed:    ad.maxUploadSpeed,
		MaxAnnounceRate:   ad.maxAnnounceRate,
		MaxPortChanges:    ad.maxPortChanges,
		MinDownloadRatio:  ad.minDownloadRatio,
		RapidReconnectSec: ad.rapidReconnectSec,
	}
}

// SetThresholds updates detection thresholds without recreating the detector.
// Only fields with non-zero values are applied, so partial updates are safe.
func (ad *AnomalyDetector) SetThresholds(t ThresholdConfig) {
	if t.MaxUploadSpeed > 0 {
		ad.maxUploadSpeed = t.MaxUploadSpeed
	}
	if t.MaxAnnounceRate > 0 {
		ad.maxAnnounceRate = t.MaxAnnounceRate
	}
	if t.MaxPortChanges > 0 {
		ad.maxPortChanges = t.MaxPortChanges
	}
	if t.MinDownloadRatio > 0 {
		ad.minDownloadRatio = t.MinDownloadRatio
	}
	if t.RapidReconnectSec > 0 {
		ad.rapidReconnectSec = t.RapidReconnectSec
	}
}

// DetectAnomaly analyzes peer behavior and returns anomaly type if detected
func (ad *AnomalyDetector) DetectAnomaly(behavior *PeerBehavior) (bool, string) {
	// Check for impossible upload speed
	if behavior.UploadSpeed > ad.maxUploadSpeed {
		return true, "impossible_upload_speed"
	}

	// Check for ratio cheating (huge upload, minimal download)
	if behavior.Uploaded > 100e9 && behavior.Downloaded < 1e6 {
		downloadRatio := float64(behavior.Downloaded) / float64(behavior.Uploaded)
		if downloadRatio < ad.minDownloadRatio {
			return true, "ratio_cheating"
		}
	}

	// Check for rapid reconnections (DDoS pattern)
	if behavior.AnnounceCount > 0 {
		duration := time.Since(behavior.FirstSeen).Hours()
		announceRate := int(float64(behavior.AnnounceCount) / max(duration, 1.0))
		if announceRate > ad.maxAnnounceRate {
			return true, "ddos_pattern"
		}
	}

	// Check for port scanning
	if len(behavior.PortHistory) > ad.maxPortChanges {
		return true, "port_scanning"
	}

	// Check for rapid reconnects
	if len(behavior.ConnectionTimes) >= 2 {
		lastTwo := behavior.ConnectionTimes[len(behavior.ConnectionTimes)-2:]
		if lastTwo[1].Sub(lastTwo[0]).Seconds() < float64(ad.rapidReconnectSec) {
			return true, "rapid_reconnect"
		}
	}

	// Check for impossible completion (download > torrent size)
	if behavior.Downloaded > behavior.TorrentSize*2 {
		return true, "impossible_download"
	}

	return false, ""
}

// PeerBehavior holds peer behavior data for analysis
type PeerBehavior struct {
	Uploaded        int64
	Downloaded      int64
	UploadSpeed     float64
	TorrentSize     int64
	AnnounceCount   int
	PortHistory     []uint16
	ConnectionTimes []time.Time
	FirstSeen       time.Time
}

// ClientAnomalyDetector detects suspicious client patterns
type ClientAnomalyDetector struct{}

// NewClientAnomalyDetector creates a new client anomaly detector
func NewClientAnomalyDetector() *ClientAnomalyDetector {
	return &ClientAnomalyDetector{}
}

// DetectClientAnomaly detects suspicious client behavior
func (cad *ClientAnomalyDetector) DetectClientAnomaly(clientID string, userAgent string) (bool, string) {
	// Check for known malicious client patterns
	maliciousPatterns := []string{
		"BitSpirit",  // Known for ratio cheating
		"BitComet",   // Can be used for ratio manipulation
		"XunLei",     // Known for aggressive behavior
		"Thunder",    // Aggressive downloader
	}

	for _, pattern := range maliciousPatterns {
		if contains(userAgent, pattern) {
			return true, "known_malicious_client"
		}
	}

	// Check for spoofed client IDs
	if len(clientID) != 20 {
		return true, "invalid_client_id_length"
	}

	// Check for all-zero or all-same character client ID
	if isRepeatingChar(clientID) {
		return true, "suspicious_client_id_pattern"
	}

	return false, ""
}

// Helper functions

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func isRepeatingChar(s string) bool {
	if len(s) == 0 {
		return false
	}

	firstChar := s[0]
	for i := 1; i < len(s); i++ {
		if s[i] != firstChar {
			return false
		}
	}
	return true
}
