package tracker

// GUC (Gödel Unified Council) test coverage for freeleech accounting in Ocelot.
//
// Distribution:
//
//	Knuth  (~5): algorithmic correctness, data structure invariants, complexity
//	Turing (~5): termination, halting, decidability of operations
//	Church (~5): functional purity, side-effect isolation, referential transparency
//	Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"context"
	"net"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// flHash is a 20-byte info hash used exclusively by the freeleech GUC tests.
const flHash = "freeleechhash0000000"

// workerForFreeleech builds a Worker backed by the recording MockDB so tests
// can inspect which DB calls were made. The pre-seeded torrent has the given
// FreeType. A single user (ID=99) is placed at passkey "flpass0001".
func workerForFreeleech(ft FreeType) (*Worker, *MockDB, *Torrent, *User) {
	db := newMockDB()
	sc := newMockSiteComm()
	w := &Worker{
		Config: &Config{
			AnnounceInterval: 1800,
			NumWantLimit:     50,
			AllowPrivateIPs:  true,
		},
		DB:        db,
		SiteComm:  sc,
		Torrents:  NewTorrentList(),
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     &Stats{},
	}
	torrent := NewTorrent(TorrentID(99))
	torrent.FreeType = ft
	w.Torrents.Set(flHash, torrent)

	user := NewUser(UserID(99), true, false)
	w.Users.Set("flpass0001", user)

	return w, db, torrent, user
}

// flDoAnnounce sends one announce to w using the freeleech test hash and user.
// The passkey arg is empty because w.Admission is nil; user is passed directly.
func flDoAnnounce(w *Worker, user *User, pid []byte, event string, uploaded, downloaded, left int64) (*AnnounceResponse, error) {
	req := &AnnounceRequest{
		InfoHash:   flHash,
		PeerID:     pid,
		Port:       6881,
		Uploaded:   uploaded,
		Downloaded: downloaded,
		Left:       left,
		Compact:    true,
		Event:      event,
		NumWant:    50,
	}
	return w.Announce(context.Background(), req, user, net.ParseIP("1.1.1.1"), "guc-fl", "")
}

// flPID returns a 20-byte peer ID with every byte set to b. Named distinctly
// from announce_peers_guc_test.go's makePeerID to avoid a redeclaration.
func flPID(b byte) []byte {
	id := make([]byte, 20)
	for i := range id {
		id[i] = b
	}
	return id
}

// ── Knuth: algorithmic correctness, data structure invariants ─────────────────

// TestGUC_Knuth_FreeNormalStatDeltasCorrect verifies that under FreeNormal both
// the upload delta and the download delta are faithfully passed to RecordUserStats.
func TestGUC_Knuth_FreeNormalStatDeltasCorrect(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x01)

	// First announce: sets baseline at uploaded=1000, downloaded=500.
	if _, err := flDoAnnounce(w, user, pid, "started", 1000, 500, 2000); err != nil {
		t.Fatalf("first announce: %v", err)
	}
	db.reset()

	// Second announce: uploaded=3000 (+2000), downloaded=1500 (+1000).
	if _, err := flDoAnnounce(w, user, pid, "", 3000, 1500, 1500); err != nil {
		t.Fatalf("second announce: %v", err)
	}

	db.mu.Lock()
	stats := append([]recordedUserStats(nil), db.UserStats...)
	db.mu.Unlock()

	if len(stats) != 1 {
		t.Fatalf("RecordUserStats calls = %d, want 1", len(stats))
	}
	if stats[0].Uploaded != 2000 {
		t.Errorf("uploaded delta = %d, want 2000", stats[0].Uploaded)
	}
	if stats[0].Downloaded != 1000 {
		t.Errorf("downloaded delta = %d, want 1000", stats[0].Downloaded)
	}
}

