package tracker

import (
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

func (m *mockDB) RecordPeer(_ UserID, _ TorrentID, _ int, _, _, _, _, _, _ int64, _, _ uint32, _, _, _ string, _ bool) error {
	m.peerRecords++
	return nil
}
func (m *mockDB) LoadRecommendedInterval(_ TorrentID) (int, bool) { return 0, false }
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
func (m *mockDB) DeleteTorrentHash(_ string) error    { return nil }
func (m *mockDB) DeleteUserPasskey(_ string) error    { return nil }
func (m *mockDB) AddWhitelistEntry(_ string) error    { m.wlAdds++; return nil }
func (m *mockDB) RemoveWhitelistEntry(_ string) error { m.wlRemoves++; return nil }
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

func (m *mockSiteComm) ExpireToken(_ TorrentID, _ UserID)           { m.expired++ }
func (m *mockSiteComm) BanUser(_ int64) error                       { return nil }
func (m *mockSiteComm) UnbanUser(_ int64) error                     { return nil }
func (m *mockSiteComm) NotifyFreeleech(_ int64, _ int) error        { return nil }
func (m *mockSiteComm) ReportAnomaly(_ int64, _ float64) error      { return nil }
func (m *mockSiteComm) UpdateStats(_ int64, _ int64, _ int64) error { return nil }

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
	_, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
	if err == nil {
		t.Error("expected error for non-compact announce")
	}
}

func TestAnnounce_RejectShortPeerID(t *testing.T) {
	w, _, _ := newTestWorker()
	req := newAnnounceReq("started", 1000)
	req.PeerID = []byte("tooshort")
	u := NewUser(1, true, false)
	_, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
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
	_, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
	if err == nil {
		t.Error("expected error for non-whitelisted client")
	}
}

func TestAnnounce_RejectUnregisteredTorrent(t *testing.T) {
	w, _, _ := newTestWorker()
	req := newAnnounceReq("started", 1000)
	u := NewUser(1, true, false)
	// Torrent not added to w.Torrents
	_, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
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
	resp, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "TestClient/1.0")
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
	_, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
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
	if _, err := w.Announce(req, u, ip, ""); err != nil {
		t.Fatal(err)
	}

	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Leechers.Size() != 1 {
		t.Fatal("leecher not added")
	}

	// Then: stop
	req2 := newAnnounceReq("stopped", 1000)
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
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
	if _, err := w.Announce(req, u, ip, ""); err != nil {
		t.Fatal(err)
	}

	// Complete
	req2 := newAnnounceReq("completed", 0)
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
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
	resp, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	// AdaptiveInterval may return a shorter interval than baseInterval for sparse swarms.
	if resp.Interval <= 0 {
		t.Errorf("Interval %d must be positive", resp.Interval)
	}
	if resp.MinInterval != int32(w.Config.AnnounceInterval) {
		t.Errorf("MinInterval = %d, want %d", resp.MinInterval, w.Config.AnnounceInterval)
	}
}

func TestAnnounce_Response_Peers_EmptyForNewLeecher(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 1000)
	resp, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
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
	if _, err := w.Announce(seederReq, seeder, net.ParseIP("5.5.5.5"), ""); err != nil {
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
	resp, err := w.Announce(leecherReq, leecher, net.ParseIP("10.0.0.2"), "")
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
	_, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
	if err == nil {
		t.Error("expected error for user with CanLeech=false trying to leech")
	}
}

// ── SelectPeersOptimized ──────────────────────────────────────────────────────

func TestSelectPeers_NumWantLimit(t *testing.T) {
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
	got := SelectPeersOptimized(tor, self, 999, 5, true)
	if len(got) != 30 { // 5 peers × 6 bytes
		t.Errorf("SelectPeersOptimized returned %d bytes, want 30 (5 peers)", len(got))
	}
}

func TestSelectPeers_ExcludesSelf(t *testing.T) {
	w, _, _ := newTestWorker()
	_ = w

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

	got := SelectPeersOptimized(tor, self, 42, 50, true)
	if len(got) != 0 {
		t.Errorf("SelectPeersOptimized returned %d bytes including self, want 0", len(got))
	}
}

// ── selectPeers additional paths ─────────────────────────────────────────────

