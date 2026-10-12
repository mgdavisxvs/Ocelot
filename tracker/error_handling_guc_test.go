package tracker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// gucHTTPBody extracts the HTTP response body from raw HTTP/1.1 response bytes.
func gucHTTPBody(raw []byte) []byte {
	sep := []byte("\r\n\r\n")
	idx := bytes.Index(raw, sep)
	if idx < 0 {
		return raw
	}
	return raw[idx+len(sep):]
}

// gucDecodeUpdateResp unmarshals a JSON update response, failing the test on error.
func gucDecodeUpdateResp(t *testing.T, data []byte) UpdateResponse {
	t.Helper()
	var resp UpdateResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("failed to unmarshal UpdateResponse: %v (data=%q)", err, data)
	}
	return resp
}

// ── Knuth: algorithmic correctness, loop invariants, data-structure invariants ─

// TestGUC_ParseAnnounce_MissingRequiredFields verifies that ParseAnnounceParams
// returns a descriptive error when any one of the three mandatory fields
// (info_hash, peer_id, port) is absent. The parse loop must not silently
// produce a partially-populated struct.
func TestGUC_ParseAnnounce_MissingRequiredFields(t *testing.T) {
	clientIP := net.ParseIP("1.2.3.4")

	tests := []struct {
		name   string
		params url.Values
		errSub string
	}{
		{
			name:   "missing_info_hash",
			params: url.Values{"peer_id": {testPeerID}, "port": {"6881"}},
			errSub: "missing info_hash",
		},
		{
			name:   "missing_peer_id",
			params: url.Values{"info_hash": {testInfoHash}, "port": {"6881"}},
			errSub: "missing peer_id",
		},
		{
			name:   "missing_port",
			params: url.Values{"info_hash": {testInfoHash}, "peer_id": {testPeerID}},
			errSub: "missing port",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ParseAnnounceParams(tc.params, clientIP)
			if err == nil {
				t.Errorf("expected error containing %q, got nil (req=%+v)", tc.errSub, req)
				return
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("error %q does not contain expected substring %q", err.Error(), tc.errSub)
			}
		})
	}
}

// TestGUC_ParseAnnounce_InvalidPort verifies that non-numeric, overflow, and
// negative port values are rejected. The uint16 parse must guard against all
// out-of-domain inputs.
func TestGUC_ParseAnnounce_InvalidPort(t *testing.T) {
	clientIP := net.ParseIP("1.2.3.4")
	base := url.Values{"info_hash": {testInfoHash}, "peer_id": {testPeerID}}

	badPorts := []string{"notaport", "99999", "-1", "6.5", "0x1A"}

	for _, p := range badPorts {
		params := make(url.Values)
		for k, v := range base {
			params[k] = v
		}
		params.Set("port", p)
		_, err := ParseAnnounceParams(params, clientIP)
		if err == nil {
			t.Errorf("port=%q: expected parse error, got nil", p)
		}
	}
}

// TestGUC_HandleUpdate_UnknownAction verifies that an unrecognised action token
// falls through the switch to the default case and returns JSON with
// success=false and status="error" — the algorithmic contract of HandleUpdate.
func TestGUC_HandleUpdate_UnknownAction(t *testing.T) {
	f := newTestFixture()
	req, _ := http.NewRequest("GET", "/update?action=does_not_exist", nil)
	data, err := f.worker.HandleUpdate(req)
	if data == nil {
		t.Fatal("HandleUpdate returned nil data for unknown action")
	}
	resp := gucDecodeUpdateResp(t, data)
	if resp.Success {
		t.Errorf("unknown action: expected success=false, got true")
	}
	if resp.Status != "error" {
		t.Errorf("unknown action: expected status=error, got %q", resp.Status)
	}
	if err == nil {
		t.Errorf("unknown action: expected non-nil error return value")
	}
}

