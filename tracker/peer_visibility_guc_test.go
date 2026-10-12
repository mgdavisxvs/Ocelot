package tracker

// peer_visibility_guc_test.go — GUC (Gödel Unified Council) test coverage for
// peer visibility logic in the Ocelot tracker.
//
// Lens distribution:
//   Knuth  (algorithmic correctness)  : TestGUC_PrivateIP_*, TestGUC_PublicIP_*,
//                                       TestGUC_CompactIPPort_FormatCorrectness,
//                                       TestGUC_StoppedPeer_RemovedFromList,
//                                       TestGUC_CollectVisiblePeers_ReturnsOnlyVisible
//   Turing (termination/halting)      : TestGUC_VisibleFlag_*, TestGUC_StoppedPeer_Absent*,
//                                       TestGUC_ReAnnounce_*, TestGUC_PortZero_*,
//                                       TestGUC_SeederLeecherCounts_*
//   Church (functional purity)        : TestGUC_ValidateIPNotPrivate_Purity,
//                                       TestGUC_PeerIsVisible_NoSideEffects,
//                                       TestGUC_CollectVisiblePeers_NoMutation,
//                                       TestGUC_CompactIPPort_Deterministic,
//                                       TestGUC_ValidPublicIPSeeder_AlwaysVisible
//   Gödel  (formal consistency)       : TestGUC_InvalidIP_Sticky*,
//                                       TestGUC_VisibleAndInvalidIP_NeverCoexist,
//                                       TestGUC_InvisiblePeer_NeverInCompactOutput,
//                                       TestGUC_StoppedPeer_CountDecrement,
//                                       TestGUC_LeecherCannotLeech_NotVisible

import (
	"context"
	"net"
	"testing"
)

// ── internal helpers ─────────────────────────────────────────────────────────

// newWorkerPrivateBlock returns a Worker whose config rejects private IPs.
func newWorkerPrivateBlock() *Worker {
	f := newTestFixture()
	f.worker.Config.AllowPrivateIPs = false
	return f.worker
}

// pvAnnounce fires a minimal announce for the default user on the default
// torrent.  req.IP is intentionally left nil so that clientIP is used.
func pvAnnounce(w *Worker, peerID string, clientIP net.IP, port uint16, left int64, event string) (*AnnounceResponse, error) {
	req := &AnnounceRequest{
		InfoHash: testInfoHash,
		PeerID:   []byte(peerID),
		Port:     port,
		Left:     left,
		Compact:  true,
		Event:    event,
		NumWant:  50,
	}
	user, _ := w.Users.Get(testPasskey)
	return w.Announce(context.Background(), req, user, clientIP, "TestClient/1.0", testPasskey)
}

// fetchPeer looks up a peer in seeders then leechers of the default torrent.
func fetchPeer(w *Worker, peerID string, userID UserID) *Peer {
	torrent, ok := w.Torrents.Get(testInfoHash)
	if !ok {
		return nil
	}
	key := PeerKeyPrime([]byte(peerID), userID, torrent.ID)
	if p, ok := torrent.Seeders.Get(key); ok {
		return p
	}
	if p, ok := torrent.Leechers.Get(key); ok {
		return p
	}
	return nil
}

// ── Knuth: algorithmic correctness ───────────────────────────────────────────

// TestGUC_PrivateIP_SetsInvalidIPAndNotVisible (Knuth) verifies that each
// RFC-1918 / bogon range sets InvalidIP=true and collapses Visible to false.
func TestGUC_PrivateIP_SetsInvalidIPAndNotVisible(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"rfc1918-10", "10.0.0.1"},
		{"rfc1918-172", "172.16.0.1"},
		{"rfc1918-192", "192.168.1.100"},
		{"loopback", "127.0.0.1"},
		{"link-local", "169.254.1.1"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			w := newWorkerPrivateBlock()
			ip := net.ParseIP(tc.ip)

			if _, err := pvAnnounce(w, testPeerID, ip, testPort, 500, "started"); err != nil {
				t.Fatalf("announce returned unexpected error: %v", err)
			}

			user, _ := w.Users.Get(testPasskey)
			peer := fetchPeer(w, testPeerID, user.ID)
			if peer == nil {
				t.Fatal("peer not found in any list after announce")
			}
			if !peer.InvalidIP {
				t.Errorf("expected InvalidIP=true for private IP %s, got false", tc.ip)
			}
			if peer.Visible {
				t.Errorf("expected Visible=false for private IP %s, got true", tc.ip)
			}
		})
	}
}

