package tracker

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Round-trip tests exercise the full handleRequest dispatch path, verifying
// that the HTTP parsing layer, passkey/password auth, and response encoding
// all work together correctly for announce, scrape, and update endpoints.

// ── Announce round-trip ───────────────────────────────────────────────────────

func TestRoundTrip_Announce_ValidLeecher(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 1024)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	if strings.Contains(body, "failure reason") {
		t.Fatalf("unexpected failure: %s", body)
	}
	if !strings.Contains(body, "interval") {
		t.Errorf("announce response missing interval bencode key: %s", body)
	}
	if !strings.Contains(body, "peers") {
		t.Errorf("announce response missing peers bencode key: %s", body)
	}

	// Stats counter must be incremented
	if f.worker.Stats.Announcements.Load() < 1 {
		t.Error("Announcements counter not incremented")
	}

	// Torrent must have one leecher
	tor, _ := f.worker.Torrents.Get(testInfoHash)
	if tor.Leechers.Size() != 1 {
		t.Errorf("leecher count = %d, want 1", tor.Leechers.Size())
	}
}

func TestRoundTrip_Announce_ValidSeeder(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 0)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	if strings.Contains(body, "failure reason") {
		t.Fatalf("unexpected failure: %s", body)
	}

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	if tor.Seeders.Size() != 1 {
		t.Errorf("seeder count = %d, want 1", tor.Seeders.Size())
	}
	if tor.Leechers.Size() != 0 {
		t.Errorf("leecher count = %d, want 0", tor.Leechers.Size())
	}
}

func TestRoundTrip_Announce_UnknownPasskey(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", testInfoHash, testPeerID, "started", 0, 0, 1024)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(raw)
	if !strings.Contains(body, "Passkey not found") {
		t.Errorf("expected 'Passkey not found', got: %s", body)
	}
}

func TestRoundTrip_Announce_UnknownTorrent(t *testing.T) {
	f := newTestFixture()
	unknownHash := strings.Repeat("\xAB", 20)
	req := buildAnnounceURL(testPasskey, unknownHash, testPeerID, "started", 0, 0, 1024)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(raw)
	if !strings.Contains(body, "failure reason") {
		t.Errorf("expected failure reason for unknown torrent: %s", body)
	}
}

func TestRoundTrip_Announce_Stopped_RemovesPeer(t *testing.T) {
	f := newTestFixture()
	ip := net.ParseIP(testIP)

	// Join
	req1 := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 512)
	if raw, _ := f.server.handleRequest(req1, ip); strings.Contains(httpBody(raw), "failure reason") {
		t.Fatal("start announce failed")
	}

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	if tor.Leechers.Size() != 1 {
		t.Fatal("leecher not added on start")
	}

	// Stop
	req2 := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "stopped", 0, 0, 512)
	if raw, _ := f.server.handleRequest(req2, ip); strings.Contains(httpBody(raw), "failure reason") {
		t.Fatal("stop announce failed")
	}

	if tor.Leechers.Size() != 0 {
		t.Errorf("leecher count = %d after stop, want 0", tor.Leechers.Size())
	}
}

func TestRoundTrip_Announce_Completed_Snatch(t *testing.T) {
	f := newTestFixture()
	ip := net.ParseIP(testIP)

	req1 := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 2048)
	f.server.handleRequest(req1, ip)

	req2 := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "completed", 500, 2048, 0)
	raw, _ := f.server.handleRequest(req2, ip)
	if strings.Contains(httpBody(raw), "failure reason") {
		t.Fatalf("completed announce failed: %s", httpBody(raw))
	}

	if len(f.db.Snatches) < 1 {
		t.Error("expected snatch record on completed event")
	}

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	if tor.Seeders.Size() != 1 {
		t.Errorf("seeder count = %d after complete, want 1", tor.Seeders.Size())
	}
}

