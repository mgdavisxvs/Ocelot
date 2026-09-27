package tracker

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── GUC routing test helpers ──────────────────────────────────────────────────

var gucIP = net.ParseIP("1.2.3.4")

// gucBody strips HTTP response headers, returning only the body bytes.
func gucBody(resp []byte) []byte {
	if idx := bytes.Index(resp, []byte("\r\n\r\n")); idx >= 0 {
		return resp[idx+4:]
	}
	return resp
}

// gucMakeReq builds an *http.Request with the given path and optional query values.
func gucMakeReq(path string, q url.Values) *http.Request {
	rawURL := path
	if len(q) > 0 {
		rawURL += "?" + q.Encode()
	}
	req, _ := http.NewRequest("GET", rawURL, nil)
	return req
}

// gucPK returns a passkey of exactly n ASCII 'x' characters.
func gucPK(n int) string {
	return strings.Repeat("x", n)
}

// gucAnnounceQ returns a minimal valid announce query for testPasskey/testInfoHash.
func gucAnnounceQ() url.Values {
	return url.Values{
		"info_hash":  {testInfoHash},
		"peer_id":    {testPeerID},
		"port":       {"6881"},
		"uploaded":   {"0"},
		"downloaded": {"0"},
		"left":       {"0"},
		"compact":    {"1"},
		"event":      {"started"},
	}
}

// ── Knuth: algorithmic correctness, loop invariants, data structure invariants ──

// TestGUC_Routing_Knuth_Passkey32CharsAccepted verifies that a passkey of
// exactly 32 characters passes the length guard and reaches the announce
// handler rather than returning "Malformed announce".
func TestGUC_Routing_Knuth_Passkey32CharsAccepted(t *testing.T) {
	f := newTestFixture()
	req := gucMakeReq("/"+testPasskey+"/announce", gucAnnounceQ())
	resp, _ := f.server.handleRequest(req, gucIP)
	if resp == nil {
		t.Fatal("expected non-nil response for 32-char passkey")
	}
	body := gucBody(resp)
	if bytes.Contains(body, []byte("Malformed announce")) {
		t.Errorf("32-char passkey should not trigger Malformed announce; body=%q", body)
	}
}

// TestGUC_Routing_Knuth_PasskeyLengthBoundaries is a table-driven test that
// asserts the passkey-length invariant: lengths other than 32 yield
// "Malformed announce"; 32 does not.
func TestGUC_Routing_Knuth_PasskeyLengthBoundaries(t *testing.T) {
	f := newTestFixture()
	tests := []struct {
		length  int
		wantErr bool
	}{
		{0, true},
		{1, true},
		{31, true},
		{32, false},
		{33, true},
		{64, true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(fmt.Sprintf("len=%d", tc.length), func(t *testing.T) {
			pk := gucPK(tc.length)
			req := gucMakeReq("/"+pk+"/announce", nil)
			resp, _ := f.server.handleRequest(req, gucIP)
			if resp == nil {
				t.Fatalf("len=%d: nil response", tc.length)
			}
			body := gucBody(resp)
			hasMalformed := bytes.Contains(body, []byte("Malformed announce"))
			if tc.wantErr && !hasMalformed {
				t.Errorf("len=%d: expected Malformed announce; body=%q", tc.length, body)
			}
			if !tc.wantErr && hasMalformed {
				t.Errorf("len=%d: unexpected Malformed announce", tc.length)
			}
		})
	}
}

// TestGUC_Routing_Knuth_PathMinimumTwoSegments verifies that paths with fewer
// than two /-separated segments are rejected before the passkey-length check.
func TestGUC_Routing_Knuth_PathMinimumTwoSegments(t *testing.T) {
	f := newTestFixture()
	cases := []struct {
		name string
		path string
	}{
		{"root slash", "/"},
		{"empty path", ""},
		{"passkey only", "/" + testPasskey},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := gucMakeReq(tc.path, nil)
			resp, _ := f.server.handleRequest(req, gucIP)
			if resp == nil {
				t.Fatalf("path=%q: expected non-nil response", tc.path)
			}
			body := gucBody(resp)
			if !bytes.Contains(body, []byte("failure reason")) {
				t.Errorf("path=%q: expected bencoded failure reason; body=%q", tc.path, body)
			}
		})
	}
}