// TestGUC_Knuth_FreeFreeDownloadDeltaZeroInStats verifies that under FreeFree
// the download delta written to RecordUserStats is zero while the upload delta
// is preserved, demonstrating the per-byte accounting invariant.
func TestGUC_Knuth_FreeFreeDownloadDeltaZeroInStats(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeFree)
	pid := flPID(0x02)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("first announce: %v", err)
	}
	db.reset()

	// Both upload (+500) and download (+1000) change; FreeFree zeroes download.
	if _, err := flDoAnnounce(w, user, pid, "", 500, 1000, 4000); err != nil {
		t.Fatalf("second announce: %v", err)
	}

	db.mu.Lock()
	stats := append([]recordedUserStats(nil), db.UserStats...)
	db.mu.Unlock()

	if len(stats) == 0 {
		t.Fatal("RecordUserStats not called; FreeFree with upload change must still credit upload")
	}
	if stats[0].Downloaded != 0 {
		t.Errorf("FreeFree downloaded = %d, want 0", stats[0].Downloaded)
	}
	if stats[0].Uploaded != 500 {
		t.Errorf("FreeFree uploaded = %d, want 500", stats[0].Uploaded)
	}
}

// TestGUC_Knuth_FreeNeutralBothDeltasZero verifies that under FreeNeutral
// RecordUserStats is never called because both upload and download deltas are
// zeroed before the guard check.
func TestGUC_Knuth_FreeNeutralBothDeltasZero(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeNeutral)
	pid := flPID(0x03)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("first announce: %v", err)
	}
	db.reset()

	if _, err := flDoAnnounce(w, user, pid, "", 800, 2000, 3000); err != nil {
		t.Fatalf("second announce: %v", err)
	}

	db.mu.Lock()
	n := len(db.UserStats)
	db.mu.Unlock()

	if n != 0 {
		t.Errorf("FreeNeutral: RecordUserStats called %d times, want 0", n)
	}
}

// TestGUC_Knuth_SeederReannounceKeepsUploadCredit verifies that a seeder making
// a subsequent announce with an increased upload counter has that delta written
// to RecordUserStats (i.e., upload credit accumulates correctly over announces).
func TestGUC_Knuth_SeederReannounceKeepsUploadCredit(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x04)

	// Establish baseline as seeder (Left=0).
	if _, err := flDoAnnounce(w, user, pid, "started", 1000, 0, 0); err != nil {
		t.Fatalf("first (seed) announce: %v", err)
	}
	db.reset()

	// Re-announce with upload increased by 1500.
	if _, err := flDoAnnounce(w, user, pid, "", 2500, 0, 0); err != nil {
		t.Fatalf("second (seed) announce: %v", err)
	}

	db.mu.Lock()
	stats := append([]recordedUserStats(nil), db.UserStats...)
	db.mu.Unlock()

	if len(stats) == 0 {
		t.Fatal("RecordUserStats not called on seeder re-announce with upload delta")
	}
	if stats[0].Uploaded != 1500 {
		t.Errorf("seeder upload delta = %d, want 1500", stats[0].Uploaded)
	}
}

// TestGUC_Knuth_ZeroDeltaProducesNoStatsCall verifies that when uploaded and
// downloaded are unchanged between announces (zero delta) RecordUserStats is
// not called — the guard `if uploadedChange > 0 || downloadedChange > 0` holds.
func TestGUC_Knuth_ZeroDeltaProducesNoStatsCall(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x05)

	if _, err := flDoAnnounce(w, user, pid, "started", 1000, 500, 2000); err != nil {
		t.Fatalf("first announce: %v", err)
	}
	db.reset()

	// Identical stats — delta = 0 on both uploaded and downloaded.
	if _, err := flDoAnnounce(w, user, pid, "", 1000, 500, 2000); err != nil {
		t.Fatalf("second announce (no change): %v", err)
	}

	db.mu.Lock()
	n := len(db.UserStats)
	db.mu.Unlock()

	if n != 0 {
		t.Errorf("zero-delta announce: RecordUserStats called %d times, want 0", n)
	}
}

// ── Turing: termination, halting, decidability ────────────────────────────────

// TestGUC_Turing_SnatchRecordedOnCompletedWithLeftZero verifies that a "completed"
// event with Left=0 triggers exactly one RecordSnatch call for the correct user.
func TestGUC_Turing_SnatchRecordedOnCompletedWithLeftZero(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x11)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("start announce: %v", err)
	}

	if _, err := flDoAnnounce(w, user, pid, "completed", 0, 5000, 0); err != nil {
		t.Fatalf("completed announce: %v", err)
	}

	db.mu.Lock()
	snatches := append([]recordedSnatch(nil), db.Snatches...)
	db.mu.Unlock()

	if len(snatches) != 1 {
		t.Fatalf("RecordSnatch called %d times, want 1", len(snatches))
	}
	if snatches[0].UserID != 99 {
		t.Errorf("snatch userID = %d, want 99", snatches[0].UserID)
	}
}

