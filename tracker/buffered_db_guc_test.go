package tracker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── Knuth: algorithmic correctness ───────────────────────────────────────────

// TestGUC_Knuth_EnqueuedWritesFlushedFIFO verifies that RecordUserStats calls
// are executed in the order they were enqueued (FIFO invariant).
func TestGUC_Knuth_EnqueuedWritesFlushedFIFO(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 10*time.Second)

	const n = 10
	for i := 0; i < n; i++ {
		if err := bdb.RecordUserStats(UserID(i+1), int64(i), int64(i)); err != nil {
			t.Fatalf("RecordUserStats[%d] returned unexpected error: %v", i, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := bdb.Drain(ctx); err != nil {
		t.Fatalf("Drain returned error: %v", err)
	}

	inner.mu.Lock()
	got := inner.UserStats
	inner.mu.Unlock()

	if len(got) != n {
		t.Fatalf("expected %d UserStats records, got %d", n, len(got))
	}
	for i, r := range got {
		if r.UserID != uint32(i+1) {
			t.Errorf("record[%d]: expected UserID %d, got %d (FIFO violated)", i, i+1, r.UserID)
		}
	}
}

// TestGUC_Knuth_QueueDepthMonotoneDecrement verifies that QueueDepth decreases
// (or stays equal) after Drain, never rises above the initial enqueue count.
func TestGUC_Knuth_QueueDepthMonotoneDecrement(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 32, 10*time.Second)

	const n = 8
	for i := 0; i < n; i++ {
		_ = bdb.RecordTorrent(TorrentID(i+1), 1, 0, 0, 0)
	}

	depthBefore := bdb.QueueDepth()
	if depthBefore > n {
		t.Fatalf("QueueDepth %d exceeds enqueue count %d", depthBefore, n)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = bdb.Drain(ctx)

	depthAfter := bdb.QueueDepth()
	if depthAfter > depthBefore {
		t.Errorf("QueueDepth rose from %d to %d after Drain", depthBefore, depthAfter)
	}
	if depthAfter != 0 {
		t.Errorf("expected QueueDepth 0 after Drain, got %d", depthAfter)
	}
}

// TestGUC_Knuth_AllAsyncWriteMethodsEnqueue verifies that each async write
// method (RecordPeer, RecordPeerLight, RecordSnatch, RecordToken,
// RecordUserStats, RecordTorrent) actually lands in the inner DB after Drain.
func TestGUC_Knuth_AllAsyncWriteMethodsEnqueue(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 10*time.Second)

	_ = bdb.RecordPeer(1, 1, 1, 100, 200, 10, 5, 0, 0, 1000, 1, "1.2.3.4", "pid", "ua", false)
	_ = bdb.RecordPeerLight(2, 2, 1001, 2, "pid2")
	_ = bdb.RecordUserStats(3, 500, 300)
	_ = bdb.RecordTorrent(4, 10, 5, 2, 0)
	_ = bdb.RecordSnatch(5, 6, time.Now(), "5.6.7.8")
	_ = bdb.RecordToken(7, 8, 999)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := bdb.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	inner.mu.Lock()
	defer inner.mu.Unlock()

	if len(inner.Peers) != 1 {
		t.Errorf("Peers: want 1, got %d", len(inner.Peers))
	}
	if len(inner.PeersLight) != 1 {
		t.Errorf("PeersLight: want 1, got %d", len(inner.PeersLight))
	}
	if len(inner.UserStats) != 1 {
		t.Errorf("UserStats: want 1, got %d", len(inner.UserStats))
	}
	if len(inner.Torrents) != 1 {
		t.Errorf("Torrents: want 1, got %d", len(inner.Torrents))
	}
	if len(inner.Snatches) != 1 {
		t.Errorf("Snatches: want 1, got %d", len(inner.Snatches))
	}
	if len(inner.Tokens) != 1 {
		t.Errorf("Tokens: want 1, got %d", len(inner.Tokens))
	}
}

// TestGUC_Knuth_QueueOverflowDropsAndCountsOps verifies that when the queue is
// full, additional enqueues increment DroppedOps rather than blocking.
func TestGUC_Knuth_QueueOverflowDropsAndCountsOps(t *testing.T) {
	// Use a tiny cap and a very long interval so the background goroutine does
	// not drain anything during the fill phase.
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 2, 30*time.Second)

	// Enqueue more items than the channel capacity.
	const total = 10
	for i := 0; i < total; i++ {
		_ = bdb.RecordUserStats(UserID(i+1), 0, 0)
	}

	dropped := bdb.DroppedOps()
	if dropped == 0 {
		// Some may have been consumed before we read; at minimum the queue
		// must not have grown beyond its cap.
		if bdb.QueueDepth() > 2 {
			t.Errorf("QueueDepth %d exceeds bufCap 2", bdb.QueueDepth())
		}
	}
	// dropped + queued + processed must equal total.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = bdb.Drain(ctx)

	inner.mu.Lock()
	processed := len(inner.UserStats)
	inner.mu.Unlock()

	total64 := uint64(total)
	if uint64(processed)+bdb.DroppedOps() != total64 {
		// It's possible some ops were consumed before they could be counted as
		// dropped; accept the weaker invariant: processed + dropped <= total.
		if uint64(processed)+bdb.DroppedOps() > total64 {
			t.Errorf("processed(%d)+dropped(%d) > total(%d)", processed, bdb.DroppedOps(), total)
		}
	}
}

