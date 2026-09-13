package ml

import (
	"net"
	"testing"
	"time"
)

func TestPeerScorerCreation(t *testing.T) {
	ps := NewPeerScorer()

	if ps == nil {
		t.Fatal("NewPeerScorer returned nil")
	}

	// Verify weights sum to approximately 1.0
	sum := 0.0
	for _, weight := range ps.weights {
		sum += weight
	}

	if sum < 0.99 || sum > 1.01 {
		t.Errorf("Expected weights to sum to ~1.0, got %.2f", sum)
	}
}

func TestPeerScorerBasicScoring(t *testing.T) {
	ps := NewPeerScorer()

	now := time.Now()
	peer := &PeerInfo{
		IP:                net.ParseIP("192.168.1.1"),
		Port:              6881,
		Uploaded:          1000000, // 1 MB
		Downloaded:        500000,
		FirstSeen:         now.Add(-24 * time.Hour), // 1 day ago
		LastAnnounce:      now.Add(-5 * time.Minute),
		CompletedSessions: 10,
		TotalSessions:     12,
	}

	requesterIP := net.ParseIP("192.168.1.2")

	score := ps.Score(peer, requesterIP)

	// Score should be between 0 and 1
	if score < 0 || score > 1 {
		t.Errorf("Expected score between 0 and 1, got %.4f", score)
	}

	// Good peer should have score > 0.5
	if score < 0.5 {
		t.Errorf("Expected good peer to score > 0.5, got %.4f", score)
	}
}

func TestPeerScorerComparison(t *testing.T) {
	ps := NewPeerScorer()

	now := time.Now()
	requesterIP := net.ParseIP("192.168.1.100")

	// High-quality peer
	goodPeer := &PeerInfo{
		IP:                net.ParseIP("192.168.1.101"),
		Uploaded:          10000000, // 10 MB
		Downloaded:        1000000,
		FirstSeen:         now.Add(-7 * 24 * time.Hour), // 1 week ago
		LastAnnounce:      now.Add(-1 * time.Minute),
		CompletedSessions: 100,
		TotalSessions:     105,
	}

	// Low-quality peer
	badPeer := &PeerInfo{
		IP:                net.ParseIP("10.0.0.1"),
		Uploaded:          100,
		Downloaded:        5000000,
		FirstSeen:         now.Add(-1 * time.Hour),
		LastAnnounce:      now.Add(-30 * time.Minute),
		CompletedSessions: 1,
		TotalSessions:     10,
	}

	goodScore := ps.Score(goodPeer, requesterIP)
	badScore := ps.Score(badPeer, requesterIP)

	if goodScore <= badScore {
		t.Errorf("Expected good peer (%.4f) to score higher than bad peer (%.4f)",
			goodScore, badScore)
	}
}

func TestPeerScorerSelectBest(t *testing.T) {
	ps := NewPeerScorer()

	now := time.Now()
	requesterIP := net.ParseIP("192.168.1.1")

	peers := []*PeerInfo{
		{
			IP:                net.ParseIP("192.168.1.10"),
			Uploaded:          5000000,
			FirstSeen:         now.Add(-5 * 24 * time.Hour),
			LastAnnounce:      now.Add(-2 * time.Minute),
			CompletedSessions: 50,
			TotalSessions:     55,
		},
		{
			IP:                net.ParseIP("192.168.1.20"),
			Uploaded:          1000000,
			FirstSeen:         now.Add(-1 * 24 * time.Hour),
			LastAnnounce:      now.Add(-10 * time.Minute),
			CompletedSessions: 10,
			TotalSessions:     15,
		},
		{
			IP:                net.ParseIP("192.168.1.30"),
			Uploaded:          10000000,
			FirstSeen:         now.Add(-10 * 24 * time.Hour),
			LastAnnounce:      now.Add(-1 * time.Minute),
			CompletedSessions: 100,
			TotalSessions:     102,
		},
	}

	// Select best 2 peers
	best := ps.SelectBest(peers, requesterIP, 2)

	if len(best) != 2 {
		t.Fatalf("Expected 2 peers, got %d", len(best))
	}

	// First peer should be the one with highest upload and completion rate
	if best[0].IP.String() != "192.168.1.30" {
		t.Errorf("Expected best peer to be 192.168.1.30, got %s", best[0].IP.String())
	}
}

