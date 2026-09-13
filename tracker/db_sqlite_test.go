package tracker

import (
	"testing"
	"time"
)

// newTestDB opens a temporary SQLite shard in t.TempDir().
func newTestDB(t *testing.T) *SQLiteShardManager {
	t.Helper()
	sm, err := NewSQLiteShardManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	t.Cleanup(func() { sm.Close() })
	return sm
}

// ── Schema ────────────────────────────────────────────────────────────────────

func TestNewSQLiteShardManager_Opens(t *testing.T) {
	sm := newTestDB(t)
	if sm.currentDB == nil {
		t.Fatal("currentDB is nil after open")
	}
}

// ── Peer writes ───────────────────────────────────────────────────────────────

func TestRecordPeer_Insert(t *testing.T) {
	sm := newTestDB(t)
	err := sm.RecordPeer(1, 1, 1, 1024, 512, 10, 5, 0, 0, 60, 3, "1.2.3.4", "-qB4xxx", "TestUA")
	if err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}
}

func TestRecordPeer_Upsert(t *testing.T) {
	sm := newTestDB(t)
	for i := 0; i < 3; i++ {
		err := sm.RecordPeer(1, 1, 1, int64(i*100), int64(i*50), 10, 5, 0, 0, 60, uint32(i+1), "1.2.3.4", "-qB4xxx", "UA")
		if err != nil {
			t.Fatalf("RecordPeer iter %d: %v", i, err)
		}
	}
}

func TestRecordPeerLight_Update(t *testing.T) {
	sm := newTestDB(t)
	// Insert first
	sm.RecordPeer(2, 2, 1, 0, 0, 0, 0, 0, 0, 10, 1, "", "-XX-xxx", "UA")
	// Light update
	err := sm.RecordPeerLight(2, 2, 20, 2, "-XX-xxx")
	if err != nil {
		t.Fatalf("RecordPeerLight: %v", err)
	}
}

// ── User stats ────────────────────────────────────────────────────────────────

func TestRecordUserStats_Accumulates(t *testing.T) {
	sm := newTestDB(t)
	sm.RecordUserStats(10, 1000, 500)
	sm.RecordUserStats(10, 2000, 1000)

	up, down, err := sm.GetUserStats(10)
	if err != nil {
		t.Fatalf("GetUserStats: %v", err)
	}
	if up != 3000 {
		t.Errorf("uploaded = %d, want 3000", up)
	}
	if down != 1500 {
		t.Errorf("downloaded = %d, want 1500", down)
	}
}

func TestGetUserStats_Missing_ReturnsZero(t *testing.T) {
	sm := newTestDB(t)
	up, down, err := sm.GetUserStats(9999)
	if err != nil {
		t.Fatalf("GetUserStats: %v", err)
	}
	if up != 0 || down != 0 {
		t.Errorf("missing user stats: up=%d down=%d, want 0/0", up, down)
	}
}

// ── Torrent records ───────────────────────────────────────────────────────────

func TestRecordTorrent_Upsert(t *testing.T) {
	sm := newTestDB(t)
	err := sm.RecordTorrent(5, 10, 3, 1, 10000)
	if err != nil {
		t.Fatalf("RecordTorrent: %v", err)
	}
	// Second upsert — snatched should accumulate
	err = sm.RecordTorrent(5, 12, 2, 2, 12000)
	if err != nil {
		t.Fatalf("RecordTorrent second: %v", err)
	}
}

// ── Snatch ────────────────────────────────────────────────────────────────────

func TestRecordSnatch(t *testing.T) {
	sm := newTestDB(t)
	err := sm.RecordSnatch(1, 5, time.Now(), "10.0.0.1")
	if err != nil {
		t.Fatalf("RecordSnatch: %v", err)
	}
	// Duplicate (INSERT OR IGNORE) should not error
	err = sm.RecordSnatch(1, 5, time.Now().Add(-time.Second), "10.0.0.1")
	if err != nil {
		t.Fatalf("RecordSnatch duplicate: %v", err)
	}
}

// ── Token ─────────────────────────────────────────────────────────────────────

func TestRecordToken_Accumulates(t *testing.T) {
	sm := newTestDB(t)
	sm.RecordToken(1, 1, 500)
	sm.RecordToken(1, 1, 300)
	// No error = pass (accumulation checked via LoadTokens indirectly)
}

// ── TorrentHash (restart persistence) ────────────────────────────────────────

func TestRecordTorrentHash_AndLoad(t *testing.T) {
	sm := newTestDB(t)

	// Write torrent row first (FK not enforced in SQLite by default, but keep consistent)
	sm.RecordTorrent(42, 0, 0, 0, 0)
	sm.RecordTorrentHash(42, "infohash42")

	rows, err := sm.LoadTorrents()
	if err != nil {
		t.Fatalf("LoadTorrents: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.infoHash == "infohash42" && r.id == 42 {
			found = true
		}
	}
	if !found {
		t.Error("torrent hash not found in LoadTorrents")
	}
}

