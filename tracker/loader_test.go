package tracker

import (
	"net"
	"sync"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newLoader(t *testing.T) (*SQLiteShardManager, *TorrentList, *UserList, *Whitelist, *Loader) {
	t.Helper()
	db := newTestDB(t)
	torrents := NewTorrentList()
	users := NewUserList()
	wl := NewWhitelist()
	return db, torrents, users, wl, NewLoader(db, torrents, users, wl)
}

// ── CreateSchemaIfNeeded ──────────────────────────────────────────────────────

func TestCreateSchemaIfNeeded_ReturnsNil(t *testing.T) {
	_, _, _, _, loader := newLoader(t)
	if err := loader.CreateSchemaIfNeeded(); err != nil {
		t.Fatalf("CreateSchemaIfNeeded: %v", err)
	}
}

// ── LoadAll — empty DB ────────────────────────────────────────────────────────

func TestLoadAll_EmptyDB_MapsEmpty(t *testing.T) {
	_, torrents, users, wl, loader := newLoader(t)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got := torrents.Size(); got != 0 {
		t.Errorf("torrents.Size() = %d, want 0", got)
	}
	if got := users.Size(); got != 0 {
		t.Errorf("users.Size() = %d, want 0", got)
	}
	if got := len(wl.GetAll()); got != 0 {
		t.Errorf("whitelist len = %d, want 0", got)
	}
}

// ── LoadAll — torrents ────────────────────────────────────────────────────────

func TestLoadAll_PopulatesTorrents(t *testing.T) {
	db, torrents, users, wl, loader := newLoader(t)
	db.RecordTorrent(1, 0, 0, 0, 5000)
	db.RecordTorrentHash(1, testInfoHash)

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	tor, ok := torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent not found after LoadAll")
	}
	if tor.ID != TorrentID(1) {
		t.Errorf("torrent.ID = %d, want 1", tor.ID)
	}
	if tor.Balance != 5000 {
		t.Errorf("torrent.Balance = %d, want 5000", tor.Balance)
	}
	_ = users
	_ = wl
}

func TestLoadAll_PopulatesCompleted(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	// snatched column maps to Completed
	db.RecordTorrent(2, 0, 0, 7, 0)
	db.RecordTorrentHash(2, testInfoHash)

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	tor, _ := torrents.Get(testInfoHash)
	if tor == nil {
		t.Fatal("torrent not found")
	}
	if tor.Completed != 7 {
		t.Errorf("Completed = %d, want 7", tor.Completed)
	}
}

func TestLoadAll_PopulatesFreeType(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	// Insert directly to set free_type (RecordTorrent uses default 0)
	db.currentDB.Exec(`INSERT INTO torrents (id, seeders, leechers, snatched, balance, free_type, last_action)
		VALUES (3, 0, 0, 0, 0, 1, 0)`)
	db.RecordTorrentHash(3, testInfoHash)

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	tor, ok := torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent not found")
	}
	if tor.FreeType != FreeFree {
		t.Errorf("FreeType = %v, want FreeFree", tor.FreeType)
	}
}

// ── LoadAll — users ───────────────────────────────────────────────────────────

func TestLoadAll_PopulatesUsers(t *testing.T) {
	db, _, users, _, loader := newLoader(t)
	db.RecordUserPasskey(1, testPasskey, true, false)

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	user, ok := users.Get(testPasskey)
	if !ok {
		t.Fatal("user not found after LoadAll")
	}
	if user.ID != UserID(1) {
		t.Errorf("user.ID = %d, want 1", user.ID)
	}
	if !user.CanLeech.Load() {
		t.Error("CanLeech should be true")
	}
	if user.ProtectIP.Load() {
		t.Error("ProtectIP should be false")
	}
}

func TestLoadAll_PopulatesUserProtectIP(t *testing.T) {
	db, _, users, _, loader := newLoader(t)
	db.RecordUserPasskey(2, testPasskey2, false, true)

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	user, ok := users.Get(testPasskey2)
	if !ok {
		t.Fatal("user not found")
	}
	if user.CanLeech.Load() {
		t.Error("CanLeech should be false")
	}
	if !user.ProtectIP.Load() {
		t.Error("ProtectIP should be true")
	}
}

// ── LoadAll — whitelist ───────────────────────────────────────────────────────

func TestLoadAll_PopulatesWhitelist(t *testing.T) {
	db, _, _, wl, loader := newLoader(t)
	db.AddWhitelistEntry("-qB4")
	db.AddWhitelistEntry("-DE1")

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	prefixes := wl.GetAll()
	if len(prefixes) != 2 {
		t.Fatalf("whitelist len = %d, want 2: %v", len(prefixes), prefixes)
	}
}

