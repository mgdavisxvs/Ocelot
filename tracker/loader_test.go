package tracker

import (
	"encoding/hex"

	"testing"
)

// newShardManager opens a shard manager on a temporary directory.
func newShardManager(t *testing.T) (*SQLiteShardManager, string) {
	t.Helper()

	dir := t.TempDir()
	db, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("failed to open shard manager: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	return db, dir
}

func newLoaderFixture(t *testing.T, db *SQLiteShardManager) (*Loader, *TorrentList, *UserList, *Whitelist) {
	t.Helper()

	torrents := NewTorrentList()
	users := NewUserList()
	whitelist := NewWhitelist()

	loader := NewLoader(db, torrents, users, whitelist)
	if err := loader.CreateSchemaIfNeeded(); err != nil {
		t.Fatalf("CreateSchemaIfNeeded failed: %v", err)
	}

	return loader, torrents, users, whitelist
}

func TestCreateSchemaIsIdempotent(t *testing.T) {
	db, _ := newShardManager(t)
	loader, _, _, _ := newLoaderFixture(t, db)

	// Running twice must not fail; startup calls it on every boot.
	if err := loader.CreateSchemaIfNeeded(); err != nil {
		t.Errorf("second CreateSchemaIfNeeded failed: %v", err)
	}
}

func TestLoadAllOnEmptyDatabase(t *testing.T) {
	db, _ := newShardManager(t)
	loader, torrents, users, _ := newLoaderFixture(t, db)

	// A fresh install is legitimately empty and must not error.
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll on an empty database failed: %v", err)
	}

	if torrents.Size() != 0 {
		t.Errorf("torrents = %d, want 0", torrents.Size())
	}
	if users.Size() != 0 {
		t.Errorf("users = %d, want 0", users.Size())
	}
}

func TestLoadTorrentsKeysByInfoHash(t *testing.T) {
	db, _ := newShardManager(t)
	loader, torrents, _, _ := newLoaderFixture(t, db)

	infoHash := hex.EncodeToString([]byte(testInfoHash(0xE1)))
	if err := db.RecordTorrentInfoHash(TorrentID(5), infoHash); err != nil {
		t.Fatalf("RecordTorrentInfoHash failed: %v", err)
	}

	if err := loader.LoadTorrents(); err != nil {
		t.Fatalf("LoadTorrents failed: %v", err)
	}

	torrent, ok := torrents.Get(infoHash)
	if !ok {
		t.Fatalf("torrent is not reachable by its info_hash; keys present: %d", torrents.Size())
	}
	if torrent.ID != 5 {
		t.Errorf("torrent ID = %d, want 5", torrent.ID)
	}
}

func TestLoadTorrentsSkipsRowsWithoutInfoHash(t *testing.T) {
	db, _ := newShardManager(t)
	loader, torrents, _, _ := newLoaderFixture(t, db)

	// A legacy row written before info_hash was persisted. It can never be
	// matched by an announce, so loading it would only be misleading.
	if err := db.RecordTorrent(TorrentID(9), 1, 2, 0, 0); err != nil {
		t.Fatalf("RecordTorrent failed: %v", err)
	}

	if err := loader.LoadTorrents(); err != nil {
		t.Fatalf("LoadTorrents failed: %v", err)
	}

	if torrents.Size() != 0 {
		t.Errorf("loaded %d unusable torrents, want 0", torrents.Size())
	}
}

func TestLoadTorrentsRestoresSwarmState(t *testing.T) {
	db, _ := newShardManager(t)
	loader, torrents, _, _ := newLoaderFixture(t, db)

	infoHash := hex.EncodeToString([]byte(testInfoHash(0xE2)))
	if err := db.RecordTorrentInfoHash(TorrentID(6), infoHash); err != nil {
		t.Fatalf("RecordTorrentInfoHash failed: %v", err)
	}
	if err := db.RecordTorrent(TorrentID(6), 3, 4, 11, 512); err != nil {
		t.Fatalf("RecordTorrent failed: %v", err)
	}

	if err := loader.LoadTorrents(); err != nil {
		t.Fatalf("LoadTorrents failed: %v", err)
	}

	torrent, ok := torrents.Get(infoHash)
	if !ok {
		t.Fatal("torrent was not loaded")
	}
	if torrent.Completed != 11 {
		t.Errorf("Completed = %d, want 11", torrent.Completed)
	}
	if torrent.Balance != 512 {
		t.Errorf("Balance = %d, want 512", torrent.Balance)
	}
}