func TestSelectPeers_ZeroNumwant(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)
	self := &Peer{UserID: 1}
	got := w.selectPeers(tor, self, 1, 0, true)
	if len(got) != 0 {
		t.Errorf("numwant=0 should return empty bytes, got %d", len(got))
	}
}

func TestSelectPeers_SeederReceivesLeechers(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)

	// Add 2 leechers
	for i := 0; i < 2; i++ {
		p := &Peer{
			UserID:  UserID(10 + i),
			IP:      net.ParseIP("2.3.4.5"),
			Port:    uint16(7000 + i),
			Visible: true,
		}
		p.IPPort = CompactIPPort(p.IP, p.Port)
		tor.Leechers.Set(string(rune('x'+i)), p)
	}

	// Call from a seeder perspective (isLeecher=false)
	self := &Peer{UserID: 99}
	got := w.selectPeers(tor, self, 99, 50, false)
	if len(got) != 12 { // 2 leechers × 6 bytes
		t.Errorf("seeder selectPeers: got %d bytes, want 12", len(got))
	}
}

func TestSelectPeers_InvisiblePeersExcluded(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)

	visible := &Peer{UserID: 1, IP: net.ParseIP("1.1.1.1"), Port: 6000, Visible: true}
	visible.IPPort = CompactIPPort(visible.IP, visible.Port)
	invisible := &Peer{UserID: 2, IP: net.ParseIP("1.1.1.2"), Port: 6001, Visible: false}
	invisible.IPPort = CompactIPPort(invisible.IP, invisible.Port)

	tor.Seeders.Set("vis", visible)
	tor.Seeders.Set("inv", invisible)

	self := &Peer{UserID: 99}
	got := w.selectPeers(tor, self, 99, 50, true)
	if len(got) != 6 { // only 1 visible seeder
		t.Errorf("expected 6 bytes (1 visible peer), got %d", len(got))
	}
}

func TestSelectPeers_LastSelectedSeederRoundRobin(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)

	// Add 3 seeders with deterministic keys
	for i := 0; i < 3; i++ {
		p := &Peer{
			UserID:  UserID(i + 1),
			IP:      net.ParseIP("5.5.5.5"),
			Port:    uint16(6000 + i),
			Visible: true,
		}
		p.IPPort = CompactIPPort(p.IP, p.Port)
		tor.Seeders.Set(string(rune('a'+i)), p)
	}

	// Set a LastSelectedSeeder so the round-robin path is exercised.
	tor.LastSelectedSeeder = "a"

	self := &Peer{UserID: 99}
	got := w.selectPeers(tor, self, 99, 1, true)
	// Should return exactly 1 seeder (6 bytes), starting from the next seeder.
	if len(got) != 6 {
		t.Errorf("expected 6 bytes (1 seeder after round-robin), got %d", len(got))
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

// ── Announce — FreeType paths ─────────────────────────────────────────────────

// doTwoAnnounces performs a "started" then a second announce with higher stats.
// It returns the mockDB after both calls.
func doTwoAnnounces(t *testing.T, freeType FreeType, hasToken bool) (*Worker, *mockDB, *mockSiteComm) {
	t.Helper()
	w, db, sc := newTestWorker()

	const infoHash = "testhash000000000001"
	tor := NewTorrent(1)
	tor.FreeType = freeType
	if hasToken {
		u := NewUser(1, true, false)
		tor.TokenedUsers[u.ID] = struct{}{}
	}
	w.Torrents.Set(infoHash, tor)
	u := NewUser(1, true, false)
	w.Users.Set("testpasskey", u)

	ip := net.ParseIP("10.0.0.1")

	// First announce: sets baseline uploaded/downloaded.
	req1 := newAnnounceReq("started", 500)
	req1.Uploaded = 1000
	req1.Downloaded = 500
	if _, err := w.Announce(req1, u, ip, ""); err != nil {
		t.Fatalf("first announce: %v", err)
	}

	// Second announce: higher stats → causes transfer-delta calculation.
	req2 := newAnnounceReq("", 500)
	req2.Uploaded = 3000
	req2.Downloaded = 1500
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
		t.Fatalf("second announce: %v", err)
	}

	return w, db, sc
}

func TestAnnounce_FreeNeutral_NoStatsRecorded(t *testing.T) {
	_, db, _ := doTwoAnnounces(t, FreeNeutral, false)
	// FreeNeutral: both uploadedChange and downloadedChange → 0, so RecordUserStats must not be called.
	if db.userStatRecs != 0 {
		t.Errorf("userStatRecs = %d, want 0 for FreeNeutral", db.userStatRecs)
	}
}