func TestRoundTrip_Announce_LeecherReceivesSeeder(t *testing.T) {
	f := newTestFixture()

	// Seeder uses testPasskey2 so it is a different user (UserID=2)
	seederReq := buildAnnounceURL(testPasskey2, testInfoHash, testPeerID2, "started", 100, 0, 0)
	f.server.handleRequest(seederReq, net.ParseIP("192.168.1.1"))

	// Leecher announces
	leecherReq := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 1024)
	raw, _ := f.server.handleRequest(leecherReq, net.ParseIP(testIP))
	body := httpBody(raw)

	// The seeder is at 192.168.1.1:6881 → 6 compact bytes → "6:" in response
	if !strings.Contains(body, "6:") {
		t.Errorf("leecher should receive seeder peer bytes (6:), got: %s", body)
	}
}

func TestRoundTrip_Announce_ShortPasskeyRejected(t *testing.T) {
	f := newTestFixture()
	// "short" is only 5 chars — handleRequest must reject it as Malformed
	req, err := http.NewRequest("GET", "/short/announce?info_hash=aaaabbbbccccddddeeee&peer_id=ppppqqqqrrrrsssstttt&port=6881&compact=1", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	if !strings.Contains(string(raw), "Malformed") {
		t.Errorf("short passkey should be rejected: %s", string(raw))
	}
}

func TestRoundTrip_Announce_BearerTokenAuth_NotApplicable(t *testing.T) {
	// Announce uses passkey-in-path, not Bearer; a valid passkey still works
	// regardless of the Authorization header.
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 1024)
	req.Header.Set("Authorization", "Bearer wrongpassword")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)
	if strings.Contains(body, "failure reason") {
		t.Errorf("announce should use passkey path, not Bearer header: %s", body)
	}
}

// ── Update round-trip ─────────────────────────────────────────────────────────

func TestRoundTrip_Update_AddTorrent(t *testing.T) {
	f := newTestFixture()
	req := buildUpdateURL(sitePass, "add_torrent", url.Values{
		"torrent_id": {"99"},
		"info_hash":  {"newhash000000000000"},
		"free_type":  {"0"},
	})
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("update response not valid JSON: %v — body: %s", err, body)
	}
	if resp["success"] != true {
		t.Errorf("add_torrent success = %v, want true", resp["success"])
	}

	if _, ok := f.worker.Torrents.Get("newhash000000000000"); !ok {
		t.Error("torrent not registered after add_torrent")
	}
}

func TestRoundTrip_Update_WrongPassword(t *testing.T) {
	f := newTestFixture()
	req := buildUpdateURL(strings.Repeat("z", 32), "add_torrent", url.Values{
		"torrent_id": {"1"},
		"info_hash":  {"h"},
	})
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	if !strings.Contains(string(raw), "Authentication failure") {
		t.Errorf("wrong password should yield Authentication failure: %s", string(raw))
	}
}

func TestRoundTrip_Update_BearerToken(t *testing.T) {
	f := newTestFixture()
	// Use wrong passkey in path but correct password in Bearer header
	req := buildUpdateURL(strings.Repeat("y", 32), "add_torrent", url.Values{
		"torrent_id": {"77"},
		"info_hash":  {"bearerauthtest000000"},
	})
	req.Header.Set("Authorization", "Bearer "+sitePass)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("Bearer update not valid JSON: %v — body: %s", err, body)
	}
	if resp["success"] != true {
		t.Errorf("Bearer auth failed: %v", resp)
	}
}

func TestRoundTrip_Update_DeleteTorrent(t *testing.T) {
	f := newTestFixture()

	// Verify torrent is initially present
	if _, ok := f.worker.Torrents.Get(testInfoHash); !ok {
		t.Fatal("test torrent missing from fixture")
	}

	req := buildUpdateURL(sitePass, "delete_torrent", url.Values{
		"info_hash": {testInfoHash},
	})
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("delete_torrent response not JSON: %v — body: %s", err, body)
	}
	if resp["success"] != true {
		t.Errorf("delete_torrent failed: %v", resp)
	}
	if _, ok := f.worker.Torrents.Get(testInfoHash); ok {
		t.Error("torrent still present after delete_torrent")
	}
}

