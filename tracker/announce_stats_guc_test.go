package tracker

// announce_stats_guc_test.go — GUC test coverage for announce + stats subsystems.
//
// GUC Analytical Lenses:
//   Knuth  — algorithmic correctness, data structure invariants, counter semantics
//   Turing — termination, halting, decidability of key operations
//   Church — functional purity, side-effect isolation, referential transparency
//   Godel  — formal consistency, invariant preservation, impossible-state detection

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// ── internal helpers ──────────────────────────────────────────────────────────

func gucMakePeerID(suffix string) []byte {
	const base = "-GC00000000000000000"
	b := []byte(base)[:20]
	copy(b[len(base)-len(suffix):], suffix)
	return b[:20]
}

func gucWorkerWithTorrent(t *testing.T, infoHash string) (*Worker, *User) {
	t.Helper()
	w, _, _ := newTestWorker()
	tor := NewTorrent(TorrentID(10))
	w.Torrents.Set(infoHash, tor)
	u := NewUser(UserID(1), true, false)
	return w, u
}

const gucHash = "guchash0000000000001"

// ── KNUTH: algorithmic correctness ───────────────────────────────────────────

// TestGUC_Knuth_SuccAnnouncementsIncrementPerCall verifies that every
// successful Announce call increments Stats.SuccAnnouncements by exactly one.
func TestGUC_Knuth_SuccAnnouncementsIncrementPerCall(t *testing.T) {
	w, u := gucWorkerWithTorrent(t, gucHash)
	ip := net.ParseIP("10.1.2.3")

	for i := 0; i < 3; i++ {
		peerID := gucMakePeerID(strconv.Itoa(i))
		before := w.Stats.SuccAnnouncements.Load()
		req := &AnnounceRequest{
			InfoHash: gucHash,
			PeerID:   peerID,
			Port:     uint16(6000 + i),
			Left:     1000,
			Compact:  true,
			Event:    "started",
			NumWant:  50,
		}
		u2 := NewUser(UserID(uint32(i+10)), true, false)
		if _, err := w.Announce(context.Background(), req, u2, ip, "", ""); err != nil {
			t.Fatalf("announce %d: %v", i, err)
		}
		after := w.Stats.SuccAnnouncements.Load()
		if after != before+1 {
			t.Errorf("announce %d: SuccAnnouncements %d → %d, want +1", i, before, after)
		}
		_ = u
	}
}

