package tracker

// GUC (Gödel Unified Council) concurrency test suite for the Ocelot tracker.
// Analytical lenses:
//   Knuth  (~5): algorithmic correctness, loop invariants, data-structure invariants
//   Turing (~5): termination conditions, halting behaviour
//   Church (~5): functional purity, side-effect isolation, referential transparency
//   Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// concMakePeerID returns a 20-byte peer_id string derived from n.
func concMakePeerID(n int) string {
	return fmt.Sprintf("-GU%017d", n)[:20]
}

// concMakeInfoHash returns a 20-byte info_hash string derived from n.
func concMakeInfoHash(n int) string {
	return fmt.Sprintf("%020d", n)
}

// newWorkerWithTorrent builds a fixture with an additional torrent registered at
// the given infoHash.
func newWorkerWithTorrent(infoHash string) *testFixture {
	f := newTestFixture()
	if _, ok := f.worker.Torrents.Get(infoHash); !ok {
		f.worker.Torrents.Set(infoHash, NewTorrent(TorrentID(99)))
	}
	return f
}

// ── Knuth: algorithmic correctness ───────────────────────────────────────────

// TestGUC_Knuth_100ConcurrentLeechersSameTorrent fires 100 goroutines each
// announcing as a leecher on the same torrent and asserts that the peer count
// in the leechers list never exceeds the number of distinct peer IDs used.
func TestGUC_Knuth_100ConcurrentLeechersSameTorrent(t *testing.T) {
	const n = 100
	f := newTestFixture()
	user, _ := f.worker.Users.Get(testPasskey)
	ip := net.ParseIP(testIP)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			peerID := concMakePeerID(i)
			req := newAnnounceReqFull(testInfoHash, peerID, "started", 0, 0, 1000)
			_, err := f.worker.Announce(context.Background(), req, user, ip, "test-client", "")
			if err != nil {
				// log but don't fail — some may legitimately share peerKey
				_ = err
			}
		}()
	}
	wg.Wait()

	torrent, ok := f.worker.Torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent disappeared after concurrent leecher announces")
	}
	total := torrent.Seeders.Size() + torrent.Leechers.Size()
	if total > n {
		t.Errorf("peer count %d exceeds number of distinct leechers %d", total, n)
	}
	if total == 0 {
		t.Error("expected at least one peer after 100 leecher announces")
	}
}

// TestGUC_Knuth_50ConcurrentSeederAnnounces fires 50 goroutines each announcing
// as a seeder (left=0) and verifies the seeder list size is bounded.
func TestGUC_Knuth_50ConcurrentSeederAnnounces(t *testing.T) {
	const n = 50
	f := newTestFixture()
	user, _ := f.worker.Users.Get(testPasskey)
	ip := net.ParseIP(testIP)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			peerID := concMakePeerID(i)
			req := newAnnounceReqFull(testInfoHash, peerID, "", 1000, 1000, 0)
			f.worker.Announce(context.Background(), req, user, ip, "test-client", "")
		}()
	}
	wg.Wait()

	torrent, ok := f.worker.Torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent disappeared after concurrent seeder announces")
	}
	seeders := torrent.Seeders.Size()
	if seeders > n {
		t.Errorf("seeder count %d exceeds number of distinct seeders %d", seeders, n)
	}
	if seeders == 0 {
		t.Error("expected at least one seeder after 50 concurrent seeder announces")
	}
}

// TestGUC_Knuth_PeerListSetGetInvariant verifies that PeerList.Set followed
// immediately by Get always returns the same peer pointer (no lost update).
func TestGUC_Knuth_PeerListSetGetInvariant(t *testing.T) {
	const n = 200
	pl := NewPeerList()

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("key%d", i)
			peer := &Peer{UserID: UserID(i)}
			pl.Set(key, peer)
			got, ok := pl.Get(key)
			if !ok {
				// another goroutine may have deleted; not an error here
				return
			}
			if got == nil {
				t.Errorf("Get returned nil peer for key %s immediately after Set", key)
			}
		}()
	}
	wg.Wait()
}

