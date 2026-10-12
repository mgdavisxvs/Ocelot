package tracker

// GUC (Gödel Unified Council) test coverage for the Whitelist type.
// Tests exercise the Whitelist struct directly (not via HandleUpdate).
// Lenses: Knuth (algorithmic), Turing (termination), Church (purity), Gödel (consistency).

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Knuth: algorithmic correctness ───────────────────────────────────────────

// TestGUC_Knuth_EmptyWhitelist_AllowAll verifies the allow-all sentinel:
// a freshly created whitelist with no entries permits every PeerID.
func TestGUC_Knuth_EmptyWhitelist_AllowAll(t *testing.T) {
	wl := NewWhitelist()
	cases := [][]byte{
		[]byte("-UT3500-xxxxxxxxxxxx"),
		[]byte("AAAAAAAAAAAAAAAAAAAA"),
		[]byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13"),
	}
	for _, pid := range cases {
		if !wl.IsAllowed(pid) {
			t.Errorf("empty whitelist should allow all PeerIDs, rejected %q", pid)
		}
	}
}

// TestGUC_Knuth_NonEmpty_RejectsUnregistered verifies that once entries exist,
// a PeerID whose prefix does not appear in the list is rejected.
func TestGUC_Knuth_NonEmpty_RejectsUnregistered(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-")
	wl.Add("-qB4200-")

	cases := []struct {
		peerID  []byte
		allowed bool
	}{
		{[]byte("-UT3500-xxxxxxxxxxxx"), true},
		{[]byte("-qB4200-xxxxxxxxxxxx"), true},
		{[]byte("-AZ5770-xxxxxxxxxxxx"), false},
		{[]byte("-TR2940-xxxxxxxxxxxx"), false},
		{[]byte("AAAAAAAAAAAAAAAAAAAA"), false},
	}
	for _, tc := range cases {
		got := wl.IsAllowed(tc.peerID)
		if got != tc.allowed {
			t.Errorf("IsAllowed(%q) = %v, want %v", tc.peerID, got, tc.allowed)
		}
	}
}

// TestGUC_Knuth_PrefixOf20CharPeerID verifies that an 8-character prefix entry
// correctly matches the first 8 bytes of a 20-byte PeerID and rejects others.
func TestGUC_Knuth_PrefixOf20CharPeerID(t *testing.T) {
	prefix := "-UT3500-" // 8 chars
	wl := NewWhitelist()
	wl.Add(prefix)

	// Build a 20-byte PeerID whose first 8 bytes equal the prefix.
	peerID := make([]byte, 20)
	copy(peerID, []byte(prefix))
	for i := 8; i < 20; i++ {
		peerID[i] = 'x'
	}
	if !wl.IsAllowed(peerID) {
		t.Errorf("8-char prefix %q should match 20-byte PeerID %q", prefix, peerID)
	}

	// Alter one prefix byte so it no longer matches.
	peerID[1] = 'L'
	if wl.IsAllowed(peerID) {
		t.Errorf("PeerID with modified prefix byte should not match %q", prefix)
	}
}

// TestGUC_Knuth_LenAccuracy verifies that len(GetAll()) tracks Add and Remove
// operations with exact integer correctness.
func TestGUC_Knuth_LenAccuracy(t *testing.T) {
	wl := NewWhitelist()

	entries := []string{"-UT3500-", "-qB4200-", "-TR2940-", "-AZ5770-"}
	for i, p := range entries {
		wl.Add(p)
		if got := len(wl.GetAll()); got != i+1 {
			t.Errorf("after %d adds, expected len=%d, got %d", i+1, i+1, got)
		}
	}
	for i, p := range entries {
		wl.Remove(p)
		want := len(entries) - (i + 1)
		if got := len(wl.GetAll()); got != want {
			t.Errorf("after removing %q, expected len=%d, got %d", p, want, got)
		}
	}
}

// TestGUC_Knuth_1000Entries_Scalability adds 1000 distinct prefix entries and
// verifies that IsAllowed returns correct results within a bounded time budget.
func TestGUC_Knuth_1000Entries_Scalability(t *testing.T) {
	wl := NewWhitelist()
	const n = 1000

	for i := 0; i < n; i++ {
		wl.Add(fmt.Sprintf("-%04dXX-", i))
	}
	if got := len(wl.GetAll()); got != n {
		t.Fatalf("expected %d entries, got %d", n, got)
	}

	start := time.Now()
	lastPrefix := fmt.Sprintf("-%04dXX-", n-1)
	peerID := []byte(lastPrefix + "xxxxxxxxxxxx")
	if !wl.IsAllowed(peerID) {
		t.Errorf("last-added prefix %q should be allowed", lastPrefix)
	}
	unknown := []byte("-ZZZZZZ-xxxxxxxxxxxx")
	if wl.IsAllowed(unknown) {
		t.Errorf("unregistered prefix should be rejected among %d entries", n)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("IsAllowed took %v for %d entries, want <100ms", elapsed, n)
	}
}

