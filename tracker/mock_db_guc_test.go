package tracker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// ── Knuth: algorithmic correctness, loop invariants, data structure invariants ──

// TestGUC_Knuth_RecordPeerAppendsCorrectFields verifies that RecordPeer appends a
// recordedPeer whose fields exactly match the arguments passed in.
func TestGUC_Knuth_RecordPeerAppendsCorrectFields(t *testing.T) {
	db := newMockDB()
	err := db.RecordPeer(
		UserID(7), TorrentID(42), 1,
		100, 200, 10, 20, 50, 0,
		uint32(time.Now().Unix()), 5,
		"10.0.0.1", "peer-id-1", "ua/1.0", false,
	)
	if err != nil {
		t.Fatalf("RecordPeer returned unexpected error: %v", err)
	}
	if len(db.Peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(db.Peers))
	}
	p := db.Peers[0]
	if p.UserID != 7 {
		t.Errorf("UserID: want 7, got %d", p.UserID)
	}
	if p.TorrentID != 42 {
		t.Errorf("TorrentID: want 42, got %d", p.TorrentID)
	}
	if p.Uploaded != 100 {
		t.Errorf("Uploaded: want 100, got %d", p.Uploaded)
	}
	if p.Downloaded != 200 {
		t.Errorf("Downloaded: want 200, got %d", p.Downloaded)
	}
	if p.Left != 50 {
		t.Errorf("Left: want 50, got %d", p.Left)
	}
	if p.IP != "10.0.0.1" {
		t.Errorf("IP: want 10.0.0.1, got %s", p.IP)
	}
}

// TestGUC_Knuth_RecordSnatches verifies RecordSnatch stores all fields correctly.
func TestGUC_Knuth_RecordSnatches(t *testing.T) {
	db := newMockDB()
	now := time.Now().Truncate(time.Second)
	err := db.RecordSnatch(UserID(3), TorrentID(9), now, "192.168.1.1")
	if err != nil {
		t.Fatalf("RecordSnatch error: %v", err)
	}
	if len(db.Snatches) != 1 {
		t.Fatalf("expected 1 snatch, got %d", len(db.Snatches))
	}
	s := db.Snatches[0]
	if s.UserID != 3 {
		t.Errorf("snatch UserID: want 3, got %d", s.UserID)
	}
	if s.TorrentID != 9 {
		t.Errorf("snatch TorrentID: want 9, got %d", s.TorrentID)
	}
	if !s.T.Equal(now) {
		t.Errorf("snatch time: want %v, got %v", now, s.T)
	}
	if s.IP != "192.168.1.1" {
		t.Errorf("snatch IP: want 192.168.1.1, got %s", s.IP)
	}
}

// TestGUC_Knuth_CallCountAccurate verifies that repeated calls increment slice lengths
// monotonically — a loop invariant that len(Peers) == call count.
func TestGUC_Knuth_CallCountAccurate(t *testing.T) {
	db := newMockDB()
	const n = 10
	for i := 0; i < n; i++ {
		if err := db.RecordPeer(UserID(i), TorrentID(1), 1, 0, 0, 0, 0, 0, 0, 0, 0, "", "", "", false); err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if got := len(db.Peers); got != i+1 {
			t.Errorf("after %d calls, len(Peers)=%d (invariant broken)", i+1, got)
		}
	}
}

// TestGUC_Knuth_MultipleTorrentsTrackedSeparately verifies that snatches for
// distinct torrents accumulate in insertion order without cross-contamination.
func TestGUC_Knuth_MultipleTorrentsTrackedSeparately(t *testing.T) {
	db := newMockDB()
	pairs := []struct {
		uid uint32
		tid uint32
	}{
		{1, 10},
		{2, 20},
		{3, 30},
	}
	for _, p := range pairs {
		_ = db.RecordSnatch(UserID(p.uid), TorrentID(p.tid), time.Now(), "0.0.0.0")
	}
	if len(db.Snatches) != len(pairs) {
		t.Fatalf("expected %d snatches, got %d", len(pairs), len(db.Snatches))
	}
	for i, p := range pairs {
		if db.Snatches[i].UserID != p.uid || db.Snatches[i].TorrentID != p.tid {
			t.Errorf("snatch[%d]: want uid=%d tid=%d, got uid=%d tid=%d",
				i, p.uid, p.tid, db.Snatches[i].UserID, db.Snatches[i].TorrentID)
		}
	}
}

