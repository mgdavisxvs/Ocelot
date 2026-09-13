package tracker

import (
	"testing"
)

func TestFisherYatesShuffle(t *testing.T) {
	// Create test peers
	peers := make([]*Peer, 10)
	for i := range peers {
		peers[i] = &Peer{
			PeerID: []byte{byte(i)},
		}
	}

	// Shuffle multiple times and verify randomness
	originalOrder := make([]byte, len(peers))
	for i := range peers {
		originalOrder[i] = peers[i].PeerID[0]
	}

	// Shuffle
	FisherYatesShuffle(peers)

	// Verify all peers still present
	found := make(map[byte]bool)
	for _, p := range peers {
		found[p.PeerID[0]] = true
	}

	if len(found) != 10 {
		t.Errorf("Expected 10 unique peers after shuffle, got %d", len(found))
	}

	// Verify order changed (with 99.9% probability)
	changed := false
	for i := range peers {
		if peers[i].PeerID[0] != originalOrder[i] {
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
			PeerID: []byte{byte(i)},
		}
	}

	// Sample 10 peers
	sample := ReservoirSample(peers, 10)

	if len(sample) != 10 {
		t.Errorf("Expected 10 peers in sample, got %d", len(sample))
	}

	// Verify all sampled peers are unique
	seen := make(map[byte]bool)
	for _, p := range sample {
		if seen[p.PeerID[0]] {
			t.Error("Duplicate peer in reservoir sample")
		}
		seen[p.PeerID[0]] = true
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
		name       string
		seeders    int
		leechers   int
		baseInt    int
		expectMin  int32
		expectMax  int32
	}{
		{
			name:      "Small swarm",
			seeders:   5,
			leechers:  5,
			baseInt:   1800,
			expectMin: 600,
			expectMax: 900,
		},
		{
			name:      "Medium swarm",
			seeders:   50,
			leechers:  50,
			baseInt:   1800,
			expectMin: 1000,
			expectMax: 1500,
		},
		{
			name:      "Large swarm",
			seeders:   200,
			leechers:  100,
			baseInt:   1800,
			expectMin: 1800,
			expectMax: 3000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval := AdaptiveInterval(tt.seeders, tt.leechers, tt.baseInt)

			if interval < tt.expectMin || interval > tt.expectMax {
				t.Errorf("Interval %d out of expected range [%d, %d]",
					interval, tt.expectMin, tt.expectMax)
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
		peers[i] = &Peer{PeerID: []byte{byte(i)}}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FisherYatesShuffle(peers)
	}
}

func BenchmarkReservoirSample(b *testing.B) {
	peers := make([]*Peer, 1000)
	for i := range peers {
		peers[i] = &Peer{PeerID: []byte{byte(i % 256)}}
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
