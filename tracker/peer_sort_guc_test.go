package tracker

// GUC test coverage for TopKPeerEntries (peer_sort.go).
//
// Analytical lenses:
//   Knuth  (~5): algorithmic correctness, loop invariants, data-structure invariants
//   Turing (~5): termination conditions, halting behaviour
//   Church (~5): functional purity, side-effect isolation, referential transparency
//   Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// makePeers builds a slice of n PeerEntry values with IPs 10.0.x.y and ports 1000+i.
func makePeers(n int) []PeerEntry {
	peers := make([]PeerEntry, n)
	for i := 0; i < n; i++ {
		peers[i] = PeerEntry{
			IP:   net.IPv4(10, byte((i>>8)&0xFF), byte(i&0xFF), 1),
			Port: uint16(1000 + (i % 60000)),
		}
	}
	return peers
}

// identityScore returns the score as the float64 cast of the last octet of the
// peer's IP address, giving a deterministic ordering from 0..255.
func identityScore(p PeerEntry) float64 {
	if p.IP == nil {
		return 0
	}
	v4 := p.IP.To4()
	if v4 == nil {
		return 0
	}
	return float64(v4[2])*256 + float64(v4[3])
}

// portScore returns the score as float64(port).
func portScore(p PeerEntry) float64 {
	return float64(p.Port)
}

// fixedScore always returns the same constant — used for equal-scores tests.
func fixedScore(_ PeerEntry) float64 {
	return 42.0
}

// peKey returns a string key for a PeerEntry for uniqueness checks.
func peKey(p PeerEntry) string {
	return fmt.Sprintf("%s:%d", p.IP.String(), p.Port)
}

// gucMinInt returns the smaller of a and b (local to GUC tests).
func gucMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── Knuth: algorithmic correctness ────────────────────────────────────────────

// TestGUC_Knuth_KZeroReturnsEmpty verifies that k=0 returns nil/empty.
func TestGUC_Knuth_KZeroReturnsEmpty(t *testing.T) {
	peers := makePeers(10)
	result := TopKPeerEntries(peers, 0, portScore)
	if len(result) != 0 {
		t.Errorf("k=0: expected empty result, got %d entries", len(result))
	}
}

// TestGUC_Knuth_KGreaterThanNReturnsAll verifies that k>n returns all n entries.
func TestGUC_Knuth_KGreaterThanNReturnsAll(t *testing.T) {
	var tests = []struct {
		n int
		k int
	}{
		{n: 5, k: 10},
		{n: 1, k: 100},
		{n: 0, k: 5},
		{n: 3, k: 3},
	}
	for _, tc := range tests {
		peers := makePeers(tc.n)
		result := TopKPeerEntries(peers, tc.k, portScore)
		want := gucMinInt(tc.k, tc.n)
		if len(result) != want {
			t.Errorf("n=%d k=%d: expected %d entries, got %d", tc.n, tc.k, want, len(result))
		}
	}
}

// TestGUC_Knuth_LengthEqualsMinKN verifies the invariant len(result)==min(k,n).
func TestGUC_Knuth_LengthEqualsMinKN(t *testing.T) {
	var tests = []struct{ n, k int }{
		{100, 50},
		{50, 100},
		{50, 50},
		{1000, 1},
		{10000, 50},
	}
	for _, tc := range tests {
		peers := makePeers(tc.n)
		result := TopKPeerEntries(peers, tc.k, portScore)
		want := gucMinInt(tc.k, tc.n)
		if len(result) != want {
			t.Errorf("n=%d k=%d: expected len=%d, got len=%d", tc.n, tc.k, want, len(result))
		}
	}
}

// TestGUC_Knuth_TopKContainsHighestScores verifies that the returned entries have
// the highest scores in the input set.
func TestGUC_Knuth_TopKContainsHighestScores(t *testing.T) {
	// Build 100 peers with scores 0..99 (portScore = 1000+i, so deterministic).
	const n, k = 100, 10
	peers := make([]PeerEntry, n)
	for i := 0; i < n; i++ {
		peers[i] = PeerEntry{IP: net.IPv4(10, 0, 0, 1), Port: uint16(i)}
	}
	score := func(p PeerEntry) float64 { return float64(p.Port) }
	result := TopKPeerEntries(peers, k, score)

	if len(result) != k {
		t.Fatalf("expected %d results, got %d", k, len(result))
	}
	// Every result port must be >= 90 (top 10 of 0..99).
	for _, r := range result {
		if r.Port < uint16(n-k) {
			t.Errorf("entry with port %d is not in the top-%d", r.Port, k)
		}
	}
}

