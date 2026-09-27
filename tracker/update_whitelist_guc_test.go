package tracker

// GUC (Gödel Unified Council) test coverage for whitelist update operations.
// Lenses: Knuth (algorithmic), Turing (termination/halting), Church (purity/isolation), Gödel (consistency/invariants).

import (
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newWhitelistFixture() *testFixture {
	f := newTestFixture()
	f.worker.Whitelist = NewWhitelist()
	return f
}

func doWhitelistUpdate(f *testFixture, action, prefix string) UpdateResponse {
	extra := url.Values{}
	if prefix != "" {
		extra.Set("prefix", prefix)
	}
	req := buildUpdateURL(sitePass, action, extra)
	raw, _ := f.worker.HandleUpdate(req)
	var resp UpdateResponse
	_ = json.Unmarshal(raw, &resp)
	return resp
}

// ── Knuth: algorithmic correctness ────────────────────────────────────────────

// TestGUC_Knuth_AddWhitelist_PrefixStored verifies that after add_whitelist the
// prefix appears in GetAll and the DB records the add.
func TestGUC_Knuth_AddWhitelist_PrefixStored(t *testing.T) {
	f := newWhitelistFixture()
	resp := doWhitelistUpdate(f, "add_whitelist", "-UT3500-")
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	all := f.worker.Whitelist.GetAll()
	if len(all) != 1 || all[0] != "-UT3500-" {
		t.Errorf("expected whitelist to contain -UT3500-, got %v", all)
	}
	if len(f.db.WhitelistAdded) != 1 || f.db.WhitelistAdded[0] != "-UT3500-" {
		t.Errorf("expected DB WhitelistAdded=['-UT3500-'], got %v", f.db.WhitelistAdded)
	}
}

// TestGUC_Knuth_RemoveWhitelist_PrefixGone verifies that remove_whitelist
// removes only the target prefix and leaves others intact.
func TestGUC_Knuth_RemoveWhitelist_PrefixGone(t *testing.T) {
	f := newWhitelistFixture()
	doWhitelistUpdate(f, "add_whitelist", "-UT3500-")
	doWhitelistUpdate(f, "add_whitelist", "-qB4200-")

	resp := doWhitelistUpdate(f, "remove_whitelist", "-UT3500-")
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	all := f.worker.Whitelist.GetAll()
	if len(all) != 1 || all[0] != "-qB4200-" {
		t.Errorf("expected whitelist=['-qB4200-'] after remove, got %v", all)
	}
	if len(f.db.WhitelistRemoved) != 1 || f.db.WhitelistRemoved[0] != "-UT3500-" {
		t.Errorf("expected DB WhitelistRemoved=['-UT3500-'], got %v", f.db.WhitelistRemoved)
	}
}

// TestGUC_Knuth_AddWhitelist_TableDriven exercises multiple distinct prefixes
// via a table-driven approach and verifies each is stored correctly.
func TestGUC_Knuth_AddWhitelist_TableDriven(t *testing.T) {
	cases := []struct {
		prefix string
	}{
		{"-UT3500-"},
		{"-qB4200-"},
		{"-TR2940-"},
		{"-AZ5770-"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.prefix, func(t *testing.T) {
			f := newWhitelistFixture()
			resp := doWhitelistUpdate(f, "add_whitelist", tc.prefix)
			if !resp.Success {
				t.Fatalf("add_whitelist(%q) failed: %s", tc.prefix, resp.Error)
			}
			all := f.worker.Whitelist.GetAll()
			found := false
			for _, p := range all {
				if p == tc.prefix {
					found = true
				}
			}
			if !found {
				t.Errorf("prefix %q not found in whitelist after add: %v", tc.prefix, all)
			}
		})
	}
}

// TestGUC_Knuth_PrefixMatch_8Char verifies that IsAllowed uses prefix matching
// on the first N chars of a 20-byte PeerID — specifically an 8-char prefix
// against a 20-char PeerID.
func TestGUC_Knuth_PrefixMatch_8Char(t *testing.T) {
	wl := NewWhitelist()
	prefix := "-UT3500-" // 8 chars
	wl.Add(prefix)

	// PeerID whose first 8 bytes match the prefix (20 bytes total)
	matchPeerID := []byte("-UT3500-" + "xxxxxxxxxxxx") // 8+12 = 20
	if !wl.IsAllowed(matchPeerID) {
		t.Errorf("expected PeerID starting with %q to be allowed", prefix)
	}

	// PeerID whose first 8 bytes do NOT match
	noMatchPeerID := []byte("-LT1234-" + "xxxxxxxxxxxx")
	if wl.IsAllowed(noMatchPeerID) {
		t.Errorf("expected PeerID not matching %q to be rejected", prefix)
	}
}

// TestGUC_Knuth_AddWhitelist_MissingPrefix verifies that omitting the prefix
// parameter returns an error without mutating state.
func TestGUC_Knuth_AddWhitelist_MissingPrefix(t *testing.T) {
	f := newWhitelistFixture()
	resp := doWhitelistUpdate(f, "add_whitelist", "")
	if resp.Success {
		t.Fatal("expected failure for missing prefix, got success")
	}
	if len(f.worker.Whitelist.GetAll()) != 0 {
		t.Errorf("whitelist should be empty after failed add, got %v", f.worker.Whitelist.GetAll())
	}
	if len(f.db.WhitelistAdded) != 0 {
		t.Errorf("DB should have no adds after failed request, got %v", f.db.WhitelistAdded)
	}
}

// ── Turing: termination/halting ───────────────────────────────────────────────

// TestGUC_Turing_RemoveWhitelist_MissingPrefix verifies that remove_whitelist
// with an empty prefix terminates without error and does not mutate the list.
func TestGUC_Turing_RemoveWhitelist_MissingPrefix(t *testing.T) {
	f := newWhitelistFixture()
	doWhitelistUpdate(f, "add_whitelist", "-UT3500-")

	resp := doWhitelistUpdate(f, "remove_whitelist", "")
	if resp.Success {
		t.Fatal("expected failure for missing prefix")
	}
	if len(f.worker.Whitelist.GetAll()) != 1 {
		t.Errorf("whitelist should still have 1 entry, got %v", f.worker.Whitelist.GetAll())
	}
}

// TestGUC_Turing_RemoveWhitelist_MissingEntry verifies that removing a prefix
// that does not exist terminates safely (DB call made, no panic, no mutation error).
func TestGUC_Turing_RemoveWhitelist_MissingEntry(t *testing.T) {
	f := newWhitelistFixture()
	doWhitelistUpdate(f, "add_whitelist", "-UT3500-")

	// Removing a prefix that was never added — DB returns nil, in-memory no-op.
	resp := doWhitelistUpdate(f, "remove_whitelist", "-qB9999-")
	if !resp.Success {
		t.Fatalf("expected success removing non-existent prefix, got: %s", resp.Error)
	}
	all := f.worker.Whitelist.GetAll()
	if len(all) != 1 || all[0] != "-UT3500-" {
		t.Errorf("whitelist should still contain only -UT3500-, got %v", all)
	}
}

// TestGUC_Turing_EmptyWhitelist_AllowsAll confirms that an empty whitelist
// admits any PeerID (allow-all sentinel), verifying the halting condition
// on the prefix-scan loop.
func TestGUC_Turing_EmptyWhitelist_AllowsAll(t *testing.T) {
	wl := NewWhitelist()
	peerIDs := [][]byte{
		[]byte("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
		[]byte("-UT3500-xxxxxxxxxxxx"),
		[]byte("AAAAAAAAAAAAAAAAAAAA"),
	}
	for _, pid := range peerIDs {
		if !wl.IsAllowed(pid) {
			t.Errorf("empty whitelist should allow any PeerID, rejected %q", pid)
		}
	}
}

// TestGUC_Turing_WhitelistWithEntries_RejectsNonMatching verifies that once
// entries exist, non-matching PeerIDs are rejected without infinite looping.
func TestGUC_Turing_WhitelistWithEntries_RejectsNonMatching(t *testing.T) {
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
		{[]byte("AAAAAAAAAAAAAAAAAAAA"), false},
	}
	for _, tc := range cases {
		got := wl.IsAllowed(tc.peerID)
		if got != tc.allowed {
			t.Errorf("IsAllowed(%q)=%v, want %v", tc.peerID, got, tc.allowed)
		}
	}
}

// TestGUC_Turing_AllowAllAction_UnknownAction verifies that an unknown action
// terminates with an error response (no infinite loop, no panic).
func TestGUC_Turing_AllowAllAction_UnknownAction(t *testing.T) {
	f := newWhitelistFixture()
	extra := url.Values{}
	req := buildUpdateURL(sitePass, "allow_all", extra)
	raw, err := f.worker.HandleUpdate(req)
	if err == nil {
		t.Fatal("expected error for unknown action 'allow_all'")
	}
	var resp UpdateResponse
	_ = json.Unmarshal(raw, &resp)
	if resp.Success {
		t.Fatal("expected success=false for unknown action")
	}
	if !strings.Contains(resp.Error, "Unknown action") {
		t.Errorf("expected 'Unknown action' in error, got %q", resp.Error)
	}
}

// ── Church: functional purity / side-effect isolation ────────────────────────

// TestGUC_Church_AddWhitelist_IdempotentAdd verifies that adding the same prefix
// twice produces exactly one entry (idempotency — no duplicate side effects).
func TestGUC_Church_AddWhitelist_IdempotentAdd(t *testing.T) {
	f := newWhitelistFixture()
	doWhitelistUpdate(f, "add_whitelist", "-UT3500-")
	doWhitelistUpdate(f, "add_whitelist", "-UT3500-")

	all := f.worker.Whitelist.GetAll()
	if len(all) != 1 {
		t.Errorf("expected exactly 1 entry after duplicate adds, got %v", all)
	}
	// DB should record two add calls (each HTTP request is independent).
	if len(f.db.WhitelistAdded) != 2 {
		t.Errorf("expected 2 DB add calls, got %d", len(f.db.WhitelistAdded))
	}
}

// TestGUC_Church_WhitelistIsolation_FixturePerTest verifies that each test
// fixture is isolated — adding to one fixture does not affect another.
func TestGUC_Church_WhitelistIsolation_FixturePerTest(t *testing.T) {
	f1 := newWhitelistFixture()
	f2 := newWhitelistFixture()

	doWhitelistUpdate(f1, "add_whitelist", "-UT3500-")

	all2 := f2.worker.Whitelist.GetAll()
	if len(all2) != 0 {
		t.Errorf("fixture isolation failed: f2 whitelist should be empty, got %v", all2)
	}
}

// TestGUC_Church_AddWhitelist_ResponsePurity verifies that the JSON response
// contains exactly the expected fields and no unexpected mutations to Status.
func TestGUC_Church_AddWhitelist_ResponsePurity(t *testing.T) {
	f := newWhitelistFixture()
	extra := url.Values{"prefix": {"-qB4200-"}}
	req := buildUpdateURL(sitePass, "add_whitelist", extra)
	raw, err := f.worker.HandleUpdate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp UpdateResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected Status='ok', got %q", resp.Status)
	}
	if !resp.Success {
		t.Errorf("expected Success=true")
	}
	if resp.Error != "" {
		t.Errorf("expected empty Error field, got %q", resp.Error)
	}
}

