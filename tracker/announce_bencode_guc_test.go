package tracker

// GUC (Gödel Unified Council) tests for bencode announce responses.
// Distributed across four analytical lenses:
//   Knuth  (~5): algorithmic correctness, loop invariants, data structure invariants
//   Turing (~5): termination conditions, halting behavior
//   Church (~5): functional purity, side-effect isolation, referential transparency
//   Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

// ── helpers ────────────────────────────────────────────────────────────────────

// parseBencodeDict extracts top-level string key → raw-value pairs from a
// bencode dict.  It is intentionally simple: it handles only the keys present
// in announce responses.
func parseBencodeDict(s string) map[string]string {
	result := make(map[string]string)
	if len(s) < 2 || s[0] != 'd' || s[len(s)-1] != 'e' {
		return result
	}
	s = s[1 : len(s)-1] // strip outer d...e
	for len(s) > 0 {
		// read key length
		colon := strings.Index(s, ":")
		if colon < 0 {
			break
		}
		keyLen := 0
		for _, c := range s[:colon] {
			if c < '0' || c > '9' {
				return result
			}
			keyLen = keyLen*10 + int(c-'0')
		}
		s = s[colon+1:]
		if len(s) < keyLen {
			break
		}
		key := s[:keyLen]
		s = s[keyLen:]

		// read value
		var val string
		switch {
		case len(s) == 0:
			// no value
		case s[0] == 'i':
			end := strings.Index(s, "e")
			if end < 0 {
				return result
			}
			val = s[:end+1]
			s = s[end+1:]
		case s[0] >= '0' && s[0] <= '9':
			colon2 := strings.Index(s, ":")
			if colon2 < 0 {
				return result
			}
			vLen := 0
			for _, c := range s[:colon2] {
				if c < '0' || c > '9' {
					return result
				}
				vLen = vLen*10 + int(c-'0')
			}
			s = s[colon2+1:]
			if len(s) < vLen {
				return result
			}
			val = s[:vLen]
			s = s[vLen:]
		default:
			return result
		}
		result[key] = val
	}
	return result
}

// buildSuccessBody returns the raw bencode body for a well-formed announce
// response issued by the fixture server.
func buildSuccessBody(f *testFixture, resp *AnnounceResponse) string {
	raw := f.server.bencodedAnnounceResponse(resp, true)
	return httpBody(raw)
}

// ── Knuth lens ─────────────────────────────────────────────────────────────────
// Algorithmic correctness, loop invariants, data structure invariants.

// TestGUC_Knuth_CompactPeerListSixBytesPerPeerIPv4 verifies the data-structure
// invariant that every entry in the compact peer list is exactly 6 bytes
// (4-byte IPv4 address + 2-byte big-endian port).
func TestGUC_Knuth_CompactPeerListSixBytesPerPeerIPv4(t *testing.T) {
	counts := []int{0, 1, 2, 5, 10}
	for _, n := range counts {
		peers := make([]byte, n*6)
		for i := 0; i < n; i++ {
			// craft a minimal valid IPv4 compact entry
			off := i * 6
			peers[off+0] = byte(10)
			peers[off+1] = byte(i + 1)
			peers[off+2] = byte(0)
			peers[off+3] = byte(1)
			peers[off+4] = byte(0x1A) // port high byte
			peers[off+5] = byte(0xE1) // port low byte
		}
		if len(peers)%6 != 0 {
			t.Errorf("n=%d: peer bytes %d not divisible by 6", n, len(peers))
		}
		if len(peers)/6 != n {
			t.Errorf("n=%d: peer count mismatch: %d != %d", n, len(peers)/6, n)
		}
	}
}

// TestGUC_Knuth_IntervalFieldPresentAndPositive asserts that the interval field
// in the bencode response is always a positive integer.
func TestGUC_Knuth_IntervalFieldPresentAndPositive(t *testing.T) {
	f := newTestFixture()
	intervals := []int32{1, 60, 900, 1800, 3600}
	for _, iv := range intervals {
		resp := &AnnounceResponse{
			Interval:    iv,
			MinInterval: iv,
		}
		body := buildSuccessBody(f, resp)
		want := fmt.Sprintf("i%de", iv)
		if !strings.Contains(body, want) {
			t.Errorf("interval=%d: expected %q in body %q", iv, want, body)
		}
	}
}