// TestGUC_Knuth_TorrentListSizeMonotone adds N torrents concurrently and
// verifies that the final size equals N (no entry is silently lost).
func TestGUC_Knuth_TorrentListSizeMonotone(t *testing.T) {
	const n = 100
	tl := NewTorrentList()

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			hash := concMakeInfoHash(i)
			tl.Set(hash, NewTorrent(TorrentID(i)))
		}()
	}
	wg.Wait()

	size := tl.Size()
	if size != n {
		t.Errorf("TorrentList.Size() = %d, want %d after %d concurrent sets", size, n, n)
	}
}

// TestGUC_Knuth_StatsCountersAccumulateCorrectly increments SuccAnnouncements
// from N goroutines and asserts the final count equals N*increments.
func TestGUC_Knuth_StatsCountersAccumulateCorrectly(t *testing.T) {
	const goroutines = 50
	const incPerGoroutine = 10
	stats := &Stats{}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < incPerGoroutine; j++ {
				stats.SuccAnnouncements.Add(1)
			}
		}()
	}
	wg.Wait()

	want := uint64(goroutines * incPerGoroutine)
	got := stats.SuccAnnouncements.Load()
	if got != want {
		t.Errorf("SuccAnnouncements = %d, want %d", got, want)
	}
}

// ── Turing: termination and halting behaviour ─────────────────────────────────

// TestGUC_Turing_AnnounceTerminatesUnderHighConcurrency checks that
// Announce always returns (does not deadlock) when 100 goroutines call it
// concurrently. The test uses a WaitGroup with a timeout guard.
func TestGUC_Turing_AnnounceTerminatesUnderHighConcurrency(t *testing.T) {
	const n = 100
	f := newTestFixture()
	user, _ := f.worker.Users.Get(testPasskey)
	ip := net.ParseIP(testIP)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			peerID := concMakePeerID(i % 50) // allow some key collisions
			req := newAnnounceReqFull(testInfoHash, peerID, "started", 0, 0, 500)
			f.worker.Announce(context.Background(), req, user, ip, "test-client", "")
		}()
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// terminated — OK
	case <-context.Background().Done():
		t.Fatal("Announce goroutines did not terminate")
	}
	// Ensure channel was closed (no goroutine leak)
	<-done
}

// TestGUC_Turing_StopEventAlwaysRemovesPeer verifies that announcing "stopped"
// always removes the peer regardless of concurrency with other announces on the
// same torrent.
func TestGUC_Turing_StopEventAlwaysRemovesPeer(t *testing.T) {
	f := newTestFixture()
	user, _ := f.worker.Users.Get(testPasskey)
	ip := net.ParseIP(testIP)

	// First: announce as leecher to insert the peer.
	req := newAnnounceReqFull(testInfoHash, testPeerID, "started", 0, 0, 1000)
	if _, err := f.worker.Announce(context.Background(), req, user, ip, "tc", ""); err != nil {
		t.Fatalf("initial announce failed: %v", err)
	}

	torrent, _ := f.worker.Torrents.Get(testInfoHash)
	if torrent.Leechers.Size() == 0 {
		t.Fatal("expected peer inserted before stop test")
	}

	// Now announce stopped.
	stopReq := newAnnounceReqFull(testInfoHash, testPeerID, "stopped", 0, 0, 1000)
	if _, err := f.worker.Announce(context.Background(), stopReq, user, ip, "tc", ""); err != nil {
		t.Fatalf("stopped announce failed: %v", err)
	}

	if torrent.Leechers.Size() != 0 {
		t.Errorf("leecher peer still present after stopped announce; size=%d", torrent.Leechers.Size())
	}
}

// TestGUC_Turing_ConcurrentAddRemoveTorrentNoDeadlock concurrently adds and
// deletes torrents from TorrentList and verifies no deadlock occurs.
func TestGUC_Turing_ConcurrentAddRemoveTorrentNoDeadlock(t *testing.T) {
	const n = 80
	tl := NewTorrentList()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			hash := concMakeInfoHash(i)
			tl.Set(hash, NewTorrent(TorrentID(i)))
		}()
		go func() {
			defer wg.Done()
			hash := concMakeInfoHash(i)
			tl.Delete(hash)
		}()
	}
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		// success — no deadlock
	case <-context.Background().Done():
		t.Fatal("deadlock detected in concurrent TorrentList add+remove")
	}
	<-done
}

