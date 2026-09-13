package tracker

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test fixtures

type peerRecord struct {
	UserID     UserID
	TorrentID  TorrentID
	Active     int
	Uploaded   int64
	Downloaded int64
	Left       int64
	IP         string
	PeerID     string
	UserAgent  string
}

type snatchRecord struct {
	UserID    UserID
	TorrentID TorrentID
	IP        string
}

type torrentRecord struct {
	TorrentID TorrentID
	Seeders   uint32
	Leechers  uint32
	Snatched  int
}

type userStatRecord struct {
	UserID     UserID
	Uploaded   int64
	Downloaded int64
}

// mockDB records every DatabaseInterface call so tests can assert on the
// persistence side effects of an announce.
type mockDB struct {
	mu sync.Mutex

	peers       []peerRecord
	lightPeers  int
	userStats   []userStatRecord
	torrents    []torrentRecord
	snatches    []snatchRecord
	tokens      int
	closed      bool
	deactivated []PeerRef
}

// DeactivatePeers satisfies the optional PeerDeactivator interface the reaper
// probes for.
func (m *mockDB) DeactivatePeers(refs []PeerRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deactivated = append(m.deactivated, refs...)
	return nil
}

func (m *mockDB) RecordPeer(userID UserID, torrentID TorrentID, active int,
	uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64,
	announceTime, announces uint32, ip, peerID, userAgent string) error {

	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers = append(m.peers, peerRecord{
		UserID:     userID,
		TorrentID:  torrentID,
		Active:     active,
		Uploaded:   uploaded,
		Downloaded: downloaded,
		Left:       left,
		IP:         ip,
		PeerID:     peerID,
		UserAgent:  userAgent,
	})
	return nil
}

func (m *mockDB) RecordPeerLight(userID UserID, torrentID TorrentID, announceTime, announces uint32, peerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lightPeers++
	return nil
}

func (m *mockDB) RecordUserStats(userID UserID, uploaded, downloaded int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userStats = append(m.userStats, userStatRecord{userID, uploaded, downloaded})
	return nil
}

func (m *mockDB) RecordTorrent(torrentID TorrentID, seeders, leechers uint32, snatched int, balance int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.torrents = append(m.torrents, torrentRecord{torrentID, seeders, leechers, snatched})
	return nil
}

func (m *mockDB) RecordSnatch(userID UserID, torrentID TorrentID, t time.Time, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snatches = append(m.snatches, snatchRecord{userID, torrentID, ip})
	return nil
}

func (m *mockDB) RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens++
	return nil
}

func (m *mockDB) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockDB) peerCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.peers)
}

func (m *mockDB) lightCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lightPeers
}

func (m *mockDB) snatchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.snatches)
}

// mockSiteComm records token expiry callbacks.
type mockSiteComm struct {
	mu      sync.Mutex
	expired []TorrentID
}

func (m *mockSiteComm) ExpireToken(torrentID TorrentID, userID UserID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expired = append(m.expired, torrentID)
}

// testHarness bundles a Worker with its mocks and the fixtures under test.
type testHarness struct {
	worker   *Worker
	db       *mockDB
	siteComm *mockSiteComm
	torrent  *Torrent
	infoHash string
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()

	db := &mockDB{}
	siteComm := &mockSiteComm{}

	torrent := NewTorrent(TorrentID(1))
	torrents := NewTorrentList()
	infoHash := testInfoHash(0xAA)
	torrents.Set(infoHash, torrent)

	worker := &Worker{
		Config: &Config{
			ListenAddr:       ":34000",
			AnnounceInterval: 1800,
			PeersTimeout:     7200,
			MaxMiddlemen:     10000,
			NumWantLimit:     50,
			KeepaliveTimeout: 60 * time.Second,
			SitePassword:     "sitepassword",
		},
		DB:        db,
		SiteComm:  siteComm,
		Torrents:  torrents,
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     &Stats{StartTime: time.Now()},
	}

	return &testHarness{
		worker:   worker,
		db:       db,
		siteComm: siteComm,
		torrent:  torrent,
		infoHash: infoHash,
	}
}