// TestGUC_Routing_Knuth_ActionDispatchAlgorithm verifies that the routing
// switch dispatches each recognized action to its handler by inspecting
// response-body signatures unique to each handler.
func TestGUC_Routing_Knuth_ActionDispatchAlgorithm(t *testing.T) {
	f := newTestFixture()
	tests := []struct {
		action      string
		passkey     string
		bodyContain string
		bodyAbsent  string
	}{
		// announce with known passkey → bencoded announce (no Malformed)
		{"announce", testPasskey, "8:interval", "Malformed announce"},
		// scrape with known passkey → "d5:filesd..." body
		{"scrape", testPasskey, "files", "Malformed announce"},
		// default case → literal "Nothing to see here"
		{"completely_unknown_xyz", testPasskey, "Nothing to see here", ""},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.action, func(t *testing.T) {
			req := gucMakeReq("/"+tc.passkey+"/"+tc.action, gucAnnounceQ())
			resp, _ := f.server.handleRequest(req, gucIP)
			if resp == nil {
				t.Fatalf("action=%q: nil response", tc.action)
			}
			body := gucBody(resp)
			if tc.bodyContain != "" && !bytes.Contains(body, []byte(tc.bodyContain)) {
				t.Errorf("action=%q: body=%q does not contain %q", tc.action, body, tc.bodyContain)
			}
			if tc.bodyAbsent != "" && bytes.Contains(body, []byte(tc.bodyAbsent)) {
				t.Errorf("action=%q: body=%q must not contain %q", tc.action, body, tc.bodyAbsent)
			}
		})
	}
}

// TestGUC_Routing_Knuth_SitePasswordAuthInvariant verifies the update-action
// authentication loop: only the exact SitePassword value authenticates;
// any deviation returns "Authentication failure".
func TestGUC_Routing_Knuth_SitePasswordAuthInvariant(t *testing.T) {
	f := newTestFixture()
	tests := []struct {
		name    string
		passkey string
		wantOK  bool
	}{
		{"exact sitepass", sitePass, true},
		{"user passkey", testPasskey, false},
		{"all zeros 32", strings.Repeat("0", 32), false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := gucMakeReq("/"+tc.passkey+"/update", url.Values{"action": {"noop"}})
			resp, _ := f.server.handleRequest(req, gucIP)
			if resp == nil {
				t.Fatalf("passkey=%q: nil response", tc.passkey)
			}
			body := gucBody(resp)
			hasAuthFail := bytes.Contains(body, []byte("Authentication failure"))
			if tc.wantOK && hasAuthFail {
				t.Errorf("passkey=%q: expected auth OK but got Authentication failure", tc.passkey)
			}
			if !tc.wantOK && !hasAuthFail {
				t.Errorf("passkey=%q: expected Authentication failure; body=%q", tc.passkey, body)
			}
		})
	}
}

// ── Turing: termination conditions, halting behavior ─────────────────────────

// TestGUC_Routing_Turing_RootPathHalts verifies that "/" halts immediately with
// an error rather than entering an unbounded dispatch loop.
func TestGUC_Routing_Turing_RootPathHalts(t *testing.T) {
	f := newTestFixture()
	req := gucMakeReq("/", nil)
	resp, _ := f.server.handleRequest(req, gucIP)
	if resp == nil {
		t.Fatal("handleRequest must not return nil for root path")
	}
	body := gucBody(resp)
	if !bytes.Contains(body, []byte("failure reason")) {
		t.Errorf("expected bencoded failure reason for root path; body=%q", body)
	}
}