// TestGUC_PublicIP_SetsVisibleTrue (Knuth) verifies that a routable public
// IPv4 results in InvalidIP=false and Visible=true for a seeder.
func TestGUC_PublicIP_SetsVisibleTrue(t *testing.T) {
	w := newTestFixture().worker
	w.Config.AllowPrivateIPs = false

	ip := net.ParseIP("1.2.3.4")
	if _, err := pvAnnounce(w, testPeerID, ip, testPort, 0, "started"); err != nil {
		t.Fatalf("announce failed: %v", err)
	}

	user, _ := w.Users.Get(testPasskey)
	peer := fetchPeer(w, testPeerID, user.ID)
	if peer == nil {
		t.Fatal("peer not found after announce")
	}
	if peer.InvalidIP {
		t.Errorf("expected InvalidIP=false for public IP, got true")
	}
	if !peer.Visible {
		t.Errorf("expected Visible=true for public-IP seeder, got false")
	}
}

// TestGUC_CompactIPPort_FormatCorrectness (Knuth) verifies the 6-byte compact
// encoding: bytes [0:4] hold IPv4, byte [4] holds port high byte, byte [5] low.
func TestGUC_CompactIPPort_FormatCorrectness(t *testing.T) {
	cases := []struct {
		name     string
		ip       string
		port     uint16
		expected [6]byte
	}{
		{"std-port", "1.2.3.4", 6881, [6]byte{1, 2, 3, 4, 0x1A, 0xE1}},
		{"https", "255.0.0.1", 443, [6]byte{255, 0, 0, 1, 0x01, 0xBB}},
		{"zero-port", "10.0.0.1", 0, [6]byte{10, 0, 0, 1, 0x00, 0x00}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			got := CompactIPPort(ip, tc.port)
			if len(got) != 6 {
				t.Fatalf("expected 6 bytes, got %d", len(got))
			}
			for i, want := range tc.expected {
				if got[i] != want {
					t.Errorf("byte[%d]: want 0x%02X, got 0x%02X", i, want, got[i])
				}
			}
		})
	}
}

// TestGUC_StoppedPeer_RemovedFromList (Knuth) verifies that a "stopped" event
// deletes the peer from the torrent's peer list entirely, not merely marks it.
func TestGUC_StoppedPeer_RemovedFromList(t *testing.T) {
	w := newTestFixture().worker
	ip := net.ParseIP(testIP)

	if _, err := pvAnnounce(w, testPeerID, ip, testPort, 500, "started"); err != nil {
		t.Fatalf("start announce failed: %v", err)
	}

	torrent, _ := w.Torrents.Get(testInfoHash)
	if torrent.Leechers.Size() == 0 {
		t.Fatal("peer should be present in leechers before stop")
	}

	if _, err := pvAnnounce(w, testPeerID, ip, testPort, 500, "stopped"); err != nil {
		t.Fatalf("stop announce failed: %v", err)
	}

	user, _ := w.Users.Get(testPasskey)
	if p := fetchPeer(w, testPeerID, user.ID); p != nil {
		t.Error("peer should be absent from all lists after stopped event, but is still present")
	}
}