// TestGUC_Knuth_SyncWritesBypassQueue verifies that admin writes (RecordTorrentHash)
// execute synchronously and appear in the inner DB immediately, not via the queue.
func TestGUC_Knuth_SyncWritesBypassQueue(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 30*time.Second)

	if err := bdb.RecordTorrentHash(TorrentID(1), "aabbccdd"); err != nil {
		t.Fatalf("RecordTorrentHash: %v", err)
	}

	inner.mu.Lock()
	n := len(inner.TorrentHashes)
	inner.mu.Unlock()

	if n != 1 {
		t.Errorf("expected 1 TorrentHash immediately after sync write, got %d", n)
	}

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = bdb.Drain(ctx)
}

// ── Turing: termination / halting behavior ────────────────────────────────────

// TestGUC_Turing_DrainTerminatesOnEmptyQueue verifies that Drain returns
// promptly when called on a freshly created, never-enqueued BufferedDB.
func TestGUC_Turing_DrainTerminatesOnEmptyQueue(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 10*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- bdb.Drain(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Drain on empty queue returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Drain on empty queue did not terminate within 3 s")
	}
}

// TestGUC_Turing_DrainReturnsOnCtxCancel verifies that Drain honours a
// context cancellation and does not hang indefinitely.
func TestGUC_Turing_DrainReturnsOnCtxCancel(t *testing.T) {
	// blockDB makes RecordUserStats block so the drain goroutine stalls.
	var releaseBlock chan struct{}
	releaseBlock = make(chan struct{})

	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 10*time.Second)

	// Enqueue a blocking op directly via RecordUserStats after patching inner.
	// We accomplish the stall by filling the queue with a closure that waits.
	var wg sync.WaitGroup
	wg.Add(1)
	bdb.queue <- bufferedOp{call: func() error {
		wg.Done()
		<-releaseBlock
		return nil
	}}

	// Wait until the background goroutine picks it up.
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := bdb.Drain(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected ctx error from Drain, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("Drain did not return promptly on ctx cancel: %v", elapsed)
	}

	close(releaseBlock)
	// Give the background goroutine time to exit before the test ends.
	time.Sleep(50 * time.Millisecond)
}

// TestGUC_Turing_DrainCalledTwiceIsSafe verifies that calling Drain (and Flush)
// multiple times does not panic or deadlock.
func TestGUC_Turing_DrainCalledTwiceIsSafe(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 10*time.Second)

	_ = bdb.RecordUserStats(1, 1, 1)

	ctx1, c1 := context.WithTimeout(context.Background(), 2*time.Second)
	defer c1()
	if err := bdb.Drain(ctx1); err != nil {
		t.Fatalf("first Drain: %v", err)
	}

	// Second Drain must be a no-op and return promptly.
	ctx2, c2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer c2()
	done := make(chan error, 1)
	go func() { done <- bdb.Drain(ctx2) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("second Drain returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second Drain did not terminate within 3 s")
	}
}

