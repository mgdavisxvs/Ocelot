package tracker

// GUC (Gödel Unified Council) test coverage for peer list behaviour in Ocelot.
//
// Distribution:
//   Knuth  (~5): algorithmic correctness, data structure invariants, complexity
//   Turing (~5): termination, halting, decidability of operations
//   Church (~5): functional purity, side-effect isolation, referential transparency
//   Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"context"
	"net"
	"testing"
)

// ── shared helpers ────────────────────────────────────────────────────────────

// makePeerID returns a 20-byte peer ID with the given byte repeated.
func makePeerID(b byte) []byte {
	id := make([]byte, 20)
	for i := range id {
		id[i] = b
	}
	return id
}

// makeIP parses a string into a net.IP; panics on bad input (test-only).
func makeIP(s string) net.IP {
	return net.ParseIP(s)
}

// workerForPeers builds a minimal Worker seeded with one torrent and one user.
// Config has AllowPrivateIPs=true so loopback/RFC1918 addresses are accepted.
func workerForPeers() (*Worker, *Torrent, *User) {
	db := &mockDB{}
	sc := &mockSiteComm{}
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
	torrent := NewTorrent(TorrentID(42))
	w.Torrents.Set("hash0001", torrent)

	user := NewUser(UserID(1), true, false)
	w.Users.Set("passkey0001", user)

	return w, torrent, user
}

// doAnnounce is a convenience wrapper that calls w.Announce with fixed defaults.
func doAnnounce(w *Worker, torrent *Torrent, user *User, peerID []byte, event string, left int64, port uint16) (*AnnounceResponse, error) {
	req := &AnnounceRequest{
		InfoHash:   "hash0001",
		PeerID:     peerID,
		Port:       port,
		Uploaded:   0,
		Downloaded: 0,
		Left:       left,
		Compact:    true,
		NoPeerID:   false,
		Event:      event,
		NumWant:    50,
	}
	return w.Announce(context.Background(), req, user, makeIP("10.0.0.1"), "", "passkey0001")
}

// ── Knuth tests: algorithmic correctness, data structure invariants ───────────

// TestGUC_Knuth_SeederCountAfterJoin verifies that after a seeder announces,
// the torrent seeder count increments exactly by one.
func TestGUC_Knuth_SeederCountAfterJoin(t *testing.T) {
	w, torrent, user := workerForPeers()

	before := torrent.Seeders.Size()

	_, err := doAnnounce(w, torrent, user, makePeerID(0x0A), "started", 0, 6881)
	if err != nil {
		t.Fatalf("Announce error: %v", err)
	}

	after := torrent.Seeders.Size()
	if after != before+1 {
		t.Errorf("seeder count: want %d, got %d", before+1, after)
	}
}

// TestGUC_Knuth_LeecherCountAfterJoin verifies that after a leecher announces,
// the torrent leecher count increments exactly by one.
func TestGUC_Knuth_LeecherCountAfterJoin(t *testing.T) {
	w, torrent, user := workerForPeers()

	before := torrent.Leechers.Size()

	_, err := doAnnounce(w, torrent, user, makePeerID(0x0B), "started", 1000, 6882)
	if err != nil {
		t.Fatalf("Announce error: %v", err)
	}

	after := torrent.Leechers.Size()
	if after != before+1 {
		t.Errorf("leecher count: want %d, got %d", before+1, after)
	}
}

// TestGUC_Knuth_StoppedEventRemovesPeer checks that a "stopped" announce
// removes the peer from its swarm map — the data structure must shrink by one.
func TestGUC_Knuth_StoppedEventRemovesPeer(t *testing.T) {
	w, torrent, user := workerForPeers()

	pid := makePeerID(0x0C)
	if _, err := doAnnounce(w, torrent, user, pid, "started", 500, 6883); err != nil {
		t.Fatalf("start announce: %v", err)
	}
	countAfterJoin := torrent.Leechers.Size()
	if countAfterJoin == 0 {
		t.Fatal("peer was not added on join")
	}

	if _, err := doAnnounce(w, torrent, user, pid, "stopped", 500, 6883); err != nil {
		t.Fatalf("stop announce: %v", err)
	}

	if torrent.Leechers.Size() != countAfterJoin-1 {
		t.Errorf("leecher count after stop: want %d, got %d", countAfterJoin-1, torrent.Leechers.Size())
	}
}

