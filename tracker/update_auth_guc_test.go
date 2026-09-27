package tracker

// update_auth_guc_test.go — GUC (Gödel Unified Council) test coverage for
// update-route authentication in the Ocelot tracker.
//
// Lenses:
//
//	Knuth  (~5): algorithmic correctness, auth decision logic
//	Turing (~5): termination/halting behavior on malformed or empty inputs
//	Church (~5): functional purity, side-effect isolation, response-format discipline
//	Gödel  (~5): formal invariant preservation, impossible-state detection

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// ── test helpers ──────────────────────────────────────────────────────────────

// buildAuthUpdateReq creates a GET request for /<passkey>/update?action=<action>
// with no Authorization header.
func buildAuthUpdateReq(passkey, action string) *http.Request {
	q := url.Values{"action": {action}}
	req, _ := http.NewRequest("GET", "/"+passkey+"/update?"+q.Encode(), nil)
	return req
}

// buildAuthUpdateReqWithHeader creates a GET request with the given Authorization header.
// Pass an empty string to omit the header.
func buildAuthUpdateReqWithHeader(passkey, action, authHeader string) *http.Request {
	req := buildAuthUpdateReq(passkey, action)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

// runAuthRequest calls handleRequest with the fixture server and a standard test IP.
func runAuthRequest(f *testFixture, req *http.Request) string {
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	return string(raw)
}

// authHTTPBody splits a raw HTTP response at the blank line and returns the body.
func authHTTPBody(raw string) string {
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return raw
}

// authHTTPHeader returns the value of the named response header (case-insensitive).
func authHTTPHeader(raw, name string) string {
	target := strings.ToLower(name) + ":"
	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), target) {
			return strings.TrimSpace(line[len(target):])
		}
	}
	return ""
}

// ── Knuth: algorithmic correctness ───────────────────────────────────────────

// TestGUC_Knuth_Auth_PathPasskeyCorrect_UpdateSucceeds verifies that placing
// the SitePassword in the path passkey position dispatches the update handler
// and returns a JSON response — not an auth-failure bencode error.
func TestGUC_Knuth_Auth_PathPasskeyCorrect_UpdateSucceeds(t *testing.T) {
	f := newTestFixture()
	// Use an action that will fail for other reasons (missing args) but auth must pass.
	req := buildAuthUpdateReq(sitePass, "add_torrent")
	raw := runAuthRequest(f, req)

	if strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected auth to pass with sitePass in path, but got: %s", raw)
	}
	body := authHTTPBody(raw)
	var resp UpdateResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Errorf("expected JSON body after auth success, got: %s (err: %v)", body, err)
	}
}

// TestGUC_Knuth_Auth_PathPasskeyWrong_AuthFails verifies that a 32-character
// passkey that is not the SitePassword is rejected with an authentication failure.
func TestGUC_Knuth_Auth_PathPasskeyWrong_AuthFails(t *testing.T) {
	f := newTestFixture()
	// testPasskey is 32 chars but != sitePass.
	req := buildAuthUpdateReq(testPasskey, "add_torrent")
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected 'Authentication failure' for wrong passkey; got: %s", raw)
	}
}

// TestGUC_Knuth_Auth_BearerTokenCorrect_UpdateSucceeds verifies that an
// incorrect path passkey is redeemed by a correct "Bearer <sitePass>"
// Authorization header, causing the update handler to be dispatched.
func TestGUC_Knuth_Auth_BearerTokenCorrect_UpdateSucceeds(t *testing.T) {
	f := newTestFixture()
	// testPasskey in path (not sitePass) + "Bearer <sitePass>" in header.
	req := buildAuthUpdateReqWithHeader(testPasskey, "add_torrent", "Bearer "+sitePass)
	raw := runAuthRequest(f, req)

	if strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected auth to pass with Bearer sitePass; got: %s", raw)
	}
	body := authHTTPBody(raw)
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Errorf("expected JSON body after Bearer auth success, got: %s (err: %v)", body, err)
	}
}

// TestGUC_Knuth_Auth_BearerTokenWrong_AuthFails verifies that a Bearer token
// whose value is not the SitePassword is rejected with an authentication failure.
func TestGUC_Knuth_Auth_BearerTokenWrong_AuthFails(t *testing.T) {
	f := newTestFixture()
	// A 32-char string that is neither sitePass nor testPasskey.
	wrongToken := "badtoken123456789012345678901234"
	req := buildAuthUpdateReqWithHeader(testPasskey, "add_torrent", "Bearer "+wrongToken)
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected 'Authentication failure' for wrong Bearer token; got: %s", raw)
	}
}