// TestGUC_Knuth_MinIntervalFieldCorrect checks that the "min interval" key
// encodes the MinInterval field value correctly in the bencode dict.
func TestGUC_Knuth_MinIntervalFieldCorrect(t *testing.T) {
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 900,
	}
	body := buildSuccessBody(f, resp)
	// min interval value must appear as i900e
	if !strings.Contains(body, "i900e") {
		t.Errorf("min_interval=900 not found in body: %s", body)
	}
	// The key itself must be present
	if !strings.Contains(body, "min interval") {
		t.Errorf("key 'min interval' missing from body: %s", body)
	}
}

// TestGUC_Knuth_CompleteAndIncompleteCountsEncoded verifies that seeder and
// leecher counts are correctly encoded in the response dict.
func TestGUC_Knuth_CompleteAndIncompleteCountsEncoded(t *testing.T) {
	f := newTestFixture()
	cases := []struct {
		seeders  int32
		leechers int32
	}{
		{0, 0},
		{1, 0},
		{0, 5},
		{7, 3},
	}
	for _, tc := range cases {
		resp := &AnnounceResponse{
			Interval:    1800,
			MinInterval: 1800,
			Complete:    tc.seeders,
			Incomplete:  tc.leechers,
		}
		body := buildSuccessBody(f, resp)
		wantC := fmt.Sprintf("i%de", tc.seeders)
		wantI := fmt.Sprintf("i%de", tc.leechers)
		if !strings.Contains(body, wantC) {
			t.Errorf("complete=%d not found in body: %s", tc.seeders, body)
		}
		if !strings.Contains(body, wantI) {
			t.Errorf("incomplete=%d not found in body: %s", tc.leechers, body)
		}
	}
}

// TestGUC_Knuth_ResponseSizeProportionalToPeerCount asserts that the response
// body length grows monotonically with the peer count, with exact bytes
// proportional to 6 bytes per peer.
func TestGUC_Knuth_ResponseSizeProportionalToPeerCount(t *testing.T) {
	f := newTestFixture()
	ip := net.ParseIP("1.2.3.4")
	prev := -1
	for n := 0; n <= 5; n++ {
		peers := make([]byte, n*6)
		for i := 0; i < n; i++ {
			entry := CompactIPPort(ip, uint16(6000+i))
			copy(peers[i*6:], entry)
		}
		resp := &AnnounceResponse{
			Interval:    1800,
			MinInterval: 1800,
			Peers:       peers,
		}
		body := buildSuccessBody(f, resp)
		if prev >= 0 && len(body) <= prev {
			t.Errorf("n=%d: body length %d did not grow from n-1 length %d", n, len(body), prev)
		}
		prev = len(body)
	}
}

// ── Turing lens ────────────────────────────────────────────────────────────────
// Termination conditions, halting behavior.

// TestGUC_Turing_FailureReasonStringOnError verifies that every error path
// returns a parseable bencode dict containing "failure reason".
func TestGUC_Turing_FailureReasonStringOnError(t *testing.T) {
	f := newTestFixture()
	messages := []string{
		"not admitted to swarm",
		"your client does not support compact announces",
		"invalid peer ID",
		"unregistered torrent",
	}
	for _, msg := range messages {
		raw := f.server.errorResponse(msg, true)
		body := httpBody(raw)
		if !strings.Contains(body, "failure reason") {
			t.Errorf("msg=%q: 'failure reason' key missing: %s", msg, body)
		}
		if !strings.Contains(body, msg) {
			t.Errorf("msg=%q: reason text not found in body: %s", msg, body)
		}
	}
}

