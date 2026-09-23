package tracker

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ── bencodedAnnounceResponse ──────────────────────────────────────────────────

func TestBencodedAnnounceResponse_NoPeers(t *testing.T) {
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Complete:    3,
		Incomplete:  1,
		Peers:       []byte{},
	}
	raw := f.server.bencodedAnnounceResponse(resp, true)
	body := httpBody(raw)

	if !strings.Contains(body, "i1800e") {
		t.Errorf("missing interval i1800e: %s", body)
	}
	if !strings.Contains(body, "i3e") {
		t.Errorf("missing complete i3e: %s", body)
	}
	if !strings.Contains(body, "0:") {
		t.Errorf("missing empty peers 0:: %s", body)
	}
	if strings.Contains(body, "warning") {
		t.Error("no warning should be present")
	}
}

func TestBencodedAnnounceResponse_WithPeers(t *testing.T) {
	f := newTestFixture()
	peers := CompactIPPort(net.ParseIP("1.2.3.4"), 6881)
	resp := &AnnounceResponse{
		Interval:    900,
		MinInterval: 900,
		Complete:    1,
		Incomplete:  0,
		Peers:       peers,
	}
	raw := f.server.bencodedAnnounceResponse(resp, true)
	body := httpBody(raw)

	// 6 bytes of peers → "6:<bytes>"
	if !strings.Contains(body, "6:") {
		t.Errorf("missing 6-byte peer prefix: %s", body)
	}
}

func TestBencodedAnnounceResponse_WithWarning(t *testing.T) {
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Warning:     "IPv6 not supported",
	}
	raw := f.server.bencodedAnnounceResponse(resp, true)
	body := httpBody(raw)

	if !strings.Contains(body, "warning message") {
		t.Errorf("warning key missing: %s", body)
	}
	if !strings.Contains(body, "IPv6 not supported") {
		t.Errorf("warning text missing: %s", body)
	}
}

// ── Stats API ─────────────────────────────────────────────────────────────────

func TestStatsAPI_ReturnsJSON(t *testing.T) {
	f := newTestFixture()
	req := buildAPIRequest(sitePass, "stats", "")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("stats API not valid JSON: %v — body: %s", err, body)
	}
	for _, field := range []string{"torrent_count", "user_count", "announcements", "uptime_seconds"} {
		if _, ok := m[field]; !ok {
			t.Errorf("stats response missing field %q", field)
		}
	}
}

func TestStatsAPI_WrongPassword(t *testing.T) {
	f := newTestFixture()
	req := buildAPIRequest(strings.Repeat("x", 32), "stats", "")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	if !strings.Contains(string(raw), "Authentication failure") {
		t.Error("wrong password should yield Authentication failure")
	}
}

// ── Torrents API ──────────────────────────────────────────────────────────────

func TestTorrentsAPI_ReturnsList(t *testing.T) {
	f := newTestFixture()
	req := buildAPIRequest(sitePass, "torrents", "limit=50")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	var list []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("torrents API not valid JSON array: %v — body: %s", err, body)
	}
	// fixture seeds one torrent
	if len(list) != 1 {
		t.Errorf("torrent count = %d, want 1", len(list))
	}
	entry := list[0]
	for _, field := range []string{"id", "seeders", "leechers", "completed", "free_type"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("torrent entry missing field %q", field)
		}
	}
}

// ── Peers API ────────────────────────────────────────────────────────────────

func TestPeersAPI_KnownHash(t *testing.T) {
	f := newTestFixture()
	tor, _ := f.worker.Torrents.Get(testInfoHash)
	ip := net.ParseIP("10.0.0.2")
	tor.Seeders.Set("s1", &Peer{
		UserID: 99, IP: ip, Port: 7000, Visible: true,
		IPPort: CompactIPPort(ip, 7000), Uploaded: 1024,
	})

	req := buildAPIRequest(sitePass, "peers", "info_hash="+urlEscapeRaw(testInfoHash)+"&limit=10")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	var list []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("peers API not valid JSON: %v — body: %s", err, body)
	}
	if len(list) != 1 {
		t.Errorf("peer count = %d, want 1", len(list))
	}
}

func TestPeersAPI_MissingInfoHash(t *testing.T) {
	f := newTestFixture()
	req := buildAPIRequest(sitePass, "peers", "")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	if !strings.Contains(string(raw), "Missing info_hash") {
		t.Errorf("should error on missing info_hash: %s", string(raw))
	}
}