// TestGUC_Routing_Turing_UnknownActionHalts verifies that an unrecognized action
// is handled by the switch default case and produces a definite, non-error body.
func TestGUC_Routing_Turing_UnknownActionHalts(t *testing.T) {
	f := newTestFixture()
	req := gucMakeReq("/"+testPasskey+"/nonexistent_action_abc", nil)
	resp, _ := f.server.handleRequest(req, gucIP)
	if resp == nil {
		t.Fatal("handleRequest must not return nil for unknown action")
	}
	body := gucBody(resp)
	if !bytes.Contains(body, []byte("Nothing to see here")) {
		t.Errorf("expected 'Nothing to see here' for unknown action; body=%q", body)
	}
}

// TestGUC_Routing_Turing_ExtraSegmentsIgnored verifies that path segments beyond
// /<passkey>/<action> are discarded and the correct handler is still invoked.
func TestGUC_Routing_Turing_ExtraSegmentsIgnored(t *testing.T) {
	f := newTestFixture()
	before := f.worker.Stats.Announcements.Load()
	req := gucMakeReq("/"+testPasskey+"/announce/extra/ignored", gucAnnounceQ())
	resp, _ := f.server.handleRequest(req, gucIP)
	after := f.worker.Stats.Announcements.Load()
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if after <= before {
		t.Errorf("Announcements counter should have incremented: before=%d after=%d", before, after)
	}
}

// TestGUC_Routing_Turing_UpdateDistinctFromAnnounce verifies that the update
// action is a distinct termination branch: the site password succeeds for update
// but fails announce (unknown user), proving the two routes never alias.
func TestGUC_Routing_Turing_UpdateDistinctFromAnnounce(t *testing.T) {
	f := newTestFixture()
	// sitePass is not in the user list → announce returns passkey error.
	annResp, _ := f.server.handleRequest(
		gucMakeReq("/"+sitePass+"/announce", gucAnnounceQ()), gucIP)
	annBody := gucBody(annResp)
	if !bytes.Contains(annBody, []byte("failure reason")) {
		t.Errorf("sitePass as user passkey should produce failure reason; body=%q", annBody)
	}

	// same passkey for update should authenticate successfully.
	updResp, _ := f.server.handleRequest(
		gucMakeReq("/"+sitePass+"/update", url.Values{"action": {"noop"}}), gucIP)
	updBody := gucBody(updResp)
	if bytes.Contains(updBody, []byte("Authentication failure")) {
		t.Errorf("sitePass for update should auth OK; body=%q", updBody)
	}
}

// TestGUC_Routing_Turing_AllValidActionsTerminate checks that every action in
// the routing switch produces a finite, non-nil response.
func TestGUC_Routing_Turing_AllValidActionsTerminate(t *testing.T) {
	f := newTestFixture()
	cases := []struct {
		passkey string
		action  string
	}{
		{testPasskey, "announce"},
		{testPasskey, "scrape"},
		{sitePass, "update"},
		{sitePass, "stats"},
		{sitePass, "torrents"},
		{sitePass, "peers"},
		{sitePass, "whitelist"},
	}
	q := url.Values{
		"info_hash":  {testInfoHash},
		"peer_id":    {testPeerID},
		"port":       {"6881"},
		"uploaded":   {"0"},
		"downloaded": {"0"},
		"left":       {"0"},
		"compact":    {"1"},
		"action":     {"noop"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.action, func(t *testing.T) {
			req := gucMakeReq("/"+tc.passkey+"/"+tc.action, q)
			resp, _ := f.server.handleRequest(req, gucIP)
			if resp == nil {
				t.Fatalf("action=%q: nil response", tc.action)
			}
			if len(resp) == 0 {
				t.Errorf("action=%q: empty response", tc.action)
			}
		})
	}
}

// ── Church: functional purity, side-effect isolation, referential transparency ──

