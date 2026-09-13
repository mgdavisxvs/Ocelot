package tracker

import (
	"testing"
)

func TestFisherYatesShuffle(t *testing.T) {
	// Create test peers
	peers := make([]*Peer, 10)
	for i := range peers {
		peers[i] = &Peer{
			UserID: UserID(i),
			Port:   uint16(6881 + i),
		}
	}

	// Shuffle multiple times and verify randomness
	originalOrder := make([]UserID, len(peers))
	for i := range peers {
		originalOrder[i] = peers[i].UserID
	}

	// Shuffle
	FisherYatesShuffle(peers)

	// Verify all peers still present
	found := make(map[UserID]bool)
	for _, p := range peers {
		found[p.UserID] = true
	}

	if len(found) != 10 {
		t.Errorf("Expected 10 unique peers after shuffle, got %d", len(found))
	}

	// Verify order changed (with 99.9% probability)
	changed := false
	for i := range peers {
		if peers[i].UserID != originalOrder[i] {
			changed = true
			break
		}
	}

	if !changed {
		t.Log("Warning: Shuffle did not change order (rare but possible)")
	}
}

func TestReservoirSample(t *testing.T) {
	// Create 100 test peers
	peers := make([]*Peer, 100)
	for i := range peers {
		peers[i] = &Peer{
			UserID: UserID(i),
			Port:   uint16(6881 + (i % 256)),
		}
	}

	// Sample 10 peers
	sample := ReservoirSample(peers, 10)

	if len(sample) != 10 {
		t.Errorf("Expected 10 peers in sample, got %d", len(sample))
	}

	// Verify all sampled peers are unique
	seen := make(map[UserID]bool)
	for _, p := range sample {
		if seen[p.UserID] {
			t.Error("Duplicate peer in reservoir sample")
		}
		seen[p.UserID] = true
	}

	// Test edge cases
	t.Run("Sample size 0", func(t *testing.T) {
		sample := ReservoirSample(peers, 0)
		if len(sample) != 0 {
			t.Errorf("Expected empty sample, got %d peers", len(sample))
		}
	})

	t.Run("Sample size exceeds peer count", func(t *testing.T) {
		sample := ReservoirSample(peers, 200)
		if len(sample) != 100 {
			t.Errorf("Expected all 100 peers, got %d", len(sample))
		}
	})
}

func TestAdaptiveInterval(t *testing.T) {
	tests := []struct {
		name     string
		seeders  int
		leechers int
		baseInt  int
		expected int32
	}{
		{
			name:     "Very small swarm",
			seeders:  2,
			leechers: 3,
			baseInt:  1800,
			expected: 600, // < 10 peers
		},
		{
			name:     "Small swarm",
			seeders:  5,
			leechers: 5,
			baseInt:  1800,
			expected: 1200, // 10 peers, not < 10, so second tier
		},
		{
			name:     "Medium swarm",
			seeders:  30,
			leechers: 40,
			baseInt:  1800,
			expected: 1200, // 70 peers, < 100
		},
		{
			name:     "Large swarm",
			seeders:  60,
			leechers: 40,
			baseInt:  1800,
			expected: 2400, // 100 peers, not < 100, so third tier
		},
		{
			name:     "Huge swarm",
			seeders:  200,
			leechers: 100,
			baseInt:  1800,
			expected: 2400, // 300 peers, >= 100
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval := AdaptiveInterval(tt.seeders, tt.leechers, tt.baseInt)

			if interval != tt.expected {
				t.Errorf("Expected interval %d, got %d",
					tt.expected, interval)
			}
		})
	}
}

func TestWhitelistTrie(t *testing.T) {
	trie := NewWhitelistTrie()

	// Add prefixes
	prefixes := []string{
		"-TR3000-",
		"-qB4420-",
		"-DE13F0-",
	}

	for _, prefix := range prefixes {
		trie.Add(prefix)
	}

	// Test allowed peer IDs
	allowedTests := []struct {
		peerID   string
		expected bool
	}{
		{"-TR3000-abcdefghij12", true},
		{"-qB4420-xyz123456789", true},
		{"-DE13F0-test12345678", true},
		{"-XX0000-notallowed12", false},
		{"random_peer_id_1234", false},
	}

	for _, tt := range allowedTests {
		result := trie.IsAllowed([]byte(tt.peerID))
		if result != tt.expected {
			t.Errorf("IsAllowed(%s) = %v, expected %v",
				tt.peerID, result, tt.expected)
		}
	}
}

func BenchmarkFisherYatesShuffle(b *testing.B) {
	peers := make([]*Peer, 100)
	for i := range peers {
		peers[i] = &Peer{UserID: UserID(i), Port: 6881}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FisherYatesShuffle(peers)
	}
}

func BenchmarkReservoirSample(b *testing.B) {
	peers := make([]*Peer, 1000)
	for i := range peers {
		peers[i] = &Peer{UserID: UserID(i % 256), Port: 6881}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ReservoirSample(peers, 50)
	}
}

func BenchmarkWhitelistTrie(b *testing.B) {
	trie := NewWhitelistTrie()
	trie.Add("-TR3000-")
	trie.Add("-qB4420-")

	peerID := []byte("-TR3000-test1234567")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		trie.IsAllowed(peerID)
	}
}