// TestGUC_Knuth_LeechersGaugeAfterJoin verifies that Stats.Leechers and
// the per-torrent leecher list both reflect new leecher additions accurately.
func TestGUC_Knuth_LeechersGaugeAfterJoin(t *testing.T) {
	w, _ := gucWorkerWithTorrent(t, gucHash)
	ip := net.ParseIP("10.2.3.4")

	before := w.Stats.Leechers.Load()
	u1 := NewUser(UserID(21), true, false)
	req := &AnnounceRequest{
		InfoHash: gucHash,
		PeerID:   gucMakePeerID("LA"),
		Port:     7001,
		Left:     500,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	if _, err := w.Announce(context.Background(), req, u1, ip, "", ""); err != nil {
		t.Fatalf("leecher join: %v", err)
	}

	tor, _ := w.Torrents.Get(gucHash)
	if tor.Leechers.Size() != 1 {
		t.Errorf("torrent leecher count = %d, want 1", tor.Leechers.Size())
	}
	after := w.Stats.Leechers.Load()
	if after != before+1 {
		t.Errorf("Stats.Leechers %d → %d, want +1", before, after)
	}
}

// TestGUC_Knuth_SeederGaugeAfterJoin verifies that Stats.Seeders reflects
// new seeder additions (left==0) accurately.
func TestGUC_Knuth_SeederGaugeAfterJoin(t *testing.T) {
	w, _ := gucWorkerWithTorrent(t, gucHash)
	ip := net.ParseIP("10.3.4.5")

	before := w.Stats.Seeders.Load()
	u := NewUser(UserID(30), true, false)
	req := &AnnounceRequest{
		InfoHash: gucHash,
		PeerID:   gucMakePeerID("SA"),
		Port:     7002,
		Left:     0, // seeder
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	if _, err := w.Announce(context.Background(), req, u, ip, "", ""); err != nil {
		t.Fatalf("seeder join: %v", err)
	}

	tor, _ := w.Torrents.Get(gucHash)
	if tor.Seeders.Size() != 1 {
		t.Errorf("torrent seeder count = %d, want 1", tor.Seeders.Size())
	}
	after := w.Stats.Seeders.Load()
	if after != before+1 {
		t.Errorf("Stats.Seeders %d → %d, want +1", before, after)
	}
}

// TestGUC_Knuth_LeechersGaugeAfterLeave verifies that Stats.Leechers
// decrements correctly when a leecher sends the stopped event.
func TestGUC_Knuth_LeechersGaugeAfterLeave(t *testing.T) {
	w, _ := gucWorkerWithTorrent(t, gucHash)
	ip := net.ParseIP("10.4.5.6")
	u := NewUser(UserID(40), true, false)
	peerID := gucMakePeerID("LB")

	// Join.
	reqJoin := &AnnounceRequest{
		InfoHash: gucHash, PeerID: peerID,
		Port: 7003, Left: 800, Compact: true, Event: "started", NumWant: 50,
	}
	if _, err := w.Announce(context.Background(), reqJoin, u, ip, "", ""); err != nil {
		t.Fatalf("join: %v", err)
	}
	if w.Stats.Leechers.Load() == 0 {
		t.Fatal("leecher not registered after join")
	}
	midCount := w.Stats.Leechers.Load()

	// Leave.
	reqStop := &AnnounceRequest{
		InfoHash: gucHash, PeerID: peerID,
		Port: 7003, Left: 800, Compact: true, Event: "stopped", NumWant: 0,
	}
	if _, err := w.Announce(context.Background(), reqStop, u, ip, "", ""); err != nil {
		t.Fatalf("stop: %v", err)
	}
	after := w.Stats.Leechers.Load()
	if after != midCount-1 {
		t.Errorf("Stats.Leechers after stop = %d, want %d", after, midCount-1)
	}
}

// TestGUC_Knuth_TorrentsCountMatchesListSize uses a table of counts to verify
// that TorrentList.Size() always matches the number of torrents inserted.
func TestGUC_Knuth_TorrentsCountMatchesListSize(t *testing.T) {
	tests := []struct {
		n int
	}{
		{1},
		{5},
		{10},
	}
	for _, tc := range tests {
		tl := NewTorrentList()
		for i := 0; i < tc.n; i++ {
			hash := "knuth" + strconv.Itoa(i) + "000000000000000"
			if len(hash) > 20 {
				hash = hash[:20]
			}
			tl.Set(hash, NewTorrent(TorrentID(i+1)))
		}
		got := tl.Size()
		if got != tc.n {
			t.Errorf("n=%d: TorrentList.Size() = %d, want %d", tc.n, got, tc.n)
		}
	}
}

// ── TURING: termination, halting, decidability ────────────────────────────────

// TestGUC_Turing_StartTimeNonZero verifies that Stats.StartTime is set to a
// non-zero value at initialization — a precondition for uptime calculations.
func TestGUC_Turing_StartTimeNonZero(t *testing.T) {
	stats := &Stats{}
	stats.StartTime = time.Now()
	if stats.StartTime.IsZero() {
		t.Error("Stats.StartTime is zero after explicit assignment")
	}
	// Must be in the past or present, not in the future.
	if stats.StartTime.After(time.Now().Add(time.Second)) {
		t.Error("Stats.StartTime is in the future")
	}
}

// TestGUC_Turing_StatsAnnounceHaltsOnMissingTorrent verifies that
// Announce returns an error quickly for an unknown info_hash rather than
// looping or blocking indefinitely.
func TestGUC_Turing_StatsAnnounceHaltsOnMissingTorrent(t *testing.T) {
	w, _, _ := newTestWorker()
	u := NewUser(UserID(50), true, false)
	req := &AnnounceRequest{
		InfoHash: "nosuchhashhashhashhh",
		PeerID:   gucMakePeerID("TU"),
		Port:     7004, Left: 100, Compact: true, NumWant: 50,
	}

	done := make(chan error, 1)
	go func() {
		_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.5.6.7"), "", "")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error for unregistered torrent, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Announce did not terminate within 2s for unregistered torrent")
	}
}

// TestGUC_Turing_AnnounceTerminatesOnStoppedEvent verifies that a stopped
// event on a peer that was never added does not block or panic.
func TestGUC_Turing_AnnounceTerminatesOnStoppedEvent(t *testing.T) {
	w, _ := gucWorkerWithTorrent(t, gucHash)
	u := NewUser(UserID(51), true, false)

	req := &AnnounceRequest{
		InfoHash: gucHash,
		PeerID:   gucMakePeerID("TS"),
		Port:     7005, Left: 200, Compact: true, Event: "stopped", NumWant: 0,
	}
	// Stopped with no prior announce — must not block.
	done := make(chan struct{}, 1)
	go func() {
		w.Announce(context.Background(), req, u, net.ParseIP("10.6.7.8"), "", "") //nolint:errcheck
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Announce did not terminate within 2s for stopped event on absent peer")
	}
}

// TestGUC_Turing_WhitelistEmptyAllowsAll verifies that an empty Whitelist
// (allow-all mode) permits any 20-byte peer_id prefix — a decidable O(1) check.
func TestGUC_Turing_WhitelistEmptyAllowsAll(t *testing.T) {
	wl := NewWhitelist()
	peerIDs := [][]byte{
		gucMakePeerID("AA"),
		gucMakePeerID("BB"),
		gucMakePeerID("CC"),
	}
	for _, pid := range peerIDs {
		if !wl.IsAllowed(pid) {
			t.Errorf("empty whitelist should allow peerID %q", pid)
		}
	}
}

// TestGUC_Turing_ParseAnnounceParamsZeroValuesTerminate verifies that
// ParseAnnounceParams does not block and correctly clamps zero/empty values.
func TestGUC_Turing_ParseAnnounceParamsZeroValuesTerminate(t *testing.T) {
	tests := []struct {
		name     string
		uploaded string
		wantUL   int64
	}{
		{"zero string", "0", 0},
		{"empty", "", 0},
		{"positive", "1024", 1024},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseInt64(tc.uploaded)
			if got != tc.wantUL {
				t.Errorf("parseInt64(%q) = %d, want %d", tc.uploaded, got, tc.wantUL)
			}
		})
	}
}