// addUser registers a user under a 32-character passkey and returns both.
func (h *testHarness) addUser(t *testing.T, id UserID, canLeech bool) (*User, string) {
	t.Helper()

	user := NewUser(id, canLeech, false)
	passkey := fmt.Sprintf("%032d", id)
	h.worker.Users.Set(passkey, user)
	return user, passkey
}

func testInfoHash(marker byte) string {
	hash := make([]byte, 20)
	for i := range hash {
		hash[i] = marker
	}
	return string(hash)
}

// testPeerID builds a valid 20-byte peer ID with a distinguishing suffix.
func testPeerID(suffix string) []byte {
	id := []byte("-qB4380-000000000000")
	copy(id[8:], suffix)
	return id
}

// announceParams builds a request with the fields a real client always sends.
func announceParams(infoHash string, peerID []byte, port uint16, left int64, event string) *AnnounceRequest {
	return &AnnounceRequest{
		InfoHash: infoHash,
		PeerID:   peerID,
		Port:     port,
		Left:     left,
		Event:    event,
		Compact:  true,
		IP:       net.ParseIP("10.0.0.1"),
	}
}

// Announce flow

func TestAnnounceStartedRegistersLeecher(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")

	resp, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qBittorrent/4.3.9")
	if err != nil {
		t.Fatalf("announce failed: %v", err)
	}

	if resp.Incomplete != 1 {
		t.Errorf("incomplete = %d, want 1", resp.Incomplete)
	}
	if resp.Complete != 0 {
		t.Errorf("complete = %d, want 0", resp.Complete)
	}
	if h.torrent.Leechers.Size() != 1 {
		t.Errorf("leecher list size = %d, want 1", h.torrent.Leechers.Size())
	}
	if h.torrent.Seeders.Size() != 0 {
		t.Errorf("seeder list size = %d, want 0", h.torrent.Seeders.Size())
	}
	if h.db.peerCount() != 1 {
		t.Errorf("RecordPeer called %d times, want 1", h.db.peerCount())
	}
}

func TestAnnounceStartedRegistersSeeder(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, testPeerID("seed0001"), 6881, 0, "started")

	resp, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qBittorrent/4.3.9")
	if err != nil {
		t.Fatalf("announce failed: %v", err)
	}

	if resp.Complete != 1 {
		t.Errorf("complete = %d, want 1", resp.Complete)
	}
	if h.torrent.Seeders.Size() != 1 {
		t.Errorf("seeder list size = %d, want 1", h.torrent.Seeders.Size())
	}
}

func TestAnnounceCompletedRecordsSnatch(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)
	peerID := testPeerID("peer0001")

	// Join as a leecher first.
	start := announceParams(h.infoHash, peerID, 6881, 1<<30, "started")
	if _, err := h.worker.Announce(start, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("started announce failed: %v", err)
	}

	// Then finish the download.
	done := announceParams(h.infoHash, peerID, 6881, 0, "completed")
	done.Downloaded = 1 << 30

	resp, err := h.worker.Announce(done, user, net.ParseIP("10.0.0.1"), "qB")
	if err != nil {
		t.Fatalf("completed announce failed: %v", err)
	}

	if h.db.snatchCount() != 1 {
		t.Errorf("RecordSnatch called %d times, want 1", h.db.snatchCount())
	}
	if h.torrent.Completed != 1 {
		t.Errorf("torrent.Completed = %d, want 1", h.torrent.Completed)
	}
	if resp.Incomplete != 0 {
		t.Errorf("incomplete = %d, want 0 after completion", resp.Incomplete)
	}
}

func TestAnnounceStoppedRemovesPeer(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)
	peerID := testPeerID("peer0001")

	start := announceParams(h.infoHash, peerID, 6881, 1<<30, "started")
	if _, err := h.worker.Announce(start, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("started announce failed: %v", err)
	}
	if h.torrent.Leechers.Size() != 1 {
		t.Fatalf("precondition failed: leechers = %d, want 1", h.torrent.Leechers.Size())
	}

	stop := announceParams(h.infoHash, peerID, 6881, 1<<30, "stopped")
	resp, err := h.worker.Announce(stop, user, net.ParseIP("10.0.0.1"), "qB")
	if err != nil {
		t.Fatalf("stopped announce failed: %v", err)
	}

	if h.torrent.Leechers.Size() != 0 {
		t.Errorf("leechers = %d, want 0 after stop", h.torrent.Leechers.Size())
	}
	if len(resp.Peers) != 0 {
		t.Errorf("a stopped announce returned %d bytes of peers, want none", len(resp.Peers))
	}
}

