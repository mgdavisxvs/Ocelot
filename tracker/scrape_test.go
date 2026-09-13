package tracker

import (
	"net"
	"strings"
	"testing"
)

func TestScrape_UnknownPasskey(t *testing.T) {
	f := newTestFixture()
	req := buildScrapeURL("unknownpasskey0000000000000000ab", testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)
	if !strings.Contains(body, "Passkey not found") {
		t.Errorf("expected passkey error, got: %s", body)
	}
}

func TestScrape_KnownHash(t *testing.T) {
	f := newTestFixture()

	// seed some counters on the torrent
	tor, _ := f.worker.Torrents.Get(testInfoHash)
	tor.mu.Lock()
	tor.Completed = 7
	tor.mu.Unlock()

	req := buildScrapeURL(testPasskey, testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	// response must include the info_hash section
	if !strings.Contains(body, "files") {
		t.Errorf("scrape response missing 'files': %s", body)
	}
	// completed count = 7 → i7e
	if !strings.Contains(body, "i7e") {
		t.Errorf("scrape response missing downloaded count i7e: %s", body)
	}
}

func TestScrape_UnknownHash_Omitted(t *testing.T) {
	f := newTestFixture()
	unknownHash := strings.Repeat("\x99", 20)
	req := buildScrapeURL(testPasskey, unknownHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	// unknown hash must not appear in response body
	if strings.Contains(body, unknownHash) {
		t.Errorf("unknown hash should be omitted, got: %q", body)
	}
	// but the outer files dict must still be present
	if !strings.Contains(body, "files") {
		t.Errorf("scrape response missing 'files' wrapper: %q", body)
	}
}

func TestScrape_MultipleHashes(t *testing.T) {
	f := newTestFixture()

	// add a second torrent
	tor2 := NewTorrent(TorrentID(2))
	hash2 := strings.Repeat("\x02", 20)
	f.worker.Torrents.Set(hash2, tor2)

	req := buildScrapeURL(testPasskey, testInfoHash, hash2)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	// Both hashes must appear by their length prefix
	if !strings.Contains(body, "20:"+testInfoHash) {
		t.Errorf("scrape missing first hash: %s", body)
	}
	if !strings.Contains(body, "20:"+hash2) {
		t.Errorf("scrape missing second hash: %s", body)
	}
}

func TestScrape_SeederLeecherCounts(t *testing.T) {
	f := newTestFixture()

	// manually place two seeders and one leecher
	tor, _ := f.worker.Torrents.Get(testInfoHash)
	ip := net.ParseIP("10.0.0.1")
	tor.Seeders.Set("s1", &Peer{UserID: 10, IP: ip, Port: 1000, IPPort: CompactIPPort(ip, 1000), Visible: true})
	tor.Seeders.Set("s2", &Peer{UserID: 11, IP: ip, Port: 1001, IPPort: CompactIPPort(ip, 1001), Visible: true})
	tor.Leechers.Set("l1", &Peer{UserID: 12, IP: ip, Port: 1002, IPPort: CompactIPPort(ip, 1002), Visible: true, Left: 1000})

	req := buildScrapeURL(testPasskey, testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	// complete=2 seeders
	if !strings.Contains(body, "i2e") {
		t.Errorf("scrape should show 2 seeders: %s", body)
	}
	// incomplete=1 leecher
	if !strings.Contains(body, "i1e") {
		t.Errorf("scrape should show 1 leecher: %s", body)
	}
}
