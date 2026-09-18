package tracker

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"
)

// ── Mock database for announce tests ─────────────────────────────────────────

type mockDB struct {
	peerRecords     int
	peerLightRecs   int
	userStatRecs    int
	torrentRecs     int
	snatchRecs      int
	tokenRecs       int
	torrentHashRecs int
	userPasskeyRecs int
	wlAdds          int
	wlRemoves       int
}

func (m *mockDB) RecordPeer(_ UserID, _ TorrentID, _ int, _, _, _, _, _, _ int64, _, _ uint32, _, _, _ string) error {
	m.peerRecords++
	return nil
}
func (m *mockDB) RecordPeerLight(_ UserID, _ TorrentID, _, _ uint32, _ string) error {
	m.peerLightRecs++
	return nil
}
func (m *mockDB) RecordUserStats(_ UserID, _, _ int64) error {
	m.userStatRecs++
	return nil
}
func (m *mockDB) RecordTorrent(_ TorrentID, _, _ uint32, _ int, _ int64) error {
	m.torrentRecs++
	return nil
}
func (m *mockDB) RecordSnatch(_ UserID, _ TorrentID, _ time.Time, _ string) error {
	m.snatchRecs++
	return nil
}
func (m *mockDB) RecordToken(_ UserID, _ TorrentID, _ int64) error {
	m.tokenRecs++
	return nil
}
func (m *mockDB) RecordTorrentHash(_ TorrentID, _ string) error {
	m.torrentHashRecs++
	return nil
}
func (m *mockDB) RecordUserPasskey(_ UserID, _ string, _, _ bool) error {
	m.userPasskeyRecs++
	return nil
}
func (m *mockDB) AddWhitelistEntry(_ string) error             { m.wlAdds++; return nil }
func (m *mockDB) RemoveWhitelistEntry(_ string) error          { m.wlRemoves++; return nil }
func (m *mockDB) DeleteToken(_ UserID, _ TorrentID) error      { return nil }
func (m *mockDB) LoadTorrents() ([]torrentLoadRow, error) {
	return nil, nil
}
func (m *mockDB) LoadUsers() ([]userLoadRow, error) { return nil, nil }
func (m *mockDB) LoadWhitelist() ([]string, error)  { return nil, nil }
func (m *mockDB) LoadTokens() (map[string][]UserID, error) {
	return nil, nil
}
func (m *mockDB) CheckpointWAL() error { return nil }
func (m *mockDB) CheckRotation() error { return nil }
func (m *mockDB) Close() error         { return nil }

// ── Mock SiteComm ─────────────────────────────────────────────────────────────

type mockSiteComm struct{ expired int }

func (m *mockSiteComm) ExpireToken(_ TorrentID, _ UserID)              { m.expired++ }
func (m *mockSiteComm) BanUser(_ int64) error                          { return nil }
func (m *mockSiteComm) UnbanUser(_ int64) error                        { return nil }
func (m *mockSiteComm) NotifyFreeleech(_ int64, _ int) error           { return nil }
func (m *mockSiteComm) ReportAnomaly(_ int64, _ float64) error         { return nil }
func (m *mockSiteComm) UpdateStats(_ int64, _ int64, _ int64) error    { return nil }

// ── Helper: build a minimal Worker ───────────────────────────────────────────

func newTestWorker() (*Worker, *mockDB, *mockSiteComm) {
	db := &mockDB{}
	sc := &mockSiteComm{}
	w := &Worker{
		Config: &Config{
			AnnounceInterval: 1800,
			NumWantLimit:     50,
			PeersTimeout:     7200,
		},
		DB:        db,
		SiteComm:  sc,
		Torrents:  NewTorrentList(),
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     &Stats{StartTime: time.Now()},
	}
	return w, db, sc
}

// ── Helper: build a standard announce request ─────────────────────────────────

func newAnnounceReq(event string, left int64) *AnnounceRequest {
	peerID := make([]byte, 20)
	copy(peerID, "-qB4test000000000000")
	return &AnnounceRequest{
		InfoHash:   "testhash000000000001",
		PeerID:     peerID,
		Port:       6881,
		Uploaded:   0,
		Downloaded: 0,
		Left:       left,
		Compact:    true,
		Event:      event,
		NumWant:    50,
	}
}

// ── ParseAnnounceParams ───────────────────────────────────────────────────────