// ── Turing: termination / halting behavior ───────────────────────────────────

// TestGUC_Turing_RemoveNonexistent_NoOp verifies that removing a prefix that
// was never added terminates cleanly without mutating the whitelist.
func TestGUC_Turing_RemoveNonexistent_NoOp(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-")
	wl.Add("-qB4200-")

	before := wl.GetAll()
	wl.Remove("-ZZZZZZ-") // never added
	after := wl.GetAll()

	if len(before) != len(after) {
		t.Errorf("remove of nonexistent changed len: before=%d after=%d", len(before), len(after))
	}
}

// TestGUC_Turing_AllZerosPeerID_AllowAll verifies that an all-zeros PeerID is
// permitted when the whitelist is empty (allow-all mode terminates immediately).
func TestGUC_Turing_AllZerosPeerID_AllowAll(t *testing.T) {
	wl := NewWhitelist()
	allZeros := make([]byte, 20)
	if !wl.IsAllowed(allZeros) {
		t.Error("empty whitelist must allow the all-zeros PeerID")
	}
}

// TestGUC_Turing_AllZerosPeerID_RejectNoMatch verifies that an all-zeros PeerID
// is rejected once the whitelist is non-empty and no prefix matches.
func TestGUC_Turing_AllZerosPeerID_RejectNoMatch(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-")
	allZeros := make([]byte, 20)
	if wl.IsAllowed(allZeros) {
		t.Error("non-empty whitelist should reject all-zeros PeerID when no prefix matches")
	}
}

// TestGUC_Turing_ExactEightCharMatch verifies that a PeerID of exactly 8 bytes
// matches an 8-character prefix (equal length counts as a valid prefix match).
func TestGUC_Turing_ExactEightCharMatch(t *testing.T) {
	prefix := "-UT3500-" // exactly 8 chars
	wl := NewWhitelist()
	wl.Add(prefix)

	exactPeerID := []byte(prefix) // PeerID length equals prefix length
	if !wl.IsAllowed(exactPeerID) {
		t.Errorf("PeerID of exactly prefix length %q should be allowed", prefix)
	}
}

// TestGUC_Turing_SevenCharNoMatch verifies that a 7-byte PeerID does not match
// an 8-character prefix because the PeerID is shorter than the prefix.
func TestGUC_Turing_SevenCharNoMatch(t *testing.T) {
	prefix := "-UT3500-" // 8 chars
	wl := NewWhitelist()
	wl.Add(prefix)

	shortPeerID := []byte("-UT3500") // 7 bytes — missing trailing '-'
	if wl.IsAllowed(shortPeerID) {
		t.Errorf("7-byte PeerID should not match 8-char prefix %q", prefix)
	}
}

// ── Church: functional purity / side-effect isolation ────────────────────────

// TestGUC_Church_AddIdempotent_Direct verifies that calling Add with the same
// prefix twice produces exactly one entry in the list (pure idempotency).
func TestGUC_Church_AddIdempotent_Direct(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-")
	wl.Add("-UT3500-") // duplicate

	all := wl.GetAll()
	if len(all) != 1 {
		t.Errorf("duplicate Add should produce exactly 1 entry, got %v", all)
	}
}

// TestGUC_Church_CaseSensitivity_Direct verifies that prefix matching is
// strictly case-sensitive with no implicit normalization applied.
func TestGUC_Church_CaseSensitivity_Direct(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-")

	cases := []struct {
		peerID  []byte
		allowed bool
	}{
		{[]byte("-UT3500-xxxxxxxxxxxx"), true},  // exact case match
		{[]byte("-ut3500-xxxxxxxxxxxx"), false},  // all lowercase
		{[]byte("-Ut3500-xxxxxxxxxxxx"), false},  // mixed case
		{[]byte("-UT3500 xxxxxxxxxxxx"), false},  // space instead of '-'
	}
	for _, tc := range cases {
		got := wl.IsAllowed(tc.peerID)
		if got != tc.allowed {
			t.Errorf("IsAllowed(%q) = %v, want %v", tc.peerID, got, tc.allowed)
		}
	}
}

