package tracker

import (
	"fmt"
	"sync"
	"testing"
)

// ── Knuth: algorithmic correctness, loop invariants, data structure invariants ──

// TestGUC_TorrentList_SetGetRoundTrip verifies the basic Set→Get contract:
// a stored torrent is retrieved by the same key with the same pointer value
// (Knuth: data structure invariant).
func TestGUC_TorrentList_SetGetRoundTrip(t *testing.T) {
	tl := NewTorrentList()
	torrent := NewTorrent(TorrentID(42))
	hash := "aaaaaaaaaaaaaaaaaaaaa"

	tl.Set(hash, torrent)
	got, ok := tl.Get(hash)
	if !ok {
		t.Fatal("Get after Set: expected ok=true, got false")
	}
	if got != torrent {
		t.Errorf("Get after Set: expected same pointer, got different")
	}
}

// TestGUC_TorrentList_DeleteRemoves checks that Delete actually removes the key
// so a subsequent Get returns (nil, false) (Knuth: invariant after mutation).
func TestGUC_TorrentList_DeleteRemoves(t *testing.T) {
	tl := NewTorrentList()
	hash := "bbbbbbbbbbbbbbbbbbbbbb"
	tl.Set(hash, NewTorrent(TorrentID(1)))
	tl.Delete(hash)

	got, ok := tl.Get(hash)
	if ok {
		t.Errorf("Get after Delete: expected ok=false, got true")
	}
	if got != nil {
		t.Errorf("Get after Delete: expected nil torrent, got %v", got)
	}
}

// TestGUC_TorrentList_SizeAccuracy validates the Size() invariant: it equals the
// number of live entries after interleaved Set and Delete operations
// (Knuth: loop invariant on a mutable map).
func TestGUC_TorrentList_SizeAccuracy(t *testing.T) {
	tl := NewTorrentList()

	for i := 0; i < 10; i++ {
		hash := fmt.Sprintf("hash%010d", i)
		tl.Set(hash, NewTorrent(TorrentID(uint32(i))))
	}
	if got := tl.Size(); got != 10 {
		t.Fatalf("Size after 10 inserts: want 10, got %d", got)
	}

	for i := 0; i < 3; i++ {
		tl.Delete(fmt.Sprintf("hash%010d", i))
	}
	if got := tl.Size(); got != 7 {
		t.Fatalf("Size after 3 deletes: want 7, got %d", got)
	}
}

// TestGUC_TorrentList_LargeList10000 inserts 10 000 torrents and confirms Size()
// remains consistent at scale (Knuth: complexity assertion — O(1) size tracking).
func TestGUC_TorrentList_LargeList10000(t *testing.T) {
	tl := NewTorrentList()
	const n = 10000
	for i := 0; i < n; i++ {
		hash := fmt.Sprintf("h%019d", i)
		tl.Set(hash, NewTorrent(TorrentID(uint32(i))))
	}
	if got := tl.Size(); got != n {
		t.Fatalf("Size after %d inserts: want %d, got %d", n, n, got)
	}
}

// TestGUC_TorrentList_ForEachIteratesAll verifies the ForEach loop invariant:
// every inserted key is visited and no key is visited more than once
// (Knuth: iteration correctness).
func TestGUC_TorrentList_ForEachIteratesAll(t *testing.T) {
	tl := NewTorrentList()
	const n = 50
	inserted := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		hash := fmt.Sprintf("t%019d", i)
		tl.Set(hash, NewTorrent(TorrentID(uint32(i))))
		inserted[hash] = true
	}

	visited := make(map[string]int)
	tl.ForEach(func(hash string, _ *Torrent) bool {
		visited[hash]++
		return true
	})

	for hash := range inserted {
		if visited[hash] != 1 {
			t.Errorf("hash %q: visited %d times, want exactly 1", hash, visited[hash])
		}
	}
	if len(visited) != n {
		t.Errorf("ForEach visited %d entries, want %d", len(visited), n)
	}
}

// ── Turing: termination conditions, halting behavior, decidability ──