// TestGUC_CollectVisiblePeers_ReturnsOnlyVisible (Knuth) verifies that
// collectVisiblePeers never yields a peer with Visible=false.
func TestGUC_CollectVisiblePeers_ReturnsOnlyVisible(t *testing.T) {
	pl := NewPeerList()
	pl.Set("vis1", &Peer{UserID: UserID(10), Visible: true, IP: net.ParseIP("1.2.3.4"), Port: 6881})
	pl.Set("vis2", &Peer{UserID: UserID(11), Visible: true, IP: net.ParseIP("2.3.4.5"), Port: 6882})
	pl.Set("invis", &Peer{UserID: UserID(20), Visible: false, IP: net.ParseIP("5.6.7.8"), Port: 6883})
	pl.Set("invalid", &Peer{UserID: UserID(30), Visible: false, InvalidIP: true, IP: net.ParseIP("10.0.0.1"), Port: 6884})

	results := collectVisiblePeers(pl, UserID(0)) // exclude nobody matching
	for _, p := range results {
		if !p.Visible {
			t.Errorf("collectVisiblePeers returned a peer with Visible=false (UserID=%d)", p.UserID)
		}
	}
	if len(results) != 2 {
		t.Errorf("expected exactly 2 visible peers, got %d", len(results))
	}
}

// ── Turing: termination / halting behavior ────────────────────────────────────

