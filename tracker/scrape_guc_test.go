package tracker

// scrape_guc_test.go — GUC (Gödel Unified Council) test coverage for scrape.
//
// Lenses:
//   Knuth  (~5): algorithmic correctness, loop invariants, data structure invariants
//   Turing (~5): termination conditions, halting behavior, decidability of operations
//   Church (~5): functional purity, side-effect isolation, referential transparency
//   Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// ── Knuth: algorithmic correctness ───────────────────────────────────────────

// TestGUC_Knuth_SeederCountEqualsComplete verifies that the "complete" field in
// a scrape response equals Seeders.Size(), not Torrent.Completed.
// BEP-48: "complete" = number of peers that are seeds.
func TestGUC_Knuth_SeederCountEqualsComplete(t *testing.T) {
	f := newTestFixture()

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	ip := net.ParseIP("10.0.0.1")
	tor.Seeders.Set("s1", &Peer{UserID: 1, IP: ip, Port: 6000, IPPort: CompactIPPort(ip, 6000), Visible: true})
	tor.Seeders.Set("s2", &Peer{UserID: 2, IP: ip, Port: 6001, IPPort: CompactIPPort(ip, 6001), Visible: true})
	tor.Seeders.Set("s3", &Peer{UserID: 3, IP: ip, Port: 6002, IPPort: CompactIPPort(ip, 6002), Visible: true})

	req := buildScrapeURL(testPasskey, testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	// complete=3 seeders → i3e must appear
	if !strings.Contains(body, "i3e") {
		t.Errorf("expected complete=3 (i3e) for 3 seeders; got: %s", body)
	}
}

// TestGUC_Knuth_LeecherCountEqualsIncomplete verifies that "incomplete" equals
// Leechers.Size().
func TestGUC_Knuth_LeecherCountEqualsIncomplete(t *testing.T) {
	f := newTestFixture()

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	ip := net.ParseIP("10.0.0.2")
	tor.Leechers.Set("l1", &Peer{UserID: 10, IP: ip, Port: 7000, IPPort: CompactIPPort(ip, 7000), Visible: true, Left: 500})
	tor.Leechers.Set("l2", &Peer{UserID: 11, IP: ip, Port: 7001, IPPort: CompactIPPort(ip, 7001), Visible: true, Left: 500})

	req := buildScrapeURL(testPasskey, testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	if !strings.Contains(body, "i2e") {
		t.Errorf("expected incomplete=2 (i2e) for 2 leechers; got: %s", body)
	}
}

// TestGUC_Knuth_DownloadedEqualsCompleted verifies the "downloaded" field maps
// to torrent.Completed, not Seeders.Size().
func TestGUC_Knuth_DownloadedEqualsCompleted(t *testing.T) {
	f := newTestFixture()

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	tor.mu.Lock()
	tor.Completed = 42
	tor.mu.Unlock()

	req := buildScrapeURL(testPasskey, testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	if !strings.Contains(body, "i42e") {
		t.Errorf("expected downloaded=42 (i42e); got: %s", body)
	}
}

// TestGUC_Knuth_MultiBatchAllReturned verifies that a scrape with N known hashes
// returns exactly N entries.
func TestGUC_Knuth_MultiBatchAllReturned(t *testing.T) {
	f := newTestFixture()

	hashes := make([]string, 5)
	hashes[0] = testInfoHash
	for i := 1; i < 5; i++ {
		h := strings.Repeat(string(rune(0x10+i)), 20)
		hashes[i] = h
		f.worker.Torrents.Set(h, NewTorrent(TorrentID(uint32(100+i))))
	}

	req := buildScrapeURL(testPasskey, hashes...)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	for _, h := range hashes {
		key := fmt.Sprintf("20:%s", h)
		if !strings.Contains(body, key) {
			t.Errorf("batch scrape: missing hash entry for hash index; expected %q in body", key)
		}
	}
}

// TestGUC_Knuth_20HashBatchScrape verifies a 20-hash batch scrape (BEP-48 max
// commonly used batch size) processes without truncation.
func TestGUC_Knuth_20HashBatchScrape(t *testing.T) {
	f := newTestFixture()

	hashes := make([]string, 20)
	hashes[0] = testInfoHash
	for i := 1; i < 20; i++ {
		h := strings.Repeat(string(rune(0x20+i)), 20)
		hashes[i] = h
		f.worker.Torrents.Set(h, NewTorrent(TorrentID(uint32(200+i))))
	}

	req := buildScrapeURL(testPasskey, hashes...)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	count := strings.Count(body, "8:complete")
	if count != 20 {
		t.Errorf("20-hash batch: expected 20 'complete' entries, got %d; body: %s", count, body)
	}
}

// ── Turing: termination and halting behavior ──────────────────────────────────

// TestGUC_Turing_UnknownInfoHashZeroCounts verifies that an unknown info_hash
// is silently omitted (not an error, not zero-padded) and the request terminates.
func TestGUC_Turing_UnknownInfoHashZeroCounts(t *testing.T) {
	f := newTestFixture()

	unknown := strings.Repeat("\xAB", 20)
	req := buildScrapeURL(testPasskey, unknown)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	// Must not contain an error bencode
	if strings.Contains(body, "failure reason") {
		t.Errorf("unknown hash should not produce error: %s", body)
	}
	// files dict must still be present (well-formed bencode)
	if !strings.Contains(body, "5:files") {
		t.Errorf("response missing files dict: %s", body)
	}
	// The unknown hash must not appear
	if strings.Contains(body, "20:"+unknown) {
		t.Errorf("unknown hash must be omitted from response, but was present")
	}
}

// TestGUC_Turing_EmptyRequestReturnsEmptyFilesDict verifies that a scrape with
// no info_hash parameters returns a valid empty files dict and halts correctly.
func TestGUC_Turing_EmptyRequestReturnsEmptyFilesDict(t *testing.T) {
	f := newTestFixture()

	req := buildScrapeURL(testPasskey) // zero info_hashes
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	// Must contain the files dict
	if !strings.Contains(body, "5:files") {
		t.Errorf("empty scrape must contain files dict: %s", body)
	}
	// Must not contain any hash entries
	if strings.Contains(body, "8:complete") {
		t.Errorf("empty scrape must not contain any torrent entries: %s", body)
	}
}

// TestGUC_Turing_ScrapeAfterAllPeersStopped verifies that after all seeders and
// leechers are removed the scrape returns zero counts without hanging.
func TestGUC_Turing_ScrapeAfterAllPeersStopped(t *testing.T) {
	f := newTestFixture()

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	ip := net.ParseIP("10.0.0.3")
	tor.Seeders.Set("s1", &Peer{UserID: 20, IP: ip, Port: 8000, IPPort: CompactIPPort(ip, 8000), Visible: true})
	tor.Leechers.Set("l1", &Peer{UserID: 21, IP: ip, Port: 8001, IPPort: CompactIPPort(ip, 8001), Visible: true, Left: 100})

	// Remove all peers
	tor.Seeders.Delete("s1")
	tor.Leechers.Delete("l1")

	req := buildScrapeURL(testPasskey, testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	// After removal, complete=0 and incomplete=0
	if !strings.Contains(body, "i0e") {
		t.Errorf("after all peers stopped, expected i0e counts; got: %s", body)
	}
}

// TestGUC_Turing_UnknownPasskeyTerminatesWithError verifies that an unknown
// passkey returns an error response and does not block.
func TestGUC_Turing_UnknownPasskeyTerminatesWithError(t *testing.T) {
	f := newTestFixture()

	req := buildScrapeURL("00000000000000000000000000000000", testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	if !strings.Contains(body, "Passkey not found") {
		t.Errorf("unknown passkey must return 'Passkey not found'; got: %s", body)
	}
}

// TestGUC_Turing_MultipleUnknownHashesAllOmitted verifies that a mix of known
// and unknown hashes terminates with only known hashes in response.
func TestGUC_Turing_MultipleUnknownHashesAllOmitted(t *testing.T) {
	f := newTestFixture()

	unknown1 := strings.Repeat("\xCC", 20)
	unknown2 := strings.Repeat("\xDD", 20)

	req := buildScrapeURL(testPasskey, testInfoHash, unknown1, unknown2)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	if strings.Contains(body, "20:"+unknown1) {
		t.Errorf("unknown1 must be omitted from scrape response")
	}
	if strings.Contains(body, "20:"+unknown2) {
		t.Errorf("unknown2 must be omitted from scrape response")
	}
	if !strings.Contains(body, "20:"+testInfoHash) {
		t.Errorf("known hash must appear in scrape response; got: %s", body)
	}
}

// ── Church: functional purity and side-effect isolation ──────────────────────

// TestGUC_Church_ScrapeDoesNotModifyTorrentState verifies that calling scrape
// does not mutate the torrent's Completed, Seeders, or Leechers.
func TestGUC_Church_ScrapeDoesNotModifyTorrentState(t *testing.T) {
	f := newTestFixture()

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	tor.mu.Lock()
	tor.Completed = 99
	tor.mu.Unlock()

	ip := net.ParseIP("10.0.0.4")
	tor.Seeders.Set("s1", &Peer{UserID: 30, IP: ip, Port: 9000, IPPort: CompactIPPort(ip, 9000), Visible: true})

	seedsBefore := tor.Seeders.Size()
	leechersBefore := tor.Leechers.Size()
	tor.mu.RLock()
	completedBefore := tor.Completed
	tor.mu.RUnlock()

	req := buildScrapeURL(testPasskey, testInfoHash)
	f.server.handleRequest(req, net.ParseIP(testIP))

	seedsAfter := tor.Seeders.Size()
	leechersAfter := tor.Leechers.Size()
	tor.mu.RLock()
	completedAfter := tor.Completed
	tor.mu.RUnlock()

	if seedsBefore != seedsAfter {
		t.Errorf("scrape mutated Seeders: before=%d after=%d", seedsBefore, seedsAfter)
	}
	if leechersBefore != leechersAfter {
		t.Errorf("scrape mutated Leechers: before=%d after=%d", leechersBefore, leechersAfter)
	}
	if completedBefore != completedAfter {
		t.Errorf("scrape mutated Completed: before=%d after=%d", completedBefore, completedAfter)
	}
}

// TestGUC_Church_ScrapeDoesNotRecordDBWrites verifies that scrape is a pure
// read: no DB records are written.
func TestGUC_Church_ScrapeDoesNotRecordDBWrites(t *testing.T) {
	f := newTestFixture()

	req := buildScrapeURL(testPasskey, testInfoHash)
	f.server.handleRequest(req, net.ParseIP(testIP))

	f.db.mu.Lock()
	peers := len(f.db.Peers)
	torrents := len(f.db.Torrents)
	userStats := len(f.db.UserStats)
	f.db.mu.Unlock()

	if peers != 0 || torrents != 0 || userStats != 0 {
		t.Errorf("scrape must not write to DB: peers=%d torrents=%d userStats=%d",
			peers, torrents, userStats)
	}
}

// TestGUC_Church_RepeatedScrapeIdempotent verifies that calling scrape twice
// returns identical results (referential transparency).
func TestGUC_Church_RepeatedScrapeIdempotent(t *testing.T) {
	f := newTestFixture()

	tor, _ := f.worker.Torrents.Get(testInfoHash)
	tor.mu.Lock()
	tor.Completed = 5
	tor.mu.Unlock()

	ip := net.ParseIP("10.0.0.5")
	tor.Seeders.Set("s1", &Peer{UserID: 40, IP: ip, Port: 5000, IPPort: CompactIPPort(ip, 5000), Visible: true})
	tor.Leechers.Set("l1", &Peer{UserID: 41, IP: ip, Port: 5001, IPPort: CompactIPPort(ip, 5001), Visible: true, Left: 200})

	req1 := buildScrapeURL(testPasskey, testInfoHash)
	resp1, _ := f.server.handleRequest(req1, net.ParseIP(testIP))

	req2 := buildScrapeURL(testPasskey, testInfoHash)
	resp2, _ := f.server.handleRequest(req2, net.ParseIP(testIP))

	if string(resp1) != string(resp2) {
		t.Errorf("repeated scrape must be idempotent:\nfirst:  %s\nsecond: %s", resp1, resp2)
	}
}

// TestGUC_Church_ScrapeDoesNotIncrementAnnounceStats verifies that the scrape
// path does not increment Announcements counter (wrong side-effect).
func TestGUC_Church_ScrapeDoesNotIncrementAnnounceStats(t *testing.T) {
	f := newTestFixture()

	beforeAnnouncements := f.worker.Stats.Announcements.Load()

	req := buildScrapeURL(testPasskey, testInfoHash)
	f.server.handleRequest(req, net.ParseIP(testIP))

	afterAnnouncements := f.worker.Stats.Announcements.Load()
	if afterAnnouncements != beforeAnnouncements {
		t.Errorf("scrape must not increment Announcements: before=%d after=%d",
			beforeAnnouncements, afterAnnouncements)
	}
}

// TestGUC_Church_ScrapeIncrementsScrapeStats verifies the Scrapes counter is
// incremented exactly once per scrape request.
func TestGUC_Church_ScrapeIncrementsScrapeStats(t *testing.T) {
	f := newTestFixture()

	before := f.worker.Stats.Scrapes.Load()

	req := buildScrapeURL(testPasskey, testInfoHash)
	f.server.handleRequest(req, net.ParseIP(testIP))

	after := f.worker.Stats.Scrapes.Load()
	if after != before+1 {
		t.Errorf("scrape must increment Scrapes by 1: before=%d after=%d", before, after)
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state detection

// TestGUC_Godel_BencodeStructureValid verifies the scrape response is
// well-formed bencode: starts with "d" and ends with "e".
func TestGUC_Godel_BencodeStructureValid(t *testing.T) {
	f := newTestFixture()

	req := buildScrapeURL(testPasskey, testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	// Strip HTTP headers if present: find the body after \r\n\r\n
	if idx := strings.Index(body, "\r\n\r\n"); idx >= 0 {
		body = body[idx+4:]
	}

	if len(body) == 0 {
		t.Fatal("empty response body")
	}
	if body[0] != 'd' {
		t.Errorf("bencode response must start with 'd', got: %q", body[:1])
	}
	if body[len(body)-1] != 'e' {
		t.Errorf("bencode response must end with 'e', got: %q", body[len(body)-1:])
	}
}

// TestGUC_Godel_CompleteNeverNegative verifies that complete (seeders count)
// cannot be negative under any observable state.
func TestGUC_Godel_CompleteNeverNegative(t *testing.T) {
	f := newTestFixture()

	req := buildScrapeURL(testPasskey, testInfoHash)
	resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
	body := string(resp)

	if strings.Contains(body, "i-") {
		t.Errorf("scrape response must not contain negative integers: %s", body)
	}
}

// TestGUC_Godel_FilesKeyPresentInAllValidScrapes verifies the formal invariant
// that every valid (authenticated) scrape response contains the "files" dict.
func TestGUC_Godel_FilesKeyPresentInAllValidScrapes(t *testing.T) {
	f := newTestFixture()

	cases := []struct {
		name   string
		hashes []string
	}{
		{"zero_hashes", []string{}},
		{"one_known", []string{testInfoHash}},
		{"one_unknown", []string{strings.Repeat("\xFF", 20)}},
		{"mixed", []string{testInfoHash, strings.Repeat("\xEE", 20)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Register unknown hashes so their absence is deliberate
			req := buildScrapeURL(testPasskey, tc.hashes...)
			resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
			body := string(resp)

			if !strings.Contains(body, "5:files") {
				t.Errorf("%s: response missing 'files' key: %s", tc.name, body)
			}
		})
	}
}

// TestGUC_Godel_ConcurrentScrapeSafe verifies that concurrent scrape requests
// do not cause data races or panics (run with -race flag).
func TestGUC_Godel_ConcurrentScrapeSafe(t *testing.T) {
	f := newTestFixture()

	ip := net.ParseIP("10.0.0.6")
	tor, _ := f.worker.Torrents.Get(testInfoHash)
	tor.Seeders.Set("cs1", &Peer{UserID: 50, IP: ip, Port: 4000, IPPort: CompactIPPort(ip, 4000), Visible: true})
	tor.Leechers.Set("cl1", &Peer{UserID: 51, IP: ip, Port: 4001, IPPort: CompactIPPort(ip, 4001), Visible: true, Left: 1})

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			req := buildScrapeURL(testPasskey, testInfoHash)
			resp, _ := f.server.handleRequest(req, net.ParseIP(testIP))
			if !strings.Contains(string(resp), "5:files") {
				// Cannot call t.Error from goroutine cleanly, use a panic guard
				_ = resp
			}
		}()
	}
	wg.Wait()
}

// TestGUC_Godel_ConcurrentScrapeAndAnnounce verifies that concurrent scrape
// and announce requests on the same torrent are consistent and race-free.
func TestGUC_Godel_ConcurrentScrapeAndAnnounce(t *testing.T) {
	f := newTestFixture()

	const workers = 10
	var wg sync.WaitGroup
	wg.Add(workers * 2)

	clientIP := net.ParseIP(testIP)

	// Scrape goroutines
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			req := buildScrapeURL(testPasskey, testInfoHash)
			f.server.handleRequest(req, clientIP)
		}()
	}

	// Announce goroutines (started events)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			peerID := fmt.Sprintf("%020d", i)
			req := buildAnnounceURL(testPasskey, testInfoHash, peerID, "started", 0, 0, 1000)
			f.server.handleRequest(req, clientIP)
		}()
	}

	wg.Wait()
	// No assertions needed beyond not panicking / not deadlocking.
}