func TestParseAnnounceParams_Valid(t *testing.T) {
	params := url.Values{
		"info_hash":  {"\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14"},
		"peer_id":    {"-qB40000000000000000"},
		"port":       {"6881"},
		"uploaded":   {"1048576"},
		"downloaded": {"2097152"},
		"left":       {"10000"},
		"compact":    {"1"},
		"event":      {"started"},
		"numwant":    {"30"},
	}
	req, err := ParseAnnounceParams(params, net.ParseIP("10.0.0.1"))
	if err != nil {
		t.Fatalf("ParseAnnounceParams: %v", err)
	}
	if req.Port != 6881 {
		t.Errorf("Port = %d, want 6881", req.Port)
	}
	if req.Uploaded != 1048576 {
		t.Errorf("Uploaded = %d", req.Uploaded)
	}
	if req.Downloaded != 2097152 {
		t.Errorf("Downloaded = %d", req.Downloaded)
	}
	if req.Left != 10000 {
		t.Errorf("Left = %d", req.Left)
	}
	if !req.Compact {
		t.Error("Compact should be true")
	}
	if req.Event != "started" {
		t.Errorf("Event = %q", req.Event)
	}
	if req.NumWant != 30 {
		t.Errorf("NumWant = %d", req.NumWant)
	}
}

func TestParseAnnounceParams_MissingInfoHash(t *testing.T) {
	params := url.Values{
		"peer_id": {"-qB40000000000000000"},
		"port":    {"6881"},
	}
	_, err := ParseAnnounceParams(params, nil)
	if err == nil {
		t.Error("expected error for missing info_hash")
	}
}

func TestParseAnnounceParams_MissingPeerID(t *testing.T) {
	params := url.Values{
		"info_hash": {"00000000000000000000"},
		"port":      {"6881"},
	}
	_, err := ParseAnnounceParams(params, nil)
	if err == nil {
		t.Error("expected error for missing peer_id")
	}
}

func TestParseAnnounceParams_MissingPort(t *testing.T) {
	params := url.Values{
		"info_hash": {"00000000000000000000"},
		"peer_id":   {"-qB40000000000000000"},
	}
	_, err := ParseAnnounceParams(params, nil)
	if err == nil {
		t.Error("expected error for missing port")
	}
}

func TestParseAnnounceParams_InvalidPort(t *testing.T) {
	params := url.Values{
		"info_hash": {"00000000000000000000"},
		"peer_id":   {"-qB40000000000000000"},
		"port":      {"notaport"},
	}
	_, err := ParseAnnounceParams(params, nil)
	if err == nil {
		t.Error("expected error for invalid port")
	}
}

func TestParseAnnounceParams_NegativeValues_ClampedToZero(t *testing.T) {
	params := url.Values{
		"info_hash":  {"00000000000000000000"},
		"peer_id":    {"-qB40000000000000000"},
		"port":       {"6881"},
		"uploaded":   {"-100"},
		"downloaded": {"-200"},
		"left":       {"-50"},
	}
	req, err := ParseAnnounceParams(params, nil)
	if err != nil {
		t.Fatalf("ParseAnnounceParams: %v", err)
	}
	if req.Uploaded != 0 || req.Downloaded != 0 || req.Left != 0 {
		t.Error("negative values should clamp to 0")
	}
}

func TestParseAnnounceParams_IPParam(t *testing.T) {
	params := url.Values{
		"info_hash": {"00000000000000000000"},
		"peer_id":   {"-qB40000000000000000"},
		"port":      {"6881"},
		"ip":        {"203.0.113.5"},
	}
	req, err := ParseAnnounceParams(params, net.ParseIP("10.0.0.1"))
	if err != nil {
		t.Fatalf("ParseAnnounceParams: %v", err)
	}
	if !req.IP.Equal(net.ParseIP("203.0.113.5")) {
		t.Errorf("IP = %v, want 203.0.113.5", req.IP)
	}
}

// ── Announce — rejection cases ────────────────────────────────────────────────

func TestAnnounce_RejectNonCompact(t *testing.T) {
	w, _, _ := newTestWorker()
	req := newAnnounceReq("started", 1000)
	req.Compact = false
	u := NewUser(1, true, false)
	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "")
	if err == nil {
		t.Error("expected error for non-compact announce")
	}
}

func TestAnnounce_RejectShortPeerID(t *testing.T) {
	w, _, _ := newTestWorker()
	req := newAnnounceReq("started", 1000)
	req.PeerID = []byte("tooshort")
	u := NewUser(1, true, false)
	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "")
	if err == nil {
		t.Error("expected error for short peer ID")
	}
}