// TestGUC_Routing_Church_AnnounceIncrementsStat verifies that the announce route
// has exactly one isolated side effect: incrementing the Announcements counter,
// never the Scrapes counter.
func TestGUC_Routing_Church_AnnounceIncrementsStat(t *testing.T) {
	f := newTestFixture()
	beforeAnn := f.worker.Stats.Announcements.Load()
	beforeScr := f.worker.Stats.Scrapes.Load()

	f.server.handleRequest(gucMakeReq("/"+testPasskey+"/announce", gucAnnounceQ()), gucIP)

	afterAnn := f.worker.Stats.Announcements.Load()
	afterScr := f.worker.Stats.Scrapes.Load()
	if afterAnn != beforeAnn+1 {
		t.Errorf("Announcements: want %d, got %d", beforeAnn+1, afterAnn)
	}
	if afterScr != beforeScr {
		t.Errorf("Scrapes must not change on announce: before=%d after=%d", beforeScr, afterScr)
	}
}

// TestGUC_Routing_Church_ScrapeIncrementsStat verifies that the scrape route
// isolates its counter side effect to Scrapes, leaving Announcements unchanged.
func TestGUC_Routing_Church_ScrapeIncrementsStat(t *testing.T) {
	f := newTestFixture()
	beforeAnn := f.worker.Stats.Announcements.Load()
	beforeScr := f.worker.Stats.Scrapes.Load()

	f.server.handleRequest(
		gucMakeReq("/"+testPasskey+"/scrape", url.Values{"info_hash": {testInfoHash}}), gucIP)

	afterAnn := f.worker.Stats.Announcements.Load()
	afterScr := f.worker.Stats.Scrapes.Load()
	if afterScr != beforeScr+1 {
		t.Errorf("Scrapes: want %d, got %d", beforeScr+1, afterScr)
	}
	if afterAnn != beforeAnn {
		t.Errorf("Announcements must not change on scrape: before=%d after=%d", beforeAnn, afterAnn)
	}
}

// TestGUC_Routing_Church_QueryStringPreservedToAnnounce verifies that the
// query string is delivered intact to the announce handler: valid params produce
// a non-error response; a missing required parameter triggers a failure reason.
func TestGUC_Routing_Church_QueryStringPreservedToAnnounce(t *testing.T) {
	f := newTestFixture()
	cases := []struct {
		name        string
		q           url.Values
		wantFailure bool
	}{
		{
			"full valid params",
			gucAnnounceQ(),
			false,
		},
		{
			"missing info_hash",
			url.Values{
				"peer_id":    {testPeerID},
				"port":       {"6881"},
				"uploaded":   {"0"},
				"downloaded": {"0"},
				"left":       {"0"},
				"compact":    {"1"},
			},
			true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := gucMakeReq("/"+testPasskey+"/announce", tc.q)
			resp, _ := f.server.handleRequest(req, gucIP)
			if resp == nil {
				t.Fatalf("%s: nil response", tc.name)
			}
			body := gucBody(resp)
			hasFailure := bytes.Contains(body, []byte("failure reason"))
			if tc.wantFailure && !hasFailure {
				t.Errorf("%s: expected failure reason; body=%q", tc.name, body)
			}
			if !tc.wantFailure && hasFailure {
				t.Errorf("%s: unexpected failure reason; body=%q", tc.name, body)
			}
		})
	}
}

// TestGUC_Routing_Church_KeepaliveDisabledAlwaysCloses verifies referential
// transparency when KeepaliveTimeout == 0: the Connection header is irrelevant
// and httpClose is always true, isolating close semantics from header state.
func TestGUC_Routing_Church_KeepaliveDisabledAlwaysCloses(t *testing.T) {
	f := newTestFixture() // newTestConfig sets KeepaliveTimeout = 0
	headers := []struct {
		key   string
		value string
	}{
		{"Connection", "keep-alive"},
		{"Connection", "close"},
		{"Connection", ""},
	}
	for _, hdr := range headers {
		req := gucMakeReq("/"+testPasskey+"/announce", gucAnnounceQ())
		if hdr.value != "" {
			req.Header.Set(hdr.key, hdr.value)
		}
		_, httpClose := f.server.handleRequest(req, gucIP)
		if !httpClose {
			t.Errorf("Connection=%q: expected httpClose=true when KeepaliveTimeout=0", hdr.value)
		}
	}
}

