package tracker

// GUC test suite: SiteComm interaction with the announce path.
//
// Analytical lens distribution:
//   Knuth  (K1-K5): algorithmic correctness – loop invariants, data-structure invariants
//   Turing (T1-T5): termination / halting – retry loops, early exits, degraded operation
//   Church (C1-C5): side-effect isolation – parameter encoding, mock reset, referential transparency
//   Gödel  (G1-G5): formal consistency – impossible-state detection, interface contracts,
//                    concurrent invariant preservation

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// scWorker returns a Worker whose SiteComm and DB are both recording mocks.
func scWorker() (*Worker, *MockDB, *MockSiteComm) {
	db := newMockDB()
	sc := newMockSiteComm()
	w := &Worker{
		Config: &Config{
			AnnounceInterval: 1800,
			NumWantLimit:     50,
			PeersTimeout:     7200,
			AllowPrivateIPs:  true,
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

// scAddTokenedTorrent registers a torrent in the worker's list and optionally
// adds a freeleech token for userID (pass 0 to skip the token).
func scAddTokenedTorrent(w *Worker, infoHash string, id TorrentID, ft FreeType, tokenUID UserID) *Torrent {
	tor := NewTorrent(id)
	tor.FreeType = ft
	if tokenUID != 0 {
		tor.TokenedUsers[tokenUID] = struct{}{}
	}
	w.Torrents.Set(infoHash, tor)
	return tor
}

// scDoStartThenComplete performs a "started" leecher announce followed by a
// "completed" announce (Left=0) with higher stats, triggering the delta path.
func scDoStartThenComplete(t *testing.T, w *Worker, u *User, infoHash, peerID string) {
	t.Helper()
	ip := net.ParseIP("10.0.0.1")

	req1 := newAnnounceReqFull(infoHash, peerID, "started", 1000, 500, 500)
	if _, err := w.Announce(context.Background(), req1, u, ip, "", ""); err != nil {
		t.Fatalf("start announce: %v", err)
	}

	req2 := newAnnounceReqFull(infoHash, peerID, "completed", 3000, 1500, 0)
	if _, err := w.Announce(context.Background(), req2, u, ip, "", ""); err != nil {
		t.Fatalf("completed announce: %v", err)
	}
}

// scDoStart performs only a single "started" announce (no complete).
func scDoStart(t *testing.T, w *Worker, u *User, infoHash, peerID string) {
	t.Helper()
	ip := net.ParseIP("10.0.0.1")
	req := newAnnounceReqFull(infoHash, peerID, "started", 1000, 500, 500)
	if _, err := w.Announce(context.Background(), req, u, ip, "", ""); err != nil {
		t.Fatalf("start announce: %v", err)
	}
}

// scDoStartThenStop performs "started" then "stopped", exercising the stop path.
func scDoStartThenStop(t *testing.T, w *Worker, u *User, infoHash, peerID string) {
	t.Helper()
	ip := net.ParseIP("10.0.0.1")
	req1 := newAnnounceReqFull(infoHash, peerID, "started", 1000, 500, 500)
	if _, err := w.Announce(context.Background(), req1, u, ip, "", ""); err != nil {
		t.Fatalf("start announce: %v", err)
	}
	req2 := newAnnounceReqFull(infoHash, peerID, "stopped", 1000, 500, 500)
	if _, err := w.Announce(context.Background(), req2, u, ip, "", ""); err != nil {
		t.Fatalf("stopped announce: %v", err)
	}
}

const (
	scInfoHash = "sitecomm_testhash001" // exactly 20 chars
	scPeerID   = "-SC0001-000000000000" // exactly 20 chars
	scPeerID2  = "-SC0002-000000000000" // distinct second peer
)

// ── Knuth: algorithmic correctness ───────────────────────────────────────────

// K1: ExpireToken is called exactly once when a tokened user on a FreeNormal
// torrent submits a "completed" event with an upload/download delta.
func TestGUC_SiteComm_Knuth_ExpireTokenExactlyOnceOnCompleted(t *testing.T) {
	w, _, sc := scWorker()
	u := NewUser(UserID(1), true, false)
	scAddTokenedTorrent(w, scInfoHash, TorrentID(1), FreeNormal, u.ID)

	scDoStartThenComplete(t, w, u, scInfoHash, scPeerID)

	sc.mu.Lock()
	got := len(sc.Expired)
	sc.mu.Unlock()
	if got != 1 {
		t.Errorf("ExpireToken call count = %d, want 1", got)
	}
}

// K2: No ExpireToken call when the user has no token, even on a completed event.
func TestGUC_SiteComm_Knuth_NoExpireTokenWhenUserHasNoToken(t *testing.T) {
	w, _, sc := scWorker()
	u := NewUser(UserID(2), true, false)
	// tokenUID=0 → no token registered
	scAddTokenedTorrent(w, scInfoHash, TorrentID(1), FreeNormal, 0)

	scDoStartThenComplete(t, w, u, scInfoHash, scPeerID)

	sc.mu.Lock()
	got := len(sc.Expired)
	sc.mu.Unlock()
	if got != 0 {
		t.Errorf("ExpireToken call count = %d, want 0 (no token)", got)
	}
}

// K3: After ExpireToken is triggered, the token entry is removed from the
// torrent's TokenedUsers map — the in-memory state stays consistent.
func TestGUC_SiteComm_Knuth_TokenRemovedFromMapAfterExpiry(t *testing.T) {
	w, _, _ := scWorker()
	u := NewUser(UserID(3), true, false)
	tor := scAddTokenedTorrent(w, scInfoHash, TorrentID(1), FreeNormal, u.ID)

	// Precondition: token present.
	tor.mu.RLock()
	_, before := tor.TokenedUsers[u.ID]
	tor.mu.RUnlock()
	if !before {
		t.Fatal("precondition: token not in TokenedUsers before complete")
	}

	scDoStartThenComplete(t, w, u, scInfoHash, scPeerID)

	tor.mu.RLock()
	_, after := tor.TokenedUsers[u.ID]
	tor.mu.RUnlock()
	if after {
		t.Error("token still in TokenedUsers after completed announce – invariant violated")
	}
}

// K4: The TorrentID and UserID forwarded to ExpireToken match the objects set
// up in the worker – no ID aliasing or off-by-one.
func TestGUC_SiteComm_Knuth_ExpireTokenIDsMatchSetup(t *testing.T) {
	w, _, sc := scWorker()
	const wantTorrentID = TorrentID(42)
	const wantUserID = UserID(7)
	u := NewUser(wantUserID, true, false)
	scAddTokenedTorrent(w, scInfoHash, wantTorrentID, FreeNormal, wantUserID)

	scDoStartThenComplete(t, w, u, scInfoHash, scPeerID)

	sc.mu.Lock()
	exp := sc.Expired
	sc.mu.Unlock()
	if len(exp) != 1 {
		t.Fatalf("Expired length = %d, want 1", len(exp))
	}
	if UserID(exp[0].TorrentID) != UserID(wantTorrentID) {
		t.Errorf("Expired[0].TorrentID = %v, want %v", exp[0].TorrentID, wantTorrentID)
	}
	if exp[0].UserID != wantUserID {
		t.Errorf("Expired[0].UserID = %v, want %v", exp[0].UserID, wantUserID)
	}
}

// K5 (table-driven): FreeNeutral and FreeFree torrents never trigger ExpireToken
// even when the user holds a token and announces a delta.
func TestGUC_SiteComm_Knuth_FreeTypesSuppressTokenExpiry(t *testing.T) {
	tests := []struct {
		name     string
		freeType FreeType
	}{
		{"FreeNeutral", FreeNeutral},
		{"FreeFree", FreeFree},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			w, _, sc := scWorker()
			u := NewUser(UserID(5), true, false)
			scAddTokenedTorrent(w, scInfoHash, TorrentID(1), tc.freeType, u.ID)

			scDoStartThenComplete(t, w, u, scInfoHash, scPeerID)

			sc.mu.Lock()
			got := len(sc.Expired)
			sc.mu.Unlock()
			if got != 0 {
				t.Errorf("%s: ExpireToken count = %d, want 0", tc.name, got)
			}
		})
	}
}

// ── Turing: termination / halting ────────────────────────────────────────────

// T1: An announce with a NoOpSiteComm returns a valid response and does not
// hang or panic – the code path terminates regardless of SiteComm implementation.
func TestGUC_SiteComm_Turing_AnnounceSucceedsWithNoOpSiteComm(t *testing.T) {
	db := newMockDB()
	w := &Worker{
		Config: &Config{
			AnnounceInterval: 1800,
			NumWantLimit:     50,
			PeersTimeout:     7200,
			AllowPrivateIPs:  true,
		},
		DB:        db,
		SiteComm:  &NoOpSiteComm{},
		Torrents:  NewTorrentList(),
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     &Stats{StartTime: time.Now()},
	}
	u := NewUser(UserID(1), true, false)
	scAddTokenedTorrent(w, scInfoHash, TorrentID(1), FreeNormal, u.ID)

	scDoStartThenComplete(t, w, u, scInfoHash, scPeerID)
	// Reaching here without panic confirms termination.
}

// T2: When the Gazelle endpoint repeatedly returns 5xx, the retry loop exhausts
// after siteCommMaxRetries attempts and returns a non-nil error.
func TestGUC_SiteComm_Turing_RetryExhaustionReturnsError(t *testing.T) {
	origDelay := siteCommBaseDelay
	siteCommBaseDelay = 0
	defer func() { siteCommBaseDelay = origDelay }()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway) // 502 → triggers retry
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, "pw")
	err := g.UpdateStats(1, 2, 3)
	if err == nil {
		t.Error("UpdateStats: expected non-nil error after exhausting retries, got nil")
	}
	got := atomic.LoadInt32(&attempts)
	if got != int32(siteCommMaxRetries) {
		t.Errorf("attempt count = %d, want %d", got, siteCommMaxRetries)
	}
}

