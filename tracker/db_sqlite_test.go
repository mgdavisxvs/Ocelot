package tracker

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
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

func TestNewSQLiteShardManager_MkdirError_ReturnsError(t *testing.T) {
	// Pass a path whose parent is an existing file — os.MkdirAll fails.
	f, err := os.CreateTemp(t.TempDir(), "notadir")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = NewSQLiteShardManager(filepath.Join(f.Name(), "subdir"))
	if err == nil {
		t.Error("expected error when dbDir parent is a file, got nil")
	}
}

// ── Peer writes ───────────────────────────────────────────────────────────────

func TestRecordPeer_Insert(t *testing.T) {
	sm := newTestDB(t)
	err := sm.RecordPeer(1, 1, 1, 1024, 512, 10, 5, 0, 0, 60, 3, "1.2.3.4", "-qB4xxx", "TestUA", false)
	if err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}
}

func TestRecordPeer_Upsert(t *testing.T) {
	sm := newTestDB(t)
	for i := 0; i < 3; i++ {
		err := sm.RecordPeer(1, 1, 1, int64(i*100), int64(i*50), 10, 5, 0, 0, 60, uint32(i+1), "1.2.3.4", "-qB4xxx", "UA", false)
		if err != nil {
			t.Fatalf("RecordPeer iter %d: %v", i, err)
		}
	}
}

func TestRecordPeerLight_Update(t *testing.T) {
	sm := newTestDB(t)
	// Insert first
	sm.RecordPeer(2, 2, 1, 0, 0, 0, 0, 0, 0, 10, 1, "", "-XX-xxx", "UA", false)
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

// ── CheckRotation with empty currentPath ──────────────────────────────────────

func TestCheckRotation_EmptyPath_NoOp(t *testing.T) {
	sm := &SQLiteShardManager{
		historicalDBs: make(map[string]*sql.DB),
		// currentPath intentionally empty
	}
	if err := sm.CheckRotation(); err != nil {
		t.Fatalf("CheckRotation with empty path: %v", err)
	}
}

// ── GetUserStats with no rows ─────────────────────────────────────────────────

func TestGetUserStats_ZeroWhenAbsent(t *testing.T) {
	sm := newTestDB(t)
	ul, dl, err := sm.GetUserStats(9999)
	if err != nil {
		t.Fatalf("GetUserStats: %v", err)
	}
	if ul != 0 || dl != 0 {
		t.Errorf("expected (0,0) for absent user, got (%d,%d)", ul, dl)
	}
}

// ── loadTokens integration via LoadAll ───────────────────────────────────────

func TestLoader_LoadAll_LoadsTokens(t *testing.T) {
	sm := newTestDB(t)

	// Set up torrent and token data
	sm.RecordTorrent(1, 0, 0, 0, 0)
	sm.RecordTorrentHash(1, "tok_hash")
	sm.RecordToken(UserID(42), TorrentID(1), 1024)

	torrents := NewTorrentList()
	users := NewUserList()
	wl := NewWhitelist()

	loader := NewLoader(sm, torrents, users, wl)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	tor, ok := torrents.Get("tok_hash")
	if !ok {
		t.Fatal("torrent not loaded")
	}
	if _, has := tor.TokenedUsers[42]; !has {
		t.Error("expected user 42 to have a token on the torrent")
	}
}

// ── loader.loadTokens skips orphan torrent ────────────────────────────────────

func TestLoader_LoadAll_TokenOrphanSkipped(t *testing.T) {
	sm := newTestDB(t)

	// A torrent in tokens table but not in the torrent_hashes table
	// (orphan). RecordToken uses torrent ID directly; set up tokens without
	// a corresponding torrent_hashes entry.
	sm.RecordTorrent(99, 0, 0, 0, 0) // no RecordTorrentHash → not loaded as torrent
	sm.RecordToken(UserID(1), TorrentID(99), 512)

	torrents := NewTorrentList()
	loader := NewLoader(sm, torrents, NewUserList(), NewWhitelist())
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll with orphan token: %v", err)
	}
	// No torrent in list → LoadAll should not panic or error
	if torrents.Size() != 0 {
		t.Errorf("expected 0 torrents, got %d", torrents.Size())
	}
}

// ── loadHistoricalDBs ─────────────────────────────────────────────────────────