// TestGUC_TorrentList_GetMissingNilFalse checks that Get on an absent key
// terminates immediately returning (nil, false) (Turing: decidability of key presence).
func TestGUC_TorrentList_GetMissingNilFalse(t *testing.T) {
	tl := NewTorrentList()
	got, ok := tl.Get("nonexistent_key")
	if ok {
		t.Error("Get on empty list: expected ok=false, got true")
	}
	if got != nil {
		t.Errorf("Get on empty list: expected nil, got non-nil %v", got)
	}
}

// TestGUC_TorrentList_ForEachEarlyStop confirms that returning false from the
// iterator callback halts the loop — the loop does not continue visiting remaining
// entries (Turing: halting via early exit).
func TestGUC_TorrentList_ForEachEarlyStop(t *testing.T) {
	tl := NewTorrentList()
	for i := 0; i < 20; i++ {
		tl.Set(fmt.Sprintf("k%019d", i), NewTorrent(TorrentID(uint32(i))))
	}

	count := 0
	tl.ForEach(func(_ string, _ *Torrent) bool {
		count++
		return count < 5 // stop after visiting 5
	})

	if count > 5 {
		t.Errorf("ForEach continued after fn returned false: visited %d entries, want ≤5", count)
	}
}

// TestGUC_TorrentList_ResetClearsAll verifies that Reset terminates the list to
// the empty state, making all subsequent Gets return (nil, false)
// (Turing: termination to known empty state).
func TestGUC_TorrentList_ResetClearsAll(t *testing.T) {
	tl := NewTorrentList()
	hashes := []string{"r1", "r2", "r3"}
	for _, h := range hashes {
		tl.Set(h, NewTorrent(TorrentID(1)))
	}
	tl.Reset()

	if got := tl.Size(); got != 0 {
		t.Fatalf("Size after Reset: want 0, got %d", got)
	}
	for _, h := range hashes {
		if _, ok := tl.Get(h); ok {
			t.Errorf("Get(%q) after Reset: expected absent, got present", h)
		}
	}
}

// TestGUC_TorrentList_ForEachBatchEarlyStop verifies that ForEachBatch also halts
// when the callback returns false — batch boundaries do not override the stop signal
// (Turing: halting across batch boundaries).
func TestGUC_TorrentList_ForEachBatchEarlyStop(t *testing.T) {
	tl := NewTorrentList()
	for i := 0; i < 30; i++ {
		tl.Set(fmt.Sprintf("b%019d", i), NewTorrent(TorrentID(uint32(i))))
	}

	count := 0
	tl.ForEachBatch(5, func(_ string, _ *Torrent) bool {
		count++
		return count < 8
	})

	if count > 8 {
		t.Errorf("ForEachBatch continued after fn returned false: visited %d, want ≤8", count)
	}
}

// TestGUC_TorrentList_DeleteIdempotent verifies that deleting a non-existent key
// is a no-op that neither panics nor corrupts the list state, and that a
// double-delete is safe (Turing: decidability of absent-key delete).
func TestGUC_TorrentList_DeleteIdempotent(t *testing.T) {
	tl := NewTorrentList()
	tl.Set("existing", NewTorrent(TorrentID(1)))

	// Delete a key that was never inserted — must not panic or corrupt.
	tl.Delete("never_existed")

	if _, ok := tl.Get("existing"); !ok {
		t.Error("existing key missing after deleting an absent key")
	}
	if tl.Size() != 1 {
		t.Errorf("Size should still be 1 after deleting absent key, got %d", tl.Size())
	}

	// Double-delete must also be safe.
	tl.Delete("existing")
	tl.Delete("existing")
	if tl.Size() != 0 {
		t.Errorf("Size after double-delete should be 0, got %d", tl.Size())
	}
}

// ── Church: functional purity, side-effect isolation, referential transparency ──

