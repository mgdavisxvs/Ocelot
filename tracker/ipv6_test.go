package tracker

import (
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// announceQuery builds the query string a client would send.
func announceQuery(infoHash, peerSuffix, ip string) string {
	params := url.Values{}
	params.Set("info_hash", infoHash)
	params.Set("peer_id", string(testPeerID(peerSuffix)))
	params.Set("port", "6881")
	params.Set("left", "1024")
	params.Set("compact", "1")
	params.Set("event", "started")
	params.Set("ip", ip)
	return params.Encode()
}

func TestCompactIPPortIPv4(t *testing.T) {
	compact := CompactIPPort(net.ParseIP("192.0.2.10"), 51413)

	if len(compact) != CompactIPv4Len {
		t.Fatalf("length = %d, want %d", len(compact), CompactIPv4Len)
	}

	if got := net.IPv4(compact[0], compact[1], compact[2], compact[3]); !got.Equal(net.ParseIP("192.0.2.10")) {
		t.Errorf("decoded IP = %s, want 192.0.2.10", got)
	}
	if port := binary.BigEndian.Uint16(compact[4:6]); port != 51413 {
		t.Errorf("decoded port = %d, want 51413", port)
	}
}

func TestCompactIPPortIPv6(t *testing.T) {
	compact := CompactIPPort(net.ParseIP("2001:db8::1"), 6881)

	if len(compact) != CompactIPv6Len {
		t.Fatalf("length = %d, want %d", len(compact), CompactIPv6Len)
	}

	if got := net.IP(compact[0:16]); !got.Equal(net.ParseIP("2001:db8::1")) {
		t.Errorf("decoded IP = %s, want 2001:db8::1", got)
	}
	if port := binary.BigEndian.Uint16(compact[16:18]); port != 6881 {
		t.Errorf("decoded port = %d, want 6881", port)
	}
}

func TestCompactIPPortTreatsMappedAddressAsIPv4(t *testing.T) {
	// ::ffff:192.0.2.10 is an IPv4 address in IPv6 clothing and belongs in the
	// 6-byte list, not peers6.
	compact := CompactIPPort(net.ParseIP("::ffff:192.0.2.10"), 6881)

	if len(compact) != CompactIPv4Len {
		t.Fatalf("length = %d, want %d for an IPv4-mapped address", len(compact), CompactIPv4Len)
	}
}

func TestCompactIPPortRejectsUnusableAddress(t *testing.T) {
	if compact := CompactIPPort(nil, 6881); compact != nil {
		t.Errorf("CompactIPPort(nil) = %v, want nil", compact)
	}
}

func TestAnnounceAcceptsIPv6Peer(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")
	req.IP = net.ParseIP("2001:db8::1")

	resp, err := h.worker.Announce(req, user, req.IP, "qB")
	if err != nil {
		t.Fatalf("IPv6 announce failed: %v", err)
	}

	if resp.Warning != "" {
		t.Errorf("IPv6 announce produced a warning: %q", resp.Warning)
	}
	if h.torrent.Leechers.Size() != 1 {
		t.Fatalf("leechers = %d, want 1", h.torrent.Leechers.Size())
	}

	h.torrent.Leechers.ForEach(func(_ string, peer *Peer) bool {
		if peer.InvalidIP {
			t.Error("IPv6 peer was marked as having an invalid address")
		}
		if !peer.Visible {
			t.Error("IPv6 peer is invisible, so no other peer will ever receive it")
		}
		if len(peer.IPPort) != CompactIPv6Len {
			t.Errorf("compact length = %d, want %d", len(peer.IPPort), CompactIPv6Len)
		}
		return true
	})
}

func TestAnnounceReturnsIPv6PeerInPeers6(t *testing.T) {
	h := newTestHarness(t)
	seeder, _ := h.addUser(t, 1, true)
	leecher, _ := h.addUser(t, 2, true)

	seed := announceParams(h.infoHash, testPeerID("seed0001"), 51413, 0, "started")
	seed.IP = net.ParseIP("2001:db8::10")
	if _, err := h.worker.Announce(seed, seeder, seed.IP, "qB"); err != nil {
		t.Fatalf("IPv6 seeder announce failed: %v", err)
	}

	leech := announceParams(h.infoHash, testPeerID("peer0002"), 6881, 1<<30, "started")
	leech.IP = net.ParseIP("2001:db8::20")

	resp, err := h.worker.Announce(leech, leecher, leech.IP, "qB")
	if err != nil {
		t.Fatalf("IPv6 leecher announce failed: %v", err)
	}

	if len(resp.Peers) != 0 {
		t.Errorf("peers = %d bytes, want 0 — IPv6 peers do not belong in the IPv4 key", len(resp.Peers))
	}
	if len(resp.Peers6) != CompactIPv6Len {
		t.Fatalf("peers6 = %d bytes, want %d", len(resp.Peers6), CompactIPv6Len)
	}

	if got := net.IP(resp.Peers6[0:16]); !got.Equal(net.ParseIP("2001:db8::10")) {
		t.Errorf("peer IP = %s, want 2001:db8::10", got)
	}
	if port := binary.BigEndian.Uint16(resp.Peers6[16:18]); port != 51413 {
		t.Errorf("peer port = %d, want 51413", port)
	}
}

func TestAnnounceKeepsAddressFamiliesSeparate(t *testing.T) {
	h := newTestHarness(t)

	v4Seeder, _ := h.addUser(t, 1, true)
	v6Seeder, _ := h.addUser(t, 2, true)
	leecher, _ := h.addUser(t, 3, true)

	v4 := announceParams(h.infoHash, testPeerID("seed0001"), 51413, 0, "started")
	v4.IP = net.ParseIP("192.0.2.10")
	if _, err := h.worker.Announce(v4, v4Seeder, v4.IP, "qB"); err != nil {
		t.Fatalf("IPv4 seeder announce failed: %v", err)
	}

	v6 := announceParams(h.infoHash, testPeerID("seed0002"), 51414, 0, "started")
	v6.IP = net.ParseIP("2001:db8::10")
	if _, err := h.worker.Announce(v6, v6Seeder, v6.IP, "qB"); err != nil {
		t.Fatalf("IPv6 seeder announce failed: %v", err)
	}

	leech := announceParams(h.infoHash, testPeerID("peer0003"), 6881, 1<<30, "started")
	resp, err := h.worker.Announce(leech, leecher, net.ParseIP("10.0.0.3"), "qB")
	if err != nil {
		t.Fatalf("leecher announce failed: %v", err)
	}

	if len(resp.Peers) != CompactIPv4Len {
		t.Errorf("peers = %d bytes, want %d (one IPv4 peer)", len(resp.Peers), CompactIPv4Len)
	}
	if len(resp.Peers6) != CompactIPv6Len {
		t.Errorf("peers6 = %d bytes, want %d (one IPv6 peer)", len(resp.Peers6), CompactIPv6Len)
	}

	if got := net.IPv4(resp.Peers[0], resp.Peers[1], resp.Peers[2], resp.Peers[3]); !got.Equal(net.ParseIP("192.0.2.10")) {
		t.Errorf("IPv4 peer = %s, want 192.0.2.10", got)
	}
	if got := net.IP(resp.Peers6[0:16]); !got.Equal(net.ParseIP("2001:db8::10")) {
		t.Errorf("IPv6 peer = %s, want 2001:db8::10", got)
	}
}

func TestAnnounceNumWantCountsBothFamilies(t *testing.T) {
	h := newTestHarness(t)
	h.worker.Config.NumWantLimit = 2

	// Four seeders, half on each address family.
	for i := 1; i <= 4; i++ {
		seeder, _ := h.addUser(t, UserID(i), true)
		req := announceParams(h.infoHash, testPeerID(string(rune('a'+i))+"eed001"), uint16(51410+i), 0, "started")
		if i%2 == 0 {
			req.IP = net.ParseIP("192.0.2." + string(rune('0'+i)))
		} else {
			req.IP = net.ParseIP("2001:db8::" + string(rune('0'+i)))
		}
		if _, err := h.worker.Announce(req, seeder, req.IP, "qB"); err != nil {
			t.Fatalf("seeder %d announce failed: %v", i, err)
		}
	}

	leecher, _ := h.addUser(t, 99, true)
	leech := announceParams(h.infoHash, testPeerID("peer0099"), 6881, 1<<30, "started")

	resp, err := h.worker.Announce(leech, leecher, net.ParseIP("10.0.0.99"), "qB")
	if err != nil {
		t.Fatalf("leecher announce failed: %v", err)
	}

	total := len(resp.Peers)/CompactIPv4Len + len(resp.Peers6)/CompactIPv6Len
	if total > 2 {
		t.Errorf("returned %d peers across both families, want at most 2", total)
	}
}

func TestBencodedResponseIncludesPeers6(t *testing.T) {
	h := newTestHarness(t)
	seeder, _ := h.addUser(t, 1, true)
	leecher, passkey := h.addUser(t, 2, true)

	seed := announceParams(h.infoHash, testPeerID("seed0001"), 51413, 0, "started")
	seed.IP = net.ParseIP("2001:db8::10")
	if _, err := h.worker.Announce(seed, seeder, seed.IP, "qB"); err != nil {
		t.Fatalf("seeder announce failed: %v", err)
	}
	_ = leecher

	server := newTestServer(t, h)

	req := httptest.NewRequest(http.MethodGet, "/"+passkey+"/announce?"+announceQuery(h.infoHash, "peer0002", "2001:db8::20"), nil)
	body := string(server.handleAnnounce(req, passkey, net.ParseIP("2001:db8::20"), true))

	if !strings.Contains(body, "6:peers6") {
		t.Errorf("response is missing the peers6 key: %q", body)
	}

	// Bencode dictionaries are ordered by key: peers must precede peers6.
	peersAt := strings.Index(body, "5:peers")
	peers6At := strings.Index(body, "6:peers6")
	if peersAt < 0 || peers6At < 0 || peersAt > peers6At {
		t.Errorf("peers/peers6 keys are out of bencode order: %q", body)
	}
}

func TestBencodedResponseOmitsEmptyPeers6(t *testing.T) {
	h := newTestHarness(t)
	_, passkey := h.addUser(t, 1, true)

	server := newTestServer(t, h)

	req := httptest.NewRequest(http.MethodGet, "/"+passkey+"/announce?"+announceQuery(h.infoHash, "peer0001", "10.0.0.1"), nil)
	body := string(server.handleAnnounce(req, passkey, net.ParseIP("10.0.0.1"), true))

	if strings.Contains(body, "peers6") {
		t.Errorf("peers6 should be omitted when there are no IPv6 peers: %q", body)
	}
}