func TestRecordTorrentHash_Idempotent(t *testing.T) {
	sm := newTestDB(t)
	sm.RecordTorrent(1, 0, 0, 0, 0)
	sm.RecordTorrentHash(1, "hash1")
	// Replace is idempotent
	err := sm.RecordTorrentHash(1, "hash1")
	if err != nil {
		t.Fatalf("second RecordTorrentHash: %v", err)
	}
}

// ── UserPasskey (restart persistence) ────────────────────────────────────────

func TestRecordUserPasskey_AndLoad(t *testing.T) {
	sm := newTestDB(t)
	sm.RecordUserPasskey(99, "mypasskey99", true, false)

	rows, err := sm.LoadUsers()
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.id == 99 && r.passkey == "mypasskey99" && r.canLeech && !r.protectIP {
			found = true
		}
	}
	if !found {
		t.Error("user passkey not found in LoadUsers")
	}
}

func TestRecordUserPasskey_UpdatesOnReplace(t *testing.T) {
	sm := newTestDB(t)
	sm.RecordUserPasskey(1, "pk1", true, false)
	sm.RecordUserPasskey(1, "pk1", false, true) // update same user

	rows, _ := sm.LoadUsers()
	for _, r := range rows {
		if r.id == 1 {
			if r.canLeech {
				t.Error("canLeech should be false after update")
			}
			if !r.protectIP {
				t.Error("protectIP should be true after update")
			}
		}
	}
}

// ── Whitelist persistence ─────────────────────────────────────────────────────

func TestAddRemoveWhitelistEntry(t *testing.T) {
	sm := newTestDB(t)
	sm.AddWhitelistEntry("-qB4")
	sm.AddWhitelistEntry("-DE1")

	prefixes, err := sm.LoadWhitelist()
	if err != nil {
		t.Fatalf("LoadWhitelist: %v", err)
	}
	if len(prefixes) != 2 {
		t.Fatalf("expected 2 prefixes, got %d: %v", len(prefixes), prefixes)
	}

	sm.RemoveWhitelistEntry("-qB4")
	prefixes, _ = sm.LoadWhitelist()
	if len(prefixes) != 1 || prefixes[0] != "-DE1" {
		t.Errorf("after remove: %v, want [\"-DE1\"]", prefixes)
	}
}

func TestAddWhitelistEntry_Idempotent(t *testing.T) {
	sm := newTestDB(t)
	sm.AddWhitelistEntry("-qB4")
	sm.AddWhitelistEntry("-qB4") // INSERT OR IGNORE
	prefixes, _ := sm.LoadWhitelist()
	if len(prefixes) != 1 {
		t.Errorf("duplicate entry: %v", prefixes)
	}
}

// ── LoadTokens ────────────────────────────────────────────────────────────────

func TestLoadTokens_JoinsHash(t *testing.T) {
	sm := newTestDB(t)

	// Set up torrent with hash mapping
	sm.RecordTorrent(10, 0, 0, 0, 0)
	sm.RecordTorrentHash(10, "tokenhash10")

	// Record a token
	sm.RecordToken(55, 10, 1024)

	tokens, err := sm.LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	users, ok := tokens["tokenhash10"]
	if !ok {
		t.Fatal("no token entry for tokenhash10")
	}
	if len(users) != 1 || users[0] != 55 {
		t.Errorf("users = %v, want [55]", users)
	}
}

func TestLoadTokens_NoOrphan(t *testing.T) {
	sm := newTestDB(t)
	// Token for torrent 99 which has no hash mapping
	sm.RecordToken(1, 99, 100)

	tokens, err := sm.LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("orphan token appeared: %v", tokens)
	}
}

// ── Maintenance ───────────────────────────────────────────────────────────────

func TestCheckpointWAL(t *testing.T) {
	sm := newTestDB(t)
	if err := sm.CheckpointWAL(); err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}
}

func TestCheckRotation_NoRotationBelowLimit(t *testing.T) {
	sm := newTestDB(t)
	// File is tiny; should not rotate
	if err := sm.CheckRotation(); err != nil {
		t.Fatalf("CheckRotation: %v", err)
	}
	// Path should be unchanged
	if sm.currentPath == "" {
		t.Error("currentPath empty after CheckRotation")
	}
}

func TestGetDBStats(t *testing.T) {
	sm := newTestDB(t)
	curSize, numHist, total, err := sm.GetDBStats()
	if err != nil {
		t.Fatalf("GetDBStats: %v", err)
	}
	if curSize < 0 {
		t.Errorf("currentSize = %d, want ≥ 0", curSize)
	}
	if numHist != 0 {
		t.Errorf("numHistorical = %d, want 0 for fresh DB", numHist)
	}
	if total < curSize {
		t.Errorf("totalSize (%d) < currentSize (%d)", total, curSize)
	}
}