// TestGUC_Knuth_ArgumentCaptureCorrect verifies RecordTorrent stores exact field values.
func TestGUC_Knuth_ArgumentCaptureCorrect(t *testing.T) {
	db := newMockDB()
	tests := []struct {
		tid      TorrentID
		seeders  uint32
		leechers uint32
		snatched int
		balance  int64
	}{
		{TorrentID(1), 5, 3, 12, 1000},
		{TorrentID(2), 0, 0, 0, -500},
		{TorrentID(3), 99, 1, 7, 0},
	}
	for _, tc := range tests {
		if err := db.RecordTorrent(tc.tid, tc.seeders, tc.leechers, tc.snatched, tc.balance); err != nil {
			t.Fatalf("RecordTorrent(%v): %v", tc.tid, err)
		}
	}
	if len(db.Torrents) != len(tests) {
		t.Fatalf("expected %d torrent records, got %d", len(tests), len(db.Torrents))
	}
	for i, tc := range tests {
		r := db.Torrents[i]
		if r.TorrentID != uint32(tc.tid) || r.Seeders != tc.seeders ||
			r.Leechers != tc.leechers || r.Snatched != tc.snatched || r.Balance != tc.balance {
			t.Errorf("Torrents[%d] mismatch: want %+v, got %+v", i, tc, r)
		}
	}
}

// ── Turing: termination conditions, halting behavior ──────────────────────────

// TestGUC_Turing_ErrorInjectionRecordPeer verifies that setting ReturnErr causes
// RecordPeer to return immediately with that error and appends nothing.
func TestGUC_Turing_ErrorInjectionRecordPeer(t *testing.T) {
	db := newMockDB()
	sentinel := errors.New("injected db error")
	db.ReturnErr = sentinel
	err := db.RecordPeer(UserID(1), TorrentID(1), 1, 0, 0, 0, 0, 0, 0, 0, 0, "", "", "", false)
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
	if len(db.Peers) != 0 {
		t.Errorf("expected no peers recorded on error, got %d", len(db.Peers))
	}
}

// TestGUC_Turing_ErrorInjectionRecordSnatch verifies that injected errors halt
// RecordSnatch and leave the Snatches slice unchanged.
func TestGUC_Turing_ErrorInjectionRecordSnatch(t *testing.T) {
	db := newMockDB()
	sentinel := errors.New("snatch inject")
	db.ReturnErr = sentinel
	err := db.RecordSnatch(UserID(1), TorrentID(1), time.Now(), "1.2.3.4")
	if !errors.Is(err, sentinel) {
		t.Fatalf("want injected error, got %v", err)
	}
	if len(db.Snatches) != 0 {
		t.Errorf("expected no snatches on error, got %d", len(db.Snatches))
	}
}

// TestGUC_Turing_ResetClearsAllRecordedCalls verifies that reset() halts all
// accumulated state and the slice lengths return to zero (halting predicate).
func TestGUC_Turing_ResetClearsAllRecordedCalls(t *testing.T) {
	db := newMockDB()
	_ = db.RecordPeer(UserID(1), TorrentID(1), 1, 0, 0, 0, 0, 0, 0, 0, 0, "", "", "", false)
	_ = db.RecordSnatch(UserID(1), TorrentID(1), time.Now(), "0.0.0.0")
	_ = db.RecordUserStats(UserID(1), 100, 200)
	_ = db.RecordTorrent(TorrentID(1), 1, 0, 1, 0)
	db.reset()
	if len(db.Peers) != 0 || len(db.Snatches) != 0 || len(db.UserStats) != 0 || len(db.Torrents) != 0 {
		t.Errorf("reset did not clear all slices: peers=%d snatches=%d userStats=%d torrents=%d",
			len(db.Peers), len(db.Snatches), len(db.UserStats), len(db.Torrents))
	}
}

// TestGUC_Turing_ErrorClearsAfterReset verifies that after reset() a previously
// injected error still exists (ReturnErr is not cleared by reset), so error
// injection is deliberate and long-lived.
func TestGUC_Turing_ErrorClearsAfterReset(t *testing.T) {
	db := newMockDB()
	db.ReturnErr = errors.New("persistent error")
	db.reset()
	// ReturnErr is not cleared by reset — callers must clear it explicitly.
	// Verify that a RecordPeer call still fails (termination expected).
	err := db.RecordPeer(UserID(1), TorrentID(1), 1, 0, 0, 0, 0, 0, 0, 0, 0, "", "", "", false)
	if err == nil {
		t.Error("expected error after reset with ReturnErr still set, got nil")
	}
}