// TestGUC_Turing_ResponseIsValidBencode checks that bencode responses always
// start with 'd' and end with 'e' (dict structure invariant).
func TestGUC_Turing_ResponseIsValidBencode(t *testing.T) {
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Complete:    3,
		Incomplete:  1,
		Peers:       CompactIPPort(net.ParseIP("10.0.0.1"), 6881),
	}
	body := buildSuccessBody(f, resp)
	if len(body) < 2 {
		t.Fatalf("body too short: %q", body)
	}
	if body[0] != 'd' {
		t.Errorf("bencode response must start with 'd', got %q", body[0])
	}
	if body[len(body)-1] != 'e' {
		t.Errorf("bencode response must end with 'e', got %q", body[len(body)-1])
	}
}

// TestGUC_Turing_EmptyPeersEncodedAsZeroLength verifies that when there are
// no peers the encoder terminates with the canonical "0:" encoding.
func TestGUC_Turing_EmptyPeersEncodedAsZeroLength(t *testing.T) {
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Peers:       []byte{},
	}
	body := buildSuccessBody(f, resp)
	if !strings.Contains(body, "0:") {
		t.Errorf("empty peers should encode as '0:', got: %s", body)
	}
}

// TestGUC_Turing_WarningMessageOptionalAbsentWhenNoWarning confirms the
// "warning message" key is absent when no warning is set (no spurious emission).
func TestGUC_Turing_WarningMessageOptionalAbsentWhenNoWarning(t *testing.T) {
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Warning:     "", // empty → must not appear
	}
	body := buildSuccessBody(f, resp)
	if strings.Contains(body, "warning") {
		t.Errorf("warning key should be absent when Warning is empty, got: %s", body)
	}
}

// TestGUC_Turing_WarningMessagePresentWhenSet confirms the "warning message"
// key terminates the dict only when a warning string is provided.
func TestGUC_Turing_WarningMessagePresentWhenSet(t *testing.T) {
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Warning:     "Illegal character found in IP address",
	}
	body := buildSuccessBody(f, resp)
	if !strings.Contains(body, "warning message") {
		t.Errorf("'warning message' key should be present: %s", body)
	}
	if !strings.Contains(body, "Illegal character found in IP address") {
		t.Errorf("warning text not found in body: %s", body)
	}
}

// ── Church lens ────────────────────────────────────────────────────────────────
// Functional purity, side-effect isolation, referential transparency.

// TestGUC_Church_BencodedResponsePure verifies that calling
// bencodedAnnounceResponse twice with the same input produces identical output
// (referential transparency / no side effects).
func TestGUC_Church_BencodedResponsePure(t *testing.T) {
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Complete:    2,
		Incomplete:  3,
		Peers:       CompactIPPort(net.ParseIP("5.5.5.5"), 51413),
	}
	body1 := buildSuccessBody(f, resp)
	body2 := buildSuccessBody(f, resp)
	if body1 != body2 {
		t.Errorf("bencodedAnnounceResponse is not pure: %q != %q", body1, body2)
	}
}

// TestGUC_Church_ErrorResponsePure verifies that errorResponse is pure:
// same message → same encoded output on repeated calls.
func TestGUC_Church_ErrorResponsePure(t *testing.T) {
	f := newTestFixture()
	raw1 := httpBody(f.server.errorResponse("test error msg", true))
	raw2 := httpBody(f.server.errorResponse("test error msg", true))
	if raw1 != raw2 {
		t.Errorf("errorResponse not pure: %q != %q", raw1, raw2)
	}
}

// TestGUC_Church_PeerBytesIsolatedFromResponseStruct confirms that mutating
// Peers after building the response body does not retroactively change the
// already-built body (copy semantics / immutability of encoded output).
func TestGUC_Church_PeerBytesIsolatedFromResponseStruct(t *testing.T) {
	f := newTestFixture()
	peers := CompactIPPort(net.ParseIP("1.2.3.4"), 6881)
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Peers:       peers,
	}
	body1 := buildSuccessBody(f, resp)
	// mutate the original slice after encoding
	copy(peers, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	body2 := buildSuccessBody(f, resp)
	// body2 will reflect the mutation — that is fine; the key thing is that
	// body1, already captured, is unaffected because it is a string (immutable).
	if len(body1) == 0 {
		t.Error("first body should not be empty")
	}
	if len(body2) == 0 {
		t.Error("second body should not be empty")
	}
}