// TestGUC_Turing_NoDoubleSnatchOnRepeatedCompleted verifies that a second
// "completed" event for a peer already in the seeder swarm does not produce
// a second snatch record — the tracker detects the already-seeder state.
func TestGUC_Turing_NoDoubleSnatchOnRepeatedCompleted(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x12)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 3000); err != nil {
		t.Fatalf("start: %v", err)
	}
	// First completed → valid snatch.
	if _, err := flDoAnnounce(w, user, pid, "completed", 0, 3000, 0); err != nil {
		t.Fatalf("completed #1: %v", err)
	}
	// Second completed → peer already in seeders; completedTorrent becomes false.
	if _, err := flDoAnnounce(w, user, pid, "completed", 0, 3000, 0); err != nil {
		t.Fatalf("completed #2: %v", err)
	}

	db.mu.Lock()
	n := len(db.Snatches)
	db.mu.Unlock()

	if n != 1 {
		t.Errorf("snatch count = %d after two 'completed' events, want exactly 1", n)
	}
}

// TestGUC_Turing_StoppedEventTerminatesPeer verifies that a "stopped" announce
// removes the peer from the swarm, decrementing the leecher count to zero.
func TestGUC_Turing_StoppedEventTerminatesPeer(t *testing.T) {
	w, _, torrent, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x13)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 2000); err != nil {
		t.Fatalf("start: %v", err)
	}
	if torrent.Leechers.Size() != 1 {
		t.Fatalf("leecher count after start = %d, want 1", torrent.Leechers.Size())
	}

	if _, err := flDoAnnounce(w, user, pid, "stopped", 0, 0, 2000); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if torrent.Leechers.Size() != 0 {
		t.Errorf("leecher count after stop = %d, want 0", torrent.Leechers.Size())
	}
}

// TestGUC_Turing_LecherToSeederTransition verifies that a peer announcing with
// Left=0 (no "completed" event) is moved from the Leechers map to the Seeders
// map in a single step.
func TestGUC_Turing_LecherToSeederTransition(t *testing.T) {
	w, _, torrent, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x14)

	// Join as leecher.
	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Announce with Left=0 but no "completed" event.
	if _, err := flDoAnnounce(w, user, pid, "", 0, 5000, 0); err != nil {
		t.Fatalf("transition announce: %v", err)
	}

	if torrent.Leechers.Size() != 0 {
		t.Errorf("leechers after Left=0 = %d, want 0", torrent.Leechers.Size())
	}
	if torrent.Seeders.Size() != 1 {
		t.Errorf("seeders after Left=0 = %d, want 1", torrent.Seeders.Size())
	}
}

// TestGUC_Turing_CompletedWithPositiveLeftDoesNotSnatch verifies that a
// "completed" event with Left > 0 does not produce a snatch record because
// completedTorrent is set to (req.Left == 0) and is thus false here.
func TestGUC_Turing_CompletedWithPositiveLeftDoesNotSnatch(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x15)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("start: %v", err)
	}

	// "completed" but still has Left=2000 — completedTorrent = false, no snatch.
	if _, err := flDoAnnounce(w, user, pid, "completed", 0, 3000, 2000); err != nil {
		t.Fatalf("completed+Left>0: %v", err)
	}

	db.mu.Lock()
	n := len(db.Snatches)
	db.mu.Unlock()

	if n != 0 {
		t.Errorf("snatch count = %d for completed+Left>0, want 0", n)
	}
}

// ── Church: functional purity, side-effect isolation ─────────────────────────