func TestAnnounceReturnsSeederToLeecher(t *testing.T) {
	h := newTestHarness(t)
	seeder, _ := h.addUser(t, 1, true)
	leecher, _ := h.addUser(t, 2, true)

	// A seeder joins on a known address and port.
	seed := announceParams(h.infoHash, testPeerID("seed0001"), 51413, 0, "started")
	seed.IP = net.ParseIP("192.0.2.10")
	if _, err := h.worker.Announce(seed, seeder, net.ParseIP("192.0.2.10"), "qB"); err != nil {
		t.Fatalf("seeder announce failed: %v", err)
	}

	// A different user then joins as a leecher and should be handed the seeder.
	leech := announceParams(h.infoHash, testPeerID("peer0002"), 6881, 1<<30, "started")
	resp, err := h.worker.Announce(leech, leecher, net.ParseIP("10.0.0.2"), "qB")
	if err != nil {
		t.Fatalf("leecher announce failed: %v", err)
	}

	if len(resp.Peers) != 6 {
		t.Fatalf("peers = %d bytes, want 6 (one compact peer)", len(resp.Peers))
	}

	gotIP := net.IPv4(resp.Peers[0], resp.Peers[1], resp.Peers[2], resp.Peers[3])
	if !gotIP.Equal(net.ParseIP("192.0.2.10")) {
		t.Errorf("peer IP = %s, want 192.0.2.10", gotIP)
	}

	gotPort := binary.BigEndian.Uint16(resp.Peers[4:6])
	if gotPort != 51413 {
		t.Errorf("peer port = %d, want 51413", gotPort)
	}
}

func TestAnnounceExcludesSelfFromPeerList(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	peerID := testPeerID("peer0001")
	req := announceParams(h.infoHash, peerID, 6881, 1<<30, "started")

	if _, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("first announce failed: %v", err)
	}

	// Announcing again must not hand the peer back to itself.
	resp, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB")
	if err != nil {
		t.Fatalf("second announce failed: %v", err)
	}

	if len(resp.Peers) != 0 {
		t.Errorf("peers = %d bytes, want 0 — a peer must not receive itself", len(resp.Peers))
	}
}

func TestAnnounceRejectsNonCompactClient(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")
	req.Compact = false

	_, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB")
	if err == nil {
		t.Fatal("expected a non-compact announce to be rejected")
	}
	if !strings.Contains(err.Error(), "compact") {
		t.Errorf("error = %q, want it to mention compact announces", err)
	}
}

func TestAnnounceRejectsInvalidPeerIDLength(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, []byte("too-short"), 6881, 1<<30, "started")

	_, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB")
	if err == nil {
		t.Fatal("expected a short peer ID to be rejected")
	}
	if !strings.Contains(err.Error(), "peer ID") {
		t.Errorf("error = %q, want it to mention the peer ID", err)
	}
}

func TestAnnounceRejectsUnregisteredTorrent(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	req := announceParams(testInfoHash(0xBB), testPeerID("peer0001"), 6881, 1<<30, "started")

	_, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB")
	if err == nil {
		t.Fatal("expected an unknown info_hash to be rejected")
	}
	if !strings.Contains(err.Error(), "unregistered") {
		t.Errorf("error = %q, want it to mention an unregistered torrent", err)
	}
}

func TestAnnounceRejectsLeechingWhenForbidden(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, false) // leeching disabled

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")

	_, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB")
	if err == nil {
		t.Fatal("expected leeching to be denied")
	}
	if !strings.Contains(err.Error(), "leeching forbidden") {
		t.Errorf("error = %q, want it to mention forbidden leeching", err)
	}
}

func TestAnnounceSeedingAllowedWhenLeechingForbidden(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, false)

	// left == 0 means seeding, which the leech restriction must not block.
	req := announceParams(h.infoHash, testPeerID("seed0001"), 6881, 0, "started")

	if _, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("seeding should be permitted without leech rights: %v", err)
	}
}