// TestGUC_UpdateSuccess_JSONStructure verifies the structural invariant of the
// success path: updateSuccess must encode success=true, status="ok", and the
// provided message into the JSON payload.
func TestGUC_UpdateSuccess_JSONStructure(t *testing.T) {
	f := newTestFixture()
	msg := "operation completed successfully"
	data, err := f.worker.updateSuccess(msg)
	if err != nil {
		t.Fatalf("updateSuccess returned unexpected error: %v", err)
	}
	resp := gucDecodeUpdateResp(t, data)
	if !resp.Success {
		t.Errorf("expected success=true, got false")
	}
	if resp.Status != "ok" {
		t.Errorf("expected status=ok, got %q", resp.Status)
	}
	if resp.Message != msg {
		t.Errorf("expected message=%q, got %q", msg, resp.Message)
	}
}

// TestGUC_ParseAnnounce_PortBoundary verifies port boundary conditions:
// 0 and 65535 are valid uint16 values and must parse without error,
// while 65536 and beyond must be rejected.
func TestGUC_ParseAnnounce_PortBoundary(t *testing.T) {
	clientIP := net.ParseIP("1.2.3.4")
	base := url.Values{"info_hash": {testInfoHash}, "peer_id": {testPeerID}}

	tests := []struct {
		port    string
		wantErr bool
	}{
		{"0", false},
		{"6881", false},
		{"65535", false},
		{"65536", true},
		{"100000", true},
	}

	for _, tc := range tests {
		params := make(url.Values)
		for k, v := range base {
			params[k] = v
		}
		params.Set("port", tc.port)
		_, err := ParseAnnounceParams(params, clientIP)
		gotErr := (err != nil)
		if gotErr != tc.wantErr {
			t.Errorf("port=%s: wantErr=%v gotErr=%v (err=%v)", tc.port, tc.wantErr, gotErr, err)
		}
	}
}

// ── Turing: termination conditions, halting behavior ─────────────────────────

// TestGUC_AnnounceError_BencodeFailureReason verifies that a request with an
// unrecognised passkey causes handleRequest to return and produce a response
// containing a bencode "failure reason" key — the call must terminate and
// never block.
func TestGUC_AnnounceError_BencodeFailureReason(t *testing.T) {
	f := newTestFixture()
	clientIP := net.ParseIP("1.2.3.4")

	// 32-char passkey not present in the user list.
	// Use URL-safe ASCII values for query params; the error fires on the passkey
	// check before announce params are ever parsed.
	badPasskey := strings.Repeat("x", 32)
	req, _ := http.NewRequest("GET", "/"+badPasskey+"/announce?info_hash=abcdef&peer_id=pqrstuv&port=6881&compact=1", nil)
	raw, _ := f.server.handleRequest(req, clientIP)

	body := gucHTTPBody(raw)
	if !bytes.Contains(body, []byte("failure reason")) {
		t.Errorf("expected bencode failure reason in announce error response, got: %q", body)
	}
}

// TestGUC_ScrapeError_BadPasskey verifies that a scrape request with an
// unrecognised passkey terminates and returns a bencode error response,
// maintaining protocol consistency with the announce error path.
func TestGUC_ScrapeError_BadPasskey(t *testing.T) {
	f := newTestFixture()
	clientIP := net.ParseIP("1.2.3.4")

	// Use an ASCII-safe info_hash; the error fires on the passkey check.
	badPasskey := strings.Repeat("y", 32)
	req, _ := http.NewRequest("GET", "/"+badPasskey+"/scrape?info_hash=asciiinfohash", nil)
	raw, _ := f.server.handleRequest(req, clientIP)

	body := gucHTTPBody(raw)
	if !bytes.Contains(body, []byte("failure reason")) {
		t.Errorf("bad passkey scrape: expected bencode failure reason, got: %q", body)
	}
}