// TestGUC_Knuth_Auth_PathAndBearerBothCorrect_Succeeds verifies that when both
// the path passkey and Bearer token equal the SitePassword the request succeeds
// (path check short-circuits; Bearer is redundant but must not break anything).
func TestGUC_Knuth_Auth_PathAndBearerBothCorrect_Succeeds(t *testing.T) {
	f := newTestFixture()
	req := buildAuthUpdateReqWithHeader(sitePass, "add_torrent", "Bearer "+sitePass)
	raw := runAuthRequest(f, req)

	if strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected auth success when both path+Bearer correct; got: %s", raw)
	}
	body := authHTTPBody(raw)
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Errorf("expected JSON body when both path+Bearer correct, got: %s (err: %v)", body, err)
	}
}

// ── Turing: termination and halting behavior ─────────────────────────────────

// TestGUC_Turing_Auth_EmptyPasskey_HaltsWithMalformed verifies that a URL
// with no passkey segment (only one path component) terminates immediately
// with "Malformed announce" before any auth logic runs.
func TestGUC_Turing_Auth_EmptyPasskey_HaltsWithMalformed(t *testing.T) {
	f := newTestFixture()
	req, _ := http.NewRequest("GET", "/update", nil) // single-segment path
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Malformed announce") {
		t.Errorf("expected 'Malformed announce' for missing passkey; got: %s", raw)
	}
}

// TestGUC_Turing_Auth_AlternativeSchemeWithSitePass_Rejected verifies that
// wrapping the SitePassword in a non-"Bearer " Authorization scheme (e.g.,
// "Token <sitePass>") is rejected: strings.TrimPrefix("Bearer ") does not strip
// "Token ", so the bearer value becomes "Token <sitePass>" which ≠ sitePass.
func TestGUC_Turing_Auth_AlternativeSchemeWithSitePass_Rejected(t *testing.T) {
	f := newTestFixture()
	// "Token " scheme — not trimmed by TrimPrefix("Bearer "), so the full string
	// "Token <sitePass>" is compared against sitePass and does not match.
	req := buildAuthUpdateReqWithHeader(testPasskey, "add_torrent", "Token "+sitePass)
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected auth failure for 'Token <sitePass>' (non-Bearer scheme); got: %s", raw)
	}
}

// TestGUC_Turing_Auth_ShortPasskey_HaltsWithMalformed verifies that a passkey
// shorter than 32 characters terminates the request with "Malformed announce"
// before reaching auth or action handling.
func TestGUC_Turing_Auth_ShortPasskey_HaltsWithMalformed(t *testing.T) {
	f := newTestFixture()
	req, _ := http.NewRequest("GET", "/shortkey/update", nil) // 8-char passkey
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Malformed announce") {
		t.Errorf("expected 'Malformed announce' for short passkey; got: %s", raw)
	}
}

// TestGUC_Turing_Auth_LongPasskey_HaltsWithMalformed verifies that a passkey
// longer than 32 characters terminates the request with "Malformed announce".
func TestGUC_Turing_Auth_LongPasskey_HaltsWithMalformed(t *testing.T) {
	f := newTestFixture()
	longKey := "abcdef1234567890abcdef1234567890X" // 33 chars
	req, _ := http.NewRequest("GET", "/"+longKey+"/update", nil)
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Malformed announce") {
		t.Errorf("expected 'Malformed announce' for 33-char passkey; got: %s", raw)
	}
}

// TestGUC_Turing_Auth_LowercaseBearerPrefix_Rejected verifies that the Bearer
// prefix check is case-sensitive: "bearer <sitePass>" (lowercase) does not
// authenticate because strings.TrimPrefix(..., "Bearer ") removes nothing.
func TestGUC_Turing_Auth_LowercaseBearerPrefix_Rejected(t *testing.T) {
	f := newTestFixture()
	req := buildAuthUpdateReqWithHeader(testPasskey, "add_torrent", "bearer "+sitePass)
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected auth failure for lowercase 'bearer' prefix; got: %s", raw)
	}
}

// ── Church: functional purity and side-effect isolation ──────────────────────

// TestGUC_Church_Auth_SuccessfulUpdate_ContentTypeIsJSON verifies that on a
// successful auth the raw HTTP response declares Content-Type: application/json,
// confirming the update route uses the JSON response helper, not the bencode one.
func TestGUC_Church_Auth_SuccessfulUpdate_ContentTypeIsJSON(t *testing.T) {
	f := newTestFixture()
	req := buildAuthUpdateReq(sitePass, "some_unknown_action_ct_test")
	raw := runAuthRequest(f, req)

	ct := authHTTPHeader(raw, "content-type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type: application/json after auth success; got: %q", ct)
	}
}