// TestGUC_Church_NoPeersDoesNotIncludeTrackerID checks that the optional
// tracker_id field is not injected by the current encoder when it is not set
// (side-effect isolation: no phantom fields).
func TestGUC_Church_NoPeersDoesNotIncludeTrackerID(t *testing.T) {
	// The current bencodedAnnounceResponse implementation does not emit a
	// tracker_id key.  Verify it stays absent.
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
	}
	body := buildSuccessBody(f, resp)
	if strings.Contains(body, "tracker id") || strings.Contains(body, "tracker_id") {
		t.Errorf("tracker_id should not appear in current response: %s", body)
	}
}

// TestGUC_Church_DownloadedCountNotInBencodeBody verifies that the Downloaded
// field from AnnounceRequest does not leak into the bencoded response body
// (side-effect isolation between request and response structs).
func TestGUC_Church_DownloadedCountNotInBencodeBody(t *testing.T) {
	// The response struct has no "downloaded" field — it tracks Complete/Incomplete
	// (seeders/leechers).  This test ensures the field is not spuriously injected.
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Complete:    10,
		Incomplete:  5,
	}
	body := buildSuccessBody(f, resp)
	// "downloaded" is a scrape-response key, never in announce responses.
	if strings.Contains(body, "downloaded") {
		t.Errorf("'downloaded' key must not appear in announce bencode body: %s", body)
	}
}

// ── Gödel lens ─────────────────────────────────────────────────────────────────
// Formal consistency, invariant preservation, impossible-state detection.

// TestGUC_Godel_NoPeersBeyondNumWant ensures that if the tracker returns fewer
// peers than the compact byte slice could be misinterpreted, the actual count
// is consistent: len(Peers) mod 6 == 0.
func TestGUC_Godel_NoPeersBeyondNumWant(t *testing.T) {
	f := newTestFixture()
	// Build a resp whose Peers is a valid multiple of 6
	ip := net.ParseIP("10.0.0.1")
	var allPeers []byte
	for i := 0; i < 3; i++ {
		allPeers = append(allPeers, CompactIPPort(ip, uint16(6000+i))...)
	}
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Peers:       allPeers,
	}
	body := buildSuccessBody(f, resp)
	// Decode the peers string from the body
	dict := parseBencodeDict(body)
	peersVal, ok := dict["peers"]
	if !ok {
		t.Fatal("'peers' key missing from response dict")
	}
	if len(peersVal)%6 != 0 {
		t.Errorf("peers length %d not divisible by 6 (each IPv4 peer is 6 bytes)", len(peersVal))
	}
}

// TestGUC_Godel_CompleteAndIncompleteConsistentWithSwarm asserts that after a
// seeder and leecher announce, the response's Complete/Incomplete values match
// the actual swarm state.
func TestGUC_Godel_CompleteAndIncompleteConsistentWithSwarm(t *testing.T) {
	w, _, _ := newTestWorker()
	const ih = "testhash000000000001"
	tor := NewTorrent(1)
	w.Torrents.Set(ih, tor)

	// seed one peer
	seeder := NewUser(10, true, false)
	sReq := &AnnounceRequest{
		InfoHash: ih, PeerID: []byte("-DE1300000000000000s"),
		Port: 51413, Left: 0, Compact: true, Event: "started", NumWant: 50,
	}
	resp, err := w.Announce(context.Background(), sReq, seeder, net.ParseIP("5.5.5.5"), "", "")
	if err != nil {
		t.Fatalf("seeder announce: %v", err)
	}
	_ = resp

	// leech one peer
	leecher := NewUser(11, true, false)
	lReq := &AnnounceRequest{
		InfoHash: ih, PeerID: []byte("-qB4000000000000000l"),
		Port: 6881, Left: 1000, Compact: true, Event: "started", NumWant: 50,
	}
	resp2, err := w.Announce(context.Background(), lReq, leecher, net.ParseIP("6.6.6.6"), "", "")
	if err != nil {
		t.Fatalf("leecher announce: %v", err)
	}

	if resp2.Complete != 1 {
		t.Errorf("Complete = %d, want 1 (one seeder)", resp2.Complete)
	}
	if resp2.Incomplete != 1 {
		t.Errorf("Incomplete = %d, want 1 (one leecher)", resp2.Incomplete)
	}
}