func TestAnnounce_FreeFree_NoDownloadStats(t *testing.T) {
	_, db, _ := doTwoAnnounces(t, FreeFree, false)
	// FreeFree: downloadedChange → 0 but uploadedChange stays → RecordUserStats called
	// with downloaded=0.  The call count is 1.
	if db.userStatRecs != 1 {
		t.Errorf("userStatRecs = %d, want 1 for FreeFree", db.userStatRecs)
	}
}

func TestAnnounce_TokenExpiry_CalledAndRemoved(t *testing.T) {
	// Token expiry fires only inside the completedTorrent block.
	// Sequence: start as leecher with token → complete the download.
	w, db, sc := newTestWorker()

	const infoHash = "testhash000000000001"
	tor := NewTorrent(1)
	tor.FreeType = FreeNormal
	u := NewUser(1, true, false)
	tor.TokenedUsers[u.ID] = struct{}{}
	w.Torrents.Set(infoHash, tor)
	w.Users.Set("testpasskey", u)
	ip := net.ParseIP("10.0.0.1")

	// First announce: join as leecher with some upload/download baseline.
	req1 := newAnnounceReq("started", 500)
	req1.Uploaded = 1000
	req1.Downloaded = 500
	if _, err := w.Announce(req1, u, ip, ""); err != nil {
		t.Fatalf("first announce: %v", err)
	}

	// Second announce: complete with higher stats — triggers token expiry path.
	req2 := newAnnounceReq("completed", 0)
	req2.Uploaded = 3000
	req2.Downloaded = 1500
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
		t.Fatalf("completed announce: %v", err)
	}

	// Token present on FreeNormal torrent with delta > 0: expireToken fires.
	if sc.expired != 1 {
		t.Errorf("ExpireToken calls = %d, want 1", sc.expired)
	}
	// RecordToken (tracking downloaded under token) should be called once.
	if db.tokenRecs != 1 {
		t.Errorf("tokenRecs = %d, want 1", db.tokenRecs)
	}
	// After expiry the token must be gone from TokenedUsers.
	tor.mu.RLock()
	_, stillHas := tor.TokenedUsers[UserID(1)]
	tor.mu.RUnlock()
	if stillHas {
		t.Error("token still in TokenedUsers after expiry")
	}
}

// ── Announce — stopped seeder decrements seeders ─────────────────────────────

func TestAnnounce_StoppedSeeder_DecrementsSeeders(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	// Join as seeder (left=0).
	req := newAnnounceReq("started", 0)
	if _, err := w.Announce(req, u, ip, ""); err != nil {
		t.Fatal(err)
	}
	if w.Stats.Seeders.Load() != 1 {
		t.Fatalf("seeders = %d, want 1 after seeder join", w.Stats.Seeders.Load())
	}

	// Stop the seeder.
	req2 := newAnnounceReq("stopped", 0)
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
		t.Fatal(err)
	}
	if w.Stats.Seeders.Load() != 0 {
		t.Errorf("seeders = %d, want 0 after seeder stop", w.Stats.Seeders.Load())
	}
}

// ── selectPeers — leecher receives other leechers when no seeders ─────────────

func TestSelectPeers_LeecherReceivesOtherLeechers(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)

	// Add 2 leechers (no seeders)
	for i := 0; i < 2; i++ {
		p := &Peer{
			UserID:  UserID(10 + i),
			IP:      net.ParseIP("3.3.3.3"),
			Port:    uint16(7000 + i),
			Visible: true,
		}
		p.IPPort = CompactIPPort(p.IP, p.Port)
		tor.Leechers.Set(string(rune('m'+i)), p)
	}

	// The requesting leecher (UserID=99) is NOT in the list.
	self := &Peer{UserID: 99}
	got := w.selectPeers(tor, self, 99, 50, true)
	// Should receive both other leechers (12 bytes = 2×6).
	if len(got) != 12 {
		t.Errorf("leecher-to-leecher: got %d bytes, want 12", len(got))
	}
}

// ── Announce — stats-reset path (client restarted) ───────────────────────────