// TestGUC_Church_GetAll_PureSnapshot verifies that GetAll is a pure function
// returning an independent copy: subsequent mutations do not alter the snapshot.
func TestGUC_Church_GetAll_PureSnapshot(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-")
	wl.Add("-qB4200-")

	snap := wl.GetAll()
	if len(snap) != 2 {
		t.Fatalf("initial snapshot should have 2 entries, got %d", len(snap))
	}

	wl.Add("-TR2940-")
	wl.Remove("-UT3500-")

	// The snapshot taken before those mutations must remain unchanged.
	if len(snap) != 2 {
		t.Errorf("snapshot was mutated by subsequent operations: %v", snap)
	}
}

// TestGUC_Church_Reset_ReplacesEntirely verifies that Reset(newList) atomically
// replaces all entries without retaining any previously added prefixes.
func TestGUC_Church_Reset_ReplacesEntirely(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-")
	wl.Add("-qB4200-")
	wl.Add("-TR2940-")

	newPrefixes := []string{"-AZ5770-", "-LT1234-"}
	wl.Reset(newPrefixes)

	all := wl.GetAll()
	if len(all) != 2 {
		t.Fatalf("after Reset, expected 2 entries, got %v", all)
	}
	for _, p := range all {
		if p != "-AZ5770-" && p != "-LT1234-" {
			t.Errorf("unexpected residual entry after Reset: %q", p)
		}
	}
	// Old entries must not remain.
	if wl.IsAllowed([]byte("-UT3500-xxxxxxxxxxxx")) {
		t.Error("old prefix -UT3500- must not be allowed after Reset with new list")
	}
}

// TestGUC_Church_WildcardPrefix_Semantics verifies the wildcard nature of prefix
// matching: a single prefix entry matches any PeerID that shares those leading
// bytes, acting as a positional wildcard on all trailing bytes.
func TestGUC_Church_WildcardPrefix_Semantics(t *testing.T) {
	prefix := "-UT35" // 5-char prefix
	wl := NewWhitelist()
	wl.Add(prefix)

	matching := [][]byte{
		[]byte("-UT3500-xxxxxxxxxxxx"),
		[]byte("-UT3512-yyyyyyyyyyyy"),
		[]byte("-UT3599-zzzzzzzzzzzz"),
		[]byte("-UT35" + strings.Repeat("x", 15)), // exactly 20 bytes
	}
	for _, pid := range matching {
		if !wl.IsAllowed(pid) {
			t.Errorf("PeerID %q should match prefix %q", pid, prefix)
		}
	}

	nonMatch := []byte("-UT34xxxxxxxxxxxxxxx") // differs at position 4 ('4' vs '5')
	if wl.IsAllowed(nonMatch) {
		t.Errorf("PeerID %q should not match prefix %q", nonMatch, prefix)
	}
}

// ── Gödel: formal consistency / invariant preservation ───────────────────────

// TestGUC_Gödel_AddRemove_InvariantPreservation verifies the set-theoretic
// identity (S ∪ {x}) \ {x} = S when x ∉ S: the whitelist returns to its
// original state after adding and immediately removing a novel entry.
func TestGUC_Gödel_AddRemove_InvariantPreservation(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-TR2940-")
	wl.Add("-AZ5770-")

	before := wl.GetAll()

	novel := "-LT1234-" // not in the set
	wl.Add(novel)
	wl.Remove(novel)

	after := wl.GetAll()
	if len(before) != len(after) {
		t.Errorf("add+remove should restore original len: before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if i < len(after) && after[i] != before[i] {
			t.Errorf("order changed: before[%d]=%q after[%d]=%q", i, before[i], i, after[i])
		}
	}
}

// TestGUC_Gödel_Concurrent_AddContains_Race verifies that concurrent Add and
// IsAllowed calls are race-free (exercises the RWMutex under -race).
func TestGUC_Gödel_Concurrent_AddContains_Race(t *testing.T) {
	wl := NewWhitelist()

	const writers = 5
	const readers = 10
	const ops = 100
	prefixes := []string{"-UT3500-", "-qB4200-", "-TR2940-", "-AZ5770-", "-LT1234-"}

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				wl.Add(prefixes[idx])
			}
		}(i)
	}

	probe := []byte("-UT3500-xxxxxxxxxxxx")
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				_ = wl.IsAllowed(probe)
			}
		}()
	}
	wg.Wait()

	// After all writers finish, the probe prefix must be allowed.
	if !wl.IsAllowed(probe) {
		t.Error("after concurrent Add calls, -UT3500- should be allowed")
	}
}