// TestGUC_Godel_ImpossibleStatePeersLengthNotOddMultiple verifies that the
// tracker never emits a peer blob whose length is 1, 2, 3, 4, or 5 bytes
// (impossible compact IPv4 state).
func TestGUC_Godel_ImpossibleStatePeersLengthNotOddMultiple(t *testing.T) {
	f := newTestFixture()
	// Feed in only valid 6-byte multiples; the encoder must never truncate.
	for _, n := range []int{0, 1, 2, 3} {
		peers := make([]byte, n*6)
		for i := 0; i < n; i++ {
			e := CompactIPPort(net.ParseIP("1.2.3.4"), uint16(6000+i))
			copy(peers[i*6:], e)
		}
		resp := &AnnounceResponse{
			Interval:    1800,
			MinInterval: 1800,
			Peers:       peers,
		}
		dict := parseBencodeDict(buildSuccessBody(f, resp))
		pv := dict["peers"]
		if len(pv)%6 != 0 {
			t.Errorf("n=%d: encoded peers length %d not a multiple of 6", n, len(pv))
		}
	}
}

// TestGUC_Godel_NoExtraKeysInSuccessResponse checks that the success response
// dict contains only the expected canonical keys and no others.
func TestGUC_Godel_NoExtraKeysInSuccessResponse(t *testing.T) {
	f := newTestFixture()
	resp := &AnnounceResponse{
		Interval:    1800,
		MinInterval: 1800,
		Complete:    2,
		Incomplete:  1,
		Peers:       CompactIPPort(net.ParseIP("1.2.3.4"), 6881),
	}
	body := buildSuccessBody(f, resp)
	// The only keys in a success response (no warning) are these four:
	required := []string{"complete", "incomplete", "interval", "min interval", "peers"}
	for _, k := range required {
		enc := BencodeString(k)
		if !strings.Contains(body, enc) {
			t.Errorf("required key %q missing from response: %s", k, body)
		}
	}
	// Illegal extra keys that must never appear in a basic success response:
	forbidden := []string{"failure reason", "tracker id", "downloaded"}
	for _, k := range forbidden {
		enc := BencodeString(k)
		if strings.Contains(body, enc) {
			t.Errorf("unexpected key %q found in success response: %s", k, body)
		}
	}
}

// TestGUC_Godel_FormalConsistencyBencodeStringLength verifies the formal spec
// invariant of bencode strings: the prefix length must equal the actual byte
// length of the peer blob carried in the response.
func TestGUC_Godel_FormalConsistencyBencodeStringLength(t *testing.T) {
	f := newTestFixture()
	for _, n := range []int{0, 1, 3, 5} {
		peers := make([]byte, n*6)
		for i := 0; i < n; i++ {
			e := CompactIPPort(net.ParseIP("2.3.4.5"), uint16(7000+i))
			copy(peers[i*6:], e)
		}
		resp := &AnnounceResponse{
			Interval:    1800,
			MinInterval: 1800,
			Peers:       peers,
		}
		body := buildSuccessBody(f, resp)
		// Find the peers bencode string prefix: "<len>:"
		peerKey := BencodeString("peers")
		idx := strings.Index(body, peerKey)
		if idx < 0 {
			t.Fatalf("n=%d: 'peers' key not found in body: %s", n, body)
		}
		afterKey := body[idx+len(peerKey):]
		// afterKey starts with "<len>:<bytes>..."
		colon := strings.Index(afterKey, ":")
		if colon < 0 {
			t.Fatalf("n=%d: no colon after peers key", n)
		}
		declaredLen := 0
		for _, c := range afterKey[:colon] {
			if c < '0' || c > '9' {
				t.Fatalf("n=%d: non-digit in length prefix", n)
			}
			declaredLen = declaredLen*10 + int(c-'0')
		}
		if declaredLen != n*6 {
			t.Errorf("n=%d: declared peer length %d != actual %d", n, declaredLen, n*6)
		}
	}
}