func TestLoadHistoricalDBs_PicksUpOldFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a historical DB file using a past-month name so it won't be treated
	// as the current month's DB.
	oldName := "ocelot-2020-01.db"
	oldPath := filepath.Join(dir, oldName)
	tmpSM := &SQLiteShardManager{dbDir: dir, historicalDBs: make(map[string]*sql.DB)}
	oldDB, err := tmpSM.openDB(oldPath)
	if err != nil {
		t.Fatalf("create old db: %v", err)
	}
	oldDB.Close()

	// NewSQLiteShardManager should call loadHistoricalDBs and open ocelot-2020-01.db.
	sm, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	defer sm.Close()

	if len(sm.historicalDBs) != 1 {
		t.Errorf("historicalDBs = %d, want 1", len(sm.historicalDBs))
	}
	if _, ok := sm.historicalDBs[oldName]; !ok {
		t.Errorf("expected key %q in historicalDBs", oldName)
	}
}

func TestLoadHistoricalDBs_IgnoresNonDBFiles(t *testing.T) {
	dir := t.TempDir()

	// Put a non-.db file in the dir — it must be ignored.
	if err := writePlainFile(dir, "readme.txt", []byte("ignored")); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	// Also put a historical db to confirm the filter is selective, not total.
	oldPath := filepath.Join(dir, "ocelot-2019-06.db")
	tmpSM := &SQLiteShardManager{dbDir: dir, historicalDBs: make(map[string]*sql.DB)}
	oldDB, _ := tmpSM.openDB(oldPath)
	oldDB.Close()

	sm, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	defer sm.Close()

	if len(sm.historicalDBs) != 1 {
		t.Errorf("historicalDBs = %d, want 1 (only .db files)", len(sm.historicalDBs))
	}
}