func TestLoadAll_EmptyWhitelist_AllowsAll(t *testing.T) {
	_, _, _, wl, loader := newLoader(t)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if !wl.IsAllowed([]byte(testPeerID)) {
		t.Error("empty whitelist should allow all peers")
	}
}

// ── LoadAll — tokens ──────────────────────────────────────────────────────────

func TestLoadAll_PopulatesTokens(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	db.RecordTorrent(1, 0, 0, 0, 0)
	db.RecordTorrentHash(1, testInfoHash)
	db.RecordToken(UserID(1), TorrentID(1), 1024)

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	tor, ok := torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent not found")
	}
	tor.mu.RLock()
	_, hasToken := tor.TokenedUsers[UserID(1)]
	tor.mu.RUnlock()
	if !hasToken {
		t.Error("UserID(1) should be in torrent.TokenedUsers after LoadAll")
	}
}

func TestLoadAll_SkipsTokensForUnknownTorrent(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	// torrent_hashes exists but no torrents row → LoadTorrents won't add it to TorrentList
	// LoadTokens (db level) DOES return this entry, so loadTokens() must skip it
	db.RecordTorrentHash(99, "orphan_hash_99")
	db.RecordToken(UserID(1), TorrentID(99), 500)

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := torrents.Get("orphan_hash_99"); ok {
		t.Error("orphan torrent should not appear in TorrentList")
	}
}

func TestLoadAll_MultipleTokensOnOneTorrent(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	db.RecordTorrent(1, 0, 0, 0, 0)
	db.RecordTorrentHash(1, testInfoHash)
	db.RecordToken(UserID(1), TorrentID(1), 100)
	db.RecordToken(UserID(2), TorrentID(1), 200)

	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	tor, _ := torrents.Get(testInfoHash)
	tor.mu.RLock()
	_, has1 := tor.TokenedUsers[UserID(1)]
	_, has2 := tor.TokenedUsers[UserID(2)]
	tor.mu.RUnlock()
	if !has1 || !has2 {
		t.Errorf("TokenedUsers: user1=%v user2=%v, want both true", has1, has2)
	}
}

// ── LoadAll — error propagation ───────────────────────────────────────────────

func TestLoadAll_TorrentsError_ReturnsError(t *testing.T) {
	_, _, _, _, loader := newLoader(t)
	// Close the DB to force an error on the first read
	loader.db.currentDB.Close()

	err := loader.LoadAll()
	if err == nil {
		t.Fatal("expected error from LoadAll when DB closed, got nil")
	}
}

// ── Reload — existing torrent metadata updated in-place ───────────────────────

func TestReload_UpdatesExistingTorrentBalance(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	db.RecordTorrent(1, 0, 0, 0, 100)
	db.RecordTorrentHash(1, testInfoHash)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Update balance directly
	db.currentDB.Exec(`UPDATE torrents SET balance = 9999 WHERE id = 1`)

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	tor, _ := torrents.Get(testInfoHash)
	if tor.Balance != 9999 {
		t.Errorf("Balance = %d after Reload, want 9999", tor.Balance)
	}
}

func TestReload_UpdatesExistingTorrentFreeType(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	db.RecordTorrent(1, 0, 0, 0, 0)
	db.RecordTorrentHash(1, testInfoHash)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	db.currentDB.Exec(`UPDATE torrents SET free_type = 2 WHERE id = 1`)

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	tor, _ := torrents.Get(testInfoHash)
	if tor.FreeType != FreeNeutral {
		t.Errorf("FreeType = %v after Reload, want FreeNeutral", tor.FreeType)
	}
}

func TestReload_PreservesPeerLists(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	db.RecordTorrent(1, 0, 0, 0, 0)
	db.RecordTorrentHash(1, testInfoHash)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Add a live peer to Seeders
	tor, _ := torrents.Get(testInfoHash)
	peerKey := "testpeer"
	tor.Seeders.Set(peerKey, &Peer{
		UserID: UserID(1),
		IP:     net.ParseIP(testIP),
		Port:   testPort,
	})

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Peer must still be present after Reload
	if tor.Seeders.Size() != 1 {
		t.Errorf("Seeders.Size() = %d after Reload, want 1 (peer list should be preserved)", tor.Seeders.Size())
	}
	if _, ok := tor.Seeders.Get(peerKey); !ok {
		t.Error("live peer was removed from Seeders during Reload")
	}
}

// ── Reload — user atomics updated in-place ────────────────────────────────────

func TestReload_UpdatesExistingUserCanLeech(t *testing.T) {
	db, _, users, _, loader := newLoader(t)
	db.RecordUserPasskey(1, testPasskey, true, false)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Flip CanLeech to false in DB
	db.RecordUserPasskey(1, testPasskey, false, false)

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	user, _ := users.Get(testPasskey)
	if user.CanLeech.Load() {
		t.Error("CanLeech should be false after Reload")
	}
}