func TestAnnounce_RejectNotWhitelisted(t *testing.T) {
	w, _, _ := newTestWorker()
	w.Whitelist.Add("-XX9") // only this prefix allowed
	req := newAnnounceReq("started", 1000)
	// PeerID starts with "-qB4", not "-XX9"
	u := NewUser(1, true, false)
	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "")
	if err == nil {
		t.Error("expected error for non-whitelisted client")
	}
}

func TestAnnounce_RejectUnregisteredTorrent(t *testing.T) {
	w, _, _ := newTestWorker()
	req := newAnnounceReq("started", 1000)
	u := NewUser(1, true, false)
	// Torrent not added to w.Torrents
	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "")
	if err == nil {
		t.Error("expected error for unregistered torrent")
	}
}

// ── Announce — started event ──────────────────────────────────────────────────

func setupAnnounce(t *testing.T) (*Worker, *mockDB, *mockSiteComm, *User) {
	t.Helper()
	w, db, sc := newTestWorker()
	const infoHash = "testhash000000000001"
	tor := NewTorrent(1)
	w.Torrents.Set(infoHash, tor)
	u := NewUser(1, true, false)
	w.Users.Set("testpasskey0000000000000000000", u)
	return w, db, sc, u
}

func TestAnnounce_Started_AddsLeecher(t *testing.T) {
	w, db, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 1000)
	resp, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "TestClient/1.0")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}

	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Leechers.Size() != 1 {
		t.Errorf("leecher count = %d, want 1", tor.Leechers.Size())
	}
	if tor.Seeders.Size() != 0 {
		t.Errorf("seeder count = %d, want 0", tor.Seeders.Size())
	}
	if db.peerRecords < 1 {
		t.Error("expected at least one peer record written")
	}
	if w.Stats.Leechers.Load() != 1 {
		t.Errorf("global leechers = %d, want 1", w.Stats.Leechers.Load())
	}
}

func TestAnnounce_Seeder_AddedToSeeders(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 0) // left=0 → seeder
	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Seeders.Size() != 1 {
		t.Errorf("seeder count = %d, want 1", tor.Seeders.Size())
	}
	if tor.Leechers.Size() != 0 {
		t.Errorf("leecher count = %d, want 0", tor.Leechers.Size())
	}
}

// ── Announce — stopped event ──────────────────────────────────────────────────

func TestAnnounce_Stopped_RemovesPeer(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	// First: join as leecher
	req := newAnnounceReq("started", 1000)
	if _, err := w.Announce(context.Background(), req, u, ip, ""); err != nil {
		t.Fatal(err)
	}

	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Leechers.Size() != 1 {
		t.Fatal("leecher not added")
	}

	// Then: stop
	req2 := newAnnounceReq("stopped", 1000)
	if _, err := w.Announce(context.Background(), req2, u, ip, ""); err != nil {
		t.Fatal(err)
	}

	if tor.Leechers.Size() != 0 {
		t.Errorf("leecher count = %d after stop, want 0", tor.Leechers.Size())
	}
	if w.Stats.Leechers.Load() != 0 {
		t.Errorf("global leechers = %d after stop, want 0", w.Stats.Leechers.Load())
	}
}

// ── Announce — completed event ────────────────────────────────────────────────

func TestAnnounce_Completed_MovesLeecherToSeeder(t *testing.T) {
	w, db, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	// Join as leecher
	req := newAnnounceReq("started", 1000)
	if _, err := w.Announce(context.Background(), req, u, ip, ""); err != nil {
		t.Fatal(err)
	}

	// Complete
	req2 := newAnnounceReq("completed", 0)
	if _, err := w.Announce(context.Background(), req2, u, ip, ""); err != nil {
		t.Fatal(err)
	}

	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Seeders.Size() != 1 {
		t.Errorf("seeder count = %d after complete, want 1", tor.Seeders.Size())
	}
	if tor.Leechers.Size() != 0 {
		t.Errorf("leecher count = %d after complete, want 0", tor.Leechers.Size())
	}
	if db.snatchRecs < 1 {
		t.Error("expected snatch record written on complete")
	}
}

// ── Announce — response fields ────────────────────────────────────────────────

func TestAnnounce_Response_Interval(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 1000)
	resp, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if resp.Interval < int32(w.Config.AnnounceInterval) {
		t.Errorf("Interval %d < AnnounceInterval %d", resp.Interval, w.Config.AnnounceInterval)
	}
	if resp.MinInterval != int32(w.Config.AnnounceInterval) {
		t.Errorf("MinInterval = %d, want %d", resp.MinInterval, w.Config.AnnounceInterval)
	}
}