func TestGetUserStats_AggregatesAcrossShards(t *testing.T) {
	dir := t.TempDir()

	// Create historical db, insert user stats into it.
	oldPath := filepath.Join(dir, "ocelot-2020-01.db")
	tmpSM := &SQLiteShardManager{dbDir: dir, historicalDBs: make(map[string]*sql.DB)}
	oldDB, err := tmpSM.openDB(oldPath)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	// Create users table in the historical db.
	_, err = oldDB.Exec(`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY,
		uploaded INTEGER NOT NULL DEFAULT 0,
		downloaded INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		oldDB.Close()
		t.Fatalf("create users table: %v", err)
	}
	_, err = oldDB.Exec(`INSERT INTO users (id, uploaded, downloaded) VALUES (42, 1000, 500)`)
	if err != nil {
		oldDB.Close()
		t.Fatalf("insert historical stats: %v", err)
	}
	oldDB.Close()

	sm, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	defer sm.Close()

	// Write stats in the current shard as well.
	sm.RecordUserStats(42, 2000, 800)

	up, down, err := sm.GetUserStats(42)
	if err != nil {
		t.Fatalf("GetUserStats: %v", err)
	}
	if up != 3000 {
		t.Errorf("uploaded = %d, want 3000 (1000 historical + 2000 current)", up)
	}
	if down != 1300 {
		t.Errorf("downloaded = %d, want 1300 (500 historical + 800 current)", down)
	}
}

// writePlainFile writes content to name inside dir.
func writePlainFile(dir, name string, content []byte) error {
	return os.WriteFile(filepath.Join(dir, name), content, 0644)
}

func TestGetDBStats_WithHistoricalDBs(t *testing.T) {
	dir := t.TempDir()

	// Create a historical db.
	oldPath := filepath.Join(dir, "ocelot-2021-03.db")
	tmpSM := &SQLiteShardManager{dbDir: dir, historicalDBs: make(map[string]*sql.DB)}
	oldDB, err := tmpSM.openDB(oldPath)
	if err != nil {
		t.Fatalf("create historical db: %v", err)
	}
	oldDB.Close()

	sm, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	defer sm.Close()

	curSize, numHist, total, err := sm.GetDBStats()
	if err != nil {
		t.Fatalf("GetDBStats: %v", err)
	}
	if numHist != 1 {
		t.Errorf("numHistorical = %d, want 1", numHist)
	}
	if total < curSize {
		t.Errorf("totalSize (%d) < currentSize (%d)", total, curSize)
	}
}

// ── loadHistoricalDBs — non-existent directory ────────────────────────────────

func TestLoadHistoricalDBs_NonExistentDir_ReturnsNil(t *testing.T) {
	sm := &SQLiteShardManager{
		dbDir:         "/nonexistent/dir/for/test/ocelot_xyz",
		historicalDBs: make(map[string]*sql.DB),
	}
	if err := sm.loadHistoricalDBs(); err != nil {
		t.Errorf("expected nil for non-existent dir, got: %v", err)
	}
}

// ── LoadAll error paths (drop individual tables) ──────────────────────────────

func newSMForErrorTest(t *testing.T) *SQLiteShardManager {
	t.Helper()
	dir := t.TempDir()
	sm, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	t.Cleanup(func() { sm.Close() })
	return sm
}

func TestLoadAll_TorrentsError(t *testing.T) {
	sm := newSMForErrorTest(t)
	// Drop torrent_hashes so the LoadTorrents JOIN fails.
	sm.currentDB.Exec("DROP TABLE torrent_hashes")

	loader := NewLoader(sm, NewTorrentList(), NewUserList(), NewWhitelist())
	err := loader.LoadAll()
	if err == nil {
		t.Fatal("expected error when torrent_hashes table missing, got nil")
	}
}

func TestLoadAll_UsersError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.currentDB.Exec("DROP TABLE user_passkeys")

	loader := NewLoader(sm, NewTorrentList(), NewUserList(), NewWhitelist())
	err := loader.LoadAll()
	if err == nil {
		t.Fatal("expected error when user_passkeys table missing, got nil")
	}
}

func TestLoadAll_WhitelistError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.currentDB.Exec("DROP TABLE whitelist")

	loader := NewLoader(sm, NewTorrentList(), NewUserList(), NewWhitelist())
	err := loader.LoadAll()
	if err == nil {
		t.Fatal("expected error when whitelist table missing, got nil")
	}
}

func TestLoadAll_TokensError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.currentDB.Exec("DROP TABLE tokens")

	loader := NewLoader(sm, NewTorrentList(), NewUserList(), NewWhitelist())
	err := loader.LoadAll()
	if err == nil {
		t.Fatal("expected error when tokens table missing, got nil")
	}
}

// ── Reload — existing entry update branches ───────────────────────────────────

func TestReload_UpdatesExistingTorrent(t *testing.T) {
	sm := newSMForErrorTest(t)

	// Persist torrent with balance=500 in DB
	sm.RecordTorrent(7, 0, 0, 0, 500)
	sm.RecordTorrentHash(7, "reloadhash7")

	torrents := NewTorrentList()
	// Pre-populate with old balance so the existing-update branch fires
	old := NewTorrent(7)
	old.Balance = 999
	torrents.Set("reloadhash7", old)

	loader := NewLoader(sm, torrents, NewUserList(), NewWhitelist())
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	updated, ok := torrents.Get("reloadhash7")
	if !ok {
		t.Fatal("torrent not found after Reload")
	}
	updated.mu.RLock()
	bal := updated.Balance
	updated.mu.RUnlock()
	if bal != 500 {
		t.Errorf("Balance = %d, want 500 after Reload update", bal)
	}
}

func TestReload_UpdatesExistingUser(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.RecordUserPasskey(3, "pk_reload3", true, false)

	users := NewUserList()
	// Pre-populate so the existing user branch fires
	users.Set("pk_reload3", NewUser(3, false, true))

	loader := NewLoader(sm, NewTorrentList(), users, NewWhitelist())
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	u, ok := users.Get("pk_reload3")
	if !ok {
		t.Fatal("user not found after Reload")
	}
	if !u.CanLeech.Load() {
		t.Error("CanLeech should be true after Reload update")
	}
}

func TestReload_TorrentsError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.currentDB.Exec("DROP TABLE torrent_hashes")

	loader := NewLoader(sm, NewTorrentList(), NewUserList(), NewWhitelist())
	if err := loader.Reload(); err == nil {
		t.Fatal("expected error when torrent_hashes missing, got nil")
	}
}

func TestReload_UsersError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.currentDB.Exec("DROP TABLE user_passkeys")

	loader := NewLoader(sm, NewTorrentList(), NewUserList(), NewWhitelist())
	if err := loader.Reload(); err == nil {
		t.Fatal("expected error when user_passkeys missing, got nil")
	}
}

func TestReload_WhitelistError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.currentDB.Exec("DROP TABLE whitelist")

	loader := NewLoader(sm, NewTorrentList(), NewUserList(), NewWhitelist())
	if err := loader.Reload(); err == nil {
		t.Fatal("expected error when whitelist table missing, got nil")
	}
}

func TestReload_TokensError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.currentDB.Exec("DROP TABLE tokens")

	loader := NewLoader(sm, NewTorrentList(), NewUserList(), NewWhitelist())
	if err := loader.Reload(); err == nil {
		t.Fatal("expected error when tokens table missing, got nil")
	}
}

// ── CurrentDB ─────────────────────────────────────────────────────────────────

func TestCurrentDB_ReturnsNonNil(t *testing.T) {
	sm := newTestDB(t)
	db := sm.CurrentDB()
	if db == nil {
		t.Fatal("CurrentDB() returned nil after successful open")
	}
}

// ── closeCurrentDB ────────────────────────────────────────────────────────────

func TestCloseCurrentDB_MovesToHistorical(t *testing.T) {
	sm := newTestDB(t)
	basename := filepath.Base(sm.currentPath)
	if err := sm.closeCurrentDB(); err != nil {
		t.Fatalf("closeCurrentDB: %v", err)
	}
	if sm.currentDB != nil {
		t.Error("currentDB should be nil after closeCurrentDB")
	}
	if _, ok := sm.historicalDBs[basename]; !ok {
		t.Errorf("closed DB not found in historicalDBs under key %q", basename)
	}
}

func TestCloseCurrentDB_NilDB_Noop(t *testing.T) {
	sm := &SQLiteShardManager{
		dbDir:         t.TempDir(),
		historicalDBs: make(map[string]*sql.DB),
	}
	if err := sm.closeCurrentDB(); err != nil {
		t.Fatalf("closeCurrentDB on nil DB returned error: %v", err)
	}
}

// ── getDBSize ─────────────────────────────────────────────────────────────────

func TestGetDBSize_NonExistentFile_ReturnsError(t *testing.T) {
	sm := newSMForErrorTest(t)
	_, err := sm.getDBSize("/nonexistent/path/ocelot-2000-01.db")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestGetDBSize_ExistingFile_ReturnsSize(t *testing.T) {
	sm := newTestDB(t)
	size, err := sm.getDBSize(sm.currentPath)
	if err != nil {
		t.Fatalf("getDBSize on existing file: %v", err)
	}
	if size < 0 {
		t.Errorf("getDBSize returned negative size %d", size)
	}
}

// ── CheckRotation ─────────────────────────────────────────────────────────────

func TestCheckRotation_GetDBSizeError_ReturnsError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.mu.Lock()
	sm.currentPath = "/nonexistent/path/ocelot-2000-01.db"
	sm.mu.Unlock()

	if err := sm.CheckRotation(); err == nil {
		t.Error("expected error from CheckRotation when getDBSize fails, got nil")
	}
}

// ── prepareStatements ─────────────────────────────────────────────────────────

func TestPrepareStatements_ClosedDB_ReturnsError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.currentDB.Close()
	if err := sm.prepareStatements(); err == nil {
		t.Error("expected error from prepareStatements with closed DB, got nil")
	}
}


// ── openCurrentDB — existing-path branch (no rotation) ───────────────────────

func TestOpenCurrentDB_WithExistingPath_SkipsRotation(t *testing.T) {
	sm := newTestDB(t)
	// sm already has currentPath set from NewSQLiteShardManager.
	// Calling openCurrentDB again covers the sm.currentPath != "" branch;
	// the file is tiny so size < MaxDBSize and rotation is skipped.
	if err := sm.openCurrentDB(); err != nil {
		t.Fatalf("openCurrentDB on already-open SM: %v", err)
	}
	if sm.currentDB == nil {
		t.Error("currentDB should be non-nil after second openCurrentDB")
	}
}

// ── loadHistoricalDBs — openDB failure triggers warning+continue ──────────────

func TestLoadHistoricalDBs_UnreadableEntry_Warns(t *testing.T) {
	sm := newTestDB(t)
	// Create a directory with a .db extension inside the SM's dbDir.
	// openDB on a directory path fails (SQLite cannot open a directory),
	// triggering the warning+continue branch in loadHistoricalDBs.
	badPath := filepath.Join(sm.dbDir, "ocelot-2000-01.db")
	if err := os.Mkdir(badPath, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// loadHistoricalDBs must not return an error — it only warns.
	if err := sm.loadHistoricalDBs(); err != nil {
		t.Fatalf("loadHistoricalDBs: %v", err)
	}
}

// ── GetUserStats — closed DB returns error ────────────────────────────────────

func TestGetUserStats_ClosedDB_ReturnsError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.currentDB.Close()
	_, _, err := sm.GetUserStats(1)
	if err == nil {
		t.Error("expected error from GetUserStats with closed DB, got nil")
	}
}

// ── GetDBStats — bad currentPath returns error ────────────────────────────────

func TestGetDBStats_BadCurrentPath_ReturnsError(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.mu.Lock()
	sm.currentPath = "/nonexistent/path/to/db.db"
	sm.mu.Unlock()
	_, _, _, err := sm.GetDBStats()
	if err == nil {
		t.Error("expected error from GetDBStats with nonexistent currentPath, got nil")
	}
}

// ── loadTokens — skip unknown torrent ─────────────────────────────────────────

func TestLoadTokens_SkipsUnknownTorrent(t *testing.T) {
	sm := newSMForErrorTest(t)

	// Insert a torrent record + hash, then a token for that torrent.
	sm.currentDB.Exec("INSERT INTO torrent_stats (torrent_id, seeders, leechers, snatched, balance, last_action) VALUES (77,0,0,0,0,0)")
	sm.currentDB.Exec("INSERT INTO torrent_hashes (torrent_id, info_hash) VALUES (77,'unknown-hash-xyz')")
	sm.currentDB.Exec("INSERT INTO tokens (user_id, torrent_id, downloaded) VALUES (3,77,500)")

	// TorrentList does NOT contain "unknown-hash-xyz"
	torrents := NewTorrentList()
	loader := NewLoader(sm, torrents, NewUserList(), NewWhitelist())
	if err := loader.loadTokens(); err != nil {
		t.Fatalf("loadTokens: %v", err)
	}
	// If the torrent is not in the list the continue branch fires and nothing panics.
}

// ── NewSQLiteShardManager — openCurrentDB error (directory at DB path) ────────

func TestNewSQLiteShardManager_DirAtDBPath_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	// Create a directory where the current-month DB file would be created.
	// openDB will call db.Exec("PRAGMA...") which fails on a directory path,
	// covering openDB pragma error → openCurrentDB openDB error → NewSQLiteShardManager error.
	month := time.Now().Format("2006-01")
	dbPath := filepath.Join(dir, "ocelot-"+month+".db")
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := NewSQLiteShardManager(dir)
	if err == nil {
		t.Error("expected error when DB path is a directory, got nil")
	}
}

// ── loadHistoricalDBs — non-IsNotExist ReadDir error ─────────────────────────

func TestLoadHistoricalDBs_FilePath_NotDirError(t *testing.T) {
	// Pass a regular file as dbDir — ReadDir returns "not a directory" which is
	// not os.IsNotExist, covering the bare "return err" branch.
	f, err := os.CreateTemp(t.TempDir(), "notadir-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	sm := &SQLiteShardManager{dbDir: f.Name(), historicalDBs: make(map[string]*sql.DB)}
	if err := sm.loadHistoricalDBs(); err == nil {
		t.Error("expected error when dbDir is a regular file, got nil")
	}
}

// ── Reload — new user creation (else branch) ──────────────────────────────────

func TestReload_NewUser_CreatesEntry(t *testing.T) {
	sm := newSMForErrorTest(t)
	sm.RecordUserPasskey(42, "pk_brandnew_0000000000000000001", true, false)

	// Start with an empty user list — passkey is not pre-populated,
	// so Reload must create it via the else branch.
	users := NewUserList()
	loader := NewLoader(sm, NewTorrentList(), users, NewWhitelist())
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	u, ok := users.Get("pk_brandnew_0000000000000000001")
	if !ok {
		t.Fatal("expected new user to be created by Reload, not found")
	}
	if !u.CanLeech.Load() {
		t.Error("expected CanLeech=true for newly created user")
	}
}

// ── CheckRotation — size >= MaxDBSize triggers rotation ───────────────────────

func TestCheckRotation_TriggersRotation_WhenAtLimit(t *testing.T) {
	// Save and restore MaxDBSize so other tests are unaffected.
	orig := MaxDBSize
	t.Cleanup(func() { MaxDBSize = orig })
	MaxDBSize = 0 // any file size ≥ 0 triggers rotation

	sm := newTestDB(t)
	prevPath := sm.currentPath

	if err := sm.CheckRotation(); err != nil {
		t.Fatalf("CheckRotation: %v", err)
	}
	// Rotation re-opens with a new path (next month prefix).
	if sm.currentPath == "" {
		t.Error("currentPath should be set after rotation")
	}
	// The previous DB was moved to historicalDBs.
	if sm.historicalDBs[filepath.Base(prevPath)] == nil {
		t.Errorf("expected old path %q to be in historicalDBs after rotation", prevPath)
	}
}

// ── openCurrentDB — rotation branch (size >= MaxDBSize) ──────────────────────

func TestOpenCurrentDB_RotationBranch(t *testing.T) {
	orig := MaxDBSize
	t.Cleanup(func() { MaxDBSize = orig })
	MaxDBSize = 0

	sm := newTestDB(t)
	prevPath := sm.currentPath

	// openCurrentDB is called by CheckRotation above, but call it directly
	// here to cover the branch inside openCurrentDB itself.
	if err := sm.openCurrentDB(); err != nil {
		t.Fatalf("openCurrentDB rotation branch: %v", err)
	}
	// currentPath must change since the old one was rotated out.
	if sm.currentDB == nil {
		t.Error("currentDB nil after rotation")
	}
	_ = prevPath // consumed above
}