func TestReload_UpdatesExistingUserProtectIP(t *testing.T) {
	db, _, users, _, loader := newLoader(t)
	db.RecordUserPasskey(1, testPasskey, true, false)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	db.RecordUserPasskey(1, testPasskey, true, true)

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	user, _ := users.Get(testPasskey)
	if !user.ProtectIP.Load() {
		t.Error("ProtectIP should be true after Reload")
	}
}

// ── Reload — new entries inserted ────────────────────────────────────────────

func TestReload_InsertsNewTorrent(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Add torrent after initial load
	const newHash = "\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05\x05"
	db.RecordTorrent(5, 0, 0, 0, 42)
	db.RecordTorrentHash(5, newHash)

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	tor, ok := torrents.Get(newHash)
	if !ok {
		t.Fatal("new torrent not found after Reload")
	}
	if tor.Balance != 42 {
		t.Errorf("Balance = %d, want 42", tor.Balance)
	}
}

func TestReload_InsertsNewUser(t *testing.T) {
	db, _, users, _, loader := newLoader(t)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Add user after initial load
	const newPasskey = "newpasskey1234567890123456789012"
	db.RecordUserPasskey(77, newPasskey, true, false)

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	user, ok := users.Get(newPasskey)
	if !ok {
		t.Fatal("new user not found after Reload")
	}
	if user.ID != UserID(77) {
		t.Errorf("user.ID = %d, want 77", user.ID)
	}
}

// ── Reload — whitelist fully replaced ─────────────────────────────────────────

func TestReload_ReplacesWhitelist(t *testing.T) {
	db, _, _, wl, loader := newLoader(t)
	db.AddWhitelistEntry("-qB4")
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(wl.GetAll()) != 1 {
		t.Fatalf("whitelist len = %d before Reload, want 1", len(wl.GetAll()))
	}

	// Replace whitelist in DB
	db.RemoveWhitelistEntry("-qB4")
	db.AddWhitelistEntry("-DE1")
	db.AddWhitelistEntry("-UT3")

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	prefixes := wl.GetAll()
	if len(prefixes) != 2 {
		t.Errorf("whitelist len = %d after Reload, want 2: %v", len(prefixes), prefixes)
	}
}

// ── Reload — tokens merge-added ───────────────────────────────────────────────

func TestReload_MergeAddsTokens(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	db.RecordTorrent(1, 0, 0, 0, 0)
	db.RecordTorrentHash(1, testInfoHash)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Add token after initial load
	db.RecordToken(UserID(2), TorrentID(1), 300)

	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	tor, _ := torrents.Get(testInfoHash)
	tor.mu.RLock()
	_, has := tor.TokenedUsers[UserID(2)]
	tor.mu.RUnlock()
	if !has {
		t.Error("UserID(2) token not found after Reload")
	}
}

func TestReload_MergeKeepsExistingTokens(t *testing.T) {
	db, torrents, _, _, loader := newLoader(t)
	db.RecordTorrent(1, 0, 0, 0, 0)
	db.RecordTorrentHash(1, testInfoHash)
	db.RecordToken(UserID(1), TorrentID(1), 100)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Add another token and reload — user 1 must still be present
	db.RecordToken(UserID(3), TorrentID(1), 200)
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	tor, _ := torrents.Get(testInfoHash)
	tor.mu.RLock()
	_, has1 := tor.TokenedUsers[UserID(1)]
	_, has3 := tor.TokenedUsers[UserID(3)]
	tor.mu.RUnlock()
	if !has1 {
		t.Error("existing UserID(1) token removed during Reload merge")
	}
	if !has3 {
		t.Error("new UserID(3) token not added during Reload merge")
	}
}

// ── Reload — concurrent safety ────────────────────────────────────────────────

func TestReload_Concurrent_NoRace(t *testing.T) {
	db, _, _, _, loader := newLoader(t)
	db.RecordTorrent(1, 0, 0, 0, 0)
	db.RecordTorrentHash(1, testInfoHash)
	db.RecordUserPasskey(1, testPasskey, true, false)
	db.AddWhitelistEntry("-qB4")
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			loader.Reload() //nolint:errcheck
		}()
	}
	wg.Wait()
}

// ── Reload — error propagation ────────────────────────────────────────────────

func TestReload_TorrentsError_ReturnsError(t *testing.T) {
	_, _, _, _, loader := newLoader(t)
	loader.db.currentDB.Close()

	if err := loader.Reload(); err == nil {
		t.Fatal("expected error from Reload when DB closed, got nil")
	}
}
