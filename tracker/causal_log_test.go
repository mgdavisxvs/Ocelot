package tracker

import (
	"sort"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func makeAnnounceEvent(userID UserID, torrentID TorrentID) *AnnounceEvent {
	return NewAnnounceEvent("trace-"+itoa(int(userID)), userID, torrentID, "started", 0, 0, 1000, 0, 1)
}

// ── CausalLog tests ───────────────────────────────────────────────────────────

func TestCausalLog_AppendAndLen(t *testing.T) {
	l := NewCausalLog(64)
	if l.Len() != 0 {
		t.Fatalf("want Len=0, got %d", l.Len())
	}
	l.Append(makeAnnounceEvent(1, 1))
	l.Append(makeAnnounceEvent(2, 1))
	if l.Len() != 2 {
		t.Fatalf("want Len=2, got %d", l.Len())
	}
}

func TestCausalLog_SinceEmpty(t *testing.T) {
	l := NewCausalLog(64)
	if got := l.Since(0); len(got) != 0 {
		t.Fatalf("Since on empty log should return nil, got %v", got)
	}
}

func TestCausalLog_SinceAfterHighSeq(t *testing.T) {
	l := NewCausalLog(64)
	l.Append(makeAnnounceEvent(1, 1))
	highSeq := l.HighSeq()
	if got := l.Since(highSeq); len(got) != 0 {
		t.Fatalf("Since(highSeq) should return 0 entries, got %d", len(got))
	}
}

func TestCausalLog_SinceReturnsCorrectSubset(t *testing.T) {
	l := NewCausalLog(64)
	l.Append(makeAnnounceEvent(1, 1))
	pivot := l.HighSeq()
	l.Append(makeAnnounceEvent(2, 1))
	l.Append(makeAnnounceEvent(3, 1))

	got := l.Since(pivot)
	if len(got) != 2 {
		t.Fatalf("want 2 entries after pivot, got %d", len(got))
	}
	for _, e := range got {
		if e.Seq <= pivot {
			t.Errorf("entry Seq %d should be > pivot %d", e.Seq, pivot)
		}
	}
}

func TestCausalLog_RingWrap(t *testing.T) {
	cap := 64 // NewCausalLog minimum; smaller values are clamped to 64
	l := NewCausalLog(cap)
	for i := 0; i < cap+4; i++ {
		l.Append(makeAnnounceEvent(UserID(i), 1))
	}
	if l.Len() != cap {
		t.Fatalf("after overflow, Len should equal cap=%d, got %d", cap, l.Len())
	}
}

func TestCausalLog_MergeIdempotent(t *testing.T) {
	l := NewCausalLog(64)
	e := makeAnnounceEvent(1, 1)
	l.Append(e)
	snapshot := l.Since(0)
	before := l.Len()

	// Merging the same snapshot twice should not duplicate entries.
	l.Merge(snapshot)
	l.Merge(snapshot)
	if l.Len() != before {
		t.Fatalf("duplicate merge changed Len: before=%d after=%d", before, l.Len())
	}
}

func TestCausalLog_MergeRemoteEntries(t *testing.T) {
	local := NewCausalLog(64)
	remote := NewCausalLog(64)

	// Populate only remote.
	for i := 0; i < 5; i++ {
		remote.Append(makeAnnounceEvent(UserID(i+100), 1))
	}
	remoteSnapshot := remote.Since(0)
	local.Merge(remoteSnapshot)

	if local.Len() != 5 {
		t.Fatalf("after merge, want Len=5, got %d", local.Len())
	}
}

func TestCausalLog_HighSeqMonotone(t *testing.T) {
	l := NewCausalLog(128)
	var prev uint64
	for i := 0; i < 20; i++ {
		l.Append(makeAnnounceEvent(UserID(i), 1))
		cur := l.HighSeq()
		if cur <= prev {
			t.Fatalf("HighSeq not monotone: prev=%d cur=%d", prev, cur)
		}
		prev = cur
	}
}

func TestCausalLog_BusSubscriber(t *testing.T) {
	bus := NewBus(64)
	defer bus.Stop()

	l := NewCausalLog(64)
	bus.Subscribe("announce.*", l.BusSubscriber())

	bus.Publish(makeAnnounceEvent(1, 1))
	bus.Publish(makeAnnounceEvent(2, 1))

	// Give the async dispatch goroutine time to deliver.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if l.Len() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if l.Len() < 2 {
		t.Fatalf("expected ≥2 entries via bus, got %d", l.Len())
	}
}

func TestCausalLog_SinceOrdering(t *testing.T) {
	l := NewCausalLog(128)
	for i := 0; i < 10; i++ {
		l.Append(makeAnnounceEvent(UserID(i), 1))
	}
	entries := l.Since(0)
	seqs := make([]uint64, len(entries))
	for i, e := range entries {
		seqs[i] = e.Seq
	}
	if !sort.SliceIsSorted(seqs, func(i, j int) bool { return seqs[i] < seqs[j] }) {
		t.Fatal("Since() returned entries out of Lamport order")
	}
}

func TestCausalLog_Concurrency(t *testing.T) {
	l := NewCausalLog(256)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(uid UserID) {
			for j := 0; j < 50; j++ {
				l.Append(makeAnnounceEvent(uid, 1))
				l.Since(0)
			}
			done <- struct{}{}
		}(UserID(i))
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