// TestGUC_Church_CaseSensitivity verifies that prefix matching is case-sensitive
// (functional purity: no implicit normalization side effects).
func TestGUC_Church_CaseSensitivity(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-") // lowercase 'T' and digits

	// Same chars but different case should not match.
	upperPeer := []byte("-ut3500-xxxxxxxxxxxx")
	lowerPeer := []byte("-UT3500-xxxxxxxxxxxx")

	if wl.IsAllowed(upperPeer) {
		t.Errorf("IsAllowed should be case-sensitive: '-ut3500-...' should not match '-UT3500-'")
	}
	if !wl.IsAllowed(lowerPeer) {
		t.Errorf("IsAllowed('-UT3500-...') should be allowed")
	}
}

// TestGUC_Church_WhitelistClear_ResetToEmpty verifies that Reset(nil) sets the
// whitelist to empty (allow-all) with no residual entries.
func TestGUC_Church_WhitelistClear_ResetToEmpty(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-")
	wl.Add("-qB4200-")
	wl.Add("-TR2940-")

	wl.Reset(nil)

	all := wl.GetAll()
	if len(all) != 0 {
		t.Errorf("expected empty whitelist after Reset(nil), got %v", all)
	}
	// After clear, allow-all must hold.
	if !wl.IsAllowed([]byte("AAAAAAAAAAAAAAAAAAAA")) {
		t.Errorf("cleared whitelist should allow all peers")
	}
}