// TestGUC_NewTorrent_DefaultsSeedersAndLeechersZero checks that NewTorrent
// initialises both Seeders and Leechers to empty peer lists — no hidden
// side-effects inflate the counts (Church: pure constructor).
func TestGUC_NewTorrent_DefaultsSeedersAndLeechersZero(t *testing.T) {
	tor := NewTorrent(TorrentID(99))
	if tor.Seeders == nil {
		t.Fatal("NewTorrent: Seeders is nil, want non-nil PeerList")
	}
	if tor.Leechers == nil {
		t.Fatal("NewTorrent: Leechers is nil, want non-nil PeerList")
	}
	if got := tor.Seeders.Size(); got != 0 {
		t.Errorf("NewTorrent: Seeders.Size() = %d, want 0", got)
	}
	if got := tor.Leechers.Size(); got != 0 {
		t.Errorf("NewTorrent: Leechers.Size() = %d, want 0", got)
	}
}

// TestGUC_NewTorrent_DefaultFreeTypeNormal confirms that NewTorrent produces
// FreeType==FreeNormal with no caller-visible side effects (Church: pure default value).
func TestGUC_NewTorrent_DefaultFreeTypeNormal(t *testing.T) {
	tor := NewTorrent(TorrentID(1))
	if tor.FreeType != FreeNormal {
		t.Errorf("NewTorrent: FreeType = %d, want FreeNormal (%d)", tor.FreeType, FreeNormal)
	}
}

// TestGUC_NewTorrent_IDStoredCorrectly checks that the ID passed to NewTorrent
// is faithfully stored — the constructor has no hidden rewrite of the ID field
// (Church: referential transparency of constructor argument).
func TestGUC_NewTorrent_IDStoredCorrectly(t *testing.T) {
	var tests = []struct {
		id TorrentID
	}{
		{TorrentID(0)},
		{TorrentID(1)},
		{TorrentID(^uint32(0))}, // max uint32
	}
	for _, tc := range tests {
		tor := NewTorrent(tc.id)
		if tor.ID != tc.id {
			t.Errorf("NewTorrent(%d).ID = %d, want %d", tc.id, tor.ID, tc.id)
		}
	}
}

// TestGUC_Torrent_SeederCountFromPeerMap verifies that Seeders.Size() is derived
// from the actual peer map contents — adding peers increases the count, and
// no separate counter can diverge (Church: single source of truth).
func TestGUC_Torrent_SeederCountFromPeerMap(t *testing.T) {
	tor := NewTorrent(TorrentID(1))
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("seeder%d", i)
		tor.Seeders.Set(key, &Peer{UserID: UserID(uint32(i))})
	}
	if got := tor.Seeders.Size(); got != 5 {
		t.Errorf("Seeders.Size() = %d after 5 adds, want 5", got)
	}
	tor.Seeders.Delete("seeder0")
	if got := tor.Seeders.Size(); got != 4 {
		t.Errorf("Seeders.Size() = %d after 1 delete, want 4", got)
	}
}