func TestRoundTrip_Update_AddUser(t *testing.T) {
	f := newTestFixture()
	newPasskey := "newuser00000000000000000000000000"[:32]
	req := buildUpdateURL(sitePass, "add_user", url.Values{
		"user_id":    {"500"},
		"passkey":    {newPasskey},
		"can_leech":  {"1"},
		"protect_ip": {"0"},
	})
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("add_user response not JSON: %v — body: %s", err, body)
	}
	if resp["success"] != true {
		t.Errorf("add_user failed: %v", resp)
	}

	u, ok := f.worker.Users.Get(newPasskey)
	if !ok {
		t.Fatal("user not found after add_user")
	}
	if u.ID != 500 {
		t.Errorf("user ID = %d, want 500", u.ID)
	}
}

func TestRoundTrip_Update_UnknownAction_JSON(t *testing.T) {
	f := newTestFixture()
	req := buildUpdateURL(sitePass, "do_the_thing", url.Values{})
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("error response not valid JSON: %v — body: %s", err, body)
	}
	if resp["success"] != false {
		t.Errorf("unknown action should return success=false, got: %v", resp)
	}
	if resp["error"] == nil {
		t.Error("unknown action should include error field")
	}
}

// ── Announce stats accounting ─────────────────────────────────────────────────

func TestRoundTrip_Stats_AnnouncementsIncrement(t *testing.T) {
	f := newTestFixture()
	before := f.worker.Stats.Announcements.Load()

	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 100)
	f.server.handleRequest(req, net.ParseIP(testIP))

	after := f.worker.Stats.Announcements.Load()
	if after != before+1 {
		t.Errorf("Announcements = %d, want %d", after, before+1)
	}
}

func TestRoundTrip_Stats_SuccessfulAnnouncementsIncrement(t *testing.T) {
	f := newTestFixture()
	before := f.worker.Stats.SuccAnnouncements.Load()

	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 100)
	f.server.handleRequest(req, net.ParseIP(testIP))

	after := f.worker.Stats.SuccAnnouncements.Load()
	if after != before+1 {
		t.Errorf("SuccAnnouncements = %d, want %d", after, before+1)
	}
}

func TestRoundTrip_Stats_FailedAnnounceNoSuccessIncrement(t *testing.T) {
	f := newTestFixture()
	before := f.worker.Stats.SuccAnnouncements.Load()

	// Unknown passkey → passkey error, no successful announce
	req := buildAnnounceURL("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", testInfoHash, testPeerID, "started", 0, 0, 100)
	f.server.handleRequest(req, net.ParseIP(testIP))

	after := f.worker.Stats.SuccAnnouncements.Load()
	if after != before {
		t.Errorf("SuccAnnouncements incremented on failed announce: %d → %d", before, after)
	}
}

// ── X-Forwarded-For header ────────────────────────────────────────────────────

func TestRoundTrip_Announce_XForwardedFor_SingleIP(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 100)
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	// Pass nil clientIP so the XFF fallback in handleAnnounce is triggered.
	raw, _ := f.server.handleRequest(req, nil)
	body := httpBody(raw)
	if strings.Contains(body, "failure reason") {
		t.Errorf("XFF announce failed: %s", body)
	}

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	found := false
	tor.Leechers.ForEach(func(_ string, p *Peer) bool {
		if p.IP.Equal(net.ParseIP("203.0.113.99")) {
			found = true
		}
		return true
	})
	if !found {
		t.Error("peer IP not set from X-Forwarded-For header")
	}
}

func TestRoundTrip_Announce_XForwardedFor_MultipleIPs(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 100)
	req.Header.Set("X-Forwarded-For", "203.0.113.55, 10.1.1.1, 172.16.0.1")

	// Pass nil clientIP so the XFF fallback fires; the first IP in the chain is used.
	raw, _ := f.server.handleRequest(req, nil)
	if strings.Contains(httpBody(raw), "failure reason") {
		t.Errorf("multi-XFF announce failed: %s", httpBody(raw))
	}

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	found := false
	tor.Leechers.ForEach(func(_ string, p *Peer) bool {
		if p.IP.Equal(net.ParseIP("203.0.113.55")) {
			found = true
		}
		return true
	})
	if !found {
		t.Error("peer IP should be first XFF entry (203.0.113.55)")
	}
}