// T3: A 4xx response terminates immediately with no retry – the HTTP error
// response loop halts after exactly one attempt.
func TestGUC_SiteComm_Turing_4xxTerminatesWithOneAttempt(t *testing.T) {
	origDelay := siteCommBaseDelay
	siteCommBaseDelay = 0
	defer func() { siteCommBaseDelay = origDelay }()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusForbidden) // 403 → permanent, no retry
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, "pw")
	_ = g.BanUser(99)

	if n := atomic.LoadInt32(&attempts); n != 1 {
		t.Errorf("attempt count = %d, want 1 for 4xx (no retry)", n)
	}
}

// T4: When siteCommMaxRetries is 1, a failing endpoint is attempted exactly once
// with no subsequent retries.
func TestGUC_SiteComm_Turing_MaxRetriesOneProducesOneAttempt(t *testing.T) {
	origMax := siteCommMaxRetries
	origDelay := siteCommBaseDelay
	siteCommMaxRetries = 1
	siteCommBaseDelay = 0
	defer func() {
		siteCommMaxRetries = origMax
		siteCommBaseDelay = origDelay
	}()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, "pw")
	_ = g.NotifyFreeleech(1, 24)

	if n := atomic.LoadInt32(&attempts); n != 1 {
		t.Errorf("attempt count = %d, want 1 when siteCommMaxRetries=1", n)
	}
}