// TestGUC_VisibleFlag_GatesPeerListInclusion (Turing) verifies that an invisible
// seeder (due to private IP) never appears in the compact peer bytes returned
// to a leecher.
func TestGUC_VisibleFlag_GatesPeerListInclusion(t *testing.T) {
	f := newTestFixture()
	w := f.worker
	w.Config.AllowPrivateIPs = false

	user2, _ := w.Users.Get(testPasskey2)

	// user2 announces as seeder with a private IP → InvalidIP=true, Visible=false
	req2 := &AnnounceRequest{
		InfoHash: testInfoHash,
		PeerID:   []byte(testPeerID2),
		Port:     6882,
		Left:     0,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	if _, err := w.Announce(context.Background(), req2, user2, net.ParseIP("192.168.1.50"), "TC/1", testPasskey2); err != nil {
		t.Fatalf("seeder announce failed: %v", err)
	}

	torrent, _ := w.Torrents.Get(testInfoHash)
	seederKey := PeerKeyPrime([]byte(testPeerID2), user2.ID, torrent.ID)
	seeder, _ := torrent.Seeders.Get(seederKey)
	if seeder == nil {
		t.Fatal("seeder not found in torrent")
	}
	if seeder.Visible {
		t.Fatal("precondition: seeder with private IP should be invisible")
	}

	// user1 announces as leecher with a public IP
	user1, _ := w.Users.Get(testPasskey)
	req1 := &AnnounceRequest{
		InfoHash: testInfoHash,
		PeerID:   []byte(testPeerID),
		Port:     6881,
		Left:     500,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	resp, err := w.Announce(context.Background(), req1, user1, net.ParseIP("2.3.4.5"), "TC/1", testPasskey)
	if err != nil {
		t.Fatalf("leecher announce failed: %v", err)
	}

	if len(resp.Peers) > 0 {
		t.Errorf("expected 0 compact peer bytes (invisible seeder), got %d bytes (%d peers)",
			len(resp.Peers), len(resp.Peers)/6)
	}
}

// TestGUC_StoppedPeer_AbsentFromBothLists (Turing) verifies that a seeder
// stopped via the "stopped" event is absent from both Seeders and Leechers,
// and that neither list grows or stays at its pre-stop size.
func TestGUC_StoppedPeer_AbsentFromBothLists(t *testing.T) {
	w := newTestFixture().worker
	ip := net.ParseIP(testIP)

	if _, err := pvAnnounce(w, testPeerID, ip, testPort, 0, "started"); err != nil {
		t.Fatalf("seeder start failed: %v", err)
	}

	torrent, _ := w.Torrents.Get(testInfoHash)
	if torrent.Seeders.Size() == 0 {
		t.Fatal("seeder should be present before stop")
	}

	if _, err := pvAnnounce(w, testPeerID, ip, testPort, 0, "stopped"); err != nil {
		t.Fatalf("seeder stop failed: %v", err)
	}

	if n := torrent.Seeders.Size(); n != 0 {
		t.Errorf("seeders should be empty after stop, got %d", n)
	}
	if n := torrent.Leechers.Size(); n != 0 {
		t.Errorf("leechers should be empty after stop, got %d", n)
	}
}

// TestGUC_ReAnnounce_NewPrivateIPUpdatesVisibility (Turing) verifies that a
// peer which was initially visible (public IP) becomes invisible when it
// re-announces with a private IP under private-IP-blocking config.
func TestGUC_ReAnnounce_NewPrivateIPUpdatesVisibility(t *testing.T) {
	w := newWorkerPrivateBlock()

	// First announce: public IP, seeder → Visible=true
	if _, err := pvAnnounce(w, testPeerID, net.ParseIP("1.2.3.4"), testPort, 0, "started"); err != nil {
		t.Fatalf("first announce failed: %v", err)
	}
	user, _ := w.Users.Get(testPasskey)
	peer := fetchPeer(w, testPeerID, user.ID)
	if peer == nil {
		t.Fatal("peer not found after first announce")
	}
	if !peer.Visible {
		t.Fatalf("precondition: peer should be Visible=true after first public-IP announce")
	}

	// Second announce: private IP, same seeder → Visible=false
	if _, err := pvAnnounce(w, testPeerID, net.ParseIP("192.168.1.1"), testPort, 0, ""); err != nil {
		t.Fatalf("second announce failed: %v", err)
	}
	peer = fetchPeer(w, testPeerID, user.ID)
	if peer == nil {
		t.Fatal("peer not found after second announce")
	}
	if peer.Visible {
		t.Errorf("expected Visible=false after re-announce with private IP")
	}
	if !peer.InvalidIP {
		t.Errorf("expected InvalidIP=true after re-announce with private IP")
	}
}

// TestGUC_PortZero_VisibilityDependsOnIPValidity (Turing) verifies that a
// port=0 announce with a valid public IPv4 does not trigger InvalidIP: the
// compact 6-byte encoding for IPv4 with port=0 is well-defined, so
// port=0 alone is not a disqualifying condition.
func TestGUC_PortZero_VisibilityDependsOnIPValidity(t *testing.T) {
	w := newTestFixture().worker
	w.Config.AllowPrivateIPs = false

	ip := net.ParseIP("8.8.8.8")
	if _, err := pvAnnounce(w, testPeerID, ip, 0, 0, "started"); err != nil {
		t.Fatalf("announce failed: %v", err)
	}

	user, _ := w.Users.Get(testPasskey)
	peer := fetchPeer(w, testPeerID, user.ID)
	if peer == nil {
		t.Fatal("peer not found after announce")
	}
	if peer.InvalidIP {
		t.Errorf("expected InvalidIP=false for valid IPv4 seeder with port=0")
	}
	if !peer.Visible {
		t.Errorf("expected Visible=true for valid public-IPv4 seeder with port=0")
	}
}

// TestGUC_SeederLeecherCounts_ReflectRemovals (Turing) verifies that the
// torrent's leecher count decrements after one of two leechers stops,
// confirming the list-size invariant terminates at the expected value.
func TestGUC_SeederLeecherCounts_ReflectRemovals(t *testing.T) {
	f := newTestFixture()
	w := f.worker
	ip := net.ParseIP(testIP)

	user1, _ := w.Users.Get(testPasskey)
	user2, _ := w.Users.Get(testPasskey2)

	announce := func(peerID string, user *User, passkey string) error {
		req := &AnnounceRequest{
			InfoHash: testInfoHash,
			PeerID:   []byte(peerID),
			Port:     testPort,
			Left:     500,
			Compact:  true,
			Event:    "started",
			NumWant:  50,
		}
		_, err := w.Announce(context.Background(), req, user, ip, "TC/1", passkey)
		return err
	}

	if err := announce(testPeerID, user1, testPasskey); err != nil {
		t.Fatalf("user1 announce failed: %v", err)
	}
	if err := announce(testPeerID2, user2, testPasskey2); err != nil {
		t.Fatalf("user2 announce failed: %v", err)
	}

	torrent, _ := w.Torrents.Get(testInfoHash)
	before := torrent.Leechers.Size()
	if before < 2 {
		t.Fatalf("expected at least 2 leechers, got %d", before)
	}

	stopReq := &AnnounceRequest{
		InfoHash: testInfoHash,
		PeerID:   []byte(testPeerID),
		Port:     testPort,
		Left:     500,
		Compact:  true,
		Event:    "stopped",
		NumWant:  0,
	}
	if _, err := w.Announce(context.Background(), stopReq, user1, ip, "TC/1", testPasskey); err != nil {
		t.Fatalf("user1 stop failed: %v", err)
	}

	after := torrent.Leechers.Size()
	if after >= before {
		t.Errorf("leecher count should decrease after stop: before=%d after=%d", before, after)
	}
}

// ── Church: functional purity / side-effect isolation ─────────────────────────

// TestGUC_ValidateIPNotPrivate_Purity (Church) verifies referential
// transparency: ValidateIPNotPrivate returns the same value on repeated calls
// with the same argument without any observable side effect.
func TestGUC_ValidateIPNotPrivate_Purity(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"1.2.3.4", true},
		{"8.8.8.8", true},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"127.0.0.1", false},
		{"169.254.0.1", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			for i := 0; i < 5; i++ {
				got := ValidateIPNotPrivate(ip)
				if got != tc.want {
					t.Errorf("call %d: ValidateIPNotPrivate(%s) = %v, want %v", i+1, tc.ip, got, tc.want)
				}
			}
		})
	}
}

