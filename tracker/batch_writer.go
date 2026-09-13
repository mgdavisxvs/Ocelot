package tracker

import (
	"sync"
	"time"
)

// BatchWriter decouples the announce hot path from synchronous DB writes.
// Callers enqueue records; a background goroutine drains the queue and calls
// DatabaseInterface methods in batches, amortising per-call overhead.
type BatchWriter struct {
	db        DatabaseInterface
	queue     chan announceRecord
	ticker    *time.Ticker
	batchSize int
	wg        sync.WaitGroup
	stopChan  chan struct{}
}

type announceRecord struct {
	userID     UserID
	torrentID  TorrentID
	active     int
	uploaded   int64
	downloaded int64
	upSpeed    int64
	downSpeed  int64
	left       int64
	corrupt    int64
	announceAt uint32
	announces  uint32
	ip         string
	peerID     string
	userAgent  string
}

// NewBatchWriter creates a BatchWriter backed by db.
// batchSize: max records flushed per transaction cycle.
// flushInterval: max latency before an under-full batch is flushed.
func NewBatchWriter(db DatabaseInterface, batchSize int, flushInterval time.Duration) *BatchWriter {
	bw := &BatchWriter{
		db:        db,
		queue:     make(chan announceRecord, batchSize*10),
		ticker:    time.NewTicker(flushInterval),
		batchSize: batchSize,
		stopChan:  make(chan struct{}),
	}
	bw.wg.Add(1)
	go bw.processLoop()
	return bw
}

// QueuePeerAnnounce enqueues a peer record for async write.
// If the queue is full the record is dropped (logged via Logger).
func (bw *BatchWriter) QueuePeerAnnounce(
	userID UserID, torrentID TorrentID, active int,
	uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64,
	announceAt, announces uint32,
	ip, peerID, userAgent string,
) {
	rec := announceRecord{
		userID: userID, torrentID: torrentID, active: active,
		uploaded: uploaded, downloaded: downloaded,
		upSpeed: upSpeed, downSpeed: downSpeed,
		left: left, corrupt: corrupt,
		announceAt: announceAt, announces: announces,
		ip: ip, peerID: peerID, userAgent: userAgent,
	}
	select {
	case bw.queue <- rec:
	default:
		GetDefaultLogger().Warn("batch writer: queue full, dropping peer announce",
			"torrent_id", torrentID, "user_id", userID)
	}
}

func (bw *BatchWriter) processLoop() {
	defer bw.wg.Done()
	batch := make([]announceRecord, 0, bw.batchSize)
	for {
		select {
		case rec := <-bw.queue:
			batch = append(batch, rec)
			if len(batch) >= bw.batchSize {
				bw.flush(batch)
				batch = batch[:0]
			}
		case <-bw.ticker.C:
			if len(batch) > 0 {
				bw.flush(batch)
				batch = batch[:0]
			}
		case <-bw.stopChan:
			// Drain remaining queued records before exiting.
		drain:
			for {
				select {
				case rec := <-bw.queue:
					batch = append(batch, rec)
				default:
					break drain
				}
			}
			if len(batch) > 0 {
				bw.flush(batch)
			}
			return
		}
	}
}

func (bw *BatchWriter) flush(batch []announceRecord) {
	for i := range batch {
		r := &batch[i]
		if err := bw.db.RecordPeer(
			r.userID, r.torrentID, r.active,
			r.uploaded, r.downloaded, r.upSpeed, r.downSpeed, r.left, r.corrupt,
			r.announceAt, r.announces, r.ip, r.peerID, r.userAgent,
		); err != nil {
			GetDefaultLogger().Warn("batch writer: RecordPeer failed",
				"torrent_id", r.torrentID, "error", err.Error())
		}
	}
}

// Stop drains the queue and waits for the goroutine to exit.
func (bw *BatchWriter) Stop() {
	close(bw.stopChan)
	bw.ticker.Stop()
	bw.wg.Wait()
}

// Size returns the number of records currently queued.
func (bw *BatchWriter) Size() int { return len(bw.queue) }

// PeerAnnounceData is kept for compatibility with existing tests.
type PeerAnnounceData struct {
	InfoHash   string
	PeerID     string
	IP         string
	Port       uint16
	Uploaded   int64
	Downloaded int64
	Remaining  int64
	Event      string
	Timestamp  int64
}

// DBOperation is kept for compatibility with existing tests.
type DBOperation struct {
	Type string
	Data interface{}
}