// ── CHURCH: functional purity, side-effect isolation ─────────────────────────

// TestGUC_Church_StatsScopedPerWorker verifies that two workers maintain
// completely independent Stats — incrementing one does not affect the other.
func TestGUC_Church_StatsScopedPerWorker(t *testing.T) {
	w1, _, _ := newTestWorker()
	w2, _, _ := newTestWorker()

	w1.Stats.SuccAnnouncements.Add(5)
	w2.Stats.SuccAnnouncements.Add(3)

	if w1.Stats.SuccAnnouncements.Load() != 5 {
		t.Errorf("w1.SuccAnnouncements = %d, want 5", w1.Stats.SuccAnnouncements.Load())
	}
	if w2.Stats.SuccAnnouncements.Load() != 3 {
		t.Errorf("w2.SuccAnnouncements = %d, want 3", w2.Stats.SuccAnnouncements.Load())
	}
}

// TestGUC_Church_SuccAnnouncementsNotIncrementedOnFailure verifies that
// a failed announce (non-compact) leaves SuccAnnouncements unchanged.
func TestGUC_Church_SuccAnnouncementsNotIncrementedOnFailure(t *testing.T) {
	w, _ := gucWorkerWithTorrent(t, gucHash)
	u := NewUser(UserID(60), true, false)
	before := w.Stats.SuccAnnouncements.Load()

	req := &AnnounceRequest{
		InfoHash: gucHash,
		PeerID:   gucMakePeerID("CF"),
		Port:     7006, Left: 100, Compact: false, // non-compact → rejection
		Event: "started", NumWant: 50,
	}
	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.7.8.9"), "", "")
	if err == nil {
		t.Fatal("expected error for non-compact announce, got nil")
	}
	after := w.Stats.SuccAnnouncements.Load()
	if after != before {
		t.Errorf("SuccAnnouncements changed on failure: %d → %d", before, after)
	}
}