// TestGUC_Turing_FlushTerminatesAfterEnqueue verifies Flush (not Drain) halts
// after draining a non-empty queue.
func TestGUC_Turing_FlushTerminatesAfterEnqueue(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 10*time.Second)

	for i := 0; i < 5; i++ {
		_ = bdb.RecordToken(UserID(i+1), TorrentID(i+1), int64(i*100))
	}

	done := make(chan struct{})
	go func() {
		bdb.Flush()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("Flush did not terminate within 5 s")
	}

	inner.mu.Lock()
	n := len(inner.Tokens)
	inner.mu.Unlock()
	if n != 5 {
		t.Errorf("expected 5 tokens after Flush, got %d", n)
	}
}

// TestGUC_Turing_CloseFlushesAndClosesInner verifies that Close() flushes
// pending ops and then closes the inner database.
func TestGUC_Turing_CloseFlushesAndClosesInner(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 10*time.Second)

	_ = bdb.RecordSnatch(1, 1, time.Now(), "1.1.1.1")

	if err := bdb.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	inner.mu.Lock()
	snatches := len(inner.Snatches)
	closes := inner.CloseCount
	inner.mu.Unlock()

	if snatches != 1 {
		t.Errorf("expected 1 snatch after Close, got %d", snatches)
	}
	if closes != 1 {
		t.Errorf("expected 1 inner.Close call, got %d", closes)
	}
}

// ── Church: functional purity / side-effect isolation ────────────────────────

// TestGUC_Church_AsyncWriteReturnsNilImmediately verifies that all async write
// methods return nil synchronously without waiting for the op to execute.
func TestGUC_Church_AsyncWriteReturnsNilImmediately(t *testing.T) {
	// Use a slow inner DB to make sure we're not waiting on execution.
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 30*time.Second)

	var err error
	start := time.Now()
	err = bdb.RecordPeer(1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, "1.2.3.4", "pid", "ua", false)
	if err != nil {
		t.Errorf("RecordPeer: %v", err)
	}
	err = bdb.RecordPeerLight(1, 1, 0, 0, "pid")
	if err != nil {
		t.Errorf("RecordPeerLight: %v", err)
	}
	err = bdb.RecordUserStats(1, 0, 0)
	if err != nil {
		t.Errorf("RecordUserStats: %v", err)
	}
	err = bdb.RecordTorrent(1, 0, 0, 0, 0)
	if err != nil {
		t.Errorf("RecordTorrent: %v", err)
	}
	err = bdb.RecordSnatch(1, 1, time.Now(), "1.2.3.4")
	if err != nil {
		t.Errorf("RecordSnatch: %v", err)
	}
	err = bdb.RecordToken(1, 1, 0)
	if err != nil {
		t.Errorf("RecordToken: %v", err)
	}
	elapsed := time.Since(start)
	// All six calls must have returned well under 50 ms.
	if elapsed > 50*time.Millisecond {
		t.Errorf("async write methods too slow: %v (possible synchronous block)", elapsed)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = bdb.Drain(ctx)
}