// ── Gödel: formal consistency / invariant preservation ───────────────────────

// TestGUC_Gödel_AddRemove_Consistent verifies the add-then-remove invariant:
// the set after add+remove equals the set before add.
func TestGUC_Gödel_AddRemove_Consistent(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-TR2940-")

	before := wl.GetAll()

	wl.Add("-UT3500-")
	wl.Remove("-UT3500-")

	after := wl.GetAll()
	if len(before) != len(after) || (len(before) > 0 && before[0] != after[0]) {
		t.Errorf("add+remove should restore original state; before=%v after=%v", before, after)
	}
}

// TestGUC_Gödel_NoImpossibleState_RemoveFromEmpty verifies that removing from
// an empty whitelist does not produce negative-length or inconsistent state.
func TestGUC_Gödel_NoImpossibleState_RemoveFromEmpty(t *testing.T) {
	wl := NewWhitelist()
	wl.Remove("-UT3500-") // should be a no-op
	all := wl.GetAll()
	if len(all) != 0 {
		t.Errorf("removing from empty whitelist must leave it empty, got %v", all)
	}
	// Invariant: empty whitelist remains allow-all.
	if !wl.IsAllowed([]byte("AAAAAAAAAAAAAAAAAAAA")) {
		t.Error("empty whitelist must allow all peers (allow-all invariant violated)")
	}
}