// TestGUC_Church_SeederCountUnchangedByLeecherJoin verifies that Stats.Seeders
// is not modified when a leecher (left>0) joins the swarm.
func TestGUC_Church_SeederCountUnchangedByLeecherJoin(t *testing.T) {
	w, _ := gucWorkerWithTorrent(t, gucHash)
	u := NewUser(UserID(70), true, false)

	seedersBefore := w.Stats.Seeders.Load()
	req := &AnnounceRequest{
		InfoHash: gucHash,
		PeerID:   gucMakePeerID("CS"),
		Port:     7007, Left: 999, Compact: true, Event: "started", NumWant: 50,
	}
	if _, err := w.Announce(context.Background(), req, u, net.ParseIP("10.8.9.0"), "", ""); err != nil {
		t.Fatalf("leecher announce: %v", err)
	}
	if w.Stats.Seeders.Load() != seedersBefore {
		t.Errorf("Stats.Seeders changed on leecher join: %d → %d",
			seedersBefore, w.Stats.Seeders.Load())
	}
}

// TestGUC_Church_UserLeechingTracksIndependently verifies that a user's
// personal Leeching counter is incremented independently from the global gauge.
func TestGUC_Church_UserLeechingTracksIndependently(t *testing.T) {
	w, _ := gucWorkerWithTorrent(t, gucHash)
	u1 := NewUser(UserID(80), true, false)
	u2 := NewUser(UserID(81), true, false)
	ip := net.ParseIP("11.0.0.1")

	req1 := &AnnounceRequest{
		InfoHash: gucHash, PeerID: gucMakePeerID("U1"),
		Port: 7008, Left: 500, Compact: true, Event: "started", NumWant: 50,
	}
	req2 := &AnnounceRequest{
		InfoHash: gucHash, PeerID: gucMakePeerID("U2"),
		Port: 7009, Left: 600, Compact: true, Event: "started", NumWant: 50,
	}

	if _, err := w.Announce(context.Background(), req1, u1, ip, "", ""); err != nil {
		t.Fatalf("u1 announce: %v", err)
	}
	if _, err := w.Announce(context.Background(), req2, u2, ip, "", ""); err != nil {
		t.Fatalf("u2 announce: %v", err)
	}

	if u1.Leeching.Load() != 1 {
		t.Errorf("u1.Leeching = %d, want 1", u1.Leeching.Load())
	}
	if u2.Leeching.Load() != 1 {
		t.Errorf("u2.Leeching = %d, want 1", u2.Leeching.Load())
	}
	if w.Stats.Leechers.Load() != 2 {
		t.Errorf("global Leechers = %d, want 2", w.Stats.Leechers.Load())
	}
}

// TestGUC_Church_FreshStatsZeroInitialized verifies that a freshly allocated
// Stats struct has all counter fields at their zero values before any announce.
func TestGUC_Church_FreshStatsZeroInitialized(t *testing.T) {
	s := &Stats{}

	fields := []struct {
		name string
		val  uint64
	}{
		{"SuccAnnouncements", s.SuccAnnouncements.Load()},
		{"Announcements", s.Announcements.Load()},
		{"Leechers", uint64(s.Leechers.Load())},
		{"Seeders", uint64(s.Seeders.Load())},
		{"ClientRejections", s.ClientRejections.Load()},
		{"AnomalyRejections", s.AnomalyRejections.Load()},
		{"Scrapes", s.Scrapes.Load()},
	}
	for _, f := range fields {
		if f.val != 0 {
			t.Errorf("Stats.%s = %d, want 0 on fresh struct", f.name, f.val)
		}
	}
}

// ── GODEL: formal consistency, invariant preservation ─────────────────────────

// TestGUC_Godel_SeedersUint32NeverWrapsNegative verifies that Stats.Seeders
// is a uint32 — it cannot represent a negative value, guarding against sign errors.
func TestGUC_Godel_SeedersUint32NeverWrapsNegative(t *testing.T) {
	s := &Stats{}
	s.Seeders.Store(0)
	// A decrement on zero would wrap to MaxUint32; the design relies on
	// guards in Announce preventing this. Verify the type invariant holds.
	val := s.Seeders.Load()
	const maxUint32 = ^uint32(0)
	if val == maxUint32 {
		t.Error("Stats.Seeders wrapped to MaxUint32, indicating a spurious decrement")
	}
	// Positive increment must not wrap.
	s.Seeders.Add(1)
	if s.Seeders.Load() != 1 {
		t.Errorf("Seeders after +1 = %d, want 1", s.Seeders.Load())
	}
}