// T5: A network error (unreachable host) causes the retry loop to exhaust and
// return a non-nil error – the caller is not left hanging indefinitely.
func TestGUC_SiteComm_Turing_NetworkErrorReturnsError(t *testing.T) {
	origDelay := siteCommBaseDelay
	siteCommBaseDelay = 0
	defer func() { siteCommBaseDelay = origDelay }()

	// Use a URL pointing to no server so every attempt gets a connection error.
	g := NewGazelleSiteComm("http://127.0.0.1:1", "pw")
	// Force a short HTTP timeout so the test doesn't take long.
	g.client.Timeout = 50 * time.Millisecond

	err := g.ReportAnomaly(1, 0.5)
	if err == nil {
		t.Error("ReportAnomaly: expected non-nil error when host unreachable, got nil")
	}
}

// ── Church: side-effect isolation / functional purity ────────────────────────

// C1: MockSiteComm.reset() correctly isolates state between test phases –
// calls made before reset are not visible after it.
func TestGUC_SiteComm_Church_MockResetIsolatesState(t *testing.T) {
	sc := newMockSiteComm()

	// Phase 1: record some calls.
	sc.ExpireToken(TorrentID(1), UserID(1))
	sc.ExpireToken(TorrentID(2), UserID(2))

	sc.mu.Lock()
	before := len(sc.Expired)
	sc.mu.Unlock()
	if before != 2 {
		t.Fatalf("before reset: Expired length = %d, want 2", before)
	}

	// Phase 2: reset, then verify clean slate.
	sc.reset()
	sc.mu.Lock()
	after := len(sc.Expired)
	sc.mu.Unlock()
	if after != 0 {
		t.Errorf("after reset: Expired length = %d, want 0", after)
	}
}

