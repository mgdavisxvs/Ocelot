package tracker

import (
	"sync"
	"time"
)

// peerRecord holds one peer announce destined for the database.
type peerRecord struct {
	userID      UserID
	torrentID   TorrentID
	active      int
	uploaded    int64
	downloaded  int64
	upSpeed     int64
	downSpeed   int64
	left        int64
	corrupt     int64
	announceTime uint32
	announces   uint32
	ip          string
	peerID      string
	userAgent   string
}

// BatchWriter handles batched database writes for performance.
type BatchWriter struct {
	db            DatabaseInterface
	buffer        chan peerRecord
	ticker        *time.Ticker
	batchSize     int
	flushInterval time.Duration
	wg            sync.WaitGroup
	stopChan      chan struct{}
	logger        *Logger
	metrics       *MetricsRecorder
}

// NewBatchWriter creates a new batch writer backed by a DatabaseInterface.
func NewBatchWriter(db DatabaseInterface, batchSize int, flushInterval time.Duration) *BatchWriter {
	bw := &BatchWriter{
		db:            db,
		buffer:        make(chan peerRecord, batchSize*10),
		ticker:        time.NewTicker(flushInterval),
		batchSize:     batchSize,
		flushInterval: flushInterval,
		stopChan:      make(chan struct{}),
		logger:        GetDefaultLogger(),
		metrics:       GetMetricsRecorder(),
	}

	bw.wg.Add(1)
	go bw.processLoop()

	return bw
}

// QueuePeerAnnounce queues a peer announce for async batched write.
func (bw *BatchWriter) QueuePeerAnnounce(
	userID UserID, torrentID TorrentID, active int,
	uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64,
	announceTime, announces uint32,
	ip, peerID, userAgent string,
) {
	r := peerRecord{
		userID: userID, torrentID: torrentID, active: active,
		uploaded: uploaded, downloaded: downloaded,
		upSpeed: upSpeed, downSpeed: downSpeed,
		left: left, corrupt: corrupt,
		announceTime: announceTime, announces: announces,
		ip: ip, peerID: peerID, userAgent: userAgent,
	}
	select {
	case bw.buffer <- r:
	default:
		bw.logger.Warn("batch writer buffer full, dropping operation",
			"type", "peer_announce",
		)
	}
}

// processLoop is the main processing loop.
func (bw *BatchWriter) processLoop() {
	defer bw.wg.Done()

	batch := make([]peerRecord, 0, bw.batchSize)

	for {
		select {
		case r := <-bw.buffer:
			batch = append(batch, r)
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
			// Drain the channel before flushing.
			draining := true
			for draining {
				select {
				case r := <-bw.buffer:
					batch = append(batch, r)
				default:
					draining = false
				}
			}
			if len(batch) > 0 {
				bw.flush(batch)
			}
			return
		}
	}
}

// flush writes a batch of peer records to the database via DatabaseInterface.
func (bw *BatchWriter) flush(batch []peerRecord) {
	start := time.Now()

	for _, r := range batch {
		if err := bw.db.RecordPeer(
			r.userID, r.torrentID, r.active,
			r.uploaded, r.downloaded, r.upSpeed, r.downSpeed, r.left, r.corrupt,
			r.announceTime, r.announces, r.ip, r.peerID, r.userAgent, false,
		); err != nil {
			bw.logger.Error("batch writer: RecordPeer failed", err)
		}
	}

	duration := time.Since(start)
	bw.logger.Debug("flushed batch",
		"operations", len(batch),
		"duration_ms", duration.Milliseconds(),
	)
	bw.metrics.RecordDBQuery("batch_flush", duration, nil)
}

// Stop gracefully stops the batch writer, flushing any remaining records.
func (bw *BatchWriter) Stop() {
	close(bw.stopChan)
	bw.ticker.Stop()
	bw.wg.Wait()
	bw.logger.Info("batch writer stopped")
}

// Size returns the current buffer queue depth.
func (bw *BatchWriter) Size() int {
	return len(bw.buffer)
}

// BatchWriterDB adapts BatchWriter + a DatabaseInterface to satisfy
// DatabaseInterface.  Announce-path writes (RecordPeer) are routed through
// the BatchWriter's async queue; all other operations delegate to the inner
// DatabaseInterface so reads and admin writes are unaffected.
type BatchWriterDB struct {
	inner DatabaseInterface
	bw    *BatchWriter
}