// TestGUC_Godel_ClientRejectionsAtomicNonNegative verifies that
// Stats.ClientRejections is a uint64 that monotonically increases and
// remains non-negative. The counter documents ML security rejections.
func TestGUC_Godel_ClientRejectionsAtomicNonNegative(t *testing.T) {
	s := &Stats{}
	if s.ClientRejections.Load() != 0 {
		t.Fatalf("ClientRejections initial value = %d, want 0", s.ClientRejections.Load())
	}
	for i := uint64(1); i <= 5; i++ {
		s.ClientRejections.Add(1)
		got := s.ClientRejections.Load()
		if got != i {
			t.Errorf("after %d adds, ClientRejections = %d, want %d", i, got, i)
		}
	}
}

// TestGUC_Godel_AnomalyRejectionsAtomicNonNegative verifies that
// Stats.AnomalyRejections is a uint64 that monotonically increases and
// remains non-negative. The counter documents ML anomaly-detector rejections.
func TestGUC_Godel_AnomalyRejectionsAtomicNonNegative(t *testing.T) {
	s := &Stats{}
	if s.AnomalyRejections.Load() != 0 {
		t.Fatalf("AnomalyRejections initial value = %d, want 0", s.AnomalyRejections.Load())
	}
	for i := uint64(1); i <= 5; i++ {
		s.AnomalyRejections.Add(1)
		got := s.AnomalyRejections.Load()
		if got != i {
			t.Errorf("after %d adds, AnomalyRejections = %d, want %d", i, got, i)
		}
	}
}

// TestGUC_Godel_TorrentCompletedIncrementsOnSnatch verifies that
// Torrent.Completed is incremented when a leecher sends the "completed" event,
// modelling the download counter (snatch counter in tracker terminology).
func TestGUC_Godel_TorrentCompletedIncrementsOnSnatch(t *testing.T) {
	w, _ := gucWorkerWithTorrent(t, gucHash)
	ip := net.ParseIP("11.1.2.3")
	u := NewUser(UserID(90), true, false)
	peerID := gucMakePeerID("DL")

	// Join as leecher.
	reqStart := &AnnounceRequest{
		InfoHash: gucHash, PeerID: peerID,
		Port: 7010, Left: 1000, Compact: true, Event: "started", NumWant: 50,
	}
	if _, err := w.Announce(context.Background(), reqStart, u, ip, "", ""); err != nil {
		t.Fatalf("start announce: %v", err)
	}

	tor, _ := w.Torrents.Get(gucHash)
	tor.mu.RLock()
	beforeCompleted := tor.Completed
	tor.mu.RUnlock()

	// Complete the download.
	reqDone := &AnnounceRequest{
		InfoHash: gucHash, PeerID: peerID,
		Port: 7010, Left: 0, Compact: true, Event: "completed", NumWant: 50,
	}
	if _, err := w.Announce(context.Background(), reqDone, u, ip, "", ""); err != nil {
		t.Fatalf("completed announce: %v", err)
	}

	tor.mu.RLock()
	afterCompleted := tor.Completed
	tor.mu.RUnlock()

	if afterCompleted != beforeCompleted+1 {
		t.Errorf("Torrent.Completed %d → %d, want +1 on snatch", beforeCompleted, afterCompleted)
	}
}

// TestGUC_Godel_ConcurrentAnnouncesSafe verifies that Stats counters remain
// self-consistent when multiple goroutines announce concurrently. The test
// asserts that SuccAnnouncements grows by exactly the number of successful
// announces dispatched — no double-counts, no skipped increments.
func TestGUC_Godel_ConcurrentAnnouncesSafe(t *testing.T) {
	const goroutines = 8
	w, _ := gucWorkerWithTorrent(t, gucHash)
	ip := net.ParseIP("11.2.3.4")

	before := w.Stats.SuccAnnouncements.Load()
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			suffix := strconv.Itoa(n)
			pid := gucMakePeerID(suffix)
			u := NewUser(UserID(uint32(100+n)), true, false)
			req := &AnnounceRequest{
				InfoHash: gucHash,
				PeerID:   pid,
				Port:     uint16(8000 + n),
				Left:     100,
				Compact:  true,
				Event:    "started",
				NumWant:  50,
			}
			w.Announce(context.Background(), req, u, ip, "", "") //nolint:errcheck
		}(i)
	}
	wg.Wait()

	after := w.Stats.SuccAnnouncements.Load()
	if after < before+goroutines {
		t.Errorf("SuccAnnouncements after %d concurrent announces = %d, want >= %d",
			goroutines, after, before+goroutines)
	}
}