func TestLoadWhitelist(t *testing.T) {
	db, _ := newShardManager(t)
	loader, _, _, whitelist := newLoaderFixture(t, db)

	if _, err := db.DB().Exec(`INSERT INTO whitelist (prefix) VALUES (?)`, "-qB43"); err != nil {
		t.Fatalf("failed to seed whitelist: %v", err)
	}

	if err := loader.LoadWhitelist(); err != nil {
		t.Fatalf("LoadWhitelist failed: %v", err)
	}

	if !whitelist.IsAllowed(testPeerID("peer0001")) {
		t.Error("a whitelisted prefix was not loaded")
	}
	if whitelist.IsAllowed([]byte("-XX0000-000000000000")) {
		t.Error("an unlisted client was allowed after loading a whitelist")
	}
}

func TestLoadUsers(t *testing.T) {
	db, _ := newShardManager(t)
	loader, _, users, _ := newLoaderFixture(t, db)

	passkey := "0123456789abcdef0123456789abcdef"
	if _, err := db.DB().Exec(
		`INSERT INTO users (id, passkey, can_leech, protect_ip, deleted) VALUES (?, ?, 1, 0, 0)`,
		21, passkey,
	); err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	if err := loader.LoadUsers(); err != nil {
		t.Fatalf("LoadUsers failed: %v", err)
	}

	user, ok := users.Get(passkey)
	if !ok {
		t.Fatal("user was not loaded by passkey")
	}
	if user.ID != 21 {
		t.Errorf("user ID = %d, want 21", user.ID)
	}
	if !user.CanLeech.Load() {
		t.Error("can_leech was not restored")
	}
}

// The defect this persistence exists to fix: a torrent provisioned over
// /update must still be announceable after the tracker restarts. Before
// info_hash was stored, every torrent came back under a synthetic key that no
// client could ever match.
func TestTorrentSurvivesRestart(t *testing.T) {
	db, dir := newShardManager(t)

	infoHash := hex.EncodeToString([]byte(testInfoHash(0xE3)))
	passkey := "0123456789abcdef0123456789abcdef"

	// Provision through the same path the site uses.
	worker := &Worker{
		Config:    DefaultConfig(),
		DB:        db,
		SiteComm:  &mockSiteComm{},
		Torrents:  NewTorrentList(),
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     &Stats{},
	}

	if _, err := worker.addTorrent(UpdateRequest{
		Action:    "add_torrent",
		TorrentID: 31,
		InfoHash:  infoHash,
	}); err != nil {
		t.Fatalf("add_torrent failed: %v", err)
	}

	if _, err := worker.addUser(UpdateRequest{
		Action:  "add_user",
		UserID:  31,
		Passkey: passkey,
	}); err != nil {
		t.Fatalf("add_user failed: %v", err)
	}

	db.Close()

	// Restart against the same directory.
	restarted, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("failed to reopen the database: %v", err)
	}
	defer restarted.Close()

	torrents := NewTorrentList()
	users := NewUserList()
	loader := NewLoader(restarted, torrents, users, NewWhitelist())

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll after restart failed: %v", err)
	}

	if _, ok := torrents.Get(infoHash); !ok {
		t.Fatalf("the torrent is unreachable by info_hash after a restart; %d torrents loaded", torrents.Size())
	}

	user, ok := users.Get(passkey)
	if !ok {
		t.Fatal("the user is unreachable by passkey after a restart")
	}

	// And the restored state must actually serve an announce.
	restartedWorker := &Worker{
		Config:    DefaultConfig(),
		DB:        restarted,
		SiteComm:  &mockSiteComm{},
		Torrents:  torrents,
		Users:     users,
		Whitelist: NewWhitelist(),
		Stats:     &Stats{},
	}

	req := announceParams(infoHash, testPeerID("peer0031"), 6881, 1<<30, "started")
	resp, err := restartedWorker.Announce(req, user, req.IP, "qB")
	if err != nil {
		t.Fatalf("announce against restored state failed: %v", err)
	}
	if resp.Incomplete != 1 {
		t.Errorf("incomplete = %d, want 1", resp.Incomplete)
	}
}

