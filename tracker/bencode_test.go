package tracker

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestBencodeString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"spam", "4:spam"},
		{"", "0:"},
		{"a", "1:a"},
		{"with spaces", "11:with spaces"},
		// Length is in bytes, not runes.
		{"日本語", "9:日本語"},
	}

	for _, tt := range tests {
		if got := BencodeString(tt.input); got != tt.want {
			t.Errorf("BencodeString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBencodeStringIsBinarySafe(t *testing.T) {
	// Peer lists and info_hashes are raw bytes, so the encoding must not
	// assume text.
	raw := string([]byte{0x00, 0xFF, 0x1A, 0x80})

	got := BencodeString(raw)
	if got != "4:"+raw {
		t.Errorf("BencodeString mangled binary input: %q", got)
	}
}

func TestBencodeInt(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{42, "i42e"},
		{0, "i0e"},
		{-1, "i-1e"},
		{1 << 40, "i1099511627776e"},
	}

	for _, tt := range tests {
		if got := BencodeInt(tt.input); got != tt.want {
			t.Errorf("BencodeInt(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBencodeDictSortsKeys(t *testing.T) {
	// Deliberately inserted out of order; BEP 3 requires sorted keys.
	got := BencodeDict(map[string]string{
		"incomplete": BencodeInt(2),
		"complete":   BencodeInt(1),
		"downloaded": BencodeInt(3),
	})

	want := "d8:completei1e10:downloadedi3e10:incompletei2ee"
	if got != want {
		t.Errorf("BencodeDict = %q, want %q", got, want)
	}
}

func TestBencodeDictEmpty(t *testing.T) {
	if got := BencodeDict(nil); got != "de" {
		t.Errorf("BencodeDict(nil) = %q, want %q", got, "de")
	}
	if got := BencodeDict(map[string]string{}); got != "de" {
		t.Errorf("BencodeDict(empty) = %q, want %q", got, "de")
	}
}

func TestBencodeDictNested(t *testing.T) {
	inner := BencodeDict(map[string]string{"complete": BencodeInt(1)})
	got := BencodeDict(map[string]string{"files": inner})

	want := "d5:filesd8:completei1eee"
	if got != want {
		t.Errorf("nested dict = %q, want %q", got, want)
	}
}

func TestBencodeList(t *testing.T) {
	if got := BencodeList(nil); got != "le" {
		t.Errorf("BencodeList(nil) = %q, want %q", got, "le")
	}

	got := BencodeList([]string{BencodeString("spam"), BencodeInt(42)})
	if want := "l4:spami42ee"; got != want {
		t.Errorf("BencodeList = %q, want %q", got, want)
	}
}

// bencodeKeys returns the top-level keys of a bencoded dictionary in the order
// they appear, so tests can assert BEP 3's sorting requirement.
func bencodeKeys(t *testing.T, encoded string) []string {
	t.Helper()

	if !strings.HasPrefix(encoded, "d") {
		t.Fatalf("not a bencoded dictionary: %q", encoded)
	}

	var keys []string
	pos := 1

	for pos < len(encoded) && encoded[pos] != 'e' {
		key, next, err := readBencodeString(encoded, pos)
		if err != nil {
			t.Fatalf("failed to read key at %d: %v (%q)", pos, err, encoded)
		}
		keys = append(keys, key)

		next, err = skipBencodeValue(encoded, next)
		if err != nil {
			t.Fatalf("failed to skip value for %q: %v", key, err)
		}
		pos = next
	}

	return keys
}

func readBencodeString(s string, pos int) (string, int, error) {
	colon := strings.IndexByte(s[pos:], ':')
	if colon < 0 {
		return "", 0, fmt.Errorf("no length delimiter")
	}
	colon += pos

	length, err := strconv.Atoi(s[pos:colon])
	if err != nil {
		return "", 0, fmt.Errorf("bad length %q", s[pos:colon])
	}

	start := colon + 1
	end := start + length
	if end > len(s) {
		return "", 0, fmt.Errorf("length %d overruns the buffer", length)
	}

	return s[start:end], end, nil
}

func skipBencodeValue(s string, pos int) (int, error) {
	if pos >= len(s) {
		return 0, fmt.Errorf("unexpected end of input")
	}

	switch c := s[pos]; {
	case c == 'i':
		end := strings.IndexByte(s[pos:], 'e')
		if end < 0 {
			return 0, fmt.Errorf("unterminated integer")
		}
		return pos + end + 1, nil

	case c == 'd' || c == 'l':
		pos++
		for pos < len(s) && s[pos] != 'e' {
			if c == 'd' {
				var err error
				if _, pos, err = readBencodeString(s, pos); err != nil {
					return 0, err
				}
			}
			var err error
			if pos, err = skipBencodeValue(s, pos); err != nil {
				return 0, err
			}
		}
		return pos + 1, nil

	case c >= '0' && c <= '9':
		_, next, err := readBencodeString(s, pos)
		return next, err

	default:
		return 0, fmt.Errorf("unexpected byte %q at %d", c, pos)
	}
}

func assertSortedKeys(t *testing.T, label, encoded string) {
	t.Helper()

	keys := bencodeKeys(t, encoded)
	if len(keys) == 0 {
		t.Fatalf("%s: no keys found in %q", label, encoded)
	}

	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)

	for i := range keys {
		if keys[i] != sorted[i] {
			t.Errorf("%s: keys are not in sorted order\n got: %v\nwant: %v", label, keys, sorted)
			return
		}
	}
}

// The scrape response used to emit complete/incomplete/downloaded, which is
// not sorted order.
func TestScrapeResponseKeysAreSorted(t *testing.T) {
	h := newTestHarness(t)
	user, passkey := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")
	if _, err := h.worker.Announce(req, user, req.IP, "qB"); err != nil {
		t.Fatalf("announce failed: %v", err)
	}

	server := newTestServer(t, h)
	body := bencodeBody(t, server.handleScrape(scrapeRequest(t, h.infoHash), passkey, req.IP, true))

	assertSortedKeys(t, "scrape", body)

	// And the per-torrent dictionary inside files.
	files := bencodeKeys(t, body)
	if len(files) != 1 || files[0] != "files" {
		t.Fatalf("unexpected top-level keys: %v", files)
	}
}

func TestAnnounceResponseKeysAreSorted(t *testing.T) {
	h := newTestHarness(t)
	seeder, _ := h.addUser(t, 1, true)
	_, passkey := h.addUser(t, 2, true)

	// An IPv6 seeder forces the peers6 key to appear alongside peers.
	seed := announceParams(h.infoHash, testPeerID("seed0001"), 51413, 0, "started")
	seed.IP = mustParseIP(t, "2001:db8::10")
	if _, err := h.worker.Announce(seed, seeder, seed.IP, "qB"); err != nil {
		t.Fatalf("seeder announce failed: %v", err)
	}

	server := newTestServer(t, h)
	req := announceHTTPRequest(t, passkey, h.infoHash, "peer0002", "2001:db8::20")
	body := bencodeBody(t, server.handleAnnounce(req, passkey, mustParseIP(t, "2001:db8::20"), true))

	assertSortedKeys(t, "announce", body)

	keys := bencodeKeys(t, body)
	if !contains(keys, "peers6") {
		t.Errorf("expected peers6 among %v", keys)
	}
}

func TestAnnounceWarningKeySorts(t *testing.T) {
	h := newTestHarness(t)
	_, passkey := h.addUser(t, 1, true)

	server := newTestServer(t, h)
	req := announceHTTPRequest(t, passkey, h.infoHash, "peer0001", "10.0.0.1")
	body := bencodeBody(t, server.handleAnnounce(req, passkey, mustParseIP(t, "10.0.0.1"), true))

	assertSortedKeys(t, "announce", body)
}

// Helpers shared by the response-shape tests.

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func mustParseIP(t *testing.T, addr string) net.IP {
	t.Helper()

	ip := net.ParseIP(addr)
	if ip == nil {
		t.Fatalf("bad IP fixture %q", addr)
	}
	return ip
}

func announceHTTPRequest(t *testing.T, passkey, infoHash, peerSuffix, ip string) *http.Request {
	t.Helper()

	return httptest.NewRequest(http.MethodGet,
		"/"+passkey+"/announce?"+announceQuery(infoHash, peerSuffix, ip), nil)
}

// bencodeBody strips the HTTP envelope the server wraps responses in.
func bencodeBody(t *testing.T, response []byte) string {
	t.Helper()

	_, body, found := strings.Cut(string(response), "\r\n\r\n")
	if !found {
		t.Fatalf("response has no header/body separator: %q", response)
	}
	return body
}