// TestGUC_PeerIsVisible_NoSideEffects (Church) verifies that peerIsVisible is
// a pure function: it reads but never writes to the Peer or User it receives.
func TestGUC_PeerIsVisible_NoSideEffects(t *testing.T) {
	w := newTestFixture().worker
	user := NewUser(UserID(99), true, false)

	peer := &Peer{
		UserID:    UserID(99),
		Left:      0,
		InvalidIP: false,
		Visible:   false, // intentionally stale — peerIsVisible must not write here
		Announces: 7,
		Port:      6881,
		IP:        net.ParseIP("1.2.3.4"),
	}

	announcesBefore := peer.Announces
	portBefore := peer.Port
	canLeechBefore := user.CanLeech.Load()
	leftBefore := peer.Left
	invalidIPBefore := peer.InvalidIP

	result := w.peerIsVisible(user, peer)

	if peer.Announces != announcesBefore {
		t.Error("peerIsVisible mutated peer.Announces")
	}
	if peer.Port != portBefore {
		t.Error("peerIsVisible mutated peer.Port")
	}
	if peer.Left != leftBefore {
		t.Error("peerIsVisible mutated peer.Left")
	}
	if peer.InvalidIP != invalidIPBefore {
		t.Error("peerIsVisible mutated peer.InvalidIP")
	}
	if user.CanLeech.Load() != canLeechBefore {
		t.Error("peerIsVisible mutated user.CanLeech")
	}
	if !result {
		t.Error("expected peerIsVisible=true for seeder with valid IP and CanLeech=true")
	}
}

// TestGUC_CollectVisiblePeers_NoMutation (Church) verifies that
// collectVisiblePeers does not modify the source PeerList (size and entries
// remain unchanged after the call).
func TestGUC_CollectVisiblePeers_NoMutation(t *testing.T) {
	pl := NewPeerList()
	pl.Set("a", &Peer{UserID: UserID(1), Visible: true})
	pl.Set("b", &Peer{UserID: UserID(2), Visible: false})
	pl.Set("c", &Peer{UserID: UserID(3), Visible: true})

	sizeBefore := pl.Size()
	_ = collectVisiblePeers(pl, UserID(0))
	sizeAfter := pl.Size()

	if sizeAfter != sizeBefore {
		t.Errorf("collectVisiblePeers mutated PeerList size: before=%d after=%d", sizeBefore, sizeAfter)
	}
	for _, key := range []string{"a", "b", "c"} {
		if _, ok := pl.Get(key); !ok {
			t.Errorf("collectVisiblePeers deleted entry %q from PeerList", key)
		}
	}
}

// TestGUC_CompactIPPort_Deterministic (Church) verifies that CompactIPPort is
// a deterministic function: identical inputs always produce identical byte slices.
func TestGUC_CompactIPPort_Deterministic(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")
	port := uint16(51413)

	first := CompactIPPort(ip, port)
	if len(first) != 6 {
		t.Fatalf("expected 6 bytes for IPv4, got %d", len(first))
	}
	for i := 1; i <= 9; i++ {
		got := CompactIPPort(ip, port)
		if len(got) != 6 {
			t.Fatalf("call %d: expected 6 bytes, got %d", i, len(got))
		}
		for j := 0; j < 6; j++ {
			if got[j] != first[j] {
				t.Errorf("call %d: byte[%d] changed: %d vs %d", i, j, first[j], got[j])
			}
		}
	}
}

