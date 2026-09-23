package tracker

import (
	"net"
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

// ── CompactIPv6Port ───────────────────────────────────────────────────────────

func TestCompactIPv6Port_ValidIPv6(t *testing.T) {
	ip := net.ParseIP("2001:db8::1")
	result := CompactIPv6Port(ip, 6881)
	if len(result) != 18 {
		t.Fatalf("expected 18 bytes, got %d", len(result))
	}
	port := uint16(result[16])<<8 | uint16(result[17])
	if port != 6881 {
		t.Errorf("port = %d, want 6881", port)
	}
}

func TestCompactIPv6Port_IPv4RejectsNil(t *testing.T) {
	ip := net.ParseIP("192.168.1.1")
	if result := CompactIPv6Port(ip, 80); result != nil {
		t.Errorf("expected nil for IPv4 address, got %v", result)
	}
}

func TestCompactIPv6Port_NilIP(t *testing.T) {
	if result := CompactIPv6Port(nil, 80); result != nil {
		t.Errorf("expected nil for nil IP, got %v", result)
	}
}

// ── BuildTrieFromSlice ────────────────────────────────────────────────────────

func TestBuildTrieFromSlice_AllowsMatchingPrefixes(t *testing.T) {
	trie := BuildTrieFromSlice([]string{"-TR3000-", "-qB4420-"})
	if !trie.IsAllowed([]byte("-TR3000-xxxxxxxxxxxx")) {
		t.Error("expected -TR3000- prefix to be allowed")
	}
	if !trie.IsAllowed([]byte("-qB4420-xxxxxxxxxxxx")) {
		t.Error("expected -qB4420- prefix to be allowed")
	}
}

func TestBuildTrieFromSlice_RejectsUnknown(t *testing.T) {
	trie := BuildTrieFromSlice([]string{"-TR3000-"})
	if trie.IsAllowed([]byte("-XX0000-xxxxxxxxxxxx")) {
		t.Error("expected unknown prefix to be rejected")
	}
}

func TestBuildTrieFromSlice_Empty(t *testing.T) {
	// Per the implementation, an empty whitelist means "allow all".
	trie := BuildTrieFromSlice(nil)
	if !trie.IsAllowed([]byte("-TR3000-xxxxxxxxxxxx")) {
		t.Error("empty trie (no restrictions) should allow all peers")
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

// ── ValidateIPNotPrivate ──────────────────────────────────────────────────────

func TestValidateIPNotPrivate_NilIP(t *testing.T) {
	if ValidateIPNotPrivate(nil) {
		t.Error("nil IP should return false")
	}
}

func TestValidateIPNotPrivate_IPv6(t *testing.T) {
	if !ValidateIPNotPrivate(net.ParseIP("2001:db8::1")) {
		t.Error("IPv6 address should return true")
	}
}

func TestValidateIPNotPrivate_PublicIP(t *testing.T) {
	if !ValidateIPNotPrivate(net.ParseIP("8.8.8.8")) {
		t.Error("public IP 8.8.8.8 should return true")
	}
}

func TestValidateIPNotPrivate_PrivateRanges(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"0.1.2.3", false},       // 0.0.0.0/8
		{"10.0.0.1", false},      // 10.0.0.0/8
		{"127.0.0.1", false},     // loopback
		{"169.254.1.1", false},   // link-local
		{"172.16.0.1", false},    // 172.16.0.0/12
		{"172.31.255.255", false}, // top of 172.16/12
		{"192.168.1.1", false},   // 192.168.0.0/16
		{"224.0.0.1", false},     // multicast
		{"255.255.255.255", false}, // broadcast
		{"1.2.3.4", true},        // public
	}
	for _, c := range cases {
		got := ValidateIPNotPrivate(net.ParseIP(c.ip))
		if got != c.want {
			t.Errorf("ValidateIPNotPrivate(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

// ── PeerKeyPrime ──────────────────────────────────────────────────────────────

func TestPeerKeyPrime_ShortPeerID_ReturnsAsIs(t *testing.T) {
	short := []byte("tooshort")
	result := PeerKeyPrime(short, 1, 1)
	if result != string(short) {
		t.Errorf("short peerID: got %q, want %q", result, string(short))
	}
}

func TestPeerKeyPrime_FullPeerID_ProducesLongerKey(t *testing.T) {
	peerID := []byte("-qB40000000000000000") // exactly 20 bytes
	result := PeerKeyPrime(peerID, 42, 100)
	if len(result) <= len(peerID) {
		t.Errorf("full peerID key length = %d, expected > %d", len(result), len(peerID))
	}
}

// ── IsAllowed — exact-length peerID covers end-of-loop return ────────────────

func TestIsAllowed_ExactPrefixLength_AllowedViaEndReturn(t *testing.T) {
	// peerID exactly matches the stored prefix; the loop exhausts the peerID
	// without hitting the inner isPrefix check, so `return node.isPrefix` fires.
	trie := BuildTrieFromSlice([]string{"-TR"})
	if !trie.IsAllowed([]byte("-TR")) {
		t.Error("peerID that exactly equals a stored prefix should be allowed")
	}
}