func TestAnnounce_UploadReset_UpdatesPeer(t *testing.T) {
	w, db, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	// First announce: uploaded=5000
	req1 := newAnnounceReq("started", 500)
	req1.Uploaded = 5000
	req1.Downloaded = 2000
	if _, err := w.Announce(req1, u, ip, ""); err != nil {
		t.Fatalf("first announce: %v", err)
	}

	// Second announce: uploaded=100 (lower — client restarted) → reset branch
	req2 := newAnnounceReq("", 500)
	req2.Uploaded = 100
	req2.Downloaded = 50
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
		t.Fatalf("reset announce: %v", err)
	}

	// peerChanged=true on reset; peer record should be updated.
	if db.peerRecords < 2 {
		t.Errorf("peerRecords = %d, want ≥ 2 (one per announce)", db.peerRecords)
	}
}

// ── Announce — corrupt bytes change path ─────────────────────────────────────

func TestAnnounce_CorruptChange_UpdatesBalance(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	// First announce: corrupt=0
	req1 := newAnnounceReq("started", 500)
	req1.Corrupt = 0
	if _, err := w.Announce(req1, u, ip, ""); err != nil {
		t.Fatalf("first announce: %v", err)
	}

	// Second announce: corrupt=1000 → corruptChange > 0 → balance decremented
	req2 := newAnnounceReq("", 500)
	req2.Corrupt = 1000
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
		t.Fatalf("corrupt announce: %v", err)
	}

	tor, _ := w.Torrents.Get(req1.InfoHash)
	tor.mu.RLock()
	balance := tor.Balance
	tor.mu.RUnlock()
	if balance != -1000 {
		t.Errorf("torrent Balance = %d, want -1000 (corrupt bytes subtracted)", balance)
	}
}

// ── Commons economic settlement ───────────────────────────────────────────────

func TestAnnounce_CommonsSettlement_Called(t *testing.T) {
	mc := newMockCommons()
	db := &mockDB{}
	sc := &mockSiteComm{}
	w := &Worker{
		Config: &Config{
			AnnounceInterval: 1800,
			NumWantLimit:     50,
			PeersTimeout:     7200,
		},
		DB:       db,
		SiteComm: sc,
		Commons:  mc,
		Torrents: NewTorrentList(),
		Users:    NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:    &Stats{StartTime: time.Now()},
	}

	const infoHash = "testhash000000000001"
	tor := NewTorrent(1)
	tor.FreeType = FreeNormal
	w.Torrents.Set(infoHash, tor)
	u := NewUser(1, true, false)
	w.Users.Set("testpasskey", u)
	ip := net.ParseIP("10.0.0.1")

	// First announce: establish baseline uploaded/downloaded.
	req1 := newAnnounceReq("started", 500)
	req1.Uploaded = 1000
	req1.Downloaded = 500
	if _, err := w.Announce(req1, u, ip, ""); err != nil {
		t.Fatalf("first announce: %v", err)
	}

	// Second announce: higher stats → uploadedChange > 0 → Commons.SettleAnnounce goroutine fires.
	req2 := newAnnounceReq("", 500)
	req2.Uploaded = 3000
	req2.Downloaded = 1500
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
		t.Fatalf("second announce: %v", err)
	}

	// Allow the async goroutine to complete.
	time.Sleep(20 * time.Millisecond)

	mc.mu.Lock()
	settled := len(mc.Settled)
	mc.mu.Unlock()
	if settled == 0 {
		t.Error("expected Commons.SettleAnnounce to be called at least once")
	}
}

// ── Announce — completed event edge cases ─────────────────────────────────────

func TestAnnounce_Completed_FromNewPeer_AddedToSeeders(t *testing.T) {
	// Peer sends "completed" as first-ever announce (not previously in Leechers
	// or Seeders). Lines 101-103 in announce.go.
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("completed", 0) // completed, left=0, peer not in any map
	if _, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), ""); err != nil {
		t.Fatalf("Announce: %v", err)
	}
	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Seeders.Size() != 1 {
		t.Errorf("expected 1 seeder after completed-from-new-peer, got %d", tor.Seeders.Size())
	}
}

