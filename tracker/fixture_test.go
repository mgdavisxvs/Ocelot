package tracker

import (
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

// ── test constants ────────────────────────────────────────────────────────────

const (
	testPasskey  = "abcdef1234567890abcdef1234567890" // exactly 32 chars
	testPasskey2 = "zzzzzz1234567890abcdef1234567890" // second user
	sitePass     = "sitepass1234567890123456789012ab" // exactly 32 chars
	testInfoHash = "\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01\x01"
	testPeerID   = "\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02\x02"
	testPeerID2  = "\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03\x03"
	testIP       = "1.2.3.4"
	testPort     = uint16(6881)
)

// ── test config ───────────────────────────────────────────────────────────────

func newTestConfig() *Config {
	return &Config{
		ListenAddr:       ":0",
		AnnounceInterval: 1800,
		PeersTimeout:     7200,
		MaxMiddlemen:     20000,
		NumWantLimit:     50,
		KeepaliveTimeout: 0,
		SitePassword:     sitePass,
		ReportPassword:   "reportpass1234567890123456789012",
		ReadTimeout:      10 * time.Second,
		WriteTimeout:     10 * time.Second,
		ScheduleInterval: 3,
	}
}

// ── test worker ───────────────────────────────────────────────────────────────

type testFixture struct {
	db       *MockDB
	siteComm *MockSiteComm
	worker   *Worker
	server   *Server
}

// newTestFixture creates a fully initialised Worker + Server backed by mocks.
// Pre-seeded: one torrent at testInfoHash (ID=1, FreeNormal),
//             one user at testPasskey (ID=1, CanLeech=true),
//             empty whitelist (allow-all).
func newTestFixture() *testFixture {
	db := newMockDB()
	sc := newMockSiteComm()

	torrents := NewTorrentList()
	t := NewTorrent(TorrentID(1))
	torrents.Set(testInfoHash, t)

	users := NewUserList()
	u := NewUser(UserID(1), true, false)
	users.Set(testPasskey, u)

	u2 := NewUser(UserID(2), true, false)
	users.Set(testPasskey2, u2)

	stats := &Stats{}
	stats.StartTime = time.Now()

	cfg := newTestConfig()
	worker := &Worker{
		Config:    cfg,
		DB:        db,
		SiteComm:  sc,
		Torrents:  torrents,
		Users:     users,
		Whitelist: NewWhitelist(),
		Stats:     stats,
	}

	server := NewServer(cfg, worker)

	return &testFixture{db: db, siteComm: sc, worker: worker, server: server}
}

func (f *testFixture) reset() {
	f.db.reset()
	f.siteComm.reset()
}

// newAnnounceReqFull creates an AnnounceRequest with explicit infoHash/peerID/stats.
// Use the simpler newAnnounceReq(event, left) from announce_test.go for basic cases.
func newAnnounceReqFull(infoHash, peerID, event string, uploaded, downloaded, left int64) *AnnounceRequest {
	return &AnnounceRequest{
		InfoHash:   infoHash,
		PeerID:     []byte(peerID),
		Port:       testPort,
		Uploaded:   uploaded,
		Downloaded: downloaded,
		Left:       left,
		Compact:    true,
		Event:      event,
		NumWant:    50,
	}
}

// buildAnnounceURL returns a pre-built *http.Request for use with handleRequest.
func buildAnnounceURL(passkey, infoHash, peerID, event string, uploaded, downloaded, left int64) *http.Request {
	q := url.Values{
		"info_hash":  {infoHash},
		"peer_id":    {peerID},
		"port":       {"6881"},
		"uploaded":   {i64s(uploaded)},
		"downloaded": {i64s(downloaded)},
		"left":       {i64s(left)},
		"compact":    {"1"},
		"event":      {event},
		"numwant":    {"50"},
	}
	req, _ := http.NewRequest("GET", "/"+passkey+"/announce?"+q.Encode(), nil)
	return req
}

func buildScrapeURL(passkey string, infoHashes ...string) *http.Request {
	q := url.Values{}
	for _, h := range infoHashes {
		q.Add("info_hash", h)
	}
	req, _ := http.NewRequest("GET", "/"+passkey+"/scrape?"+q.Encode(), nil)
	return req
}

func buildUpdateURL(sitePassword, action string, extra url.Values) *http.Request {
	q := url.Values{"action": {action}}
	for k, vs := range extra {
		q[k] = vs
	}
	req, _ := http.NewRequest("GET", "/"+sitePassword+"/update?"+q.Encode(), nil)
	return req
}

// ── misc helpers ──────────────────────────────────────────────────────────────

func i64s(n int64) string {
	return itoa(int(n))
}

func itoa(n int) string {
	// avoid importing strconv in each test file
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// atomicLoadUint64 reads a *atomic.Uint64 without importing sync/atomic directly.
func loadU64(v *atomic.Uint64) uint64 { return v.Load() }
func loadU32(v *atomic.Uint32) uint32 { return v.Load() }

// ── fakeClock ─────────────────────────────────────────────────────────────────

// fakeClock implements schedulerClock for tests, providing a manually-triggered
// tick channel via Fire().
type fakeClock struct {
	ch chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{ch: make(chan time.Time, 1)}
}

func (f *fakeClock) C() <-chan time.Time { return f.ch }
func (f *fakeClock) Stop()              {}
func (f *fakeClock) Fire()              { f.ch <- time.Now() }