// TestGUC_ValidPublicIPSeeder_AlwaysVisible (Church) verifies that any seeder
// (Left=0) with a valid public IPv4 and CanLeech=true is always Visible=true,
// regardless of which specific public address is used.
func TestGUC_ValidPublicIPSeeder_AlwaysVisible(t *testing.T) {
	publicIPs := []string{
		"1.1.1.1",
		"8.8.4.4",
		"203.0.113.10",
		"198.51.100.5",
	}

	for _, ipStr := range publicIPs {
		ipStr := ipStr
		t.Run(ipStr, func(t *testing.T) {
			w := newTestFixture().worker
			w.Config.AllowPrivateIPs = false

			if _, err := pvAnnounce(w, testPeerID, net.ParseIP(ipStr), testPort, 0, "started"); err != nil {
				t.Fatalf("announce failed: %v", err)
			}
			user, _ := w.Users.Get(testPasskey)
			peer := fetchPeer(w, testPeerID, user.ID)
			if peer == nil {
				t.Fatal("peer not found")
			}
			if !peer.Visible {
				t.Errorf("expected Visible=true for public-IP seeder %s", ipStr)
			}
		})
	}
}

// ── Gödel: formal consistency / invariant preservation ──────────────────────

// TestGUC_InvalidIP_StickyOnReAnnounceWithSamePrivateIP (Gödel) verifies
// the consistency invariant that once InvalidIP=true is established, a
// subsequent announce arriving with the same private IP preserves InvalidIP=true
// and Visible=false — there is no path that restores visibility without a
// genuinely public IP.
func TestGUC_InvalidIP_StickyOnReAnnounceWithSamePrivateIP(t *testing.T) {
	w := newWorkerPrivateBlock()
	privateIP := net.ParseIP("10.0.0.1")

	// Establish InvalidIP=true
	if _, err := pvAnnounce(w, testPeerID, privateIP, testPort, 500, "started"); err != nil {
		t.Fatalf("first announce failed: %v", err)
	}
	user, _ := w.Users.Get(testPasskey)
	peer := fetchPeer(w, testPeerID, user.ID)
	if peer == nil || !peer.InvalidIP {
		t.Fatal("precondition: peer should have InvalidIP=true after first announce")
	}

	// Re-announce with the same private IP
	if _, err := pvAnnounce(w, testPeerID, privateIP, testPort, 500, ""); err != nil {
		t.Fatalf("second announce failed: %v", err)
	}
	peer = fetchPeer(w, testPeerID, user.ID)
	if peer == nil {
		t.Fatal("peer not found after re-announce")
	}
	if !peer.InvalidIP {
		t.Error("InvalidIP should remain true on re-announce with same private IP")
	}
	if peer.Visible {
		t.Error("Visible should remain false while InvalidIP=true")
	}
}

// TestGUC_VisibleAndInvalidIP_NeverCoexist (Gödel) verifies the mutual-exclusion
// invariant: after any announce, Visible=true and InvalidIP=true must never
// both be set on the same peer.
func TestGUC_VisibleAndInvalidIP_NeverCoexist(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		left int64
	}{
		{"private-leecher", "192.168.1.1", 500},
		{"private-seeder", "10.0.0.2", 0},
		{"public-leecher", "1.2.3.4", 500},
		{"public-seeder", "5.6.7.8", 0},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			w := newWorkerPrivateBlock()
			ip := net.ParseIP(tc.ip)
			if _, err := pvAnnounce(w, testPeerID, ip, testPort, tc.left, "started"); err != nil {
				t.Fatalf("announce failed: %v", err)
			}
			user, _ := w.Users.Get(testPasskey)
			peer := fetchPeer(w, testPeerID, user.ID)
			if peer == nil {
				t.Fatal("peer not found")
			}
			if peer.Visible && peer.InvalidIP {
				t.Errorf("impossible state: Visible=true AND InvalidIP=true simultaneously for %s", tc.name)
			}
		})
	}
}