// C2: Every HTTP request sent by GazelleSiteComm includes the configured
// password parameter – no method may omit it.
func TestGUC_SiteComm_Church_PasswordIncludedInEveryRequest(t *testing.T) {
	const pw = "secret-pw-for-guc-test"
	var received []string // collects the password field from each request

	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		received = append(received, r.FormValue("password"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, pw)
	g.ExpireToken(TorrentID(1), UserID(1))
	_ = g.UpdateStats(1, 2, 3)
	_ = g.BanUser(5)
	_ = g.UnbanUser(5)
	_ = g.ReportAnomaly(5, 0.9)
	_ = g.NotifyFreeleech(10, 24)

	mu.Lock()
	defer mu.Unlock()
	for i, p := range received {
		if p != pw {
			t.Errorf("request[%d]: password = %q, want %q", i, p, pw)
		}
	}
	if len(received) == 0 {
		t.Error("no HTTP requests were recorded")
	}
}

// C3: UpdateStats encodes the three numeric arguments as the correct form
// fields with no drift between the Go int64 values and their wire representations.
func TestGUC_SiteComm_Church_UpdateStatsParamsMatch(t *testing.T) {
	var action, seeders, leechers, completed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		action = r.FormValue("action")
		seeders = r.FormValue("seeders")
		leechers = r.FormValue("leechers")
		completed = r.FormValue("completed")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, "pw")
	if err := g.UpdateStats(111, 222, 333); err != nil {
		t.Fatalf("UpdateStats: %v", err)
	}

	if action != "update_stats" {
		t.Errorf("action = %q, want update_stats", action)
	}
	if seeders != "111" {
		t.Errorf("seeders = %q, want 111", seeders)
	}
	if leechers != "222" {
		t.Errorf("leechers = %q, want 222", leechers)
	}
	if completed != "333" {
		t.Errorf("completed = %q, want 333", completed)
	}
}

// C4: ReportAnomaly formats the score with exactly four decimal places so that
// Gazelle receives a stable, reproducible string representation.
func TestGUC_SiteComm_Church_ReportAnomalyScoreFormattedFourDecimals(t *testing.T) {
	tests := []struct {
		score    float64
		wantStr  string
	}{
		{0.0, "0.0000"},
		{1.0, "1.0000"},
		{0.1234, "0.1234"},
		{3.14159, "3.1416"}, // rounds at 4th decimal
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.wantStr, func(t *testing.T) {
			var gotScore string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				gotScore = r.FormValue("score")
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			g := NewGazelleSiteComm(srv.URL, "pw")
			if err := g.ReportAnomaly(1, tc.score); err != nil {
				t.Fatalf("ReportAnomaly: %v", err)
			}
			if gotScore != tc.wantStr {
				t.Errorf("score wire value = %q, want %q", gotScore, tc.wantStr)
			}
		})
	}
}

// C5: NotifyFreeleech sends the correct action, torrentid and hours fields with
// no accidental field aliasing or integer truncation.
func TestGUC_SiteComm_Church_NotifyFreeleechParamsMatch(t *testing.T) {
	var action, torrentID, hours string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		action = r.FormValue("action")
		torrentID = r.FormValue("torrentid")
		hours = r.FormValue("hours")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, "pw")
	if err := g.NotifyFreeleech(77, 48); err != nil {
		t.Fatalf("NotifyFreeleech: %v", err)
	}

	if action != "notify_freeleech" {
		t.Errorf("action = %q, want notify_freeleech", action)
	}
	if torrentID != "77" {
		t.Errorf("torrentid = %q, want 77", torrentID)
	}
	if hours != "48" {
		t.Errorf("hours = %q, want 48", hours)
	}
}

// ── Gödel: formal consistency / impossible-state detection ───────────────────