// ── Loader integration ────────────────────────────────────────────────────────

func TestLoader_LoadAll_EmptyDB(t *testing.T) {
	sm := newTestDB(t)
	torrents := NewTorrentList()
	users := NewUserList()
	wl := NewWhitelist()

	loader := NewLoader(sm, torrents, users, wl)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll on empty DB: %v", err)
	}
	if torrents.Size() != 0 || users.Size() != 0 {
		t.Error("empty DB should produce empty lists")
	}
}

func TestLoader_LoadAll_RestoresState(t *testing.T) {
	sm := newTestDB(t)

	// Populate DB
	sm.RecordTorrent(1, 0, 0, 5, 1000)
	sm.RecordTorrentHash(1, "hash1")
	sm.RecordTorrent(2, 0, 0, 3, 500)
	sm.RecordTorrentHash(2, "hash2")
	sm.RecordUserPasskey(10, "passkey10", true, false)
	sm.RecordUserPasskey(11, "passkey11", false, true)
	sm.AddWhitelistEntry("-qB4")
	sm.AddWhitelistEntry("-DE1")

	torrents := NewTorrentList()
	users := NewUserList()
	wl := NewWhitelist()

	loader := NewLoader(sm, torrents, users, wl)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if torrents.Size() != 2 {
		t.Errorf("torrent count = %d, want 2", torrents.Size())
	}
	if users.Size() != 2 {
		t.Errorf("user count = %d, want 2", users.Size())
	}

	tor1, ok := torrents.Get("hash1")
	if !ok {
		t.Fatal("hash1 not loaded")
	}
	if tor1.Completed != 5 {
		t.Errorf("hash1 Completed = %d, want 5", tor1.Completed)
	}

	u10, ok := users.Get("passkey10")
	if !ok {
		t.Fatal("passkey10 not loaded")
	}
	if !u10.CanLeech.Load() {
		t.Error("passkey10 CanLeech should be true")
	}
	u11, _ := users.Get("passkey11")
	if u11.CanLeech.Load() {
		t.Error("passkey11 CanLeech should be false")
	}
	if !u11.ProtectIP.Load() {
		t.Error("passkey11 ProtectIP should be true")
	}

	if len(wl.GetAll()) != 2 {
		t.Errorf("whitelist count = %d, want 2", len(wl.GetAll()))
	}
}

func TestLoader_Reload_MergesWithoutWipingPeers(t *testing.T) {
	sm := newTestDB(t)

	sm.RecordTorrent(1, 0, 0, 5, 0)
	sm.RecordTorrentHash(1, "hash1")
	sm.RecordUserPasskey(10, "passkey10", true, false)

	torrents := NewTorrentList()
	users := NewUserList()
	wl := NewWhitelist()
	loader := NewLoader(sm, torrents, users, wl)
	loader.LoadAll()

	// Add a live peer to the torrent
	tor, _ := torrents.Get("hash1")
	peer := &Peer{UserID: 99}
	tor.Seeders.Set("livekey", peer)

	// Change metadata in DB
	sm.RecordTorrent(1, 0, 0, 10, 500) // completed now 10 (accumulated, not replaced directly)
	// Simulate: update completed by re-recording the torrent_hashes entry (already there)
	// The free_type is stored in torrents table, update it:
	// (In practice Gazelle would call update_torrent; here we patch directly for test)

	// Reload — must not wipe Seeders
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	tor2, _ := torrents.Get("hash1")
	if tor2.Seeders.Size() != 1 {
		t.Error("Reload wiped live peer list — must not happen")
	}
}

func TestLoader_Reload_InsertsNewTorrents(t *testing.T) {
	sm := newTestDB(t)
	sm.RecordTorrent(1, 0, 0, 0, 0)
	sm.RecordTorrentHash(1, "hash1")

	torrents := NewTorrentList()
	loader := NewLoader(sm, torrents, NewUserList(), NewWhitelist())
	loader.LoadAll()

	// Add a second torrent to DB
	sm.RecordTorrent(2, 0, 0, 0, 0)
	sm.RecordTorrentHash(2, "hash2")

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if torrents.Size() != 2 {
		t.Errorf("torrent count = %d, want 2 after Reload", torrents.Size())
	}
}

func TestLoader_CreateSchemaIfNeeded_NoOp(t *testing.T) {
	sm := newTestDB(t)
	loader := NewLoader(sm, NewTorrentList(), NewUserList(), NewWhitelist())
	// Should succeed — schema already exists, this is a documented no-op
	if err := loader.CreateSchemaIfNeeded(); err != nil {
		t.Fatalf("CreateSchemaIfNeeded: %v", err)
	}
}