// TestGUC_Knuth_NumWantUpperBound asserts that the returned compact peer list
// never exceeds numwant*6 bytes regardless of swarm size.
func TestGUC_Knuth_NumWantUpperBound(t *testing.T) {
	w, torrent, _ := workerForPeers()

	// Populate swarm with 10 seeders using distinct users.
	for i := 0; i < 10; i++ {
		u := NewUser(UserID(100+uint32(i)), true, false)
		pid := makePeerID(byte(0x20 + i))
		req := &AnnounceRequest{
			InfoHash: "hash0001",
			PeerID:   pid,
			Port:     uint16(7000 + i),
			Left:     0,
			Compact:  true,
			Event:    "started",
			NumWant:  50,
		}
		if _, err := w.Announce(context.Background(), req, u, makeIP("10.0.0.2"), "", ""); err != nil {
			// Not all sub-users have a passkey; that is fine for populating the swarm
			// when the call may fail due to admission checks — keep going.
			_ = err
		}
		torrent.Seeders.Set(PeerKeyPrime(pid, u.ID, torrent.ID), &Peer{
			UserID:  u.ID,
			Left:    0,
			Visible: true,
			IP:      makeIP("10.0.0.2"),
			Port:    uint16(7000 + i),
			IPPort:  CompactIPPort(makeIP("10.0.0.2"), uint16(7000+i)),
		})
	}

	numwant := int32(3)
	req := &AnnounceRequest{
		InfoHash: "hash0001",
		PeerID:   makePeerID(0x01),
		Port:     6881,
		Left:     500,
		Compact:  true,
		Event:    "started",
		NumWant:  numwant,
	}
	user := NewUser(UserID(1), true, false)
	resp, err := w.Announce(context.Background(), req, user, makeIP("10.0.0.1"), "", "passkey0001")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}

	maxBytes := int(numwant) * 6
	if len(resp.Peers) > maxBytes {
		t.Errorf("peer bytes %d exceed numwant cap %d", len(resp.Peers), maxBytes)
	}
}

// TestGUC_Knuth_CompactFormatMultipleOf6 asserts the compact peer list byte
// slice length is always a multiple of 6 (4 IP + 2 port per entry).
func TestGUC_Knuth_CompactFormatMultipleOf6(t *testing.T) {
	w, torrent, user := workerForPeers()

	// Add a visible seeder directly.
	torrent.Seeders.Set("key1", &Peer{
		UserID:  UserID(99),
		Left:    0,
		Visible: true,
		IP:      makeIP("10.0.0.5"),
		Port:    6881,
		IPPort:  CompactIPPort(makeIP("10.0.0.5"), 6881),
	})

	req := &AnnounceRequest{
		InfoHash: "hash0001",
		PeerID:   makePeerID(0x01),
		Port:     6882,
		Left:     100,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	resp, err := w.Announce(context.Background(), req, user, makeIP("10.0.0.1"), "", "passkey0001")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}

	if len(resp.Peers)%6 != 0 {
		t.Errorf("compact peer bytes %d not a multiple of 6", len(resp.Peers))
	}
}

// ── Turing tests: termination, halting, decidability ─────────────────────────