func TestRecordTorrentInfoHashIsUpsert(t *testing.T) {
	db, _ := newShardManager(t)

	first := hex.EncodeToString([]byte(testInfoHash(0xE4)))
	second := hex.EncodeToString([]byte(testInfoHash(0xE5)))

	if err := db.RecordTorrentInfoHash(TorrentID(41), first); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if err := db.RecordTorrentInfoHash(TorrentID(41), second); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	var stored string
	if err := db.DB().QueryRow(`SELECT info_hash FROM torrents WHERE id = ?`, 41).Scan(&stored); err != nil {
		t.Fatalf("failed to read back: %v", err)
	}
	if stored != second {
		t.Errorf("info_hash = %q, want the most recent value", stored)
	}
}

func TestMigrationAddsInfoHashToLegacySchema(t *testing.T) {
	dir := t.TempDir()

	db, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("failed to open: %v", err)
	}

	// Simulate a database created before the column existed.
	if _, err := db.DB().Exec(`DROP TABLE torrents`); err != nil {
		t.Fatalf("failed to drop: %v", err)
	}
	if _, err := db.DB().Exec(`CREATE TABLE torrents (
		id INTEGER PRIMARY KEY,
		seeders INTEGER DEFAULT 0,
		leechers INTEGER DEFAULT 0,
		snatched INTEGER DEFAULT 0,
		balance INTEGER DEFAULT 0,
		free_type INTEGER DEFAULT 0,
		last_action INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("failed to create legacy table: %v", err)
	}
	if _, err := db.DB().Exec(`INSERT INTO torrents (id, seeders) VALUES (1, 3)`); err != nil {
		t.Fatalf("failed to seed legacy row: %v", err)
	}
	db.Close()

	// Reopening must migrate rather than fail.
	migrated, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("reopening a legacy database failed: %v", err)
	}
	defer migrated.Close()

	if err := migrated.RecordTorrentInfoHash(TorrentID(1), "abc"); err != nil {
		t.Fatalf("writing info_hash after migration failed: %v", err)
	}

	var stored string
	if err := migrated.DB().QueryRow(`SELECT info_hash FROM torrents WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("failed to read migrated column: %v", err)
	}
	if stored != "abc" {
		t.Errorf("info_hash = %q, want %q", stored, "abc")
	}

	// The legacy row's data must be preserved.
	var seeders int
	if err := migrated.DB().QueryRow(`SELECT seeders FROM torrents WHERE id = 1`).Scan(&seeders); err != nil {
		t.Fatalf("failed to read legacy column: %v", err)
	}
	if seeders != 3 {
		t.Errorf("seeders = %d, want 3 — migration lost existing data", seeders)
	}
}

func TestLoadPeersIsANoOp(t *testing.T) {
	db, _ := newShardManager(t)
	loader, _, _, _ := newLoaderFixture(t, db)

	// Peers deliberately do not survive a restart; they re-announce.
	if err := loader.LoadPeers(); err != nil {
		t.Errorf("LoadPeers returned an error: %v", err)
	}
}

func TestLoaderRejectsClosedDatabase(t *testing.T) {
	dir := t.TempDir()
	db, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("failed to open: %v", err)
	}

	loader := NewLoader(db, NewTorrentList(), NewUserList(), NewWhitelist())
	db.Close()

	// Startup treats a load failure as fatal, so it has to surface one.
	if err := loader.LoadUsers(); err == nil {
		t.Error("expected an error when loading from a closed database")
	}
}