// TestGUC_HandleUpdate_DBErrorPropagated verifies that a database error during
// add_torrent causes HandleUpdate to terminate (not block) and propagate the
// failure as JSON with success=false, rather than silently swallowing the error.
func TestGUC_HandleUpdate_DBErrorPropagated(t *testing.T) {
	f := newTestFixture()
	f.db.ReturnErr = fmt.Errorf("simulated DB connection failure")

	// Use a hash that does not already exist in the fixture torrent list.
	newHash := "qqqqqqqqqqqqqqqqqqqqq"
	req, _ := http.NewRequest("GET", "/update?action=add_torrent&id=99&info_hash="+newHash, nil)
	data, err := f.worker.HandleUpdate(req)

	if err == nil {
		t.Error("expected non-nil error return when DB fails")
	}
	if data == nil {
		t.Fatal("HandleUpdate returned nil data on DB error")
	}
	resp := gucDecodeUpdateResp(t, data)
	if resp.Success {
		t.Errorf("DB error: expected success=false, got true")
	}
	if resp.Status != "error" {
		t.Errorf("DB error: expected status=error, got %q", resp.Status)
	}
}

// TestGUC_HandleRequest_ShortPath verifies that HTTP requests whose path has
// fewer than two segments (passkey + action) are rejected immediately with a
// bencode error and do not hang or panic.
func TestGUC_HandleRequest_ShortPath(t *testing.T) {
	f := newTestFixture()
	clientIP := net.ParseIP("1.2.3.4")

	tests := []struct {
		name string
		path string
	}{
		{"root_only", "/"},
		{"single_segment", "/onlyone"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", tc.path, nil)
			raw, _ := f.server.handleRequest(req, clientIP)
			body := gucHTTPBody(raw)
			if !bytes.Contains(body, []byte("failure reason")) {
				t.Errorf("path=%q: expected bencode failure reason, got: %q", tc.path, body)
			}
		})
	}
}

// TestGUC_HandleUpdate_MissingActionParam verifies that a request with no
// action parameter falls through to the default case and terminates with a
// JSON error response (not a panic or infinite loop).
func TestGUC_HandleUpdate_MissingActionParam(t *testing.T) {
	f := newTestFixture()
	req, _ := http.NewRequest("GET", "/update", nil)
	data, err := f.worker.HandleUpdate(req)
	if data == nil {
		t.Fatal("HandleUpdate returned nil data for missing action")
	}
	resp := gucDecodeUpdateResp(t, data)
	if resp.Success {
		t.Errorf("empty action: expected success=false, got true")
	}
	if err == nil {
		t.Errorf("empty action: expected non-nil error return value")
	}
}

// ── Church: functional purity, side-effect isolation ─────────────────────────

// TestGUC_ParseAnnounce_Idempotent verifies that ParseAnnounceParams is
// referentially transparent: identical inputs produce structurally identical
// outputs on repeated invocations without any hidden mutable state.
func TestGUC_ParseAnnounce_Idempotent(t *testing.T) {
	clientIP := net.ParseIP("1.2.3.4")
	params := url.Values{
		"info_hash":  {testInfoHash},
		"peer_id":    {testPeerID},
		"port":       {"6881"},
		"uploaded":   {"1000"},
		"downloaded": {"500"},
		"left":       {"0"},
		"compact":    {"1"},
		"event":      {"started"},
	}

	req1, err1 := ParseAnnounceParams(params, clientIP)
	req2, err2 := ParseAnnounceParams(params, clientIP)

	if (err1 == nil) != (err2 == nil) {
		t.Fatalf("idempotency violated: first err=%v second err=%v", err1, err2)
	}
	if err1 != nil {
		return
	}
	if req1.InfoHash != req2.InfoHash {
		t.Errorf("InfoHash not idempotent: %q vs %q", req1.InfoHash, req2.InfoHash)
	}
	if req1.Port != req2.Port {
		t.Errorf("Port not idempotent: %d vs %d", req1.Port, req2.Port)
	}
	if req1.Uploaded != req2.Uploaded {
		t.Errorf("Uploaded not idempotent: %d vs %d", req1.Uploaded, req2.Uploaded)
	}
	if req1.Left != req2.Left {
		t.Errorf("Left not idempotent: %d vs %d", req1.Left, req2.Left)
	}
	if req1.Event != req2.Event {
		t.Errorf("Event not idempotent: %q vs %q", req1.Event, req2.Event)
	}
}