// TestGUC_Routing_Church_KeepaliveEnabledHonorsConnectionHeader verifies that
// when KeepaliveTimeout > 0, the Connection: close header is the sole input
// that sets httpClose=true on HTTP/1.1 requests; absent header keeps alive.
func TestGUC_Routing_Church_KeepaliveEnabledHonorsConnectionHeader(t *testing.T) {
	f := newTestFixture()
	cfg := newTestConfig()
	cfg.KeepaliveTimeout = 30 * time.Second
	f.server.config = cfg

	// HTTP/1.1 + Connection: close → httpClose must be true.
	reqClose := gucMakeReq("/"+testPasskey+"/announce", gucAnnounceQ())
	reqClose.Proto = "HTTP/1.1"
	reqClose.ProtoMajor, reqClose.ProtoMinor = 1, 1
	reqClose.Header.Set("Connection", "close")
	_, closeFlag := f.server.handleRequest(reqClose, gucIP)
	if !closeFlag {
		t.Error("Connection: close on HTTP/1.1 should set httpClose=true")
	}

	// HTTP/1.1 + no Connection header → httpClose must be false.
	reqKeep := gucMakeReq("/"+testPasskey+"/announce", gucAnnounceQ())
	reqKeep.Proto = "HTTP/1.1"
	reqKeep.ProtoMajor, reqKeep.ProtoMinor = 1, 1
	_, keepFlag := f.server.handleRequest(reqKeep, gucIP)
	if keepFlag {
		t.Error("HTTP/1.1 without Connection: close should set httpClose=false when keepalive enabled")
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state detection ──

// TestGUC_Routing_Godel_ConcurrentRequestsNoRace verifies the absence of data
// races when many goroutines call handleRequest simultaneously on the same Server.
// Run with: go test -race ./tracker/ -run TestGUC_Routing_Godel_ConcurrentRequestsNoRace
func TestGUC_Routing_Godel_ConcurrentRequestsNoRace(t *testing.T) {
	f := newTestFixture()
	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			var req *http.Request
			switch i % 3 {
			case 0:
				req = gucMakeReq("/"+testPasskey+"/announce", gucAnnounceQ())
			case 1:
				req = gucMakeReq("/"+testPasskey+"/scrape",
					url.Values{"info_hash": {testInfoHash}})
			default:
				req = gucMakeReq("/"+testPasskey+"/completely_unknown", nil)
			}
			resp, _ := f.server.handleRequest(req, gucIP)
			if resp == nil {
				t.Errorf("goroutine %d: nil response", i)
			}
		}()
	}
	wg.Wait()
}

// TestGUC_Routing_Godel_ErrorResponsesContainFailureReason asserts the formal
// invariant that every routing-layer error encodes as a bencoded "failure reason"
// key—no error is silently dropped or disguised as a success body.
func TestGUC_Routing_Godel_ErrorResponsesContainFailureReason(t *testing.T) {
	f := newTestFixture()
	errorCases := []struct {
		name string
		path string
	}{
		{"empty passkey slot", "/" + gucPK(0) + "/announce"},
		{"31-char passkey", "/" + gucPK(31) + "/announce"},
		{"33-char passkey", "/" + gucPK(33) + "/announce"},
		{"no action", "/" + testPasskey},
		{"root path", "/"},
	}
	for _, tc := range errorCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := gucMakeReq(tc.path, nil)
			resp, _ := f.server.handleRequest(req, gucIP)
			if resp == nil {
				t.Fatalf("%s: nil response", tc.name)
			}
			body := gucBody(resp)
			if !bytes.Contains(body, []byte("failure reason")) {
				t.Errorf("%s: expected bencoded failure reason; body=%q", tc.name, body)
			}
		})
	}
}