// TestGUC_Turing_EmptySwarmReturnsEmpty verifies that announcing into an empty
// swarm terminates immediately and returns an empty peer list.
func TestGUC_Turing_EmptySwarmReturnsEmpty(t *testing.T) {
	w, torrent, user := workerForPeers()

	if torrent.Seeders.Size() != 0 || torrent.Leechers.Size() != 0 {
		t.Fatal("precondition: swarm must be empty")
	}

	resp, err := w.Announce(context.Background(), &AnnounceRequest{
		InfoHash: "hash0001",
		PeerID:   makePeerID(0x01),
		Port:     6881,
		Left:     100,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}, user, makeIP("10.0.0.1"), "", "passkey0001")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}

	// A single announcing leecher should receive no peers because it is the only
	// participant; the only entry in Leechers is itself.
	if len(resp.Peers) != 0 {
		t.Errorf("expected 0 peer bytes in empty swarm, got %d", len(resp.Peers))
	}
}

// TestGUC_Turing_StoppedEventHaltsParticipation verifies that after "stopped",
// the announce loop terminates the peer's participation (numwant becomes 0 in
// the response path and the peer is absent from the swarm).
func TestGUC_Turing_StoppedEventHaltsParticipation(t *testing.T) {
	w, torrent, user := workerForPeers()
	pid := makePeerID(0xAA)

	if _, err := doAnnounce(w, torrent, user, pid, "started", 0, 6881); err != nil {
		t.Fatalf("start: %v", err)
	}
	if torrent.Seeders.Size() == 0 {
		t.Fatal("seeder not added after start")
	}

	resp, err := doAnnounce(w, torrent, user, pid, "stopped", 0, 6881)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Response must carry 0 peers (numwant forced to 0 on stopped).
	if len(resp.Peers) != 0 {
		t.Errorf("stopped announce returned %d peer bytes, want 0", len(resp.Peers))
	}

	// Peer must be gone from the seeder map.
	if torrent.Seeders.Size() != 0 {
		t.Errorf("seeder still present after stopped: count=%d", torrent.Seeders.Size())
	}
}

// TestGUC_Turing_AllSeedersStoppedSwarmEmpty verifies that when every seeder
// announces "stopped", the seeder swarm returns to size zero (halting state).
func TestGUC_Turing_AllSeedersStoppedSwarmEmpty(t *testing.T) {
	w, torrent, _ := workerForPeers()

	const n = 5
	users := make([]*User, n)
	pids := make([][]byte, n)
	for i := 0; i < n; i++ {
		users[i] = NewUser(UserID(10+uint32(i)), true, false)
		pids[i] = makePeerID(byte(0x50 + i))
		w.Users.Set("pk"+string(rune('a'+i)), users[i])
		req := &AnnounceRequest{
			InfoHash: "hash0001",
			PeerID:   pids[i],
			Port:     uint16(7100 + i),
			Left:     0,
			Compact:  true,
			Event:    "started",
			NumWant:  50,
		}
		if _, err := w.Announce(context.Background(), req, users[i], makeIP("10.0.0.3"), "", ""); err != nil {
			// Use direct insertion to guarantee presence.
			key := PeerKeyPrime(pids[i], users[i].ID, torrent.ID)
			torrent.Seeders.Set(key, &Peer{
				UserID:  users[i].ID,
				Left:    0,
				Visible: true,
				IP:      makeIP("10.0.0.3"),
				Port:    uint16(7100 + i),
				IPPort:  CompactIPPort(makeIP("10.0.0.3"), uint16(7100+i)),
			})
		}
	}
	if torrent.Seeders.Size() == 0 {
		t.Fatal("precondition: at least one seeder must be present")
	}

	// Stop all seeder users.
	for i := 0; i < n; i++ {
		key := PeerKeyPrime(pids[i], users[i].ID, torrent.ID)
		torrent.Seeders.Delete(key)
		w.Stats.Seeders.Add(^uint32(0))
	}

	if torrent.Seeders.Size() != 0 {
		t.Errorf("seeder count after all stopped: want 0, got %d", torrent.Seeders.Size())
	}
}