func TestAnnounceBlockedByWhitelist(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	h.worker.Whitelist.Add("-lt0D")

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")

	_, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB")
	if err == nil {
		t.Fatal("expected a non-whitelisted client to be rejected")
	}
	if !strings.Contains(err.Error(), "whitelist") {
		t.Errorf("error = %q, want it to mention the whitelist", err)
	}
}

func TestAnnounceAllowedByWhitelistPrefix(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	h.worker.Whitelist.Add("-qB43")

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")

	if _, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("whitelisted client was rejected: %v", err)
	}
}

func TestAnnounceRecordsUserStatsOnTransfer(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)
	peerID := testPeerID("peer0001")

	first := announceParams(h.infoHash, peerID, 6881, 1<<30, "started")
	if _, err := h.worker.Announce(first, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("first announce failed: %v", err)
	}

	// Report progress: 100 MB up, 200 MB down since the last announce.
	second := announceParams(h.infoHash, peerID, 6881, (1<<30)-(200<<20), "")
	second.Uploaded = 100 << 20
	second.Downloaded = 200 << 20

	if _, err := h.worker.Announce(second, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("second announce failed: %v", err)
	}

	h.db.mu.Lock()
	defer h.db.mu.Unlock()

	if len(h.db.userStats) != 1 {
		t.Fatalf("RecordUserStats called %d times, want 1", len(h.db.userStats))
	}
	stat := h.db.userStats[0]
	if stat.Uploaded != 100<<20 {
		t.Errorf("uploaded delta = %d, want %d", stat.Uploaded, int64(100<<20))
	}
	if stat.Downloaded != 200<<20 {
		t.Errorf("downloaded delta = %d, want %d", stat.Downloaded, int64(200<<20))
	}
}

func TestAnnounceUsesLightRecordWhenNothingChanged(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)
	peerID := testPeerID("peer0001")

	req := announceParams(h.infoHash, peerID, 6881, 1<<30, "started")
	if _, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("first announce failed: %v", err)
	}

	before := h.db.lightCount()

	// A keepalive announce with no event and no transfer.
	idle := announceParams(h.infoHash, peerID, 6881, 1<<30, "")
	if _, err := h.worker.Announce(idle, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("idle announce failed: %v", err)
	}

	if h.db.lightCount() != before+1 {
		t.Errorf("RecordPeerLight count = %d, want %d", h.db.lightCount(), before+1)
	}
}

func TestAnnounceRespectsNumWantLimit(t *testing.T) {
	h := newTestHarness(t)
	h.worker.Config.NumWantLimit = 2

	// Seed the swarm with four seeders owned by distinct users.
	for i := 1; i <= 4; i++ {
		seeder, _ := h.addUser(t, UserID(i), true)
		req := announceParams(h.infoHash, testPeerID(fmt.Sprintf("seed%04d", i)), uint16(6880+i), 0, "started")
		req.IP = net.ParseIP(fmt.Sprintf("192.0.2.%d", i))
		if _, err := h.worker.Announce(req, seeder, req.IP, "qB"); err != nil {
			t.Fatalf("seeder %d announce failed: %v", i, err)
		}
	}

	leecher, _ := h.addUser(t, 99, true)
	req := announceParams(h.infoHash, testPeerID("peer0099"), 6881, 1<<30, "started")
	req.NumWant = 100 // more than the configured limit

	resp, err := h.worker.Announce(req, leecher, net.ParseIP("10.0.0.99"), "qB")
	if err != nil {
		t.Fatalf("leecher announce failed: %v", err)
	}

	if len(resp.Peers) > 2*6 {
		t.Errorf("peers = %d bytes, want at most %d (numwant limit 2)", len(resp.Peers), 2*6)
	}
}

func TestAnnounceReturnsPositiveIntervals(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")

	resp, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB")
	if err != nil {
		t.Fatalf("announce failed: %v", err)
	}

	if resp.Interval <= 0 {
		t.Errorf("interval = %d, want a positive value", resp.Interval)
	}
	if resp.MinInterval <= 0 {
		t.Errorf("min interval = %d, want a positive value", resp.MinInterval)
	}
	if resp.MinInterval > resp.Interval {
		t.Errorf("min interval %d exceeds interval %d", resp.MinInterval, resp.Interval)
	}
}