// TestGUC_Church_FreeFreeUploadStillCredited verifies that FreeFree suppresses
// only the download side-effect, leaving the upload delta credited to the user.
func TestGUC_Church_FreeFreeUploadStillCredited(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeFree)
	pid := flPID(0x21)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("start: %v", err)
	}
	db.reset()

	// Upload increases by 1000; download increases by 3000 (to be zeroed).
	if _, err := flDoAnnounce(w, user, pid, "", 1000, 3000, 4000); err != nil {
		t.Fatalf("announce: %v", err)
	}

	db.mu.Lock()
	stats := append([]recordedUserStats(nil), db.UserStats...)
	db.mu.Unlock()

	if len(stats) == 0 {
		t.Fatal("RecordUserStats not called; FreeFree must still credit upload change")
	}
	if stats[0].Uploaded != 1000 {
		t.Errorf("FreeFree upload credited = %d, want 1000", stats[0].Uploaded)
	}
}

// TestGUC_Church_FreeFreeNoUploadNoStatsCall verifies that when only the download
// counter changes on a FreeFree torrent (upload stays at zero) RecordUserStats is
// NOT called — the zeroed download leaves no positive delta to record.
func TestGUC_Church_FreeFreeNoUploadNoStatsCall(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeFree)
	pid := flPID(0x22)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("start: %v", err)
	}
	db.reset()

	// Only download changes; uploaded stays at 0.
	if _, err := flDoAnnounce(w, user, pid, "", 0, 2000, 3000); err != nil {
		t.Fatalf("announce: %v", err)
	}

	db.mu.Lock()
	n := len(db.UserStats)
	db.mu.Unlock()

	if n != 0 {
		t.Errorf("FreeFree download-only: RecordUserStats called %d times, want 0", n)
	}
}

// TestGUC_Church_PerTorrentFreeTypePrecedenceTable verifies that the torrent's own
// FreeType governs stat recording: FreeNormal records both, FreeFree zeroes download,
// FreeNeutral records nothing. Uses a table to cover all three in one logical unit.
func TestGUC_Church_PerTorrentFreeTypePrecedenceTable(t *testing.T) {
	tests := []struct {
		name         string
		ft           FreeType
		wantStatCall bool
		wantDownload int64
	}{
		{"FreeNormal", FreeNormal, true, 500},
		{"FreeFree", FreeFree, true, 0},
		{"FreeNeutral", FreeNeutral, false, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, db, _, user := workerForFreeleech(tc.ft)
			pid := flPID(0x30)

			if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 2000); err != nil {
				t.Fatalf("start: %v", err)
			}
			db.reset()

			// Upload +300, download +500.
			if _, err := flDoAnnounce(w, user, pid, "", 300, 500, 1500); err != nil {
				t.Fatalf("announce: %v", err)
			}

			db.mu.Lock()
			stats := append([]recordedUserStats(nil), db.UserStats...)
			db.mu.Unlock()

			if tc.wantStatCall && len(stats) == 0 {
				t.Errorf("expected RecordUserStats call, got none")
			}
			if !tc.wantStatCall && len(stats) != 0 {
				t.Errorf("expected no RecordUserStats, got %d calls", len(stats))
			}
			if tc.wantStatCall && len(stats) > 0 && stats[0].Downloaded != tc.wantDownload {
				t.Errorf("downloaded = %d, want %d", stats[0].Downloaded, tc.wantDownload)
			}
		})
	}
}