// TestGUC_Knuth_NoDuplicatesInResult verifies that the result contains no
// duplicate PeerEntry values.
func TestGUC_Knuth_NoDuplicatesInResult(t *testing.T) {
	peers := makePeers(200)
	result := TopKPeerEntries(peers, 50, portScore)

	seen := make(map[string]bool, len(result))
	for _, p := range result {
		key := peKey(p)
		if seen[key] {
			t.Errorf("duplicate entry in result: %s", key)
		}
		seen[key] = true
	}
}

// ── Turing: termination conditions ────────────────────────────────────────────

// TestGUC_Turing_EmptyInputEmptyResult verifies that an empty peer slice
// terminates immediately and returns an empty/nil slice.
func TestGUC_Turing_EmptyInputEmptyResult(t *testing.T) {
	result := TopKPeerEntries([]PeerEntry{}, 10, portScore)
	if len(result) != 0 {
		t.Errorf("empty input: expected empty result, got %d entries", len(result))
	}
}

// TestGUC_Turing_SingleElementKOne verifies that a single-element input with k=1
// terminates and returns exactly that element.
func TestGUC_Turing_SingleElementKOne(t *testing.T) {
	peer := PeerEntry{IP: net.IPv4(192, 168, 1, 1), Port: 6881}
	result := TopKPeerEntries([]PeerEntry{peer}, 1, portScore)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Port != peer.Port {
		t.Errorf("expected port %d, got %d", peer.Port, result[0].Port)
	}
}

// TestGUC_Turing_KOneReturnMaximum verifies that k=1 always returns the element
// with the maximum score.
func TestGUC_Turing_KOneReturnMaximum(t *testing.T) {
	peers := make([]PeerEntry, 500)
	for i := 0; i < 500; i++ {
		peers[i] = PeerEntry{IP: net.IPv4(10, 0, 0, 1), Port: uint16(i + 1)}
	}
	// max port = 500, score = port value
	score := func(p PeerEntry) float64 { return float64(p.Port) }
	result := TopKPeerEntries(peers, 1, score)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Port != 500 {
		t.Errorf("k=1 should return port 500 (max), got %d", result[0].Port)
	}
}

// TestGUC_Turing_NegativeScoresHandled verifies that the algorithm terminates
// correctly and returns the top-k elements when scores include negative values.
func TestGUC_Turing_NegativeScoresHandled(t *testing.T) {
	peers := make([]PeerEntry, 20)
	for i := 0; i < 20; i++ {
		peers[i] = PeerEntry{IP: net.IPv4(10, 0, 0, 1), Port: uint16(i)}
	}
	// Scores go from -10 to 9.
	score := func(p PeerEntry) float64 { return float64(p.Port) - 10 }
	result := TopKPeerEntries(peers, 5, score)
	if len(result) != 5 {
		t.Fatalf("expected 5 results, got %d", len(result))
	}
	// Top 5 are ports 15..19 (scores 5..9).
	for _, r := range result {
		if r.Port < 15 {
			t.Errorf("entry with port %d should not be in top-5 under negative-biased scores", r.Port)
		}
	}
}

// TestGUC_Turing_LargeNSmallK_CorrectSubset verifies n=10000, k=50 returns the
// correct top-50 subset and the function terminates.
func TestGUC_Turing_LargeNSmallK_CorrectSubset(t *testing.T) {
	const n, k = 10000, 50
	peers := make([]PeerEntry, n)
	for i := 0; i < n; i++ {
		peers[i] = PeerEntry{IP: net.IPv4(10, 0, 0, 1), Port: uint16(i % 65535)}
	}
	score := func(p PeerEntry) float64 { return float64(p.Port) }
	result := TopKPeerEntries(peers, k, score)

	if len(result) != k {
		t.Fatalf("expected %d results, got %d", k, len(result))
	}
	// All results should have Port >= 65485 (top 50 port values mod 65535 within 0..9999).
	// The highest port among the input is 9999 (i=9999, 9999 % 65535 == 9999).
	// Top 50: ports 9950..9999.
	for _, r := range result {
		if r.Port < uint16(n-k) {
			t.Errorf("port %d is below expected top-%d threshold", r.Port, k)
		}
	}
}

// ── Church: functional purity ─────────────────────────────────────────────────