// TestGUC_Gödel_GetAllSnapshot_Independent verifies that GetAll returns a
// snapshot: mutations after the call do not retroactively change the snapshot.
func TestGUC_Gödel_GetAllSnapshot_Independent(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-UT3500-")

	snapshot := wl.GetAll()
	wl.Add("-qB4200-")

	if len(snapshot) != 1 {
		t.Errorf("snapshot should be frozen at 1 entry, got %v", snapshot)
	}
	if len(wl.GetAll()) != 2 {
		t.Errorf("live whitelist should have 2 entries after second add")
	}
}

// TestGUC_Gödel_Concurrent_AddRemove_Safe verifies that concurrent add and
// remove operations leave the whitelist in a consistent (non-panicking,
// non-corrupt) state under the race detector.
func TestGUC_Gödel_Concurrent_AddRemove_Safe(t *testing.T) {
	wl := NewWhitelist()
	const goroutines = 20
	const ops = 50

	var wg sync.WaitGroup
	prefixes := []string{"-UT3500-", "-qB4200-", "-TR2940-", "-AZ5770-", "-LT1234-"}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := prefixes[idx%len(prefixes)]
			for j := 0; j < ops; j++ {
				if j%2 == 0 {
					wl.Add(p)
				} else {
					wl.Remove(p)
				}
				_ = wl.IsAllowed([]byte("-UT3500-xxxxxxxxxxxx"))
			}
		}(i)
	}
	wg.Wait()

	// After all goroutines finish, GetAll must return a valid (non-nil) slice.
	all := wl.GetAll()
	if all == nil {
		t.Error("GetAll returned nil after concurrent operations")
	}
}

// TestGUC_Gödel_Concurrent_HandleUpdate_WhitelistSafe verifies that concurrent
// add_whitelist/remove_whitelist HTTP update calls via HandleUpdate do not
// produce data races or corrupt the in-memory whitelist.
func TestGUC_Gödel_Concurrent_HandleUpdate_WhitelistSafe(t *testing.T) {
	f := newWhitelistFixture()
	const goroutines = 10
	const opsPerGoroutine = 20

	var wg sync.WaitGroup
	prefixes := []string{"-UT3500-", "-qB4200-", "-TR2940-"}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := prefixes[idx%len(prefixes)]
			for j := 0; j < opsPerGoroutine; j++ {
				if j%2 == 0 {
					extra := url.Values{"prefix": {p}}
					req := buildUpdateURL(sitePass, "add_whitelist", extra)
					_, _ = f.worker.HandleUpdate(req)
				} else {
					extra := url.Values{"prefix": {p}}
					req := buildUpdateURL(sitePass, "remove_whitelist", extra)
					_, _ = f.worker.HandleUpdate(req)
				}
			}
		}(i)
	}
	wg.Wait()

	// Invariant: GetAll must not panic and must return a valid slice.
	all := f.worker.Whitelist.GetAll()
	if all == nil {
		t.Error("whitelist.GetAll() returned nil after concurrent HandleUpdate calls")
	}
}