// TestGUC_Church_TwoUsersGetIndependentStats verifies that two users announcing
// on the same torrent each produce their own separate RecordUserStats entry,
// demonstrating side-effect isolation between users.
func TestGUC_Church_TwoUsersGetIndependentStats(t *testing.T) {
	db := newMockDB()
	sc := newMockSiteComm()
	w := &Worker{
		Config: &Config{
			AnnounceInterval: 1800,
			NumWantLimit:     50,
			AllowPrivateIPs:  true,
		},
		DB:        db,
		SiteComm:  sc,
		Torrents:  NewTorrentList(),
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     &Stats{},
	}
	torrent := NewTorrent(TorrentID(77))
	torrent.FreeType = FreeNormal
	w.Torrents.Set(flHash, torrent)

	u1 := NewUser(UserID(10), true, false)
	u2 := NewUser(UserID(20), true, false)

	announceAs := func(u *User, pid []byte, up, dn, left int64, event string) {
		req := &AnnounceRequest{
			InfoHash:   flHash,
			PeerID:     pid,
			Port:       6881,
			Uploaded:   up,
			Downloaded: dn,
			Left:       left,
			Compact:    true,
			Event:      event,
			NumWant:    50,
		}
		_, _ = w.Announce(context.Background(), req, u, net.ParseIP("1.1.1.1"), "agent", "")
	}

	pid1, pid2 := flPID(0xA1), flPID(0xA2)

	// Baseline announces for both users.
	announceAs(u1, pid1, 0, 0, 5000, "started")
	announceAs(u2, pid2, 0, 0, 5000, "started")
	db.reset()

	// User 1 uploads 1000; User 2 uploads 2000 and downloads 500.
	announceAs(u1, pid1, 1000, 0, 5000, "")
	announceAs(u2, pid2, 2000, 500, 4500, "")

	db.mu.Lock()
	stats := append([]recordedUserStats(nil), db.UserStats...)
	db.mu.Unlock()

	if len(stats) != 2 {
		t.Fatalf("RecordUserStats calls = %d, want 2 (one per user)", len(stats))
	}

	var u1s, u2s *recordedUserStats
	for i := range stats {
		switch stats[i].UserID {
		case 10:
			u1s = &stats[i]
		case 20:
			u2s = &stats[i]
		}
	}
	if u1s == nil {
		t.Fatal("no stats recorded for user 10")
	}
	if u2s == nil {
		t.Fatal("no stats recorded for user 20")
	}
	if u1s.Uploaded != 1000 {
		t.Errorf("user10 uploaded = %d, want 1000", u1s.Uploaded)
	}
	if u2s.Uploaded != 2000 {
		t.Errorf("user20 uploaded = %d, want 2000", u2s.Uploaded)
	}
	if u2s.Downloaded != 500 {
		t.Errorf("user20 downloaded = %d, want 500", u2s.Downloaded)
	}
}

// TestGUC_Church_FreeNormalDownloadChargedToDownloader verifies referential
// transparency of the FreeNormal path: the download delta recorded in
// RecordUserStats is exactly req.Downloaded − prev.Downloaded, no more, no less.
func TestGUC_Church_FreeNormalDownloadChargedToDownloader(t *testing.T) {
	w, db, _, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x25)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("start: %v", err)
	}
	db.reset()

	const upDelta int64 = 200
	const downDelta int64 = 800

	if _, err := flDoAnnounce(w, user, pid, "", upDelta, downDelta, 4200); err != nil {
		t.Fatalf("announce: %v", err)
	}

	db.mu.Lock()
	stats := append([]recordedUserStats(nil), db.UserStats...)
	db.mu.Unlock()

	if len(stats) != 1 {
		t.Fatalf("RecordUserStats calls = %d, want 1", len(stats))
	}
	if stats[0].Uploaded != upDelta {
		t.Errorf("FreeNormal uploaded = %d, want %d", stats[0].Uploaded, upDelta)
	}
	if stats[0].Downloaded != downDelta {
		t.Errorf("FreeNormal downloaded = %d, want %d", stats[0].Downloaded, downDelta)
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state detection ─

// TestGUC_Godel_PeerInExactlyOneSwarm verifies that after a leecher-to-seeder
// transition a peer appears in exactly one of {Seeders, Leechers} — never both,
// never neither — which would be an impossible intermediate state.
func TestGUC_Godel_PeerInExactlyOneSwarm(t *testing.T) {
	w, _, torrent, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x31)

	// Join as leecher.
	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Transition: Left=0, no "completed" event.
	if _, err := flDoAnnounce(w, user, pid, "", 0, 5000, 0); err != nil {
		t.Fatalf("transition: %v", err)
	}

	key := PeerKeyPrime(pid, user.ID, torrent.ID)
	_, inSeeders := torrent.Seeders.Get(key)
	_, inLeechers := torrent.Leechers.Get(key)

	if inSeeders && inLeechers {
		t.Error("impossible state: peer is in BOTH seeders and leechers simultaneously")
	}
	if !inSeeders && !inLeechers {
		t.Error("impossible state: peer vanished from both swarms")
	}
	if !inSeeders {
		t.Error("peer should be in seeders after Left=0 non-completed announce")
	}
}

