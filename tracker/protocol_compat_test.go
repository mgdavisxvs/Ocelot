package tracker

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// OcelotProtocol is the canonical BEP-3/BEP-23 compliance version tag.
// Increment on any breaking protocol change.
const OcelotProtocol = 1

// TestValidCompactAnnounce verifies that a well-formed compact announce
// returns a properly bencoded dictionary with all required BEP-3 keys.
func TestValidCompactAnnounce(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 100)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	if !strings.HasPrefix(body, "d") || !strings.HasSuffix(body, "e") {
		t.Fatalf("response is not a bencoded dictionary: %q", body)
	}
	for _, key := range []string{"8:complete", "10:incomplete", "8:interval", "5:peers"} {
		if !strings.Contains(body, key) {
			t.Errorf("BEP-3 required key %q missing from response: %q", key, body)
		}
	}
	if strings.Contains(body, "14:failure reason") {
		t.Errorf("valid announce should not contain failure reason: %q", body)
	}
}

// TestNonCompactRejected verifies that announce requests without compact=1
// are rejected with a bencoded failure reason (BEP-23 enforcement).
func TestNonCompactRejected(t *testing.T) {
	f := newTestFixture()

	q := url.Values{
		"info_hash":  {testInfoHash},
		"peer_id":    {testPeerID},
		"port":       {"6881"},
		"uploaded":   {"0"},
		"downloaded": {"0"},
		"left":       {"100"},
		"compact":    {"0"}, // non-compact
		"event":      {"started"},
		"numwant":    {"50"},
	}
	req, _ := http.NewRequest("GET", "/"+testPasskey+"/announce?"+q.Encode(), nil)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	if !strings.Contains(body, "14:failure reason") {
		t.Errorf("non-compact announce must be rejected; got: %q", body)
	}
	if !strings.Contains(body, "compact") {
		t.Errorf("failure reason should mention compact requirement; got: %q", body)
	}
}

// TestUnknownPasskeyReturnsError verifies that an unknown passkey produces
// a bencoded failure reason rather than a server error or empty response.
func TestUnknownPasskeyReturnsError(t *testing.T) {
	f := newTestFixture()
	unknownPasskey := "ffffffffffffffffffffffffffffffff"
	req := buildAnnounceURL(unknownPasskey, testInfoHash, testPeerID, "started", 0, 0, 100)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	if !strings.Contains(body, "14:failure reason") {
		t.Errorf("unknown passkey must yield bencoded failure reason; got: %q", body)
	}
	// Must not crash or return an empty response
	if len(body) == 0 {
		t.Error("response body must not be empty")
	}
}

// TestUnregisteredTorrentReturnsError verifies that a valid passkey with an
// unregistered torrent info_hash produces a bencoded failure reason.
func TestUnregisteredTorrentReturnsError(t *testing.T) {
	f := newTestFixture()
	unknownHash := "\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff"
	req := buildAnnounceURL(testPasskey, unknownHash, testPeerID, "started", 0, 0, 100)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	if !strings.Contains(body, "14:failure reason") {
		t.Errorf("unregistered torrent must yield bencoded failure reason; got: %q", body)
	}
}

// TestScrapeResponseStructure verifies that a scrape response has the correct
// bencoded structure: outer dict → "files" → inner dict of info_hash dicts
// each containing "complete", "incomplete", and "downloaded".
func TestScrapeResponseStructure(t *testing.T) {
	f := newTestFixture()
	req := buildScrapeURL(testPasskey, testInfoHash)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	// Outer "files" key
	if !strings.Contains(body, "5:files") {
		t.Errorf("scrape response missing 'files' key: %q", body)
	}
	// Per-torrent dict keys: complete, incomplete, downloaded
	for _, key := range []string{"8:complete", "10:incomplete", "10:downloaded"} {
		if !strings.Contains(body, key) {
			t.Errorf("scrape per-torrent dict missing key %q: %q", key, body)
		}
	}
	// Must be a bencoded dict
	if !strings.HasPrefix(body, "d") || !strings.HasSuffix(body, "e") {
		t.Errorf("scrape response is not a bencoded dictionary: %q", body)
	}
}

// TestAnnounceLatencyBound verifies that the full announce path completes
// within an acceptable in-process latency (10 ms). This guards against
// accidental O(n) scans or lock contention on the hot path.
func TestAnnounceLatencyBound(t *testing.T) {
	f := newTestFixture()
	req := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 100)

	const iterations = 200
	const maxAvgNs = 10 * time.Millisecond

	start := time.Now()
	for i := 0; i < iterations; i++ {
		f.server.handleRequest(req, net.ParseIP(testIP))
	}
	elapsed := time.Since(start)
	avg := elapsed / iterations

	if avg > maxAvgNs {
		t.Errorf("announce average latency %v exceeds %v bound", avg, maxAvgNs)
	}
}

// TestScrapeUnknownPasskey verifies scrape returns an error for unknown passkeys.
func TestScrapeUnknownPasskey(t *testing.T) {
	f := newTestFixture()
	req := buildScrapeURL("ffffffffffffffffffffffffffffffff", testInfoHash)
	raw, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := httpBody(raw)

	if !strings.Contains(body, "failure reason") {
		t.Errorf("scrape with unknown passkey should return failure reason; got: %q", body)
	}
}