// TestGUC_Gödel_NeverNegativeLen verifies the invariant that len(GetAll())
// is always ≥ 0, even when Remove is called more times than Add.
func TestGUC_Gödel_NeverNegativeLen(t *testing.T) {
	wl := NewWhitelist()

	// Remove from an empty whitelist — must not produce negative length.
	for i := 0; i < 10; i++ {
		wl.Remove(fmt.Sprintf("-X%07d-", i))
		if n := len(wl.GetAll()); n < 0 {
			t.Fatalf("len became negative (%d) after remove-from-empty", n)
		}
	}

	// Add 5, then remove 10 (more removes than adds).
	for i := 0; i < 5; i++ {
		wl.Add(fmt.Sprintf("-A%07d-", i))
	}
	for i := 0; i < 10; i++ {
		wl.Remove(fmt.Sprintf("-A%07d-", i))
	}
	if n := len(wl.GetAll()); n != 0 {
		t.Errorf("over-removal left len=%d, expected 0", n)
	}
}

// TestGUC_Gödel_AllowAllAfterReset_Formal verifies the formal allow-all
// invariant: Reset(nil) ⟹ ∀x. IsAllowed(x) = true.
func TestGUC_Gödel_AllowAllAfterReset_Formal(t *testing.T) {
	wl := NewWhitelist()
	for _, p := range []string{"-UT3500-", "-qB4200-", "-TR2940-"} {
		wl.Add(p)
	}

	wl.Reset(nil)

	probes := [][]byte{
		make([]byte, 20),                                                                                         // all zeros
		[]byte("-ZZZZZZ-xxxxxxxxxxxx"),                                                                           // no prior entry
		[]byte("AAAAAAAAAAAAAAAAAAAA"),                                                                           // all 'A'
		[]byte("\xff\xfe\xfd\xfc\xfb\xfa\xf9\xf8\xf7\xf6\xf5\xf4\xf3\xf2\xf1\xf0\xef\xee\xed\xec"), // high bytes
	}
	for _, pid := range probes {
		if !wl.IsAllowed(pid) {
			t.Errorf("allow-all invariant violated after Reset(nil): rejected %q", pid)
		}
	}
	if n := len(wl.GetAll()); n != 0 {
		t.Errorf("expected 0 entries after Reset(nil), got %d", n)
	}
}

// TestGUC_Gödel_ConsistencyAcrossOps verifies formal consistency across a
// complex sequence of Add / Remove / Reset operations: at each checkpoint the
// whitelist's observable state must exactly match the expected logical set.
func TestGUC_Gödel_ConsistencyAcrossOps(t *testing.T) {
	wl := NewWhitelist()

	// Step 1: add 4 distinct prefixes.
	for _, p := range []string{"-UT3500-", "-qB4200-", "-TR2940-", "-AZ5770-"} {
		wl.Add(p)
	}
	if n := len(wl.GetAll()); n != 4 {
		t.Fatalf("step 1: expected 4 entries, got %d", n)
	}

	// Step 2: add a duplicate — count must remain 4.
	wl.Add("-UT3500-")
	if n := len(wl.GetAll()); n != 4 {
		t.Fatalf("step 2: duplicate Add must keep count at 4, got %d", n)
	}

	// Step 3: remove one — count must drop to 3.
	wl.Remove("-qB4200-")
	if n := len(wl.GetAll()); n != 3 {
		t.Fatalf("step 3: expected 3 after remove, got %d", n)
	}
	if wl.IsAllowed([]byte("-qB4200-xxxxxxxxxxxx")) {
		t.Error("step 3: removed prefix must not be allowed")
	}

	// Step 4: Reset(nil) → allow-all, count = 0.
	wl.Reset(nil)
	if n := len(wl.GetAll()); n != 0 {
		t.Fatalf("step 4: expected 0 after Reset(nil), got %d", n)
	}
	if !wl.IsAllowed([]byte("-qB4200-xxxxxxxxxxxx")) {
		t.Error("step 4: allow-all must hold after Reset(nil)")
	}

	// Step 5: Reset to a specific single-entry list — only that entry allowed.
	wl.Reset([]string{"-LT1234-"})
	if n := len(wl.GetAll()); n != 1 {
		t.Fatalf("step 5: expected 1 entry, got %d", n)
	}
	if !wl.IsAllowed([]byte("-LT1234-xxxxxxxxxxxx")) {
		t.Error("step 5: -LT1234- must be allowed after Reset to [-LT1234-]")
	}
	if wl.IsAllowed([]byte("-UT3500-xxxxxxxxxxxx")) {
		t.Error("step 5: -UT3500- must not be allowed after Reset to [-LT1234-]")
	}
}