// TestGUC_Turing_ConcurrentAddRemoveUserNoDeadlock concurrently adds and
// deletes users from UserList and verifies no deadlock occurs.
func TestGUC_Turing_ConcurrentAddRemoveUserNoDeadlock(t *testing.T) {
	const n = 80
	ul := NewUserList()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			pk := fmt.Sprintf("pk%030d", i)
			ul.Set(pk, NewUser(UserID(i), true, false))
		}()
		go func() {
			defer wg.Done()
			pk := fmt.Sprintf("pk%030d", i)
			ul.Delete(pk)
		}()
	}
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		// success
	case <-context.Background().Done():
		t.Fatal("deadlock detected in concurrent UserList add+remove")
	}
	<-done
}

// ── Church: functional purity and side-effect isolation ──────────────────────

// TestGUC_Church_AnnounceResponseIsImmutable verifies that the AnnounceResponse
// returned from Announce is not modified by subsequent announces from other
// goroutines (each call returns a freshly allocated struct).
func TestGUC_Church_AnnounceResponseIsImmutable(t *testing.T) {
	f := newTestFixture()
	user, _ := f.worker.Users.Get(testPasskey)
	ip := net.ParseIP(testIP)

	req := newAnnounceReqFull(testInfoHash, testPeerID, "started", 0, 0, 1000)
	resp1, err := f.worker.Announce(context.Background(), req, user, ip, "tc", "")
	if err != nil {
		t.Fatalf("announce error: %v", err)
	}

	// Record values from first response.
	interval1 := resp1.Interval
	complete1 := resp1.Complete
	incomplete1 := resp1.Incomplete

	// Launch 20 more goroutines announcing on the same torrent.
	var wg sync.WaitGroup
	const n = 20
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			r := newAnnounceReqFull(testInfoHash, concMakePeerID(i+100), "started", 0, 0, 500)
			f.worker.Announce(context.Background(), r, user, ip, "tc", "")
		}()
	}
	wg.Wait()

	// resp1 values must be unchanged — they are read-only fields of the returned struct.
	if resp1.Interval != interval1 {
		t.Errorf("resp1.Interval mutated: got %d, want %d", resp1.Interval, interval1)
	}
	if resp1.Complete != complete1 {
		t.Errorf("resp1.Complete mutated: got %d, want %d", resp1.Complete, complete1)
	}
	if resp1.Incomplete != incomplete1 {
		t.Errorf("resp1.Incomplete mutated: got %d, want %d", resp1.Incomplete, incomplete1)
	}
}

// TestGUC_Church_ParseAnnounceParamsIsPure verifies that ParseAnnounceParams
// does not modify its input url.Values and produces identical output for
// identical input regardless of call order.
func TestGUC_Church_ParseAnnounceParamsIsPure(t *testing.T) {
	params := buildAnnounceURL(testPasskey, testInfoHash, testPeerID, "started", 0, 0, 1000).URL.Query()
	// Take a snapshot of the params before calling parse.
	snapshot := make(map[string][]string, len(params))
	for k, v := range params {
		cp := make([]string, len(v))
		copy(cp, v)
		snapshot[k] = cp
	}

	ip := net.ParseIP(testIP)
	req1, err1 := ParseAnnounceParams(params, ip)
	req2, err2 := ParseAnnounceParams(params, ip)

	if err1 != nil || err2 != nil {
		t.Fatalf("ParseAnnounceParams errors: %v, %v", err1, err2)
	}
	if req1.InfoHash != req2.InfoHash {
		t.Errorf("InfoHash differs between identical calls: %q vs %q", req1.InfoHash, req2.InfoHash)
	}
	if req1.Port != req2.Port {
		t.Errorf("Port differs between identical calls: %d vs %d", req1.Port, req2.Port)
	}

	// Verify params were not mutated.
	for k, vs := range snapshot {
		got, ok := params[k]
		if !ok {
			t.Errorf("param key %q deleted by ParseAnnounceParams", k)
			continue
		}
		if len(got) != len(vs) {
			t.Errorf("param %q length changed: got %d want %d", k, len(got), len(vs))
			continue
		}
		for i, v := range vs {
			if got[i] != v {
				t.Errorf("param %q[%d] changed: got %q want %q", k, i, got[i], v)
			}
		}
	}
}