func TestPeerScorerSelectBestLimitedPeers(t *testing.T) {
	ps := NewPeerScorer()

	requesterIP := net.ParseIP("192.168.1.1")
	now := time.Now()

	peers := []*PeerInfo{
		{
			IP:           net.ParseIP("192.168.1.10"),
			Uploaded:     1000000,
			FirstSeen:    now.Add(-24 * time.Hour),
			LastAnnounce: now,
		},
		{
			IP:           net.ParseIP("192.168.1.20"),
			Uploaded:     2000000,
			FirstSeen:    now.Add(-24 * time.Hour),
			LastAnnounce: now,
		},
	}

	// Request more peers than available
	best := ps.SelectBest(peers, requesterIP, 10)

	if len(best) != 2 {
		t.Errorf("Expected 2 peers (all available), got %d", len(best))
	}
}

func TestSwarmHealthPredictorCreation(t *testing.T) {
	shp := NewSwarmHealthPredictor()

	if shp == nil {
		t.Fatal("NewSwarmHealthPredictor returned nil")
	}
}

func TestSwarmHealthScore(t *testing.T) {
	shp := NewSwarmHealthPredictor()

	tests := []struct {
		name     string
		seeders  int
		leechers int
		expected int
	}{
		{
			name:     "No seeders (dead swarm)",
			seeders:  0,
			leechers: 10,
			expected: 0,
		},
		{
			name:     "All seeders (perfect health)",
			seeders:  10,
			leechers: 0,
			expected: 100,
		},
		{
			name:     "Equal seeds and leeches",
			seeders:  10,
			leechers: 10,
			expected: 100,
		},
		{
			name:     "More seeders than leechers",
			seeders:  20,
			leechers: 10,
			expected: 100,
		},
		{
			name:     "Fewer seeders (poor health)",
			seeders:  1,
			leechers: 20,
			expected: 25, // Low ratio
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := shp.HealthScore(tt.seeders, tt.leechers)

			// Allow some tolerance
			diff := score - tt.expected
			if diff < -10 || diff > 10 {
				t.Errorf("HealthScore(%d, %d) = %d, expected ~%d",
					tt.seeders, tt.leechers, score, tt.expected)
			}
		})
	}
}

func TestSwarmPredictCompletionTime(t *testing.T) {
	shp := NewSwarmHealthPredictor()

	// Scenario: 10 seeders, 5 leechers, 1 MB/s upload, 100 MB torrent
	seeders := 10
	leechers := 5
	avgUploadSpeed := 1000000.0 // 1 MB/s
	torrentSize := 100000000.0  // 100 MB

	duration := shp.PredictCompletionTime(seeders, leechers, avgUploadSpeed, torrentSize)

	// Expected: (5 * 100MB) / (10 * 1MB/s) = 50 seconds
	expectedSeconds := 50.0
	actualSeconds := duration.Seconds()

	if actualSeconds < expectedSeconds*0.9 || actualSeconds > expectedSeconds*1.1 {
		t.Errorf("Expected ~%.0f seconds, got %.0f", expectedSeconds, actualSeconds)
	}
}

func TestSwarmPredictCompletionTimeNoSeeders(t *testing.T) {
	shp := NewSwarmHealthPredictor()

	duration := shp.PredictCompletionTime(0, 10, 1000000.0, 100000000.0)

	// Should return max duration (never completes)
	if duration.Seconds() < float64(time.Hour.Seconds()*24*365) {
		t.Error("Expected very long duration for swarm with no seeders")
	}
}

func TestIPDistance(t *testing.T) {
	tests := []struct {
		name     string
		ip1      string
		ip2      string
		expected int
	}{
		{
			name:     "Same IP",
			ip1:      "192.168.1.1",
			ip2:      "192.168.1.1",
			expected: 0,
		},
		{
			name:     "Same subnet (first octet)",
			ip1:      "192.168.1.1",
			ip2:      "192.168.2.1",
			expected: 0,
		},
		{
			name:     "Different first octet",
			ip1:      "192.168.1.1",
			ip2:      "10.0.0.1",
			expected: 202, // 192 XOR 10
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip1 := net.ParseIP(tt.ip1)
			ip2 := net.ParseIP(tt.ip2)

			distance := ipDistance(ip1, ip2)

			if distance != tt.expected {
				t.Errorf("ipDistance(%s, %s) = %d, expected %d",
					tt.ip1, tt.ip2, distance, tt.expected)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		value    float64
		minVal   float64
		maxVal   float64
		expected float64
	}{
		{50, 0, 100, 0.5},
		{0, 0, 100, 0.0},
		{100, 0, 100, 1.0},
		{75, 50, 100, 0.5},
	}

	for _, tt := range tests {
		result := normalize(tt.value, tt.minVal, tt.maxVal)

		if result != tt.expected {
			t.Errorf("normalize(%.1f, %.1f, %.1f) = %.2f, expected %.2f",
				tt.value, tt.minVal, tt.maxVal, result, tt.expected)
		}
	}
}
