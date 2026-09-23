package tracker

import (
	"net"
	"sync"
	"testing"
)

// ── PeerKeyPrime ─────────────────────────────────────────────────────────────

func TestPeerKeyPrime_Length(t *testing.T) {
	peerID := make([]byte, 20)
	for i := range peerID {
		peerID[i] = byte(i)
	}
	key := PeerKeyPrime(peerID, 1, 1)
	// 1 random byte + 4 user ID bytes + 20 peer ID bytes
	if len(key) != 25 {
		t.Fatalf("PeerKeyPrime length = %d, want 25", len(key))
	}
}

func TestPeerKeyPrime_Uniqueness(t *testing.T) {
	peerID := make([]byte, 20)
	k1 := PeerKeyPrime(peerID, 1, 1)
	k2 := PeerKeyPrime(peerID, 2, 1) // different user
	k3 := PeerKeyPrime(peerID, 1, 2) // different torrent (changes random byte index)
	if k1 == k2 {
		t.Error("PeerKeyPrime: different userIDs produced same key")
	}
	_ = k3 // may or may not differ; just ensure no panic
}

func TestPeerKeyPrime_Deterministic(t *testing.T) {
	peerID := []byte("12345678901234567890")
	k1 := PeerKeyPrime(peerID, 42, 7)
	k2 := PeerKeyPrime(peerID, 42, 7)
	if k1 != k2 {
		t.Error("PeerKeyPrime is not deterministic for same inputs")
	}
}

// ── CompactIPPort ─────────────────────────────────────────────────────────────

func TestCompactIPPort_IPv4(t *testing.T) {
	ip := net.ParseIP("1.2.3.4")
	port := uint16(6881)
	compact := CompactIPPort(ip, port)
	if len(compact) != 6 {
		t.Fatalf("CompactIPPort length = %d, want 6", len(compact))
	}
	if compact[0] != 1 || compact[1] != 2 || compact[2] != 3 || compact[3] != 4 {
		t.Errorf("CompactIPPort IP bytes wrong: %v", compact[:4])
	}
	gotPort := uint16(compact[4])<<8 | uint16(compact[5])
	if gotPort != port {
		t.Errorf("CompactIPPort port = %d, want %d", gotPort, port)
	}
}

func TestCompactIPPort_IPv6_ReturnsNil(t *testing.T) {
	ip := net.ParseIP("2001:db8::1")
	compact := CompactIPPort(ip, 6881)
	if compact != nil {
		t.Error("CompactIPPort should return nil for IPv6")
	}
}

func TestCompactIPPort_Loopback(t *testing.T) {
	ip := net.ParseIP("127.0.0.1")
	compact := CompactIPPort(ip, 9999)
	if len(compact) != 6 {
		t.Fatalf("CompactIPPort length = %d, want 6", len(compact))
	}
	if compact[0] != 127 || compact[3] != 1 {
		t.Errorf("Loopback IP encoding wrong: %v", compact)
	}
}

// ── PeerList ──────────────────────────────────────────────────────────────────

func TestPeerList_SetGetDelete(t *testing.T) {
	pl := NewPeerList()
	peer := &Peer{UserID: 1}
	pl.Set("k1", peer)

	got, ok := pl.Get("k1")
	if !ok || got != peer {
		t.Fatal("Get after Set failed")
	}

	pl.Delete("k1")
	_, ok = pl.Get("k1")
	if ok {
		t.Fatal("peer still present after Delete")
	}
}

func TestPeerList_Size(t *testing.T) {
	pl := NewPeerList()
	if pl.Size() != 0 {
		t.Fatal("empty PeerList size != 0")
	}
	pl.Set("a", &Peer{})
	pl.Set("b", &Peer{})
	if pl.Size() != 2 {
		t.Fatalf("size = %d, want 2", pl.Size())
	}
	pl.Delete("a")
	if pl.Size() != 1 {
		t.Fatalf("size = %d, want 1 after delete", pl.Size())
	}
}

func TestPeerList_ForEach_Stop(t *testing.T) {
	pl := NewPeerList()
	for i := 0; i < 5; i++ {
		pl.Set(string(rune('a'+i)), &Peer{UserID: UserID(i)})
	}
	count := 0
	pl.ForEach(func(_ string, _ *Peer) bool {
		count++
		return count < 3 // stop after 3
	})
	if count != 3 {
		t.Fatalf("ForEach stop: visited %d, want 3", count)
	}
}

func TestPeerList_Concurrent(t *testing.T) {
	pl := NewPeerList()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := string(rune(n))
			pl.Set(key, &Peer{UserID: UserID(n)})
			pl.Get(key)
			pl.Size()
		}(i)
	}
	wg.Wait()
}

// ── TorrentList ───────────────────────────────────────────────────────────────

func TestTorrentList_CRUD(t *testing.T) {
	tl := NewTorrentList()
	tor := NewTorrent(1)
	tl.Set("hash1", tor)

	got, ok := tl.Get("hash1")
	if !ok || got != tor {
		t.Fatal("TorrentList Get failed")
	}
	if tl.Size() != 1 {
		t.Fatalf("size = %d, want 1", tl.Size())
	}

	tl.Delete("hash1")
	if _, ok := tl.Get("hash1"); ok {
		t.Fatal("torrent still present after Delete")
	}
}

func TestTorrentList_Reset(t *testing.T) {
	tl := NewTorrentList()
	tl.Set("a", NewTorrent(1))
	tl.Set("b", NewTorrent(2))
	tl.Reset()
	if tl.Size() != 0 {
		t.Fatalf("size = %d after Reset, want 0", tl.Size())
	}
}