// TestGUC_ErrorResponse_PureFunction verifies that the server's errorResponse
// helper is a pure function: repeated calls with identical arguments return
// byte-for-byte identical output with no observable side effects.
func TestGUC_ErrorResponse_PureFunction(t *testing.T) {
	f := newTestFixture()
	msg := "test error: something went wrong"

	out1 := f.server.errorResponse(msg, true)
	out2 := f.server.errorResponse(msg, true)
	out3 := f.server.errorResponse(msg, true)

	if !bytes.Equal(out1, out2) {
		t.Errorf("errorResponse not pure: call1 != call2")
	}
	if !bytes.Equal(out2, out3) {
		t.Errorf("errorResponse not pure: call2 != call3")
	}
	// Distinct messages must produce distinct outputs (no collision).
	out4 := f.server.errorResponse("different message", true)
	if bytes.Equal(out1, out4) {
		t.Errorf("errorResponse: distinct messages produced identical output")
	}
}

// TestGUC_ParseAnnounce_NegativeNumericsClamped verifies that negative uploaded,
// downloaded, left, and corrupt values are clamped to zero by the parser.
// This enforces the side-effect-free invariant that announce stats are never
// in an impossible negative state after parsing.
func TestGUC_ParseAnnounce_NegativeNumericsClamped(t *testing.T) {
	clientIP := net.ParseIP("1.2.3.4")
	params := url.Values{
		"info_hash":  {testInfoHash},
		"peer_id":    {testPeerID},
		"port":       {"6881"},
		"uploaded":   {"-500"},
		"downloaded": {"-200"},
		"left":       {"-1"},
		"corrupt":    {"-100"},
	}

	req, err := ParseAnnounceParams(params, clientIP)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if req.Uploaded != 0 {
		t.Errorf("negative uploaded: expected 0, got %d", req.Uploaded)
	}
	if req.Downloaded != 0 {
		t.Errorf("negative downloaded: expected 0, got %d", req.Downloaded)
	}
	if req.Left != 0 {
		t.Errorf("negative left: expected 0, got %d", req.Left)
	}
	if req.Corrupt != 0 {
		t.Errorf("negative corrupt: expected 0, got %d", req.Corrupt)
	}
}

// TestGUC_HandleRequest_AuthFailureBencode verifies that an update request
// with a wrong site password returns a bencode error (not JSON), isolating
// the authentication failure response from the update-error JSON path.
func TestGUC_HandleRequest_AuthFailureBencode(t *testing.T) {
	f := newTestFixture()
	clientIP := net.ParseIP("1.2.3.4")

	// A 32-char passkey that is not the configured site password.
	wrongPass := strings.Repeat("w", 32)
	req, _ := http.NewRequest("GET", "/"+wrongPass+"/update?action=add_torrent", nil)
	raw, _ := f.server.handleRequest(req, clientIP)
	body := gucHTTPBody(raw)

	if !bytes.Contains(body, []byte("failure reason")) {
		t.Errorf("wrong site password should return bencode failure reason, got: %q", body)
	}
}