// TestGUC_Routing_Godel_ResponseNeverNil establishes the base invariant that
// handleRequest never returns nil under any routing path—nil is an impossible
// state that this test detects.
func TestGUC_Routing_Godel_ResponseNeverNil(t *testing.T) {
	f := newTestFixture()
	paths := []string{
		"/",
		"/" + testPasskey,
		"/" + testPasskey + "/announce",
		"/" + testPasskey + "/scrape",
		"/" + testPasskey + "/anything",
		"/" + sitePass + "/update",
		"/" + sitePass + "/stats",
		"/" + sitePass + "/torrents",
		"/" + sitePass + "/peers",
		"/" + sitePass + "/whitelist",
		"/" + gucPK(31) + "/announce",
		"/" + gucPK(33) + "/announce",
	}
	for _, p := range paths {
		req := gucMakeReq(p, url.Values{"info_hash": {testInfoHash}})
		resp, _ := f.server.handleRequest(req, gucIP)
		if resp == nil {
			t.Errorf("path=%q: handleRequest returned nil — impossible state", p)
		}
	}
}

// TestGUC_Routing_Godel_PasskeyAndSitePasswordMutuallyExclusive preserves the
// credential-domain consistency invariant: a user passkey can announce but must
// not authenticate as site admin; these states must never overlap.
func TestGUC_Routing_Godel_PasskeyAndSitePasswordMutuallyExclusive(t *testing.T) {
	if testPasskey == sitePass {
		t.Skip("fixture has identical user passkey and site password; invariant not testable")
	}
	f := newTestFixture()

	// testPasskey IS in the user list → announce should not say "Passkey not found".
	annResp, _ := f.server.handleRequest(
		gucMakeReq("/"+testPasskey+"/announce", gucAnnounceQ()), gucIP)
	annBody := gucBody(annResp)
	if bytes.Contains(annBody, []byte("Passkey not found")) {
		t.Errorf("testPasskey should be found for announce; body=%q", annBody)
	}

	// testPasskey is NOT the site password → update must return Authentication failure.
	updResp, _ := f.server.handleRequest(
		gucMakeReq("/"+testPasskey+"/update", url.Values{"action": {"noop"}}), gucIP)
	updBody := gucBody(updResp)
	if !bytes.Contains(updBody, []byte("Authentication failure")) {
		t.Errorf("testPasskey must not authenticate as site admin; body=%q", updBody)
	}
}

// TestGUC_Routing_Godel_ScrapeDoesNotIncrementAnnouncements preserves the
// counter-independence invariant: Scrapes and Announcements are orthogonal
// monotone counters whose values must never cross-contaminate.
func TestGUC_Routing_Godel_ScrapeDoesNotIncrementAnnouncements(t *testing.T) {
	f := newTestFixture()
	beforeAnn := f.worker.Stats.Announcements.Load()
	beforeScr := f.worker.Stats.Scrapes.Load()

	const n = 5
	for i := 0; i < n; i++ {
		f.server.handleRequest(
			gucMakeReq("/"+testPasskey+"/scrape", url.Values{"info_hash": {testInfoHash}}), gucIP)
	}

	afterAnn := f.worker.Stats.Announcements.Load()
	afterScr := f.worker.Stats.Scrapes.Load()
	if afterAnn != beforeAnn {
		t.Errorf("Announcements must not change after %d scrapes: before=%d after=%d",
			n, beforeAnn, afterAnn)
	}
	if afterScr != beforeScr+n {
		t.Errorf("Scrapes must be exactly %d more: before=%d after=%d", n, beforeScr, afterScr)
	}
}