// TestGUC_Church_Auth_UpdateHandler_AlwaysJSON_NeverBencode verifies the comment
// guarantee in server.go ("Always returns JSON — errors are serialised by
// updateError(), never as bencode"): even an unknown action triggers a JSON error
// response, not a bencode failure dict.
func TestGUC_Church_Auth_UpdateHandler_AlwaysJSON_NeverBencode(t *testing.T) {
	f := newTestFixture()
	req := buildAuthUpdateReq(sitePass, "totally_unknown_action_bencode_test")
	raw := runAuthRequest(f, req)
	body := authHTTPBody(raw)

	// Bencode error dicts start with "d" and contain "failure reason".
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "d") && strings.Contains(trimmed, "failure reason") {
		t.Errorf("update route returned bencode on auth success — want JSON; body: %s", body)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Errorf("expected valid JSON from update handler; got: %s (err: %v)", body, err)
	}
}

// TestGUC_Church_Auth_ReportPassword_NotValidForUpdate verifies that the
// ReportPassword — a distinct credential stored separately in Config — cannot
// authenticate the update route (which only accepts SitePassword).
func TestGUC_Church_Auth_ReportPassword_NotValidForUpdate(t *testing.T) {
	f := newTestFixture()
	reportPass := f.server.config.ReportPassword
	if len(reportPass) != 32 {
		t.Skipf("test requires a 32-char ReportPassword; got len=%d", len(reportPass))
	}
	if reportPass == sitePass {
		t.Skip("test requires ReportPassword != SitePassword")
	}
	req := buildAuthUpdateReq(reportPass, "add_torrent")
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected auth failure when using ReportPassword for update route; got: %s", raw)
	}
}

// TestGUC_Church_Auth_IndependentRequests_NoStateShared verifies that two
// consecutive requests do not share any mutable auth state: each yields the
// correct independent outcome regardless of the other's result.
func TestGUC_Church_Auth_IndependentRequests_NoStateShared(t *testing.T) {
	f := newTestFixture()

	// Request A: valid auth — must succeed.
	rawA := runAuthRequest(f, buildAuthUpdateReq(sitePass, "action_A"))
	if strings.Contains(rawA, "Authentication failure") {
		t.Errorf("request A (valid auth) should not fail; got: %s", rawA)
	}

	// Request B: invalid auth — must fail.
	rawB := runAuthRequest(f, buildAuthUpdateReq(testPasskey, "action_B"))
	if !strings.Contains(rawB, "Authentication failure") {
		t.Errorf("request B (invalid auth) should fail; got: %s", rawB)
	}

	// Request C: valid auth again — must still succeed (not poisoned by B).
	rawC := runAuthRequest(f, buildAuthUpdateReq(sitePass, "action_C"))
	if strings.Contains(rawC, "Authentication failure") {
		t.Errorf("request C (valid auth after failed B) should not fail; got: %s", rawC)
	}
}

// TestGUC_Church_Auth_PathAuth_NoBearerRequired verifies that successful auth
// via path passkey alone requires no Authorization header: the decision is a
// pure function of the path, and the absence of a header does not trigger a
// fallback error.
func TestGUC_Church_Auth_PathAuth_NoBearerRequired(t *testing.T) {
	f := newTestFixture()
	req := buildAuthUpdateReq(sitePass, "action_no_bearer_required")

	if req.Header.Get("Authorization") != "" {
		t.Fatal("pre-condition violated: test request must not have an Authorization header")
	}

	raw := runAuthRequest(f, req)
	if strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected path-only auth to succeed without Authorization header; got: %s", raw)
	}
}

// ── Gödel: invariant preservation and impossible-state detection ─────────────

// TestGUC_Godel_Auth_FailureResponse_ContainsBencodeError verifies the formal
// invariant that update-route auth failures are rendered as a bencode failure
// dict (the tracker error format), keeping auth gating separate from the JSON
// update handler.
func TestGUC_Godel_Auth_FailureResponse_ContainsBencodeError(t *testing.T) {
	f := newTestFixture()
	req := buildAuthUpdateReq(testPasskey, "add_torrent")
	raw := runAuthRequest(f, req)
	body := authHTTPBody(raw)

	if !strings.Contains(body, "failure reason") {
		t.Errorf("expected bencode 'failure reason' key in auth-failure body; got: %s", body)
	}
	if !strings.Contains(body, "Authentication failure") {
		t.Errorf("expected 'Authentication failure' message in body; got: %s", body)
	}
}