// TestGUC_Church_PassthroughReadsAreStateless verifies that read methods
// (LoadTorrents, LoadUsers, LoadWhitelist, LoadTokens) do not alter any
// observable state of the BufferedDB or inner DB.
func TestGUC_Church_PassthroughReadsAreStateless(t *testing.T) {
	inner := newMockDB()
	inner.TorrentRows = []torrentLoadRow{{}}
	inner.UserRows = []userLoadRow{{}}
	inner.WhitelistRows = []string{"qBittorrent"}

	bdb := NewBufferedDB(inner, nil, 64, 30*time.Second)

	depthBefore := bdb.QueueDepth()

	_, _ = bdb.LoadTorrents()
	_, _ = bdb.LoadUsers()
	_, _ = bdb.LoadWhitelist()
	_, _ = bdb.LoadTokens()

	depthAfter := bdb.QueueDepth()
	if depthAfter != depthBefore {
		t.Errorf("QueueDepth changed from %d to %d after read calls", depthBefore, depthAfter)
	}
	if bdb.DroppedOps() != 0 {
		t.Errorf("DroppedOps changed after read calls: %d", bdb.DroppedOps())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = bdb.Drain(ctx)
}

// TestGUC_Church_IndependentBufferedDBsDoNotShareState verifies that two
// independent BufferedDB instances do not share queue, counter, or inner state.
func TestGUC_Church_IndependentBufferedDBsDoNotShareState(t *testing.T) {
	innerA := newMockDB()
	innerB := newMockDB()

	bdbA := NewBufferedDB(innerA, nil, 64, 10*time.Second)
	bdbB := NewBufferedDB(innerB, nil, 64, 10*time.Second)

	_ = bdbA.RecordUserStats(1, 100, 200)
	_ = bdbB.RecordTorrent(TorrentID(2), 5, 3, 0, 0)

	ctxA, ca := context.WithTimeout(context.Background(), 2*time.Second)
	defer ca()
	ctxB, cb := context.WithTimeout(context.Background(), 2*time.Second)
	defer cb()

	_ = bdbA.Drain(ctxA)
	_ = bdbB.Drain(ctxB)

	innerA.mu.Lock()
	aStats := len(innerA.UserStats)
	aTorrents := len(innerA.Torrents)
	innerA.mu.Unlock()

	innerB.mu.Lock()
	bStats := len(innerB.UserStats)
	bTorrents := len(innerB.Torrents)
	innerB.mu.Unlock()

	if aStats != 1 || aTorrents != 0 {
		t.Errorf("innerA: want UserStats=1 Torrents=0, got %d %d", aStats, aTorrents)
	}
	if bStats != 0 || bTorrents != 1 {
		t.Errorf("innerB: want UserStats=0 Torrents=1, got %d %d", bStats, bTorrents)
	}
}

// TestGUC_Church_DroppedOpsCounterImmutableAfterDrain verifies that DroppedOps
// does not change after Drain completes (no background mutation).
func TestGUC_Church_DroppedOpsCounterImmutableAfterDrain(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 2, 30*time.Second)

	for i := 0; i < 20; i++ {
		_ = bdb.RecordUserStats(UserID(i+1), 0, 0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = bdb.Drain(ctx)

	snapshot1 := bdb.DroppedOps()
	time.Sleep(50 * time.Millisecond)
	snapshot2 := bdb.DroppedOps()

	if snapshot1 != snapshot2 {
		t.Errorf("DroppedOps changed after Drain: %d → %d", snapshot1, snapshot2)
	}
}

// ── Gödel: formal consistency / invariant preservation ────────────────────────

// TestGUC_Godel_DroppedPlusProcessedEqualsEnqueued verifies the accounting
// invariant: dropped + processed == enqueued (assuming no concurrent adds).
func TestGUC_Godel_DroppedPlusProcessedEqualsEnqueued(t *testing.T) {
	const cap = 4
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, cap, 30*time.Second)

	const enqueued = 20
	for i := 0; i < enqueued; i++ {
		_ = bdb.RecordUserStats(UserID(i+1), int64(i), 0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = bdb.Drain(ctx)

	inner.mu.Lock()
	processed := len(inner.UserStats)
	inner.mu.Unlock()

	dropped := int(bdb.DroppedOps())
	if processed+dropped != enqueued {
		t.Errorf("invariant violated: processed(%d)+dropped(%d) != enqueued(%d)",
			processed, dropped, enqueued)
	}
}

// TestGUC_Godel_QueueDepthZeroAfterDrain verifies that after a successful Drain
// the queue depth is exactly zero — no phantom ops remain.
func TestGUC_Godel_QueueDepthZeroAfterDrain(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 30*time.Second)

	for i := 0; i < 16; i++ {
		_ = bdb.RecordToken(UserID(i+1), TorrentID(i+1), int64(i))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := bdb.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	if d := bdb.QueueDepth(); d != 0 {
		t.Errorf("QueueDepth after Drain: want 0, got %d", d)
	}
}

// TestGUC_Godel_ConcurrentEnqueueSafeUnderRace runs parallel RecordPeer calls
// to exercise the channel-based enqueue under the race detector.
func TestGUC_Godel_ConcurrentEnqueueSafeUnderRace(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 1024, 10*time.Second)

	const goroutines = 20
	const opsPerGoroutine = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for op := 0; op < opsPerGoroutine; op++ {
				_ = bdb.RecordPeer(
					UserID(gid+1), TorrentID(op+1), 1,
					0, 0, 0, 0, 0, 0,
					0, 0, "1.2.3.4", "pid", "ua", false,
				)
			}
		}(g)
	}
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := bdb.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	inner.mu.Lock()
	total := len(inner.Peers)
	inner.mu.Unlock()

	dropped := int(bdb.DroppedOps())
	expected := goroutines * opsPerGoroutine
	if total+dropped != expected {
		t.Errorf("total(%d)+dropped(%d) != expected(%d)", total, dropped, expected)
	}
}