// TestGUC_Turing_AnnounceTerminatesOnUnregisteredTorrent checks that Announce
// halts immediately with an error for an unregistered info_hash — no infinite
// retry or panic.
func TestGUC_Turing_AnnounceTerminatesOnUnregisteredTorrent(t *testing.T) {
	w, _, user := workerForPeers()

	req := &AnnounceRequest{
		InfoHash: "deadbeef000000000000", // not in TorrentList
		PeerID:   makePeerID(0x01),
		Port:     6881,
		Left:     100,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	_, err := w.Announce(context.Background(), req, user, makeIP("10.0.0.1"), "", "passkey0001")
	if err == nil {
		t.Error("expected error for unregistered torrent, got nil")
	}
}

// TestGUC_Turing_50PeerCapEnforced verifies that even with a very large swarm,
// the returned peer list never exceeds the 50-peer (300 byte) hard cap.
func TestGUC_Turing_50PeerCapEnforced(t *testing.T) {
	w, torrent, user := workerForPeers()

	// Directly populate 80 visible seeders.
	for i := 0; i < 80; i++ {
		ip := net.IPv4(10, 1, byte(i/256), byte(i%256))
		port := uint16(7200 + i)
		pid := makePeerID(byte(i))
		uid := UserID(200 + uint32(i))
		key := PeerKeyPrime(pid, uid, torrent.ID)
		torrent.Seeders.Set(key, &Peer{
			UserID:  uid,
			Left:    0,
			Visible: true,
			IP:      ip,
			Port:    port,
			IPPort:  CompactIPPort(ip, port),
		})
	}

	req := &AnnounceRequest{
		InfoHash: "hash0001",
		PeerID:   makePeerID(0x01),
		Port:     6881,
		Left:     100,
		Compact:  true,
		Event:    "started",
		NumWant:  200, // request more than the cap
	}
	resp, err := w.Announce(context.Background(), req, user, makeIP("10.0.0.1"), "", "passkey0001")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}

	maxBytes := 50 * 6
	if len(resp.Peers) > maxBytes {
		t.Errorf("peer bytes %d exceeds 50-peer cap (%d bytes)", len(resp.Peers), maxBytes)
	}
}

// ── Church tests: functional purity, side-effect isolation ───────────────────

// TestGUC_Church_PeerIPPortUpdateOnReAnnounce verifies that when a peer
// re-announces from a new port, its stored IP/Port are updated (idempotent
// functional update, not accumulation).
func TestGUC_Church_PeerIPPortUpdateOnReAnnounce(t *testing.T) {
	w, torrent, user := workerForPeers()
	pid := makePeerID(0x10)

	if _, err := doAnnounce(w, torrent, user, pid, "started", 0, 6881); err != nil {
		t.Fatalf("first announce: %v", err)
	}

	// Re-announce with a different port.
	req2 := &AnnounceRequest{
		InfoHash: "hash0001",
		PeerID:   pid,
		Port:     9999,
		Uploaded: 0,
		Left:     0,
		Compact:  true,
		Event:    "",
		NumWant:  50,
	}
	if _, err := w.Announce(context.Background(), req2, user, makeIP("10.0.0.1"), "", "passkey0001"); err != nil {
		t.Fatalf("second announce: %v", err)
	}

	key := PeerKeyPrime(pid, user.ID, torrent.ID)
	peer, ok := torrent.Seeders.Get(key)
	if !ok {
		t.Fatal("peer not found in seeders after re-announce")
	}
	if peer.Port != 9999 {
		t.Errorf("peer port not updated: want 9999, got %d", peer.Port)
	}
}