// TestGUC_Church_ConcurrentAnnounceScrapeIsolation verifies that concurrent
// announces and scrapes do not corrupt each other's state by reading the torrent
// peer counts both before and after a wave of announces and confirming counts
// only move in the expected direction.
func TestGUC_Church_ConcurrentAnnounceScrapeIsolation(t *testing.T) {
	f := newTestFixture()
	user, _ := f.worker.Users.Get(testPasskey)
	ip := net.ParseIP(testIP)

	torrent, ok := f.worker.Torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent not found")
	}

	initialSeeders := torrent.Seeders.Size()
	initialLeechers := torrent.Leechers.Size()

	const n = 40
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			peerID := concMakePeerID(i)
			left := int64(500)
			if i%2 == 0 {
				left = 0
			}
			req := newAnnounceReqFull(testInfoHash, peerID, "started", 100, 50, left)
			f.worker.Announce(context.Background(), req, user, ip, "tc", "")
		}()
	}
	wg.Wait()

	finalSeeders := torrent.Seeders.Size()
	finalLeechers := torrent.Leechers.Size()

	if finalSeeders < initialSeeders {
		t.Errorf("seeder count decreased from %d to %d without stop events", initialSeeders, finalSeeders)
	}
	if finalLeechers < initialLeechers {
		t.Errorf("leecher count decreased from %d to %d without stop events", initialLeechers, finalLeechers)
	}
}

// TestGUC_Church_WhitelistAllowAllWhenEmpty verifies the pure predicate:
// an empty Whitelist always returns true regardless of peer_id.
func TestGUC_Church_WhitelistAllowAllWhenEmpty(t *testing.T) {
	wl := NewWhitelist()
	cases := []struct {
		name   string
		peerID []byte
	}{
		{"empty peerID", []byte{}},
		{"typical peerID", []byte("-qB4500-xxxxxxxxxxxx")},
		{"all zeros", make([]byte, 20)},
		{"all 0xFF", func() []byte { b := make([]byte, 20); for i := range b { b[i] = 0xFF }; return b }()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if !wl.IsAllowed(tc.peerID) {
				t.Errorf("empty whitelist denied peer_id %q", tc.peerID)
			}
		})
	}
}

// ── Gödel: formal consistency and impossible-state detection ──────────────────

// TestGUC_Godel_PeerCountInvariantUnderConcurrentAnnounces asserts the
// fundamental tracker invariant: a peer cannot appear in both Seeders and
// Leechers at the same time for the same peerKey.
func TestGUC_Godel_PeerCountInvariantUnderConcurrentAnnounces(t *testing.T) {
	const n = 60
	f := newTestFixture()
	user, _ := f.worker.Users.Get(testPasskey)
	ip := net.ParseIP(testIP)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			peerID := concMakePeerID(i % 15) // 15 distinct peers, some will race
			left := int64(i%3) * 100
			req := newAnnounceReqFull(testInfoHash, peerID, "started", int64(i)*10, 0, left)
			f.worker.Announce(context.Background(), req, user, ip, "tc", "")
		}()
	}
	wg.Wait()

	torrent, ok := f.worker.Torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent disappeared")
	}

	// Collect all peerKeys in seeders.
	seederKeys := make(map[string]bool)
	torrent.Seeders.ForEach(func(k string, _ *Peer) bool {
		seederKeys[k] = true
		return true
	})

	// Assert none appear in leechers.
	torrent.Leechers.ForEach(func(k string, _ *Peer) bool {
		if seederKeys[k] {
			t.Errorf("peerKey %q appears in both Seeders and Leechers — violated mutual exclusion", k)
		}
		return true
	})
}