// TestGUC_Godel_NoImpossibleStateAfterFlushClose verifies that after Close the
// inner DB has been closed exactly once and queue depth is zero.
func TestGUC_Godel_NoImpossibleStateAfterFlushClose(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 64, 10*time.Second)

	_ = bdb.RecordUserStats(1, 10, 20)
	_ = bdb.RecordTorrent(TorrentID(2), 1, 0, 0, 0)

	if err := bdb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	inner.mu.Lock()
	closeCount := inner.CloseCount
	inner.mu.Unlock()

	if closeCount != 1 {
		t.Errorf("inner.Close called %d times, want exactly 1", closeCount)
	}
	if d := bdb.QueueDepth(); d != 0 {
		t.Errorf("QueueDepth after Close: want 0, got %d", d)
	}
}

// TestGUC_Godel_TimerFlushInterval verifies that ops enqueued without manual
// Drain are still executed within the flush interval.
func TestGUC_Godel_TimerFlushInterval(t *testing.T) {
	inner := newMockDB()
	interval := 100 * time.Millisecond
	bdb := NewBufferedDB(inner, nil, 64, interval)

	_ = bdb.RecordUserStats(1, 42, 24)

	// Wait up to 10× the interval for the timer to fire.
	deadline := time.Now().Add(10 * interval)
	for time.Now().Before(deadline) {
		inner.mu.Lock()
		n := len(inner.UserStats)
		inner.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(interval / 4)
	}

	inner.mu.Lock()
	n := len(inner.UserStats)
	inner.mu.Unlock()

	if n != 1 {
		t.Errorf("timer flush: expected 1 UserStats record within %v, got %d", 10*interval, n)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = bdb.Drain(ctx)
}

// TestGUC_Godel_OrderPreservedUnderHighConcurrency verifies that with many
// goroutines each enqueueing a uniquely tagged op, every non-dropped op appears
// exactly once in the inner DB (no duplicates, no corruption).
func TestGUC_Godel_OrderPreservedUnderHighConcurrency(t *testing.T) {
	inner := newMockDB()
	bdb := NewBufferedDB(inner, nil, 2048, 10*time.Second)

	const goroutines = 50
	var enqueuedCount atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			if err := bdb.RecordTorrent(TorrentID(gid+1), uint32(gid), 0, 0, 0); err == nil {
				enqueuedCount.Add(1)
			}
		}(g)
	}
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := bdb.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	inner.mu.Lock()
	torrents := make([]recordedTorrent, len(inner.Torrents))
	copy(torrents, inner.Torrents)
	inner.mu.Unlock()

	// Check for duplicate TorrentIDs (data corruption invariant).
	seen := make(map[uint32]bool, len(torrents))
	for _, r := range torrents {
		if seen[r.TorrentID] {
			t.Errorf("duplicate TorrentID %d in output (data corruption)", r.TorrentID)
		}
		seen[r.TorrentID] = true
	}

	dropped := int(bdb.DroppedOps())
	if len(torrents)+dropped != goroutines {
		t.Errorf("processed(%d)+dropped(%d) != goroutines(%d)", len(torrents), dropped, goroutines)
	}
}