func TestPeersAPI_UnknownHash(t *testing.T) {
	f := newTestFixture()
	req := buildAPIRequest(sitePass, "peers", "info_hash=nosuchhashnnnnnnnnnnnnnnn")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	// unknown hash → error response (torrent not found)
	if !strings.Contains(string(raw), "failure reason") && !strings.Contains(string(raw), "not found") {
		t.Errorf("unknown hash should error: %s", string(raw))
	}
}

// ── Whitelist API ─────────────────────────────────────────────────────────────

func TestWhitelistAPI_Empty(t *testing.T) {
	f := newTestFixture()
	req := buildAPIRequest(sitePass, "whitelist", "")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)
	if body != "[]" {
		t.Errorf("empty whitelist API = %q, want \"[]\"", body)
	}
}

func TestWhitelistAPI_WithEntries(t *testing.T) {
	f := newTestFixture()
	f.worker.Whitelist.Add("-qB")
	f.worker.Whitelist.Add("-TR")

	req := buildAPIRequest(sitePass, "whitelist", "")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	var list []string
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("whitelist API not valid JSON: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("whitelist count = %d, want 2", len(list))
	}
}

// ── queryInt ──────────────────────────────────────────────────────────────────

func TestQueryInt_Valid(t *testing.T) {
	req, _ := http.NewRequest("GET", "/?limit=25", nil)
	if v := queryInt(req, "limit", 100); v != 25 {
		t.Errorf("queryInt = %d, want 25", v)
	}
}

func TestQueryInt_Missing(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	if v := queryInt(req, "limit", 100); v != 100 {
		t.Errorf("queryInt missing = %d, want default 100", v)
	}
}

func TestQueryInt_Invalid(t *testing.T) {
	req, _ := http.NewRequest("GET", "/?limit=notanumber", nil)
	if v := queryInt(req, "limit", 100); v != 100 {
		t.Errorf("queryInt invalid = %d, want default 100", v)
	}
}

func TestQueryInt_Zero(t *testing.T) {
	req, _ := http.NewRequest("GET", "/?limit=0", nil)
	if v := queryInt(req, "limit", 100); v != 100 {
		t.Errorf("queryInt zero should return default 100, got %d", v)
	}
}

// ── Unknown route ─────────────────────────────────────────────────────────────

func TestRouting_UnknownAction(t *testing.T) {
	f := newTestFixture()
	req, _ := http.NewRequest("GET", "/"+testPasskey+"/foobar", nil)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)
	if !strings.Contains(body, "Nothing to see here") {
		t.Errorf("unknown action should return 'Nothing to see here': %s", body)
	}
}

func TestRouting_TooFewPathParts(t *testing.T) {
	f := newTestFixture()
	req, _ := http.NewRequest("GET", "/onlyone", nil)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	if !strings.Contains(string(raw), "Malformed") {
		t.Errorf("missing action should be malformed: %s", string(raw))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// buildAPIRequest constructs an HTTP request for admin API endpoints
// (stats, torrents, peers, whitelist).
func buildAPIRequest(sitePassword, action, queryExtra string) *http.Request {
	path := "/" + sitePassword + "/" + action
	if queryExtra != "" {
		path += "?" + queryExtra
	}
	req, _ := http.NewRequest("GET", path, nil)
	return req
}

// ── handleAnnounce ────────────────────────────────────────────────────────────

func TestHandleAnnounce_Success(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 1<<20)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)
	if !strings.Contains(body, "interval") {
		t.Errorf("expected interval in announce response: %s", body)
	}
	if strings.Contains(body, "failure reason") {
		t.Errorf("unexpected failure in announce: %s", body)
	}
}

func TestHandleAnnounce_InvalidPasskey_Returns_Failure(t *testing.T) {
	f := newTestFixture()
	badPasskey := strings.Repeat("z", 32)
	req := buildAnnounceURL(badPasskey, testInfoHash, testPeerID, "started", 0, 0, 1<<20)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)
	if !strings.Contains(body, "failure reason") {
		t.Errorf("expected failure reason for unknown passkey: %s", body)
	}
}

func TestHandleAnnounce_ParseError_MissingPort(t *testing.T) {
	f := newTestFixture()
	// Build URL without port — ParseAnnounceParams should error.
	q := "info_hash=" + urlEscapeRaw(testInfoHash) + "&peer_id=" + urlEscapeRaw(testPeerID) + "&compact=1&uploaded=0&downloaded=0&left=1024"
	req, _ := http.NewRequest("GET", "/"+testPasskey+"/announce?"+q, nil)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)
	if !strings.Contains(body, "failure reason") {
		t.Errorf("expected failure for missing port: %s", body)
	}
}