// TestGUC_InvisiblePeer_NeverInCompactOutput (Gödel) verifies the output
// invariant: the bytes of an invisible peer's IPPort are never present in
// the compact peer list returned by collectVisiblePeers.
func TestGUC_InvisiblePeer_NeverInCompactOutput(t *testing.T) {
	pl := NewPeerList()

	visiblePeer := &Peer{
		UserID:  UserID(50),
		Visible: true,
		Port:    7000,
		IP:      net.ParseIP("5.5.5.5"),
		IPPort:  CompactIPPort(net.ParseIP("5.5.5.5"), 7000),
		Left:    0,
	}
	invisiblePeer := &Peer{
		UserID:    UserID(51),
		Visible:   false,
		InvalidIP: true,
		Port:      7001,
		IP:        net.ParseIP("6.6.6.6"),
		IPPort:    CompactIPPort(net.ParseIP("6.6.6.6"), 7001),
		Left:      0,
	}
	pl.Set("vpeer", visiblePeer)
	pl.Set("ipeer", invisiblePeer)

	results := collectVisiblePeers(pl, UserID(0))
	for _, p := range results {
		if p == invisiblePeer {
			t.Error("invisible peer object was returned by collectVisiblePeers")
		}
		if p.IP != nil && p.IP.Equal(net.ParseIP("6.6.6.6")) {
			t.Error("invisible peer IP 6.6.6.6 appeared in collectVisiblePeers output")
		}
	}
	if len(results) != 1 {
		t.Errorf("expected exactly 1 visible peer, got %d", len(results))
	}
}

// TestGUC_StoppedPeer_CountDecrement (Gödel) verifies the strict arithmetic
// invariant: torrent.Seeders.Size() after a stop equals Size() before minus 1.
func TestGUC_StoppedPeer_CountDecrement(t *testing.T) {
	w := newTestFixture().worker
	ip := net.ParseIP(testIP)

	if _, err := pvAnnounce(w, testPeerID, ip, testPort, 0, "started"); err != nil {
		t.Fatalf("start announce failed: %v", err)
	}
	torrent, _ := w.Torrents.Get(testInfoHash)
	sizeBefore := torrent.Seeders.Size()
	if sizeBefore == 0 {
		t.Fatal("precondition: seeders must be non-empty before stop")
	}

	if _, err := pvAnnounce(w, testPeerID, ip, testPort, 0, "stopped"); err != nil {
		t.Fatalf("stop announce failed: %v", err)
	}
	sizeAfter := torrent.Seeders.Size()
	if sizeAfter != sizeBefore-1 {
		t.Errorf("seeder count should be exactly sizeBefore-1: before=%d after=%d", sizeBefore, sizeAfter)
	}
}

// TestGUC_LeecherCannotLeech_NotVisible (Gödel) verifies the consistency
// invariant that peerIsVisible returns false for a leecher (Left>0) whose user
// has CanLeech=false, preserving the rule that non-leeching users are excluded
// from visible peer sets.
func TestGUC_LeecherCannotLeech_NotVisible(t *testing.T) {
	w := newTestFixture().worker
	torrent, _ := w.Torrents.Get(testInfoHash)

	noLeechUser := NewUser(UserID(99), false, false)
	w.Users.Set("noleech99", noLeechUser)

	peer := &Peer{
		UserID:    UserID(99),
		Left:      500,
		InvalidIP: false,
		IP:        net.ParseIP("1.2.3.4"),
		Port:      6881,
		IPPort:    CompactIPPort(net.ParseIP("1.2.3.4"), 6881),
	}
	key := PeerKeyPrime([]byte(testPeerID), noLeechUser.ID, torrent.ID)
	torrent.Leechers.Set(key, peer)

	// peerIsVisible: (Left==0 || CanLeech) && !InvalidIP = (false || false) && true = false
	peer.Visible = w.peerIsVisible(noLeechUser, peer)

	if peer.Visible {
		t.Error("expected Visible=false for leecher with CanLeech=false and Left>0")
	}
}