// TestGUC_Godel_Auth_BearerWithExtraLeadingSpace_Rejected verifies the invariant
// that "Bearer  <sitePass>" (double-space) does NOT authenticate: TrimPrefix
// removes exactly "Bearer " leaving " <sitePass>" which is not equal to sitePass.
func TestGUC_Godel_Auth_BearerWithExtraLeadingSpace_Rejected(t *testing.T) {
	f := newTestFixture()
	// Double space between "Bearer" and sitePass.
	req := buildAuthUpdateReqWithHeader(testPasskey, "add_torrent", "Bearer  "+sitePass)
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected auth failure for 'Bearer  <pass>' (double-space); got: %s", raw)
	}
}

// TestGUC_Godel_Auth_CaseSensitiveBearerPrefix_BEARER_Rejected verifies the
// invariant that the Bearer prefix comparison is case-sensitive: "BEARER <sitePass>"
// (all-caps) does not pass the TrimPrefix("Bearer ") guard.
func TestGUC_Godel_Auth_CaseSensitiveBearerPrefix_BEARER_Rejected(t *testing.T) {
	f := newTestFixture()
	req := buildAuthUpdateReqWithHeader(testPasskey, "add_torrent", "BEARER "+sitePass)
	raw := runAuthRequest(f, req)

	if !strings.Contains(raw, "Authentication failure") {
		t.Errorf("expected auth failure for 'BEARER <pass>' (uppercase prefix); got: %s", raw)
	}
}

// TestGUC_Godel_Auth_UpdateSuccessResponse_HasRequiredFields verifies the schema
// invariant that every JSON response produced by a successfully-authenticated
// update request contains both "success" and "status" fields.
func TestGUC_Godel_Auth_UpdateSuccessResponse_HasRequiredFields(t *testing.T) {
	f := newTestFixture()
	req := buildAuthUpdateReq(sitePass, "unknown_schema_invariant_action")
	raw := runAuthRequest(f, req)
	body := authHTTPBody(raw)

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("expected valid JSON after auth success; got: %s (err: %v)", body, err)
	}
	if _, ok := resp["success"]; !ok {
		t.Errorf("JSON response missing 'success' field; body: %s", body)
	}
	if _, ok := resp["status"]; !ok {
		t.Errorf("JSON response missing 'status' field; body: %s", body)
	}
}

// TestGUC_Godel_Auth_AllInvalidVariants_NeverDispatchUpdate verifies the
// meta-invariant that no invalid auth variant ever reaches the update dispatcher.
// Each variant must produce an "Authentication failure" response, and none must
// return a JSON body (which would indicate the dispatcher was reached).
func TestGUC_Godel_Auth_AllInvalidVariants_NeverDispatchUpdate(t *testing.T) {
	const action = "add_torrent" // returns JSON if dispatcher is reached

	tests := []struct {
		name       string
		passkey    string
		authHeader string
	}{
		{
			name:    "wrong_32char_passkey_no_bearer",
			passkey: testPasskey,
		},
		{
			name:       "wrong_passkey_wrong_bearer",
			passkey:    testPasskey,
			authHeader: "Bearer " + testPasskey,
		},
		{
			name:       "alternative_scheme_token_wrapping_sitepass",
			passkey:    testPasskey,
			authHeader: "Token " + sitePass,
		},
		{
			name:       "lowercase_bearer_prefix",
			passkey:    testPasskey,
			authHeader: "bearer " + sitePass,
		},
		{
			name:       "uppercase_bearer_prefix",
			passkey:    testPasskey,
			authHeader: "BEARER " + sitePass,
		},
		{
			name:       "bearer_doublespace",
			passkey:    testPasskey,
			authHeader: "Bearer  " + sitePass,
		},
		{
			name:       "bearer_trailing_space_on_value",
			passkey:    testPasskey,
			authHeader: "Bearer " + sitePass + " ",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFixture()
			req := buildAuthUpdateReqWithHeader(tc.passkey, action, tc.authHeader)
			raw := runAuthRequest(f, req)

			if !strings.Contains(raw, "Authentication failure") {
				t.Errorf("variant %q: expected 'Authentication failure'; got: %s", tc.name, raw)
			}
			// JSON body beginning with "{" means the dispatcher was reached — violated.
			body := authHTTPBody(raw)
			if strings.HasPrefix(strings.TrimSpace(body), "{") {
				t.Errorf("variant %q: update dispatcher was reached for invalid auth; body: %s", tc.name, body)
			}
		})
	}
}