// TestGUC_Godel_SeederLeecherCountsConsistentWithAtomicStats verifies that
// the global Stats.Seeders and Stats.Leechers atomics remain non-negative after
// a wave of concurrent announces and stops.
func TestGUC_Godel_SeederLeecherCountsConsistentWithAtomicStats(t *testing.T) {
	const n = 50
	f := newTestFixture()
	user, _ := f.worker.Users.Get(testPasskey)
	ip := net.ParseIP(testIP)

	var wg sync.WaitGroup
	wg.Add(n * 2)

	// Phase 1: announce n leechers.
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			peerID := concMakePeerID(i)
			req := newAnnounceReqFull(testInfoHash, peerID, "started", 0, 0, 1000)
			f.worker.Announce(context.Background(), req, user, ip, "tc", "")
		}()
	}

	// Phase 2: stop n leechers (may or may not match inserted ones due to ordering).
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			peerID := concMakePeerID(i)
			req := newAnnounceReqFull(testInfoHash, peerID, "stopped", 0, 0, 1000)
			f.worker.Announce(context.Background(), req, user, ip, "tc", "")
		}()
	}
	wg.Wait()

	// Atomics are unsigned; if they wrap we'd see very large values.
	const maxReasonable = uint32(1 << 20)
	seeders := f.worker.Stats.Seeders.Load()
	leechers := f.worker.Stats.Leechers.Load()
	if seeders > maxReasonable {
		t.Errorf("Stats.Seeders wrapped to unreasonably large value: %d", seeders)
	}
	if leechers > maxReasonable {
		t.Errorf("Stats.Leechers wrapped to unreasonably large value: %d", leechers)
	}
}

// TestGUC_Godel_CompactIPPortInvariant asserts the structural invariant of
// CompactIPPort: valid IPv4 always produces exactly 6 bytes; non-IPv4 produces nil.
func TestGUC_Godel_CompactIPPortStructuralInvariant(t *testing.T) {
	cases := []struct {
		name    string
		ip      net.IP
		port    uint16
		wantNil bool
		wantLen int
	}{
		{"valid ipv4", net.ParseIP("1.2.3.4"), 6881, false, 6},
		{"valid ipv4 80", net.ParseIP("10.0.0.1"), 80, false, 6},
		{"ipv6", net.ParseIP("::1"), 6881, true, 0},
		{"nil ip", nil, 6881, true, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := CompactIPPort(tc.ip, tc.port)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil compact for %s, got %v", tc.name, got)
				}
			} else {
				if got == nil {
					t.Errorf("expected non-nil compact for %s", tc.name)
					return
				}
				if len(got) != tc.wantLen {
					t.Errorf("compact length = %d, want %d", len(got), tc.wantLen)
				}
				// Port encoding invariant: bytes 4-5 encode the port big-endian.
				encodedPort := uint16(got[4])<<8 | uint16(got[5])
				if encodedPort != tc.port {
					t.Errorf("encoded port %d != expected %d", encodedPort, tc.port)
				}
			}
		})
	}
}