// TestGUC_UpdateError_NoWorkerStateMutation verifies that calling updateError
// is a pure side-effect-free operation: it must not alter the worker's torrent
// list, user list, or trigger any DB calls.
func TestGUC_UpdateError_NoWorkerStateMutation(t *testing.T) {
	f := newTestFixture()

	torrentsBefore := f.worker.Torrents.Size()
	usersBefore := f.worker.Users.Size()
	dbPeersBefore := len(f.db.Peers)
	dbTorrentsBefore := len(f.db.Torrents)

	for i := 0; i < 5; i++ {
		_, _ = f.worker.updateError(fmt.Sprintf("error message %d", i))
	}

	if f.worker.Torrents.Size() != torrentsBefore {
		t.Errorf("updateError mutated torrent count: before=%d after=%d",
			torrentsBefore, f.worker.Torrents.Size())
	}
	if f.worker.Users.Size() != usersBefore {
		t.Errorf("updateError mutated user count: before=%d after=%d",
			usersBefore, f.worker.Users.Size())
	}
	if len(f.db.Peers) != dbPeersBefore {
		t.Errorf("updateError triggered RecordPeer DB calls: before=%d after=%d",
			dbPeersBefore, len(f.db.Peers))
	}
	if len(f.db.Torrents) != dbTorrentsBefore {
		t.Errorf("updateError triggered RecordTorrent DB calls: before=%d after=%d",
			dbTorrentsBefore, len(f.db.Torrents))
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state detection

// TestGUC_AllBencodeErrors_HaveFailureReason verifies the global bencode
// protocol invariant: every error response emitted on the announce/scrape
// path must contain the key "failure reason". We probe multiple error-producing
// paths to confirm the invariant is never violated.
func TestGUC_AllBencodeErrors_HaveFailureReason(t *testing.T) {
	f := newTestFixture()
	clientIP := net.ParseIP("1.2.3.4")

	check := func(label string, raw []byte) {
		t.Helper()
		body := gucHTTPBody(raw)
		if !bytes.Contains(body, []byte("failure reason")) {
			t.Errorf("%s: invariant violated — response lacks 'failure reason': %q", label, body)
		}
	}

	badPasskey := strings.Repeat("a", 32)

	// 1. Unknown passkey on announce path.
	req1, _ := http.NewRequest("GET", "/"+badPasskey+"/announce?info_hash=x&peer_id=y&port=6881&compact=1", nil)
	r1, _ := f.server.handleRequest(req1, clientIP)
	check("bad_passkey_announce", r1)

	// 2. Unknown passkey on scrape path.
	req2, _ := http.NewRequest("GET", "/"+badPasskey+"/scrape", nil)
	r2, _ := f.server.handleRequest(req2, clientIP)
	check("bad_passkey_scrape", r2)

	// 3. Malformed path with only one segment.
	req3, _ := http.NewRequest("GET", "/shortpath", nil)
	r3, _ := f.server.handleRequest(req3, clientIP)
	check("short_path", r3)

	// 4. Passkey shorter than 32 characters.
	req4, _ := http.NewRequest("GET", "/tooshort/announce", nil)
	r4, _ := f.server.handleRequest(req4, clientIP)
	check("short_passkey_announce", r4)
}

// TestGUC_UpdateResponse_NeverContradictory verifies the formal consistency
// invariant that success=true and success=false are mutually exclusive in all
// responses produced by the Worker. No response may carry both, and no error
// response may carry success=true or status="ok".
func TestGUC_UpdateResponse_NeverContradictory(t *testing.T) {
	f := newTestFixture()

	errorMsgs := []string{"something failed", "DB unavailable", ""}
	for _, msg := range errorMsgs {
		data, _ := f.worker.updateError(msg)
		var resp UpdateResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			t.Fatalf("updateError(%q): unmarshal failed: %v", msg, err)
		}
		if resp.Success {
			t.Errorf("updateError(%q): invariant violated — success=true in error response", msg)
		}
		if resp.Status == "ok" {
			t.Errorf("updateError(%q): invariant violated — status=ok in error response", msg)
		}
	}

	successMsgs := []string{"done", "added torrent 1", "user 42 added"}
	for _, msg := range successMsgs {
		data, err := f.worker.updateSuccess(msg)
		if err != nil {
			t.Fatalf("updateSuccess(%q) returned error: %v", msg, err)
		}
		var resp UpdateResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			t.Fatalf("updateSuccess(%q): unmarshal failed: %v", msg, err)
		}
		if !resp.Success {
			t.Errorf("updateSuccess(%q): invariant violated — success=false in success response", msg)
		}
		if resp.Status != "ok" {
			t.Errorf("updateSuccess(%q): invariant violated — status=%q, expected ok", msg, resp.Status)
		}
	}
}