// NewBatchWriterDB creates a BatchWriterDB that routes hot-path writes through
// bw and delegates all other calls to inner.
func NewBatchWriterDB(inner DatabaseInterface, bw *BatchWriter) *BatchWriterDB {
	return &BatchWriterDB{inner: inner, bw: bw}
}

func (b *BatchWriterDB) RecordPeer(userID UserID, torrentID TorrentID, active int,
	uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64,
	announceTime, announces uint32, ip, peerID, userAgent string, invalidIP bool) error {
	b.bw.QueuePeerAnnounce(
		userID, torrentID, active,
		uploaded, downloaded, upSpeed, downSpeed, left, corrupt,
		announceTime, announces, ip, peerID, userAgent,
	)
	return nil
}

func (b *BatchWriterDB) RecordPeerLight(userID UserID, torrentID TorrentID,
	announceTime, announces uint32, peerID string) error {
	return b.inner.RecordPeerLight(userID, torrentID, announceTime, announces, peerID)
}

func (b *BatchWriterDB) RecordUserStats(userID UserID, uploaded, downloaded int64) error {
	return b.inner.RecordUserStats(userID, uploaded, downloaded)
}

func (b *BatchWriterDB) RecordTorrent(torrentID TorrentID, seeders, leechers uint32,
	snatched int, balance int64) error {
	return b.inner.RecordTorrent(torrentID, seeders, leechers, snatched, balance)
}

func (b *BatchWriterDB) RecordSnatch(userID UserID, torrentID TorrentID, t time.Time, ip string) error {
	return b.inner.RecordSnatch(userID, torrentID, t, ip)
}

func (b *BatchWriterDB) RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error {
	return b.inner.RecordToken(userID, torrentID, downloaded)
}

func (b *BatchWriterDB) RecordTorrentHash(id TorrentID, infoHash string) error {
	return b.inner.RecordTorrentHash(id, infoHash)
}

func (b *BatchWriterDB) RecordUserPasskey(id UserID, passkey string, canLeech, protectIP bool) error {
	return b.inner.RecordUserPasskey(id, passkey, canLeech, protectIP)
}

func (b *BatchWriterDB) AddWhitelistEntry(prefix string) error {
	return b.inner.AddWhitelistEntry(prefix)
}

func (b *BatchWriterDB) RemoveWhitelistEntry(prefix string) error {
	return b.inner.RemoveWhitelistEntry(prefix)
}

func (b *BatchWriterDB) DeleteToken(userID UserID, torrentID TorrentID) error {
	return b.inner.DeleteToken(userID, torrentID)
}

func (b *BatchWriterDB) DeleteTorrentHash(infoHash string) error {
	return b.inner.DeleteTorrentHash(infoHash)
}

func (b *BatchWriterDB) DeleteUserPasskey(passkey string) error {
	return b.inner.DeleteUserPasskey(passkey)
}

func (b *BatchWriterDB) LoadRecommendedInterval(torrentID TorrentID) (int, bool) {
	return b.inner.LoadRecommendedInterval(torrentID)
}

func (b *BatchWriterDB) LoadTorrents() ([]torrentLoadRow, error) {
	return b.inner.LoadTorrents()
}

func (b *BatchWriterDB) LoadUsers() ([]userLoadRow, error) {
	return b.inner.LoadUsers()
}

func (b *BatchWriterDB) LoadWhitelist() ([]string, error) {
	return b.inner.LoadWhitelist()
}

func (b *BatchWriterDB) LoadTokens() (map[string][]UserID, error) {
	return b.inner.LoadTokens()
}

func (b *BatchWriterDB) CheckpointWAL() error {
	return b.inner.CheckpointWAL()
}

func (b *BatchWriterDB) CheckRotation() error {
	return b.inner.CheckRotation()
}

func (b *BatchWriterDB) Close() error {
	b.bw.Stop()
	return b.inner.Close()
}

// QueueDepth returns the current pending-write queue depth.
func (b *BatchWriterDB) QueueDepth() int {
	return b.bw.Size()
}