func TestAnnounce_Completed_PeerAlreadySeeder_CompletedFlagCleared(t *testing.T) {
	// Peer first announces as seeder (left=0, no event), then sends "completed".
	// Since the peer is in Seeders but not Leechers, completedTorrent is set
	// to false (lines 104-106).
	w, _, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	req1 := newAnnounceReq("started", 0) // left=0 → seeder
	if _, err := w.Announce(req1, u, ip, ""); err != nil {
		t.Fatalf("first announce: %v", err)
	}

	req2 := newAnnounceReq("completed", 0) // peer already in Seeders
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
		t.Fatalf("second announce: %v", err)
	}

	tor, _ := w.Torrents.Get(req1.InfoHash)
	if tor.Seeders.Size() != 1 {
		t.Errorf("seeder count = %d, want 1", tor.Seeders.Size())
	}
	if tor.Leechers.Size() != 0 {
		t.Errorf("leecher count = %d, want 0", tor.Leechers.Size())
	}
}

// ── Announce — decSeeders path (peer in both Leechers and Seeders) ───────────

func TestAnnounce_Completed_PeerInBothLists_DecSeeders(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("completed", 0)

	torrent, _ := w.Torrents.Get(req.InfoHash)

	// Pre-populate peer in BOTH Leechers and Seeders under the same key.
	peerID := make([]byte, 20)
	copy(peerID, "-qB4test000000000000")
	peerKey := PeerKeyPrime(peerID, u.ID, torrent.ID)
	preExisting := &Peer{UserID: u.ID}
	torrent.Leechers.Set(peerKey, preExisting)
	torrent.Seeders.Set(peerKey, preExisting)

	// Announce with event=completed and left=0: code finds peer in Leechers,
	// then checks Seeders — both exist → decSeeders = true.
	_, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
}

// ── ParseAnnounceParams — ipv4 fallback ───────────────────────────────────────

func TestParseAnnounceParams_IPv4Fallback(t *testing.T) {
	params := url.Values{
		"info_hash": {"00000000000000000001"},
		"peer_id":   {"-qB40000000000000000"},
		"port":      {"6881"},
		"ipv4":      {"1.2.3.4"},
		// no "ip" param
	}
	req, err := ParseAnnounceParams(params, net.ParseIP("10.0.0.1"))
	if err != nil {
		t.Fatalf("ParseAnnounceParams: %v", err)
	}
	if req.IP == nil || req.IP.String() != "1.2.3.4" {
		t.Errorf("expected IP 1.2.3.4, got %v", req.IP)
	}
}

// ── Announce — numwant clamping ───────────────────────────────────────────────

func TestAnnounce_NumWantZero_ClampsToLimit(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 1000)
	req.NumWant = 0
	_, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
	if err != nil {
		t.Fatalf("Announce with numwant=0: %v", err)
	}
}

func TestAnnounce_NumWantExceedsLimit_Clamped(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 1000)
	req.NumWant = 9999 // exceeds NumWantLimit=50
	_, err := w.Announce(req, u, net.ParseIP("10.0.0.1"), "")
	if err != nil {
		t.Fatalf("Announce with numwant=9999: %v", err)
	}
}

// ── Announce — leecher promoted to seeder without "completed" ─────────────────

func TestAnnounce_LeecherPromotedToSeeder_WithoutCompleted(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	// First announce: join as leecher (left > 0)
	req1 := newAnnounceReq("started", 1000)
	if _, err := w.Announce(req1, u, ip, ""); err != nil {
		t.Fatalf("first announce: %v", err)
	}

	tor, _ := w.Torrents.Get(req1.InfoHash)
	if tor.Leechers.Size() != 1 {
		t.Fatalf("expected 1 leecher after first announce, got %d", tor.Leechers.Size())
	}

	// Second announce: left=0, no "completed" event → leecher promoted to seeder
	req2 := newAnnounceReq("", 0) // no event, left=0
	if _, err := w.Announce(req2, u, ip, ""); err != nil {
		t.Fatalf("second announce: %v", err)
	}

	if tor.Leechers.Size() != 0 {
		t.Errorf("expected 0 leechers after promotion, got %d", tor.Leechers.Size())
	}
	if tor.Seeders.Size() != 1 {
		t.Errorf("expected 1 seeder after promotion, got %d", tor.Seeders.Size())
	}
}

// ── selectPeers — leecher loop branches ──────────────────────────────────────

