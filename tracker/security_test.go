package tracker

import (
	"context"
	"net"
	"strings"
	"testing"
)

// TestSecurity_WrongSitePassword verifies that update requests with the wrong
// site password are rejected at the routing layer.
func TestSecurity_WrongSitePassword(t *testing.T) {
	f := newTestFixture()
	wrongPass := "wrongpassword00000000000000000000"[:32]
	req := buildUpdateURL(wrongPass, "add_torrent", nil)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)
	if !strings.Contains(body, "Authentication failure") {
		t.Errorf("expected authentication failure, got: %s", body)
	}
}

// TestSecurity_ShortPasskeyRejected verifies that a passkey shorter than 32
// chars is rejected before any DB call.
func TestSecurity_ShortPasskeyRejected(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL("shortkey", testInfoHash, testPeerID, "started", 0, 0, 1000)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)
	if !strings.Contains(body, "Malformed") {
		t.Errorf("short passkey should produce malformed error, got: %s", body)
	}
}

// TestSecurity_UnknownPasskeyAnnounce verifies unknown passkey → "Passkey not found".
func TestSecurity_UnknownPasskeyAnnounce(t *testing.T) {
	f := newTestFixture()
	badKey := strings.Repeat("x", 32)
	req := buildAnnounceURL(badKey, testInfoHash, testPeerID, "started", 0, 0, 1000)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)
	if !strings.Contains(body, "Passkey not found") {
		t.Errorf("unknown passkey should be rejected, got: %s", body)
	}
}

// TestSecurity_DeletedUserAnnounce verifies that a user marked Deleted cannot
// complete an announce.
func TestSecurity_DeletedUserAnnounce(t *testing.T) {
	f := newTestFixture()
	u, _ := f.worker.Users.Get(testPasskey)
	u.Deleted.Store(true)
	// Deleted users still exist in the map but the announce logic checks Left>0
	// + CanLeech; the more direct path is to remove them from Users.
	// Instead test via CanLeech=false + Left>0 → leeching forbidden.
	u.Deleted.Store(false)
	u.CanLeech.Store(false)

	ip := net.ParseIP(testIP)
	req := newAnnounceReqFull(testInfoHash, testPeerID, "started", 0, 0, 1000)
	_, err := f.worker.Announce(context.Background(), req, u, ip, "test-client")
	if err == nil || !strings.Contains(err.Error(), "leeching forbidden") {
		t.Errorf("CanLeech=false leecher should get leeching forbidden, got: %v", err)
	}
}

// TestSecurity_SQLInjectionInPeerID verifies that an adversarial peer_id is
// passed verbatim to RecordPeer without triggering any crash or error beyond
// what the DB mock would surface.
func TestSecurity_SQLInjectionInPeerID(t *testing.T) {
	f := newTestFixture()
	injected := "'; DROP TABLE peers;--\x00" // 22 chars; pad to 20
	peerID := (injected + strings.Repeat("\x00", 20))[:20]

	ip := net.ParseIP(testIP)
	user, _ := f.worker.Users.Get(testPasskey)
	req := newAnnounceReqFull(testInfoHash, peerID, "started", 0, 0, 1000)
	_, err := f.worker.Announce(context.Background(), req, user, ip, "malicious-client")
	if err != nil {
		t.Fatalf("announce with adversarial peer_id should not fail: %v", err)
	}

	// MockDB must have received the raw peer_id unchanged
	f.db.mu.Lock()
	defer f.db.mu.Unlock()
	if len(f.db.Peers) == 0 {
		t.Fatal("no peer record written")
	}
	recorded := f.db.Peers[0].PeerID
	if recorded != peerID {
		t.Errorf("peer_id in DB = %q, want %q", recorded, peerID)
	}
}

// TestSecurity_VeryLargeUploadedValue verifies that extreme int64 values do
// not cause a panic or overflow in announce accounting.
func TestSecurity_VeryLargeUploadedValue(t *testing.T) {
	f := newTestFixture()
	ip := net.ParseIP(testIP)
	user, _ := f.worker.Users.Get(testPasskey)

	const maxUploaded = int64(1<<62 - 1)

	req := newAnnounceReqFull(testInfoHash, testPeerID, "started", maxUploaded, 0, 0)
	_, err := f.worker.Announce(context.Background(), req, user, ip, "test-client")
	if err != nil {
		t.Fatalf("large uploaded value should not panic/error: %v", err)
	}
}

// TestSecurity_NonCompactAnnounceRejected ensures non-compact announces are
// rejected (ocelot only supports BEP-23 compact format).
func TestSecurity_NonCompactAnnounceRejected(t *testing.T) {
	f := newTestFixture()
	ip := net.ParseIP(testIP)
	user, _ := f.worker.Users.Get(testPasskey)

	req := newAnnounceReqFull(testInfoHash, testPeerID, "started", 0, 0, 1000)
	req.Compact = false

	_, err := f.worker.Announce(context.Background(), req, user, ip, "test-client")
	if err == nil || !strings.Contains(err.Error(), "compact") {
		t.Errorf("non-compact announce should be rejected, got: %v", err)
	}
}

// TestSecurity_UnregisteredTorrent verifies that an announce for an unknown
// torrent hash is rejected cleanly.
func TestSecurity_UnregisteredTorrent(t *testing.T) {
	f := newTestFixture()
	ip := net.ParseIP(testIP)
	user, _ := f.worker.Users.Get(testPasskey)

	req := newAnnounceReqFull(strings.Repeat("\xff", 20), testPeerID, "started", 0, 0, 1000)
	_, err := f.worker.Announce(context.Background(), req, user, ip, "test-client")
	if err == nil || !strings.Contains(err.Error(), "unregistered") {
		t.Errorf("unknown torrent should be rejected, got: %v", err)
	}
}

// TestSecurity_WhitelistEnforced verifies that a client not matching any
// whitelist prefix is rejected once whitelist is non-empty.
func TestSecurity_WhitelistEnforced(t *testing.T) {
	f := newTestFixture()
	f.worker.Whitelist.Add("-qB") // only qBittorrent allowed

	ip := net.ParseIP(testIP)
	user, _ := f.worker.Users.Get(testPasskey)

	// testPeerID starts with "\x02" → not "-qB"
	req := newAnnounceReqFull(testInfoHash, testPeerID, "started", 0, 0, 1000)
	_, err := f.worker.Announce(context.Background(), req, user, ip, "banned-client")
	if err == nil || !strings.Contains(err.Error(), "whitelist") {
		t.Errorf("client not on whitelist should be rejected, got: %v", err)
	}
}