func TestHandleAnnounce_XForwardedFor_WithComma(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 1<<20)
	req.Header.Set("X-Forwarded-For", "5.6.7.8,9.10.11.12")
	// Pass 0.0.0.0 as clientIP so the XFF branch fires.
	raw, _ := f.server.handleRequest(req, net.ParseIP("0.0.0.0"))
	body := httpBody(raw)
	if strings.Contains(body, "failure reason") {
		t.Errorf("unexpected failure with XFF: %s", body)
	}
}

func TestHandleAnnounce_XForwardedFor_NoComma(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 1<<20)
	req.Header.Set("X-Forwarded-For", "5.6.7.8")
	raw, _ := f.server.handleRequest(req, net.ParseIP("0.0.0.0"))
	body := httpBody(raw)
	if strings.Contains(body, "failure reason") {
		t.Errorf("unexpected failure with XFF (no comma): %s", body)
	}
}

// ── handleRequest — KeepaliveTimeout branches ─────────────────────────────────

func TestHandleRequest_KeepaliveTimeout_HTTP10_AlwaysClose(t *testing.T) {
	f := newTestFixture()
	f.server.config.KeepaliveTimeout = time.Second
	req, _ := http.NewRequest("GET", "/"+testPasskey+"/foobar", nil)
	req.Proto = "HTTP/1.0"
	req.ProtoMajor = 1
	req.ProtoMinor = 0
	_, httpClose := f.server.handleRequest(req, net.ParseIP(testIP))
	if !httpClose {
		t.Error("HTTP/1.0 with KeepaliveTimeout should always close")
	}
}

func TestHandleRequest_KeepaliveTimeout_HTTP11_ConnectionClose(t *testing.T) {
	f := newTestFixture()
	f.server.config.KeepaliveTimeout = time.Second
	req, _ := http.NewRequest("GET", "/"+testPasskey+"/foobar", nil)
	req.Proto = "HTTP/1.1"
	req.ProtoMajor = 1
	req.ProtoMinor = 1
	req.Header.Set("Connection", "close")
	_, httpClose := f.server.handleRequest(req, net.ParseIP(testIP))
	if !httpClose {
		t.Error("HTTP/1.1 Connection:close with KeepaliveTimeout should close")
	}
}

func TestHandleRequest_KeepaliveTimeout_HTTP11_KeepAlive(t *testing.T) {
	f := newTestFixture()
	f.server.config.KeepaliveTimeout = time.Second
	req, _ := http.NewRequest("GET", "/"+testPasskey+"/foobar", nil)
	req.Proto = "HTTP/1.1"
	req.ProtoMajor = 1
	req.ProtoMinor = 1
	// No Connection: close header → keepalive
	_, httpClose := f.server.handleRequest(req, net.ParseIP(testIP))
	if httpClose {
		t.Error("HTTP/1.1 without Connection:close should keep alive")
	}
}

// httpBody strips the HTTP response headers and returns only the body.
func httpBody(raw []byte) string {
	s := string(raw)
	idx := strings.Index(s, "\r\n\r\n")
	if idx < 0 {
		return s
	}
	return s[idx+4:]
}

// urlEscapeRaw percent-encodes non-URL-safe bytes for use in query strings.
func urlEscapeRaw(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteByte(c)
		} else {
			b.WriteString("%" + hexByte(c))
		}
	}
	return b.String()
}

func hexByte(b byte) string {
	const hex = "0123456789ABCDEF"
	return string([]byte{hex[b>>4], hex[b&0xF]})
}

// ── Authentication failure for admin endpoints ────────────────────────────────

func TestTorrentsAPI_WrongPassword(t *testing.T) {
	f := newTestFixture()
	req := buildAPIRequest(strings.Repeat("x", 32), "torrents", "")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	if !strings.Contains(string(raw), "Authentication failure") {
		t.Errorf("torrents wrong password should yield Authentication failure: %s", string(raw))
	}
}

func TestPeersAPI_WrongPassword(t *testing.T) {
	f := newTestFixture()
	req := buildAPIRequest(strings.Repeat("x", 32), "peers", "info_hash="+urlEscapeRaw(testInfoHash))
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	if !strings.Contains(string(raw), "Authentication failure") {
		t.Errorf("peers wrong password should yield Authentication failure: %s", string(raw))
	}
}

func TestWhitelistAPI_WrongPassword(t *testing.T) {
	f := newTestFixture()
	req := buildAPIRequest(strings.Repeat("x", 32), "whitelist", "")
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	if !strings.Contains(string(raw), "Authentication failure") {
		t.Errorf("whitelist wrong password should yield Authentication failure: %s", string(raw))
	}
}