func TestSelectPeers_LeecherLoop_SameUserSkipped(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)

	// Two leechers: one with requesting UserID (skipped), one different (included).
	pSelf := &Peer{UserID: UserID(5), Visible: true, IPPort: []byte{1, 2, 3, 4, 0, 10}}
	pOther := &Peer{UserID: UserID(99), Visible: true, IPPort: []byte{5, 6, 7, 8, 0, 20}}
	tor.Leechers.Set("self", pSelf)
	tor.Leechers.Set("other", pOther)

	// isLeecher=true, no seeders → falls into leecher sub-loop
	got := w.selectPeers(tor, pSelf, UserID(5), 10, true)
	// Only the other peer (6 bytes); self-user is skipped.
	if len(got) != 6 {
		t.Errorf("expected 6 bytes (1 peer), got %d", len(got))
	}
}

func TestSelectPeers_LeecherLoop_InvisibleSkipped(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)

	pInvis := &Peer{UserID: UserID(10), Visible: false, IPPort: []byte{1, 2, 3, 4, 0, 10}}
	pVis := &Peer{UserID: UserID(11), Visible: true, IPPort: []byte{5, 6, 7, 8, 0, 20}}
	tor.Leechers.Set("inv", pInvis)
	tor.Leechers.Set("vis", pVis)

	self := &Peer{UserID: UserID(99)}
	got := w.selectPeers(tor, self, UserID(99), 10, true)
	if len(got) != 6 {
		t.Errorf("expected 6 bytes (1 visible), got %d", len(got))
	}
}

func TestSelectPeers_LeecherLoop_EarlyExit(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)

	// 3 leechers, no seeders, numwant=1 → early exit after first found.
	for i := 0; i < 3; i++ {
		p := &Peer{UserID: UserID(10 + i), Visible: true}
		p.IPPort = CompactIPPort(net.ParseIP("1.2.3.4"), uint16(6000+i))
		tor.Leechers.Set(string(rune('a'+i)), p)
	}

	self := &Peer{UserID: UserID(99)}
	got := w.selectPeers(tor, self, UserID(99), 1, true)
	if len(got) != 6 {
		t.Errorf("expected 6 bytes (numwant=1 hit early exit), got %d", len(got))
	}
}

func TestSelectPeers_SeederPath_SameUserLeecherSkipped(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)

	// isLeecher=false: seeder asking for leechers.
	// One leecher matches requester's UserID (skipped), one is different.
	pSelf := &Peer{UserID: UserID(5), Visible: true}
	pSelf.IPPort = CompactIPPort(net.ParseIP("1.2.3.4"), 6000)
	pOther := &Peer{UserID: UserID(99), Visible: true}
	pOther.IPPort = CompactIPPort(net.ParseIP("5.6.7.8"), 6001)
	tor.Leechers.Set("self", pSelf)
	tor.Leechers.Set("other", pOther)

	self := &Peer{UserID: UserID(5)}
	got := w.selectPeers(tor, self, UserID(5), 10, false)
	if len(got) != 6 {
		t.Errorf("expected 6 bytes (1 peer, self skipped), got %d", len(got))
	}
}

func TestSelectPeers_SeederPath_EarlyExit(t *testing.T) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)

	// 3 leechers, isLeecher=false, numwant=1 → early exit after first found.
	for i := 0; i < 3; i++ {
		p := &Peer{UserID: UserID(10 + i), Visible: true}
		p.IPPort = CompactIPPort(net.ParseIP("1.2.3.4"), uint16(6000+i))
		tor.Leechers.Set(string(rune('a'+i)), p)
	}

	self := &Peer{UserID: UserID(99)}
	got := w.selectPeers(tor, self, UserID(99), 1, false)
	if len(got) != 6 {
		t.Errorf("expected 6 bytes (numwant=1 early exit), got %d", len(got))
	}
}

// ── Announce — IPv6 clientIP → CompactIPPort nil → invalidIP ─────────────────

func TestAnnounce_IPv6ClientIP_CompactNil_InvalidIP(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 1000)
	// IPv6 address: ValidateIPNotPrivate returns true for IPv6 (accepted),
	// but CompactIPPort returns nil for non-IPv4 → covers peer.IPPort == nil branch.
	ipv6 := net.ParseIP("2001:db8::1")
	resp, err := w.Announce(req, u, ipv6, "")
	if err != nil {
		t.Fatalf("Announce with IPv6: %v", err)
	}
	if resp.Warning == "" {
		t.Error("expected warning for IPv6 address with nil compact IPPort")
	}
}