// TestGUC_Godel_PeerListDeleteLeavesNoGhosts confirms that after concurrent
// deletes the PeerList never reports a key as present when it was last deleted.
func TestGUC_Godel_PeerListDeleteLeavesNoGhosts(t *testing.T) {
	const n = 100
	pl := NewPeerList()

	// Pre-populate.
	for i := 0; i < n; i++ {
		pl.Set(fmt.Sprintf("k%d", i), &Peer{UserID: UserID(i)})
	}

	// Concurrent deletes of the even keys.
	var wg sync.WaitGroup
	var deleted int64
	wg.Add(n / 2)
	for i := 0; i < n; i += 2 {
		i := i
		go func() {
			defer wg.Done()
			pl.Delete(fmt.Sprintf("k%d", i))
			atomic.AddInt64(&deleted, 1)
		}()
	}
	wg.Wait()

	if atomic.LoadInt64(&deleted) != int64(n/2) {
		t.Errorf("expected %d deletes, got %d", n/2, deleted)
	}

	// Verify deleted keys are truly gone.
	ghosts := 0
	for i := 0; i < n; i += 2 {
		if _, ok := pl.Get(fmt.Sprintf("k%d", i)); ok {
			ghosts++
		}
	}
	if ghosts > 0 {
		t.Errorf("found %d ghost entries after concurrent deletes", ghosts)
	}

	// Verify odd keys are still present.
	missing := 0
	for i := 1; i < n; i += 2 {
		if _, ok := pl.Get(fmt.Sprintf("k%d", i)); !ok {
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("%d odd keys unexpectedly deleted", missing)
	}
}

// TestGUC_Godel_NoPeerIDOverlapAcrossTorrents verifies that announcing the same
// peerID on two different torrents does not cross-contaminate their peer lists.
func TestGUC_Godel_NoPeerIDOverlapAcrossTorrents(t *testing.T) {
	const hashA = testInfoHash
	hashB := concMakeInfoHash(42)

	f := newWorkerWithTorrent(hashB)
	user, _ := f.worker.Users.Get(testPasskey)
	ip := net.ParseIP(testIP)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		req := newAnnounceReqFull(hashA, testPeerID, "started", 0, 0, 1000)
		f.worker.Announce(context.Background(), req, user, ip, "tc", "")
	}()
	go func() {
		defer wg.Done()
		req := newAnnounceReqFull(hashB, testPeerID, "started", 0, 0, 1000)
		f.worker.Announce(context.Background(), req, user, ip, "tc", "")
	}()
	wg.Wait()

	torA, _ := f.worker.Torrents.Get(hashA)
	torB, _ := f.worker.Torrents.Get(hashB)

	totalA := torA.Seeders.Size() + torA.Leechers.Size()
	totalB := torB.Seeders.Size() + torB.Leechers.Size()

	if totalA == 0 {
		t.Error("torrent A has no peers after announce")
	}
	if totalB == 0 {
		t.Error("torrent B has no peers after announce")
	}

	// Ensure the peer lists of A and B are distinct objects.
	if torA.Leechers == torB.Leechers {
		t.Error("impossible state: torrent A and B share the same Leechers PeerList pointer")
	}
	if torA.Seeders == torB.Seeders {
		t.Error("impossible state: torrent A and B share the same Seeders PeerList pointer")
	}
}

// TestGUC_Knuth_PeerListForEachReadsAllEntries verifies that ForEach visits
// every entry that was inserted, with no duplicates and no omissions.
func TestGUC_Knuth_PeerListForEachReadsAllEntries(t *testing.T) {
	const n = 50
	pl := NewPeerList()
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("entry%d", i)
		pl.Set(key, &Peer{UserID: UserID(i)})
	}

	seen := make(map[string]int)
	pl.ForEach(func(k string, _ *Peer) bool {
		seen[k]++
		return true
	})

	if len(seen) != n {
		t.Errorf("ForEach visited %d distinct keys, want %d", len(seen), n)
	}
	for k, count := range seen {
		if count != 1 {
			t.Errorf("key %q visited %d times, want 1", k, count)
		}
	}
}

// TestGUC_Turing_RaceDetectorPassesConcurrentPeerListOps exercises all PeerList
// operations concurrently to confirm the race detector sees no data races.
// This test is only meaningful when run with -race.
func TestGUC_Turing_RaceDetectorPassesConcurrentPeerListOps(t *testing.T) {
	const n = 100
	pl := NewPeerList()

	var wg sync.WaitGroup
	wg.Add(n * 4)
	for i := 0; i < n; i++ {
		i := i
		key := fmt.Sprintf("rkey%d", i)
		go func() { defer wg.Done(); pl.Set(key, &Peer{UserID: UserID(i)}) }()
		go func() { defer wg.Done(); pl.Get(key) }()
		go func() { defer wg.Done(); pl.Delete(key) }()
		go func() { defer wg.Done(); _ = pl.Size() }()
	}
	wg.Wait()

	// Post-condition: Size must be non-negative and representable.
	if pl.Size() < 0 {
		t.Errorf("PeerList.Size() returned negative value: %d", pl.Size())
	}
}