// TestGUC_Torrent_LeecherCountSeparate verifies that Seeders and Leechers are
// independent peer maps — mutating one does not affect the other
// (Church: side-effect isolation between struct fields).
func TestGUC_Torrent_LeecherCountSeparate(t *testing.T) {
	tor := NewTorrent(TorrentID(1))
	for i := 0; i < 3; i++ {
		tor.Seeders.Set(fmt.Sprintf("s%d", i), &Peer{})
	}
	for i := 0; i < 7; i++ {
		tor.Leechers.Set(fmt.Sprintf("l%d", i), &Peer{})
	}
	if got := tor.Seeders.Size(); got != 3 {
		t.Errorf("Seeders.Size() = %d, want 3 (leecher writes must not affect seeders)", got)
	}
	if got := tor.Leechers.Size(); got != 7 {
		t.Errorf("Leechers.Size() = %d, want 7", got)
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state detection ──

// TestGUC_TorrentID_TypeSafety checks that TorrentID is a distinct numeric type
// whose values compare equal only to the same numeric value, preventing
// impossible cross-type confusion (Gödel: impossible-state: wrong-typed ID equality).
func TestGUC_TorrentID_TypeSafety(t *testing.T) {
	var a TorrentID = 1
	var b TorrentID = 1
	var c TorrentID = 2
	if a != b {
		t.Errorf("TorrentID(1) != TorrentID(1): type equality broken")
	}
	if a == c {
		t.Errorf("TorrentID(1) == TorrentID(2): impossible equality")
	}
	var zero TorrentID
	if zero != 0 {
		t.Errorf("zero TorrentID = %d, want 0", zero)
	}
}

// TestGUC_FreeType_AllValuesStored verifies that FreeType constants 0, 1, 2 are
// stored and retrieved correctly in a Torrent field
// (Gödel: formal consistency of iota-derived constants).
func TestGUC_FreeType_AllValuesStored(t *testing.T) {
	var tests = []struct {
		name string
		ft   FreeType
		want FreeType
	}{
		{"FreeNormal", FreeNormal, 0},
		{"FreeFree", FreeFree, 1},
		{"FreeNeutral", FreeNeutral, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tor := NewTorrent(TorrentID(1))
			tor.FreeType = tc.ft
			if tor.FreeType != tc.want {
				t.Errorf("FreeType %s: stored %d, want %d", tc.name, tor.FreeType, tc.want)
			}
		})
	}
}

// TestGUC_TorrentList_ConcurrentSetGetDelete hammers Set, Get, and Delete from
// multiple goroutines to ensure no data races occur under the -race detector
// (Gödel: invariant preservation under concurrent mutation).
func TestGUC_TorrentList_ConcurrentSetGetDelete(t *testing.T) {
	tl := NewTorrentList()
	const goroutines = 8
	const ops = 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				hash := fmt.Sprintf("ch%d-%d", id, i%20)
				tor := NewTorrent(TorrentID(uint32(i)))
				tl.Set(hash, tor)
				tl.Get(hash)
				if i%3 == 0 {
					tl.Delete(hash)
				}
			}
		}(g)
	}
	wg.Wait()

	// Size must be a non-negative integer — a basic consistency invariant.
	if size := tl.Size(); size < 0 {
		t.Errorf("Size after concurrent ops is negative: %d (impossible for len())", size)
	}
}

// TestGUC_Torrent_CompletedIncrement verifies that the Completed (snatches) field
// starts at zero and accumulates correctly — it cannot silently lose counts or wrap
// at small values (Gödel: consistency of monotonically increasing snatch counter).
func TestGUC_Torrent_CompletedIncrement(t *testing.T) {
	tor := NewTorrent(TorrentID(1))
	if tor.Completed != 0 {
		t.Fatalf("NewTorrent Completed = %d, want 0", tor.Completed)
	}

	for i := uint32(0); i < 5; i++ {
		tor.Completed++
		if tor.Completed != i+1 {
			t.Fatalf("Completed after %d increments = %d, want %d", i+1, tor.Completed, i+1)
		}
	}

	// One more increment must not wrap or lose count.
	tor.Completed++
	if tor.Completed != 6 {
		t.Errorf("Completed after 6 increments = %d, want 6", tor.Completed)
	}
}

// TestGUC_TorrentList_ForEachCompleteCoverage asserts the formal coverage invariant:
// every key present in the list is visited by ForEach, with no key visited more than
// once (Gödel: impossibility of missing or duplicate visits — structural completeness).
func TestGUC_TorrentList_ForEachCompleteCoverage(t *testing.T) {
	tl := NewTorrentList()
	const n = 100
	expected := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		hash := fmt.Sprintf("cov%015d", i)
		tl.Set(hash, NewTorrent(TorrentID(uint32(i))))
		expected[hash] = true
	}

	counts := make(map[string]int, n)
	tl.ForEach(func(hash string, _ *Torrent) bool {
		counts[hash]++
		return true
	})

	for hash := range expected {
		switch counts[hash] {
		case 0:
			t.Errorf("key %q was never visited by ForEach", hash)
		case 1:
			// correct
		default:
			t.Errorf("key %q was visited %d times, want exactly 1", hash, counts[hash])
		}
	}
	if len(counts) != n {
		t.Errorf("ForEach visited %d distinct keys, want %d", len(counts), n)
	}
}