// TestGUC_Turing_CheckpointAndRotationCounters verifies termination counters
// increment correctly and are reset by reset().
func TestGUC_Turing_CheckpointAndRotationCounters(t *testing.T) {
	db := newMockDB()
	for i := 0; i < 3; i++ {
		_ = db.CheckpointWAL()
	}
	for i := 0; i < 2; i++ {
		_ = db.CheckRotation()
	}
	if db.CheckpointCount != 3 {
		t.Errorf("CheckpointCount: want 3, got %d", db.CheckpointCount)
	}
	if db.RotationCount != 2 {
		t.Errorf("RotationCount: want 2, got %d", db.RotationCount)
	}
	db.reset()
	if db.CheckpointCount != 0 || db.RotationCount != 0 {
		t.Errorf("counters not reset: checkpoint=%d rotation=%d", db.CheckpointCount, db.RotationCount)
	}
}

// ── Church: functional purity, side-effect isolation ─────────────────────────

// TestGUC_Church_RecordPeerLightIsolatedFromPeers verifies that RecordPeerLight
// writes only to PeersLight and does not affect the Peers slice.
func TestGUC_Church_RecordPeerLightIsolatedFromPeers(t *testing.T) {
	db := newMockDB()
	err := db.RecordPeerLight(UserID(5), TorrentID(10), 12345, 3, "pid")
	if err != nil {
		t.Fatalf("RecordPeerLight error: %v", err)
	}
	if len(db.Peers) != 0 {
		t.Errorf("RecordPeerLight must not touch Peers; got %d entries", len(db.Peers))
	}
	if len(db.PeersLight) != 1 {
		t.Fatalf("expected 1 PeersLight, got %d", len(db.PeersLight))
	}
	pl := db.PeersLight[0]
	if pl.UserID != 5 || pl.TorrentID != 10 || pl.PeerID != "pid" {
		t.Errorf("PeersLight fields mismatch: %+v", pl)
	}
}

// TestGUC_Church_RecordUserStatsIsolated verifies RecordUserStats does not touch
// Peers, Snatches, or Torrents slices.
func TestGUC_Church_RecordUserStatsIsolated(t *testing.T) {
	db := newMockDB()
	err := db.RecordUserStats(UserID(2), 500, 300)
	if err != nil {
		t.Fatalf("RecordUserStats error: %v", err)
	}
	if len(db.Peers) != 0 || len(db.Snatches) != 0 || len(db.Torrents) != 0 {
		t.Errorf("RecordUserStats leaked into other slices")
	}
	if len(db.UserStats) != 1 {
		t.Fatalf("expected 1 UserStats entry, got %d", len(db.UserStats))
	}
	us := db.UserStats[0]
	if us.UserID != 2 || us.Uploaded != 500 || us.Downloaded != 300 {
		t.Errorf("UserStats mismatch: %+v", us)
	}
}

// TestGUC_Church_ResetDoesNotAffectReturnErr verifies reset() has no side effect
// on ReturnErr — each setting is functionally independent.
func TestGUC_Church_ResetDoesNotAffectReturnErr(t *testing.T) {
	db := newMockDB()
	want := errors.New("kept-across-reset")
	db.ReturnErr = want
	db.reset()
	if db.ReturnErr != want {
		t.Errorf("reset() must not clear ReturnErr; want %v, got %v", want, db.ReturnErr)
	}
}

// TestGUC_Church_WhitelistEntryIsolation verifies AddWhitelistEntry and
// RemoveWhitelistEntry write to separate slices with no cross-pollution.
func TestGUC_Church_WhitelistEntryIsolation(t *testing.T) {
	db := newMockDB()
	prefixes := []string{"-qB", "uT", "DE"}
	for _, p := range prefixes {
		if err := db.AddWhitelistEntry(p); err != nil {
			t.Fatalf("AddWhitelistEntry(%q): %v", p, err)
		}
	}
	if err := db.RemoveWhitelistEntry("uT"); err != nil {
		t.Fatalf("RemoveWhitelistEntry: %v", err)
	}
	if len(db.WhitelistAdded) != 3 {
		t.Errorf("WhitelistAdded: want 3, got %d", len(db.WhitelistAdded))
	}
	if len(db.WhitelistRemoved) != 1 || db.WhitelistRemoved[0] != "uT" {
		t.Errorf("WhitelistRemoved: want [uT], got %v", db.WhitelistRemoved)
	}
}