// TestGUC_Godel_TorrentCompletedCountExactlyOne verifies that Torrent.Completed
// is incremented by exactly 1 per valid snatch event — no more, no less.
func TestGUC_Godel_TorrentCompletedCountExactlyOne(t *testing.T) {
	w, _, torrent, user := workerForFreeleech(FreeNormal)
	pid := flPID(0x32)

	torrent.mu.RLock()
	before := torrent.Completed
	torrent.mu.RUnlock()

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 3000); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := flDoAnnounce(w, user, pid, "completed", 0, 3000, 0); err != nil {
		t.Fatalf("completed: %v", err)
	}

	torrent.mu.RLock()
	after := torrent.Completed
	torrent.mu.RUnlock()

	if after != before+1 {
		t.Errorf("Torrent.Completed = %d, want %d (before=%d)", after, before+1, before)
	}
}

// TestGUC_Godel_FreeTypeNotMutatedByAnnounce verifies that announce processing
// never mutates the torrent's FreeType field — a consistency invariant that must
// hold for all three FreeType values.
func TestGUC_Godel_FreeTypeNotMutatedByAnnounce(t *testing.T) {
	freeTypes := []FreeType{FreeNormal, FreeFree, FreeNeutral}
	for _, ft := range freeTypes {
		w, _, torrent, user := workerForFreeleech(ft)
		pid := flPID(0x33)

		if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 2000); err != nil {
			t.Fatalf("ft=%d start: %v", ft, err)
		}
		if _, err := flDoAnnounce(w, user, pid, "", 100, 200, 1800); err != nil {
			t.Fatalf("ft=%d announce: %v", ft, err)
		}

		torrent.mu.RLock()
		got := torrent.FreeType
		torrent.mu.RUnlock()

		if got != ft {
			t.Errorf("FreeType mutated: want %d, got %d", ft, got)
		}
	}
}

// TestGUC_Godel_FreeNeutralTorrentBalanceUpdated verifies a subtle consistency
// property: even under FreeNeutral (no user stats), the torrent's internal
// Balance is still updated with the raw upload−download traffic because the
// balance update occurs before the FreeType stat-zeroing switch statement.
func TestGUC_Godel_FreeNeutralTorrentBalanceUpdated(t *testing.T) {
	w, db, torrent, user := workerForFreeleech(FreeNeutral)
	pid := flPID(0x34)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("start: %v", err)
	}
	db.reset()

	torrent.mu.RLock()
	balanceBefore := torrent.Balance
	torrent.mu.RUnlock()

	// upload+300, download+1000 → balance delta = 300−1000 = −700.
	if _, err := flDoAnnounce(w, user, pid, "", 300, 1000, 4000); err != nil {
		t.Fatalf("announce: %v", err)
	}

	torrent.mu.RLock()
	balanceAfter := torrent.Balance
	torrent.mu.RUnlock()

	expected := balanceBefore + 300 - 1000
	if balanceAfter != expected {
		t.Errorf("torrent balance = %d, want %d (upload−download applied before FreeNeutral zeroing)",
			balanceAfter, expected)
	}

	// Confirm FreeNeutral still wrote no user stats.
	db.mu.Lock()
	n := len(db.UserStats)
	db.mu.Unlock()
	if n != 0 {
		t.Errorf("FreeNeutral: RecordUserStats called %d times, want 0", n)
	}
}

// TestGUC_Godel_FreeFreeBalanceReflectsFullTraffic verifies that the torrent's
// internal Balance accounts for both upload and download traffic even under
// FreeFree — the balance update is independent of per-user stat suppression.
func TestGUC_Godel_FreeFreeBalanceReflectsFullTraffic(t *testing.T) {
	w, _, torrent, user := workerForFreeleech(FreeFree)
	pid := flPID(0x35)

	if _, err := flDoAnnounce(w, user, pid, "started", 0, 0, 5000); err != nil {
		t.Fatalf("start: %v", err)
	}

	torrent.mu.RLock()
	initial := torrent.Balance
	torrent.mu.RUnlock()

	// upload+400, download+2000 → balance delta = 400−2000 = −1600.
	if _, err := flDoAnnounce(w, user, pid, "", 400, 2000, 3000); err != nil {
		t.Fatalf("announce: %v", err)
	}

	torrent.mu.RLock()
	final := torrent.Balance
	torrent.mu.RUnlock()

	expected := initial + 400 - 2000
	if final != expected {
		t.Errorf("FreeFree torrent balance = %d, want %d", final, expected)
	}
}