// TestGUC_AllUpdateErrors_HaveStatusError verifies that every error-producing
// path in HandleUpdate consistently emits JSON with success=false AND
// status="error". A missing or mismatched status field would indicate a
// formal inconsistency in the error encoding.
func TestGUC_AllUpdateErrors_HaveStatusError(t *testing.T) {
	f := newTestFixture()

	tests := []struct {
		name  string
		query string
	}{
		{"unknown_action", "action=bogus_action"},
		{"add_torrent_no_id", "action=add_torrent&info_hash=somehash"},
		{"add_torrent_no_hash", "action=add_torrent&id=5"},
		{"update_torrent_no_hash", "action=update_torrent"},
		{"delete_torrent_no_params", "action=delete_torrent"},
		{"add_user_no_id", "action=add_user&passkey=" + strings.Repeat("z", 32)},
		{"add_user_no_passkey", "action=add_user&id=5"},
		{"remove_user_no_passkey", "action=remove_user"},
		{"change_passkey_no_params", "action=change_passkey"},
		{"add_whitelist_no_prefix", "action=add_whitelist"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/update?"+tc.query, nil)
			data, err := f.worker.HandleUpdate(req)
			if data == nil {
				t.Fatalf("HandleUpdate returned nil data")
			}
			var resp UpdateResponse
			if jsonErr := json.Unmarshal(data, &resp); jsonErr != nil {
				t.Fatalf("unmarshal failed: %v (data=%q)", jsonErr, data)
			}
			if resp.Success {
				t.Errorf("expected success=false, got true")
			}
			if resp.Status != "error" {
				t.Errorf("expected status=error, got %q", resp.Status)
			}
			if err == nil {
				t.Errorf("expected non-nil error return value")
			}
		})
	}
}

// TestGUC_UnknownAction_NotBencodeError verifies the invariant that the
// default routing case ("Nothing to see here") returns plain text, NOT a
// bencode failure reason. Bencode errors must be reserved for announce/scrape
// protocol paths — mixing them into the default path would be a formal
// contradiction.
func TestGUC_UnknownAction_NotBencodeError(t *testing.T) {
	f := newTestFixture()
	clientIP := net.ParseIP("1.2.3.4")

	// testPasskey is 32 chars and exists in the user list — routing proceeds
	// past the passkey-length check to the action switch.
	req, _ := http.NewRequest("GET", "/"+testPasskey+"/unknownaction", nil)
	raw, _ := f.server.handleRequest(req, clientIP)
	body := gucHTTPBody(raw)

	if bytes.Contains(body, []byte("failure reason")) {
		t.Errorf("unknown action must not emit bencode 'failure reason', got: %q", body)
	}
	if !bytes.Contains(body, []byte("Nothing to see here")) {
		t.Errorf("unknown action: expected 'Nothing to see here' body, got: %q", body)
	}
}

// TestGUC_HandleRequest_PasskeyLengthInvariant verifies the impossibility
// guarantee: any passkey whose length is not exactly 32 characters must be
// rejected at routing, before reaching any action handler. This prevents the
// impossible state where a shorter or longer token grants access.
func TestGUC_HandleRequest_PasskeyLengthInvariant(t *testing.T) {
	f := newTestFixture()
	clientIP := net.ParseIP("1.2.3.4")

	tests := []struct {
		name    string
		passkey string
	}{
		{"length_1", "a"},
		{"length_31", strings.Repeat("a", 31)},
		{"length_33", strings.Repeat("a", 33)},
		{"length_64", strings.Repeat("a", 64)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Path has two segments so the short-path check is bypassed;
			// only the passkey-length guard should trigger.
			path := "/" + tc.passkey + "/announce"
			req, _ := http.NewRequest("GET", path, nil)
			raw, _ := f.server.handleRequest(req, clientIP)
			body := gucHTTPBody(raw)
			if !bytes.Contains(body, []byte("failure reason")) {
				t.Errorf("passkey len=%d: expected bencode 'failure reason', got: %q",
					len(tc.passkey), body)
			}
		})
	}
}