// TestGUC_Church_RecordTokenIsolated verifies RecordToken writes only to Tokens
// and produces no side-effects on other slices.
func TestGUC_Church_RecordTokenIsolated(t *testing.T) {
	db := newMockDB()
	err := db.RecordToken(UserID(4), TorrentID(8), 1024)
	if err != nil {
		t.Fatalf("RecordToken error: %v", err)
	}
	if len(db.Peers) != 0 || len(db.Snatches) != 0 || len(db.UserStats) != 0 || len(db.Torrents) != 0 {
		t.Errorf("RecordToken must not write to other slices")
	}
	if len(db.Tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(db.Tokens))
	}
	tok := db.Tokens[0]
	if tok.UserID != 4 || tok.TorrentID != 8 || tok.Downloaded != 1024 {
		t.Errorf("Token mismatch: %+v", tok)
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state ───────

// TestGUC_Godel_DBInterfaceFullyImplementedByMockDB verifies at compile time that
// *MockDB satisfies DatabaseInterface. If MockDB is incomplete this test will fail
// to compile, making the inconsistency impossible to miss.
func TestGUC_Godel_DBInterfaceFullyImplementedByMockDB(t *testing.T) {
	var _ DatabaseInterface = (*MockDB)(nil)
	// Reaching here means compile-time contract is satisfied.
}

// TestGUC_Godel_ConcurrentCallsSafeUnderRace verifies that concurrent RecordPeer
// and RecordSnatch calls do not corrupt internal state (data-race detector test).
func TestGUC_Godel_ConcurrentCallsSafeUnderRace(t *testing.T) {
	db := newMockDB()
	var wg sync.WaitGroup
	const goroutines = 20
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			_ = db.RecordPeer(UserID(i), TorrentID(i), 1, 0, 0, 0, 0, 0, 0, 0, 0, "", "", "", false)
		}(i)
		go func(i int) {
			defer wg.Done()
			_ = db.RecordSnatch(UserID(i), TorrentID(i), time.Now(), "0.0.0.0")
		}(i)
	}
	wg.Wait()
	if len(db.Peers) != goroutines {
		t.Errorf("expected %d peers after concurrent writes, got %d", goroutines, len(db.Peers))
	}
	if len(db.Snatches) != goroutines {
		t.Errorf("expected %d snatches after concurrent writes, got %d", goroutines, len(db.Snatches))
	}
}

// TestGUC_Godel_ResetToZeroStateIsConsistent verifies that a freshly constructed
// MockDB and one that has been used then reset are observably equivalent — they
// both represent the zero-state invariant.
func TestGUC_Godel_ResetToZeroStateIsConsistent(t *testing.T) {
	fresh := newMockDB()
	used := newMockDB()
	_ = used.RecordPeer(UserID(1), TorrentID(1), 1, 9, 9, 9, 9, 9, 9, 9, 9, "x", "y", "z", false)
	_ = used.RecordSnatch(UserID(1), TorrentID(1), time.Now(), "1.1.1.1")
	used.reset()

	if len(fresh.Peers) != len(used.Peers) || len(fresh.Snatches) != len(used.Snatches) ||
		len(fresh.UserStats) != len(used.UserStats) || len(fresh.Torrents) != len(used.Torrents) ||
		fresh.CheckpointCount != used.CheckpointCount || fresh.RotationCount != used.RotationCount ||
		fresh.CloseCount != used.CloseCount {
		t.Errorf("reset state differs from fresh state: fresh=%+v used=%+v", fresh, used)
	}
}

// TestGUC_Godel_ImpossibleStateDetection verifies that after an error injection
// it is impossible for new records to accumulate — the error-present and
// non-empty-slice states are mutually exclusive.
func TestGUC_Godel_ImpossibleStateDetection(t *testing.T) {
	db := newMockDB()
	db.ReturnErr = errors.New("db down")
	for i := 0; i < 5; i++ {
		_ = db.RecordPeer(UserID(i), TorrentID(i), 1, 0, 0, 0, 0, 0, 0, 0, 0, "", "", "", false)
		_ = db.RecordSnatch(UserID(i), TorrentID(i), time.Now(), "0.0.0.0")
		_ = db.RecordUserStats(UserID(i), 0, 0)
	}
	if len(db.Peers) != 0 || len(db.Snatches) != 0 || len(db.UserStats) != 0 {
		t.Errorf("impossible state: records present despite error injection — peers=%d snatches=%d userStats=%d",
			len(db.Peers), len(db.Snatches), len(db.UserStats))
	}
}

// TestGUC_Godel_CloseCountInvariant verifies that CloseCount increments by
// exactly 1 per Close() call, preserving the accounting invariant.
func TestGUC_Godel_CloseCountInvariant(t *testing.T) {
	db := newMockDB()
	closeCalls := []struct{ expected int }{{1}, {2}, {3}}
	for _, tc := range closeCalls {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error: %v", err)
		}
		if db.CloseCount != tc.expected {
			t.Errorf("CloseCount: want %d, got %d", tc.expected, db.CloseCount)
		}
	}
}