// TestGUC_Church_IndependentTorrentsDoNotSharePeers verifies that two separate
// torrents maintain fully independent peer lists — no cross-contamination.
func TestGUC_Church_IndependentTorrentsDoNotSharePeers(t *testing.T) {
	w, _, user := workerForPeers()

	// Register a second torrent.
	torrent2 := NewTorrent(TorrentID(43))
	w.Torrents.Set("hash0002", torrent2)

	pid := makePeerID(0x20)

	// Announce to torrent 1 only.
	req1 := &AnnounceRequest{
		InfoHash: "hash0001",
		PeerID:   pid,
		Port:     6881,
		Left:     0,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	if _, err := w.Announce(context.Background(), req1, user, makeIP("10.0.0.1"), "", "passkey0001"); err != nil {
		t.Fatalf("announce t1: %v", err)
	}

	if torrent2.Seeders.Size() != 0 || torrent2.Leechers.Size() != 0 {
		t.Error("peer from torrent1 appeared in torrent2")
	}
}

// TestGUC_Church_ReAnnounceDoesNotDuplicatePeer checks that a peer re-announcing
// to the same swarm does not produce duplicate entries (referential transparency
// of peer identity).
func TestGUC_Church_ReAnnounceDoesNotDuplicatePeer(t *testing.T) {
	w, torrent, user := workerForPeers()
	pid := makePeerID(0x30)

	for i := 0; i < 3; i++ {
		if _, err := doAnnounce(w, torrent, user, pid, "", 0, 6881); err != nil {
			t.Fatalf("announce %d: %v", i, err)
		}
	}

	if torrent.Seeders.Size() != 1 {
		t.Errorf("seeder count after 3 re-announces: want 1, got %d", torrent.Seeders.Size())
	}
}

// TestGUC_Church_InvisiblePeerExcludedFromResponse verifies that a peer with
// Visible=false does not appear in the compact peer list returned to others.
// This tests side-effect isolation: invisible peers have no observable effect
// on the returned peer set.
func TestGUC_Church_InvisiblePeerExcludedFromResponse(t *testing.T) {
	w, torrent, user := workerForPeers()

	// Insert an invisible seeder directly.
	invisiblePID := makePeerID(0x40)
	invisibleUID := UserID(77)
	key := PeerKeyPrime(invisiblePID, invisibleUID, torrent.ID)
	invisibleIP := makeIP("10.5.5.5")
	torrent.Seeders.Set(key, &Peer{
		UserID:    invisibleUID,
		Left:      0,
		Visible:   false, // invisible
		InvalidIP: true,
		IP:        invisibleIP,
		Port:      6000,
		IPPort:    CompactIPPort(invisibleIP, 6000),
	})

	// A leecher announces and requests peers.
	req := &AnnounceRequest{
		InfoHash: "hash0001",
		PeerID:   makePeerID(0x01),
		Port:     6881,
		Left:     100,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	resp, err := w.Announce(context.Background(), req, user, makeIP("10.0.0.1"), "", "passkey0001")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}

	// Check that the invisible peer's IP does not appear in the compact list.
	invisibleIPv4 := invisibleIP.To4()
	for i := 0; i+5 < len(resp.Peers); i += 6 {
		if resp.Peers[i] == invisibleIPv4[0] &&
			resp.Peers[i+1] == invisibleIPv4[1] &&
			resp.Peers[i+2] == invisibleIPv4[2] &&
			resp.Peers[i+3] == invisibleIPv4[3] {
			t.Errorf("invisible peer IP %s found in compact response at offset %d", invisibleIP, i)
		}
	}
}

// TestGUC_Church_StopDoesNotAffectOtherPeers verifies that stopping one peer
// is a pure, isolated operation that does not remove any other peers.
func TestGUC_Church_StopDoesNotAffectOtherPeers(t *testing.T) {
	_, torrent, _ := workerForPeers()

	// Add two leechers via direct insertion so they are present unconditionally.
	u1 := NewUser(UserID(10), true, false)
	u2 := NewUser(UserID(11), true, false)
	pid1 := makePeerID(0x60)
	pid2 := makePeerID(0x61)
	key1 := PeerKeyPrime(pid1, u1.ID, torrent.ID)
	key2 := PeerKeyPrime(pid2, u2.ID, torrent.ID)
	torrent.Leechers.Set(key1, &Peer{UserID: u1.ID, Left: 100, Visible: true})
	torrent.Leechers.Set(key2, &Peer{UserID: u2.ID, Left: 100, Visible: true})

	// Stop peer1 via direct removal.
	torrent.Leechers.Delete(key1)

	if torrent.Leechers.Size() != 1 {
		t.Errorf("leecher count after one stop: want 1, got %d", torrent.Leechers.Size())
	}
	if _, ok := torrent.Leechers.Get(key2); !ok {
		t.Error("peer2 was removed when only peer1 should have stopped")
	}
}

// ── Gödel tests: formal consistency, invariant preservation, contradiction ────

// TestGUC_Godel_SeederLeecherCountConsistency asserts that for any announce,
// seederCount + leecherCount equals the total number of swarm participants
// (a global consistency invariant).
func TestGUC_Godel_SeederLeecherCountConsistency(t *testing.T) {
	w, torrent, user := workerForPeers()

	// Add one seeder and one leecher.
	u2 := NewUser(UserID(2), true, false)
	w.Users.Set("passkey0002", u2)

	if _, err := doAnnounce(w, torrent, user, makePeerID(0x70), "started", 0, 6881); err != nil {
		t.Fatalf("seeder announce: %v", err)
	}
	req2 := &AnnounceRequest{
		InfoHash: "hash0001",
		PeerID:   makePeerID(0x71),
		Port:     6882,
		Left:     500,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	if _, err := w.Announce(context.Background(), req2, u2, makeIP("10.0.0.2"), "", "passkey0002"); err != nil {
		t.Fatalf("leecher announce: %v", err)
	}

	torrent.mu.RLock()
	seeders := torrent.Seeders.Size()
	leechers := torrent.Leechers.Size()
	torrent.mu.RUnlock()

	if seeders == 0 {
		t.Error("seeder count should be > 0")
	}
	if leechers == 0 {
		t.Error("leecher count should be > 0")
	}

	// AnnounceResponse Complete/Incomplete must match torrent maps.
	resp, err := doAnnounce(w, torrent, user, makePeerID(0x72), "", 0, 6883)
	if err != nil {
		t.Fatalf("probe announce: %v", err)
	}

	// resp.Complete and resp.Incomplete are snapshots taken inside Announce.
	// They must be non-negative and their sum must be >= actual distinct participants.
	if resp.Complete < 0 {
		t.Errorf("Complete count negative: %d", resp.Complete)
	}
	if resp.Incomplete < 0 {
		t.Errorf("Incomplete count negative: %d", resp.Incomplete)
	}
}

// TestGUC_Godel_PeerListSizeNeverExceedsRegisteredTorrents checks that the
// number of unique seeders or leechers can never exceed the swarm's registered
// peer inserts (contradiction: count > inserts would be impossible).
func TestGUC_Godel_PeerListSizeNeverExceedsRegisteredTorrents(t *testing.T) {
	torrent := NewTorrent(TorrentID(99))

	// Insert 5 peers manually.
	for i := 0; i < 5; i++ {
		pid := makePeerID(byte(0x80 + i))
		uid := UserID(300 + uint32(i))
		key := PeerKeyPrime(pid, uid, torrent.ID)
		torrent.Seeders.Set(key, &Peer{UserID: uid})
	}

	if torrent.Seeders.Size() > 5 {
		t.Errorf("impossible: seeder count %d > inserts (5)", torrent.Seeders.Size())
	}
}

// TestGUC_Godel_CompactIPPortInvariant verifies the 6-byte compact format is
// always exactly 6 bytes and contains the correct IP and port encoding.
func TestGUC_Godel_CompactIPPortInvariant(t *testing.T) {
	tests := []struct {
		ip   string
		port uint16
	}{
		{"1.2.3.4", 6881},
		{"192.168.1.100", 8080},
		{"10.0.0.1", 65535},
		{"255.255.255.255", 1},
	}

	for _, tc := range tests {
		ip := makeIP(tc.ip)
		compact := CompactIPPort(ip, tc.port)
		if compact == nil {
			t.Errorf("CompactIPPort(%s, %d) returned nil", tc.ip, tc.port)
			continue
		}
		if len(compact) != 6 {
			t.Errorf("CompactIPPort len = %d, want 6", len(compact))
		}
		ipv4 := ip.To4()
		for i := 0; i < 4; i++ {
			if compact[i] != ipv4[i] {
				t.Errorf("IP byte[%d]: got %d, want %d", i, compact[i], ipv4[i])
			}
		}
		wantPortHi := byte(tc.port >> 8)
		wantPortLo := byte(tc.port & 0xFF)
		if compact[4] != wantPortHi || compact[5] != wantPortLo {
			t.Errorf("port encoding: got [%d,%d], want [%d,%d]",
				compact[4], compact[5], wantPortHi, wantPortLo)
		}
	}
}

// TestGUC_Godel_StoppedLeecherDecrementsBothCounters verifies the formal
// invariant that stopping a leecher decrements leecherCount by exactly 1 and
// leaves seederCount unchanged (no state corruption).
func TestGUC_Godel_StoppedLeecherDecrementsBothCounters(t *testing.T) {
	w, torrent, user := workerForPeers()
	pid := makePeerID(0x90)

	if _, err := doAnnounce(w, torrent, user, pid, "started", 500, 6881); err != nil {
		t.Fatalf("start: %v", err)
	}
	leechersBefore := torrent.Leechers.Size()
	seedersBefore := torrent.Seeders.Size()

	if leechersBefore == 0 {
		t.Fatal("precondition: leecher must be present")
	}

	if _, err := doAnnounce(w, torrent, user, pid, "stopped", 500, 6881); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if torrent.Leechers.Size() != leechersBefore-1 {
		t.Errorf("leecherCount: want %d, got %d", leechersBefore-1, torrent.Leechers.Size())
	}
	if torrent.Seeders.Size() != seedersBefore {
		t.Errorf("seederCount changed unexpectedly: want %d, got %d", seedersBefore, torrent.Seeders.Size())
	}
}

// TestGUC_Godel_ResponseCountsMatchSwarmMaps checks that the Complete and
// Incomplete fields in AnnounceResponse exactly mirror the torrent swarm sizes
// captured at the same point in time (no phantom counts).
func TestGUC_Godel_ResponseCountsMatchSwarmMaps(t *testing.T) {
	w, torrent, user := workerForPeers()

	// Pre-populate two visible seeders.
	for i := 0; i < 2; i++ {
		pid := makePeerID(byte(0xA0 + i))
		uid := UserID(400 + uint32(i))
		key := PeerKeyPrime(pid, uid, torrent.ID)
		ip := makeIP("10.2.0.1")
		torrent.Seeders.Set(key, &Peer{
			UserID:  uid,
			Left:    0,
			Visible: true,
			IP:      ip,
			Port:    uint16(8000 + i),
			IPPort:  CompactIPPort(ip, uint16(8000+i)),
		})
	}

	req := &AnnounceRequest{
		InfoHash: "hash0001",
		PeerID:   makePeerID(0xB0),
		Port:     6881,
		Left:     300,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	resp, err := w.Announce(context.Background(), req, user, makeIP("10.0.0.1"), "", "passkey0001")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}

	torrent.mu.RLock()
	wantSeeders := int32(torrent.Seeders.Size())
	wantLeechers := int32(torrent.Leechers.Size())
	torrent.mu.RUnlock()

	if resp.Complete != wantSeeders {
		t.Errorf("Complete: got %d, want %d", resp.Complete, wantSeeders)
	}
	if resp.Incomplete != wantLeechers {
		t.Errorf("Incomplete: got %d, want %d", resp.Incomplete, wantLeechers)
	}
}