// TestGUC_Church_InputNotMutated verifies that TopKPeerEntries does not mutate
// the original peers slice.
func TestGUC_Church_InputNotMutated(t *testing.T) {
	const n = 100
	peers := make([]PeerEntry, n)
	for i := 0; i < n; i++ {
		peers[i] = PeerEntry{IP: net.IPv4(10, 0, 0, 1), Port: uint16(i)}
	}
	// Capture a snapshot of the original order.
	snapshot := make([]PeerEntry, n)
	copy(snapshot, peers)

	_ = TopKPeerEntries(peers, 10, portScore)

	for i, orig := range snapshot {
		if peers[i].Port != orig.Port {
			t.Errorf("input mutated at index %d: expected port %d, got %d", i, orig.Port, peers[i].Port)
		}
	}
}

// TestGUC_Church_Idempotent verifies that calling TopKPeerEntries twice with the
// same inputs produces equal results (referential transparency).
func TestGUC_Church_Idempotent(t *testing.T) {
	peers := makePeers(200)
	score := func(p PeerEntry) float64 { return float64(p.Port) }

	r1 := TopKPeerEntries(peers, 20, score)
	r2 := TopKPeerEntries(peers, 20, score)

	if len(r1) != len(r2) {
		t.Fatalf("idempotency: first call returned %d, second %d", len(r1), len(r2))
	}
	for i := range r1 {
		if peKey(r1[i]) != peKey(r2[i]) {
			t.Errorf("idempotency: position %d differs between calls: %s vs %s", i, peKey(r1[i]), peKey(r2[i]))
		}
	}
}

// TestGUC_Church_ScoreFunctionCalledForEachPeer verifies that the score function
// is called at least once per peer and that its call count is bounded (no
// explosive recomputation), confirming O(n log k) behaviour by proxy.
func TestGUC_Church_ScoreFunctionCalledForEachPeer(t *testing.T) {
	const n, k = 500, 10
	peers := makePeers(n)

	calls := 0
	score := func(p PeerEntry) float64 {
		calls++
		return float64(p.Port)
	}

	_ = TopKPeerEntries(peers, k, score)

	// Must be called at least n times (once per peer).
	if calls < n {
		t.Errorf("score function called only %d times for %d peers (expected >= %d)", calls, n, n)
	}
	// Sanity upper bound: heap-based O(n log k) should not exceed n * 4 * log2(n).
	maxCalls := n * 50 // very generous upper bound
	if calls > maxCalls {
		t.Errorf("score function called %d times — unexpectedly high (n=%d)", calls, n)
	}
}

// TestGUC_Church_AllEqualScoresReturnsKEntries verifies that when all peers have
// equal scores, exactly k entries are returned.
func TestGUC_Church_AllEqualScoresReturnsKEntries(t *testing.T) {
	var tests = []struct{ n, k int }{
		{10, 5},
		{100, 50},
		{5, 10}, // k > n: expect n
		{1, 1},
	}
	for _, tc := range tests {
		peers := makePeers(tc.n)
		result := TopKPeerEntries(peers, tc.k, fixedScore)
		want := gucMinInt(tc.k, tc.n)
		if len(result) != want {
			t.Errorf("all-equal n=%d k=%d: expected %d entries, got %d", tc.n, tc.k, want, len(result))
		}
	}
}

// TestGUC_Church_ResultContainsOnlyInputElements verifies that every element in
// the result was present in the input — no fabricated entries.
func TestGUC_Church_ResultContainsOnlyInputElements(t *testing.T) {
	peers := makePeers(300)
	inputSet := make(map[string]bool, len(peers))
	for _, p := range peers {
		inputSet[peKey(p)] = true
	}

	result := TopKPeerEntries(peers, 75, portScore)
	for _, r := range result {
		if !inputSet[peKey(r)] {
			t.Errorf("result entry %s was not in the input set", peKey(r))
		}
	}
}

// ── Gödel: formal consistency & invariant preservation ────────────────────────

// TestGUC_Godel_ResultLengthNeverExceedsMinKN checks the hard invariant
// len(result) <= min(k, n) across a wide range of inputs.
func TestGUC_Godel_ResultLengthNeverExceedsMinKN(t *testing.T) {
	var tests = []struct{ n, k int }{
		{0, 0},
		{0, 10},
		{10, 0},
		{1, 1},
		{50, 50},
		{100, 50},
		{50, 100},
		{1000, 200},
	}
	for _, tc := range tests {
		peers := makePeers(tc.n)
		result := TopKPeerEntries(peers, tc.k, portScore)
		upper := gucMinInt(tc.k, tc.n)
		if len(result) > upper {
			t.Errorf("n=%d k=%d: result length %d exceeds min(k,n)=%d", tc.n, tc.k, len(result), upper)
		}
	}
}