func TestAnnounceWarnsOnUnsupportedIP(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")
	req.IP = net.ParseIP("2001:db8::1") // IPv6 has no compact representation

	resp, err := h.worker.Announce(req, user, net.ParseIP("2001:db8::1"), "qB")
	if err != nil {
		t.Fatalf("announce failed: %v", err)
	}

	if resp.Warning == "" {
		t.Error("expected a warning for an address with no compact form")
	}
}

func TestAnnounceConcurrentPeers(t *testing.T) {
	h := newTestHarness(t)

	const peers = 25
	users := make([]*User, peers)
	for i := 0; i < peers; i++ {
		users[i], _ = h.addUser(t, UserID(i+1), true)
	}

	var wg sync.WaitGroup
	errs := make(chan error, peers)

	for i := 0; i < peers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			req := announceParams(h.infoHash, testPeerID(fmt.Sprintf("pr%06d", idx)), uint16(7000+idx), 1<<30, "started")
			req.IP = net.IPv4(10, 1, byte(idx/256), byte(idx%256))

			if _, err := h.worker.Announce(req, users[idx], req.IP, "qB"); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent announce failed: %v", err)
	}

	if got := h.torrent.Leechers.Size(); got != peers {
		t.Errorf("leechers = %d, want %d", got, peers)
	}
}

// Scrape endpoint

func newTestServer(t *testing.T, h *testHarness) *Server {
	t.Helper()

	server := NewServer(h.worker.Config, h.worker)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	return server
}

func scrapeRequest(t *testing.T, infoHashes ...string) *http.Request {
	t.Helper()

	params := url.Values{}
	for _, hash := range infoHashes {
		params.Add("info_hash", hash)
	}
	return httptest.NewRequest(http.MethodGet, "/scrape?"+params.Encode(), nil)
}

func TestScrapeReturnsTorrentCounts(t *testing.T) {
	h := newTestHarness(t)
	user, passkey := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")
	if _, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("announce failed: %v", err)
	}

	server := newTestServer(t, h)
	body := string(server.handleScrape(scrapeRequest(t, h.infoHash), passkey, net.ParseIP("10.0.0.1"), true))

	if !strings.Contains(body, "d5:filesd") {
		t.Errorf("scrape body is missing the files dictionary: %q", body)
	}
	if !strings.Contains(body, "10:incompletei1e") {
		t.Errorf("scrape body should report 1 incomplete peer: %q", body)
	}
	if !strings.Contains(body, "8:completei0e") {
		t.Errorf("scrape body should report 0 complete peers: %q", body)
	}
}

func TestScrapeReflectsSeederAndLeecherCounts(t *testing.T) {
	h := newTestHarness(t)
	seeder, _ := h.addUser(t, 1, true)
	leecher, passkey := h.addUser(t, 2, true)

	seed := announceParams(h.infoHash, testPeerID("seed0001"), 51413, 0, "started")
	if _, err := h.worker.Announce(seed, seeder, net.ParseIP("192.0.2.10"), "qB"); err != nil {
		t.Fatalf("seeder announce failed: %v", err)
	}

	leech := announceParams(h.infoHash, testPeerID("peer0002"), 6881, 1<<30, "started")
	if _, err := h.worker.Announce(leech, leecher, net.ParseIP("10.0.0.2"), "qB"); err != nil {
		t.Fatalf("leecher announce failed: %v", err)
	}

	server := newTestServer(t, h)
	body := string(server.handleScrape(scrapeRequest(t, h.infoHash), passkey, net.ParseIP("10.0.0.1"), true))

	if !strings.Contains(body, "8:completei1e") {
		t.Errorf("scrape should report 1 seeder: %q", body)
	}
	if !strings.Contains(body, "10:incompletei1e") {
		t.Errorf("scrape should report 1 leecher: %q", body)
	}
}