func TestTorrentList_ForEach(t *testing.T) {
	tl := NewTorrentList()
	tl.Set("x", NewTorrent(1))
	tl.Set("y", NewTorrent(2))
	seen := 0
	tl.ForEach(func(_ string, _ *Torrent) bool {
		seen++
		return true
	})
	if seen != 2 {
		t.Fatalf("ForEach visited %d, want 2", seen)
	}
}

// ── UserList ──────────────────────────────────────────────────────────────────

func TestUserList_CRUD(t *testing.T) {
	ul := NewUserList()
	u := NewUser(1, true, false)
	ul.Set("passkey1", u)

	got, ok := ul.Get("passkey1")
	if !ok || got != u {
		t.Fatal("UserList Get failed")
	}
	ul.Delete("passkey1")
	if _, ok := ul.Get("passkey1"); ok {
		t.Fatal("user still present after Delete")
	}
}

func TestUserList_Reset(t *testing.T) {
	ul := NewUserList()
	ul.Set("a", NewUser(1, true, false))
	ul.Reset()
	if ul.Size() != 0 {
		t.Fatalf("size = %d after Reset, want 0", ul.Size())
	}
}

func TestUserList_ForEach(t *testing.T) {
	ul := NewUserList()
	ul.Set("pk1", NewUser(1, true, false))
	ul.Set("pk2", NewUser(2, false, true))
	ul.Set("pk3", NewUser(3, true, true))

	seen := map[string]bool{}
	ul.ForEach(func(passkey string, _ *User) bool {
		seen[passkey] = true
		return true
	})
	if len(seen) != 3 {
		t.Errorf("ForEach visited %d users, want 3", len(seen))
	}
}

func TestUserList_ForEach_EarlyStop(t *testing.T) {
	ul := NewUserList()
	for i := 0; i < 5; i++ {
		ul.Set(string(rune('a'+i)), NewUser(UserID(i+1), true, false))
	}
	count := 0
	ul.ForEach(func(_ string, _ *User) bool {
		count++
		return count < 2
	})
	if count != 2 {
		t.Errorf("ForEach stopped after %d iterations, want 2", count)
	}
}

// ── Whitelist ─────────────────────────────────────────────────────────────────

func TestWhitelist_AllowAll_WhenEmpty(t *testing.T) {
	wl := NewWhitelist()
	peerID := []byte("-qB40000000000000000")
	if !wl.IsAllowed(peerID) {
		t.Error("empty whitelist should allow all peer IDs")
	}
}

func TestWhitelist_AddRemove(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-qB4")
	wl.Add("-DE1")

	allowed := []byte("-qB40000000000000000")
	if !wl.IsAllowed(allowed) {
		t.Error("whitelisted prefix should be allowed")
	}

	denied := []byte("-TR300000000000000000")
	if wl.IsAllowed(denied) {
		t.Error("non-whitelisted prefix should be denied")
	}

	wl.Remove("-qB4")
	if wl.IsAllowed(allowed) {
		t.Error("removed prefix should no longer be allowed")
	}
}

func TestWhitelist_Idempotent_Add(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-qB4")
	wl.Add("-qB4")
	wl.Add("-qB4")
	if len(wl.GetAll()) != 1 {
		t.Fatalf("duplicate Add produced %d entries, want 1", len(wl.GetAll()))
	}
}

func TestWhitelist_Reset(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-qB4")
	wl.Add("-DE1")
	wl.Reset([]string{"-TR3"})
	all := wl.GetAll()
	if len(all) != 1 || all[0] != "-TR3" {
		t.Fatalf("after Reset, whitelist = %v, want [\"-TR3\"]", all)
	}
}

func TestWhitelist_Reset_Nil(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-qB4")
	wl.Reset(nil)
	// nil reset → allow-all (empty)
	peerID := []byte("anything00000000000")
	if !wl.IsAllowed(peerID) {
		t.Error("nil Reset should produce allow-all whitelist")
	}
}

func TestWhitelist_Concurrent(t *testing.T) {
	wl := NewWhitelist()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			wl.Add("-qB4")
			wl.IsAllowed([]byte("-qB40000000000000000"))
		}()
		go func() {
			defer wg.Done()
			wl.GetAll()
			wl.Remove("-qB4")
		}()
	}
	wg.Wait()
}

// ── NewTorrent ────────────────────────────────────────────────────────────────

func TestNewTorrent_Defaults(t *testing.T) {
	tor := NewTorrent(99)
	if tor.ID != 99 {
		t.Errorf("ID = %d, want 99", tor.ID)
	}
	if tor.FreeType != FreeNormal {
		t.Errorf("FreeType = %d, want FreeNormal", tor.FreeType)
	}
	if tor.Seeders == nil || tor.Leechers == nil {
		t.Error("Seeders or Leechers is nil")
	}
	if tor.TokenedUsers == nil {
		t.Error("TokenedUsers is nil")
	}
}

// ── NewUser ───────────────────────────────────────────────────────────────────

func TestNewUser_Flags(t *testing.T) {
	u := NewUser(5, true, true)
	if u.ID != 5 {
		t.Errorf("ID = %d, want 5", u.ID)
	}
	if !u.CanLeech.Load() {
		t.Error("CanLeech should be true")
	}
	if !u.ProtectIP.Load() {
		t.Error("ProtectIP should be true")
	}

	u2 := NewUser(6, false, false)
	if u2.CanLeech.Load() {
		t.Error("CanLeech should be false")
	}
}