func TestAnnounce_Response_Peers_EmptyForNewLeecher(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 1000)
	resp, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	// No other peers → compact list should be empty
	if len(resp.Peers) != 0 {
		t.Errorf("Peers len = %d, want 0 (only peer in swarm)", len(resp.Peers))
	}
}

func TestAnnounce_LeecherReceivesSeeder(t *testing.T) {
	w, _, _, _ := setupAnnounce(t)

	// Add a seeder (different user)
	seeder := NewUser(2, true, false)
	seederPeerID := make([]byte, 20)
	copy(seederPeerID, "-DE1300000000000000s")
	seederReq := &AnnounceRequest{
		InfoHash:   "testhash000000000001",
		PeerID:     seederPeerID,
		Port:       51413,
		Left:       0,
		Compact:    true,
		Event:      "started",
		NumWant:    50,
	}
	if _, err := w.Announce(context.Background(), seederReq, seeder, net.ParseIP("192.168.1.1"), ""); err != nil {
		t.Fatalf("seeder announce: %v", err)
	}

	// Add a leecher (different user)
	leecher := NewUser(3, true, false)
	leecherPeerID := make([]byte, 20)
	copy(leecherPeerID, "-qB4000000000000000l")
	leecherReq := &AnnounceRequest{
		InfoHash:   "testhash000000000001",
		PeerID:     leecherPeerID,
		Port:       6881,
		Left:       1000,
		Compact:    true,
		Event:      "started",
		NumWant:    50,
	}
	resp, err := w.Announce(context.Background(), leecherReq, leecher, net.ParseIP("10.0.0.2"), "")
	if err != nil {
		t.Fatalf("leecher announce: %v", err)
	}

	// Leecher should receive the seeder's 6-byte compact entry
	if len(resp.Peers) != 6 {
		t.Errorf("leecher received %d peer bytes, want 6 (1 seeder)", len(resp.Peers))
	}
}

// ── Announce — CanLeech gate ──────────────────────────────────────────────────

func TestAnnounce_CanLeech_False_Forbidden(t *testing.T) {
	w, _, _, _ := setupAnnounce(t)
	u := NewUser(99, false, false) // cannot leech
	req := newAnnounceReq("started", 1000)
	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "")
	if err == nil {
		t.Error("expected error for user with CanLeech=false trying to leech")
	}
}

// ── selectPeers ───────────────────────────────────────────────────────────────

func TestSelectPeers_NumWantLimit(t *testing.T) {
	w, _, _ := newTestWorker()

	tor := NewTorrent(1)
	// Add 10 seeders
	for i := 0; i < 10; i++ {
		p := &Peer{
			UserID:  UserID(i + 100),
			IP:      net.ParseIP("1.2.3.4"),
			Port:    uint16(6000 + i),
			Visible: true,
		}
		p.IPPort = CompactIPPort(p.IP, p.Port)
		tor.Seeders.Set(string(rune(i)), p)
	}

	self := &Peer{UserID: 999}
	got := w.selectPeers(tor, self, 999, 5, true)
	if len(got) != 30 { // 5 peers × 6 bytes
		t.Errorf("selectPeers returned %d bytes, want 30 (5 peers)", len(got))
	}
}

func TestSelectPeers_ExcludesSelf(t *testing.T) {
	w, _, _ := newTestWorker()

	tor := NewTorrent(1)
	self := &Peer{UserID: 42}
	selfKey := "selfkey"
	p := &Peer{
		UserID:  42,
		IP:      net.ParseIP("10.0.0.1"),
		Port:    6881,
		Visible: true,
	}
	p.IPPort = CompactIPPort(p.IP, p.Port)
	tor.Seeders.Set(selfKey, p)

	got := w.selectPeers(tor, self, 42, 50, true)
	if len(got) != 0 {
		t.Errorf("selectPeers returned %d bytes including self, want 0", len(got))
	}
}

// ── minInt ────────────────────────────────────────────────────────────────────

func TestMinInt(t *testing.T) {
	if minInt(3, 5) != 3 {
		t.Error("minInt(3,5) != 3")
	}
	if minInt(5, 3) != 3 {
		t.Error("minInt(5,3) != 3")
	}
	if minInt(4, 4) != 4 {
		t.Error("minInt(4,4) != 4")
	}
}