// G1: A "started" announce (the peer remains a leecher, Left > 0) must never
// trigger ExpireToken even if the torrent has a token for that user.
func TestGUC_SiteComm_Godel_StartedEventNeverTriggersExpireToken(t *testing.T) {
	w, _, sc := scWorker()
	u := NewUser(UserID(1), true, false)
	scAddTokenedTorrent(w, scInfoHash, TorrentID(1), FreeNormal, u.ID)

	// Only a "started" announce, no completion.
	scDoStart(t, w, u, scInfoHash, scPeerID)

	sc.mu.Lock()
	got := len(sc.Expired)
	sc.mu.Unlock()
	if got != 0 {
		t.Errorf("ExpireToken calls after started-only = %d, want 0", got)
	}
}

// G2: A "stopped" announce must never trigger ExpireToken; the stop path removes
// the peer from the swarm without touching the SiteComm token machinery.
func TestGUC_SiteComm_Godel_StoppedEventNeverTriggersExpireToken(t *testing.T) {
	w, _, sc := scWorker()
	u := NewUser(UserID(1), true, false)
	scAddTokenedTorrent(w, scInfoHash, TorrentID(1), FreeNormal, u.ID)

	scDoStartThenStop(t, w, u, scInfoHash, scPeerID)

	sc.mu.Lock()
	got := len(sc.Expired)
	sc.mu.Unlock()
	if got != 0 {
		t.Errorf("ExpireToken calls after stopped = %d, want 0", got)
	}
}

// G3: A second "completed" announce after the token has already been expired
// must not call ExpireToken again – the system must not enter an impossible
// double-expiry state.
func TestGUC_SiteComm_Godel_TokenAlreadyRemovedNoDoubleExpiry(t *testing.T) {
	w, _, sc := scWorker()
	u := NewUser(UserID(1), true, false)
	scAddTokenedTorrent(w, scInfoHash, TorrentID(1), FreeNormal, u.ID)

	// First complete: token expires once.
	scDoStartThenComplete(t, w, u, scInfoHash, scPeerID)

	// Second "completed" from a second peer: no token for this new user.
	u2 := NewUser(UserID(2), true, false)
	ip := net.ParseIP("10.0.0.2")
	req1 := newAnnounceReqFull(scInfoHash, scPeerID2, "started", 500, 200, 500)
	if _, err := w.Announce(context.Background(), req1, u2, ip, "", ""); err != nil {
		t.Fatalf("u2 start: %v", err)
	}
	req2 := newAnnounceReqFull(scInfoHash, scPeerID2, "completed", 1500, 800, 0)
	if _, err := w.Announce(context.Background(), req2, u2, ip, "", ""); err != nil {
		t.Fatalf("u2 complete: %v", err)
	}

	sc.mu.Lock()
	got := len(sc.Expired)
	sc.mu.Unlock()
	// Only user 1's token was registered; user 2 has no token → still exactly 1.
	if got != 1 {
		t.Errorf("ExpireToken total calls = %d, want exactly 1 (no double-expiry)", got)
	}
}

// G4: Compile-time proof that both NoOpSiteComm and MockSiteComm satisfy the
// SiteCommInterface – if either drifts from the interface definition the test
// file will not compile, making the inconsistency undeniable.
func TestGUC_SiteComm_Godel_InterfaceComplianceCompileTime(t *testing.T) {
	var _ SiteCommInterface = (*NoOpSiteComm)(nil)
	var _ SiteCommInterface = (*MockSiteComm)(nil)
	var _ SiteCommInterface = (*GazelleSiteComm)(nil)
	// Reaching this line proves all three implementations are structurally correct.
	t.Log("all SiteCommInterface implementations compile-time verified")
}

// G5: Concurrent calls to MockSiteComm.ExpireToken must be race-free and the
// total recorded count must equal the number of goroutine invocations – no
// write is lost and no state is corrupted.
func TestGUC_SiteComm_Godel_ConcurrentExpireTokenCallsSafe(t *testing.T) {
	const goroutines = 50
	sc := newMockSiteComm()

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			sc.ExpireToken(TorrentID(i), UserID(i))
		}(i)
	}
	wg.Wait()

	sc.mu.Lock()
	got := len(sc.Expired)
	sc.mu.Unlock()
	if got != goroutines {
		t.Errorf("Expired count = %d, want %d (no writes lost under concurrent access)", got, goroutines)
	}
}