// TestGUC_Godel_TopKScoresAreHigherThanRemainder verifies the core ordering
// invariant: every element in the result has a score >= any element not in the
// result.
func TestGUC_Godel_TopKScoresAreHigherThanRemainder(t *testing.T) {
	const n, k = 200, 30
	peers := make([]PeerEntry, n)
	for i := 0; i < n; i++ {
		peers[i] = PeerEntry{IP: net.IPv4(10, 0, 0, 1), Port: uint16(i)}
	}
	score := func(p PeerEntry) float64 { return float64(p.Port) }

	result := TopKPeerEntries(peers, k, score)
	if len(result) != k {
		t.Fatalf("expected %d results, got %d", k, len(result))
	}

	// Find the minimum score in the result.
	minResultScore := score(result[0])
	for _, r := range result[1:] {
		if s := score(r); s < minResultScore {
			minResultScore = s
		}
	}

	// Build a set of result keys to detect non-result entries.
	resultSet := make(map[string]bool, len(result))
	for _, r := range result {
		resultSet[peKey(r)] = true
	}

	// Every non-result peer must have score <= minResultScore.
	for _, p := range peers {
		if !resultSet[peKey(p)] && score(p) > minResultScore {
			t.Errorf("non-result peer with port %d has score %.0f > minResultScore %.0f",
				p.Port, score(p), minResultScore)
		}
	}
}

// TestGUC_Godel_DescendingOrderInvariant verifies that the result slice is
// returned in strictly non-increasing score order.
func TestGUC_Godel_DescendingOrderInvariant(t *testing.T) {
	peers := make([]PeerEntry, 100)
	for i := 0; i < 100; i++ {
		peers[i] = PeerEntry{IP: net.IPv4(10, 0, 0, 1), Port: uint16(i)}
	}
	score := func(p PeerEntry) float64 { return float64(p.Port) }
	result := TopKPeerEntries(peers, 20, score)

	for i := 1; i < len(result); i++ {
		if score(result[i]) > score(result[i-1]) {
			t.Errorf("result is not in descending order at index %d: score %.0f > %.0f",
				i, score(result[i]), score(result[i-1]))
		}
	}
}

// TestGUC_Godel_HeapPropertyMaintained verifies the heap invariant across a
// large input: the minimum score in the result is >= maximum score outside it.
func TestGUC_Godel_HeapPropertyMaintained(t *testing.T) {
	const n, k = 5000, 100
	peers := make([]PeerEntry, n)
	for i := 0; i < n; i++ {
		peers[i] = PeerEntry{IP: net.IPv4(10, 0, 0, 1), Port: uint16(i % 60000)}
	}
	score := func(p PeerEntry) float64 { return float64(p.Port) }
	result := TopKPeerEntries(peers, k, score)

	if len(result) != k {
		t.Fatalf("expected %d results, got %d", k, len(result))
	}

	resultSet := make(map[string]bool, k)
	for _, r := range result {
		resultSet[peKey(r)] = true
	}

	// minScore in result
	minScore := score(result[0])
	for _, r := range result {
		if s := score(r); s < minScore {
			minScore = s
		}
	}
	// maxScore outside result
	maxOutside := -1e18
	for _, p := range peers {
		if !resultSet[peKey(p)] {
			if s := score(p); s > maxOutside {
				maxOutside = s
			}
		}
	}
	if maxOutside > minScore {
		t.Errorf("heap invariant violated: max score outside result (%.0f) > min score in result (%.0f)",
			maxOutside, minScore)
	}
}

// TestGUC_Godel_PerformanceN50000K100 verifies that TopKPeerEntries completes
// within 100 ms for n=50000 peers and k=100.
func TestGUC_Godel_PerformanceN50000K100(t *testing.T) {
	const n, k = 50000, 100
	peers := makePeers(n)
	score := func(p PeerEntry) float64 { return float64(p.Port) }

	start := time.Now()
	result := TopKPeerEntries(peers, k, score)
	elapsed := time.Since(start)

	if len(result) != k {
		t.Fatalf("expected %d results, got %d", k, len(result))
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("TopKPeerEntries(n=%d, k=%d) took %v, want <100ms", n, k, elapsed)
	}
}
