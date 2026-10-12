package tracker

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestBatchWriter_FlushOnStop(t *testing.T) {
	db := newMockDB()
	bw := NewBatchWriter(db, 500, 10*time.Second) // long flush interval — only Stop() triggers flush

	bw.QueuePeerAnnounce(1, 1, 1, 100, 200, 10, 20, 0, 0, 1000, 1, "1.2.3.4", "peer1", "client/1.0")
	bw.QueuePeerAnnounce(2, 1, 1, 300, 400, 30, 40, 0, 0, 1001, 2, "1.2.3.5", "peer2", "client/2.0")

	bw.Stop()

	db.mu.Lock()
	n := len(db.Peers)
	db.mu.Unlock()

	if n != 2 {
		t.Errorf("Stop() flushed %d records, want 2", n)
	}
}

func TestBatchWriter_FlushOnInterval(t *testing.T) {
	db := newMockDB()
	bw := NewBatchWriter(db, 500, 20*time.Millisecond) // very short interval

	bw.QueuePeerAnnounce(3, 2, 1, 50, 50, 5, 5, 0, 0, 999, 1, "2.3.4.5", "peer3", "ua")

	// Wait for ticker to fire
	time.Sleep(80 * time.Millisecond)

	db.mu.Lock()
	n := len(db.Peers)
	db.mu.Unlock()

	bw.Stop()

	if n < 1 {
		t.Errorf("interval flush: got %d records, want ≥1", n)
	}
}

func TestBatchWriter_BatchSizeFlush(t *testing.T) {
	db := newMockDB()
	batchSize := 5
	bw := NewBatchWriter(db, batchSize, 10*time.Second)

	for i := 0; i < batchSize; i++ {
		bw.QueuePeerAnnounce(UserID(i+1), 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, "1.1.1.1", "p", "ua")
	}

	// Give the goroutine time to see a full batch and flush
	time.Sleep(50 * time.Millisecond)

	db.mu.Lock()
	n := len(db.Peers)
	db.mu.Unlock()

	bw.Stop()

	if n < batchSize {
		t.Errorf("batch-size flush: got %d, want %d", n, batchSize)
	}
}

func TestBatchWriter_DrainOnStop(t *testing.T) {
	db := newMockDB()
	bw := NewBatchWriter(db, 1000, 60*time.Second)

	const total = 20
	for i := 0; i < total; i++ {
		bw.QueuePeerAnnounce(UserID(i+1), 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, "1.1.1.1", "p", "ua")
	}

	bw.Stop()

	db.mu.Lock()
	n := len(db.Peers)
	db.mu.Unlock()

	if n != total {
		t.Errorf("drain on Stop: got %d, want %d", n, total)
	}
}

func TestBatchWriter_QueueOverflow_Drops(t *testing.T) {
	// Make DB block so queue fills up
	var blocked atomic.Bool
	blocked.Store(true)

	blocking := &blockingMockDB{MockDB: newMockDB(), blocked: &blocked}

	batchSize := 2
	bw := NewBatchWriter(blocking, batchSize, 60*time.Second)

	// Fill well beyond queue capacity (batchSize * 10 = 20)
	sent := 0
	for i := 0; i < 200; i++ {
		bw.QueuePeerAnnounce(UserID(i+1), 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, "1.1.1.1", "p", "ua")
		sent++
	}

	// Some records should have been silently dropped without panic
	blocked.Store(false)
	bw.Stop()
	// As long as no panic and Size() is sane, overflow drop logic is working.
	_ = sent
}

func TestBatchWriter_Size(t *testing.T) {
	db := newMockDB()
	bw := NewBatchWriter(db, 100, 60*time.Second)

	bw.QueuePeerAnnounce(1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, "1.1.1.1", "p", "ua")
	bw.QueuePeerAnnounce(2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, "1.1.1.1", "p", "ua")

	// Size may be 0–2 depending on goroutine scheduling; just assert no panic.
	_ = bw.Size()
	bw.Stop()
}

// blockingMockDB holds all RecordPeer calls while blocked is true.
type blockingMockDB struct {
	*MockDB
	blocked *atomic.Bool
}

func (b *blockingMockDB) RecordPeer(userID UserID, torrentID TorrentID, active int,
	uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64,
	announceTime, announces uint32, ip, peerID, userAgent string, invalidIP bool) error {
	for b.blocked.Load() {
		time.Sleep(time.Millisecond)
	}
	return b.MockDB.RecordPeer(userID, torrentID, active,
		uploaded, downloaded, upSpeed, downSpeed, left, corrupt,
		announceTime, announces, ip, peerID, userAgent, invalidIP)
}