func TestScrapeReportsSnatchCount(t *testing.T) {
	h := newTestHarness(t)
	user, passkey := h.addUser(t, 1, true)
	peerID := testPeerID("peer0001")

	start := announceParams(h.infoHash, peerID, 6881, 1<<30, "started")
	if _, err := h.worker.Announce(start, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("started announce failed: %v", err)
	}

	done := announceParams(h.infoHash, peerID, 6881, 0, "completed")
	if _, err := h.worker.Announce(done, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("completed announce failed: %v", err)
	}

	server := newTestServer(t, h)
	body := string(server.handleScrape(scrapeRequest(t, h.infoHash), passkey, net.ParseIP("10.0.0.1"), true))

	if !strings.Contains(body, "10:downloadedi1e") {
		t.Errorf("scrape should report 1 completed download: %q", body)
	}
}

func TestScrapeMultipleTorrents(t *testing.T) {
	h := newTestHarness(t)
	_, passkey := h.addUser(t, 1, true)

	secondHash := testInfoHash(0xCC)
	h.worker.Torrents.Set(secondHash, NewTorrent(TorrentID(2)))

	server := newTestServer(t, h)
	body := string(server.handleScrape(scrapeRequest(t, h.infoHash, secondHash), passkey, net.ParseIP("10.0.0.1"), true))

	if !strings.Contains(body, h.infoHash) {
		t.Error("scrape body is missing the first torrent")
	}
	if !strings.Contains(body, secondHash) {
		t.Error("scrape body is missing the second torrent")
	}
	if got := strings.Count(body, "8:completei"); got != 2 {
		t.Errorf("found %d torrent entries, want 2", got)
	}
}

func TestScrapeSkipsUnknownTorrent(t *testing.T) {
	h := newTestHarness(t)
	_, passkey := h.addUser(t, 1, true)

	unknown := testInfoHash(0xDD)

	server := newTestServer(t, h)
	body := string(server.handleScrape(scrapeRequest(t, h.infoHash, unknown), passkey, net.ParseIP("10.0.0.1"), true))

	if strings.Contains(body, unknown) {
		t.Error("scrape body should omit torrents the tracker does not know")
	}
	if got := strings.Count(body, "8:completei"); got != 1 {
		t.Errorf("found %d torrent entries, want 1", got)
	}
}

func TestScrapeRejectsUnknownPasskey(t *testing.T) {
	h := newTestHarness(t)

	server := newTestServer(t, h)
	body := string(server.handleScrape(scrapeRequest(t, h.infoHash), strings.Repeat("f", 32), net.ParseIP("10.0.0.1"), true))

	if !strings.Contains(body, "failure reason") {
		t.Errorf("expected a bencoded failure for an unknown passkey: %q", body)
	}
}

func TestScrapeWithNoInfoHashesReturnsEmptyFiles(t *testing.T) {
	h := newTestHarness(t)
	_, passkey := h.addUser(t, 1, true)

	server := newTestServer(t, h)
	body := string(server.handleScrape(scrapeRequest(t), passkey, net.ParseIP("10.0.0.1"), true))

	if !strings.Contains(body, "d5:filesdee") {
		t.Errorf("expected an empty files dictionary: %q", body)
	}
}

func TestScrapeTracksPeerDeparture(t *testing.T) {
	h := newTestHarness(t)
	user, passkey := h.addUser(t, 1, true)
	peerID := testPeerID("peer0001")

	start := announceParams(h.infoHash, peerID, 6881, 1<<30, "started")
	if _, err := h.worker.Announce(start, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("started announce failed: %v", err)
	}

	server := newTestServer(t, h)

	body := string(server.handleScrape(scrapeRequest(t, h.infoHash), passkey, net.ParseIP("10.0.0.1"), true))
	if !strings.Contains(body, "10:incompletei1e") {
		t.Fatalf("precondition failed, expected 1 leecher: %q", body)
	}

	stop := announceParams(h.infoHash, peerID, 6881, 1<<30, "stopped")
	if _, err := h.worker.Announce(stop, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("stopped announce failed: %v", err)
	}

	body = string(server.handleScrape(scrapeRequest(t, h.infoHash), passkey, net.ParseIP("10.0.0.1"), true))
	if !strings.Contains(body, "10:incompletei0e") {
		t.Errorf("scrape should report 0 leechers after departure: %q", body)
	}
}
