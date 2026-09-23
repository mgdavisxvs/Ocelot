package tracker

import (
	"database/sql"
	"sync"
	"time"
)

// BatchWriter handles batched database writes for performance
type BatchWriter struct {
	db            *sql.DB
	buffer        chan DBOperation
	ticker        *time.Ticker
	batchSize     int
	flushInterval time.Duration
	wg            sync.WaitGroup
	stopChan      chan struct{}
	logger        *Logger
	metrics       *MetricsRecorder
}

// DBOperation represents a database operation to be batched
type DBOperation struct {
	Type  string      // "peer_announce", "peer_update", "torrent_update"
	Table string      // table name
	Data  interface{} // operation data
}

// PeerAnnounceData holds peer announce data
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

// NewBatchWriter creates a new batch writer
func NewBatchWriter(db *sql.DB, batchSize int, flushInterval time.Duration) *BatchWriter {
	bw := &BatchWriter{
		db:            db,
		buffer:        make(chan DBOperation, batchSize*10),
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

// QueuePeerAnnounce queues a peer announce for batched write
func (bw *BatchWriter) QueuePeerAnnounce(data *PeerAnnounceData) {
	select {
	case bw.buffer <- DBOperation{
		Type: "peer_announce",
		Data: data,
	}:
	default:
		// Buffer full, log warning
		bw.logger.Warn("batch writer buffer full, dropping operation",
			"type", "peer_announce",
		)
	}
}

// QueueTorrentUpdate queues a torrent update for batched write
func (bw *BatchWriter) QueueTorrentUpdate(infoHash string, seeders, leechers int32) {
	select {
	case bw.buffer <- DBOperation{
		Type: "torrent_update",
		Data: map[string]interface{}{
			"info_hash": infoHash,
			"seeders":   seeders,
			"leechers":  leechers,
		},
	}:
	default:
		bw.logger.Warn("batch writer buffer full, dropping operation",
			"type", "torrent_update",
		)
	}
}

// processLoop is the main processing loop
func (bw *BatchWriter) processLoop() {
	defer bw.wg.Done()

	batch := make([]DBOperation, 0, bw.batchSize)

	for {
		select {
		case op := <-bw.buffer:
			batch = append(batch, op)

			// Flush if batch is full
			if len(batch) >= bw.batchSize {
				bw.flush(batch)
				batch = batch[:0]
			}

		case <-bw.ticker.C:
			// Flush on timer
			if len(batch) > 0 {
				bw.flush(batch)
				batch = batch[:0]
			}

		case <-bw.stopChan:
			// Flush remaining and exit
			if len(batch) > 0 {
				bw.flush(batch)
			}
			return
		}
	}
}

// flush writes a batch of operations to the database
func (bw *BatchWriter) flush(batch []DBOperation) {
	start := time.Now()

	tx, err := bw.db.Begin()
	if err != nil {
		bw.logger.Error("failed to begin transaction", err)
		bw.metrics.RecordDBQuery("batch_begin", time.Since(start), err)
		return
	}

	// Prepare statements
	peerStmt, err := tx.Prepare(`INSERT OR REPLACE INTO peers
		(info_hash, peer_id, ip, port, uploaded, downloaded, remaining, last_announce, active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)`)
	if err != nil {
		tx.Rollback()
		bw.logger.Error("failed to prepare peer statement", err)
		return
	}
	defer peerStmt.Close()

	torrentStmt, err := tx.Prepare(`UPDATE torrents
		SET seeders = ?, leechers = ?, last_action = ?
		WHERE info_hash = ?`)
	if err != nil {
		tx.Rollback()
		bw.logger.Error("failed to prepare torrent statement", err)
		return
	}
	defer torrentStmt.Close()

	// Execute all operations
	for _, op := range batch {
		switch op.Type {
		case "peer_announce":
			data := op.Data.(*PeerAnnounceData)
			_, err := peerStmt.Exec(
				data.InfoHash,
				data.PeerID,
				data.IP,
				data.Port,
				data.Uploaded,
				data.Downloaded,
				data.Remaining,
				data.Timestamp,
			)
			if err != nil {
				bw.logger.Error("failed to execute peer statement", err)
			}

		case "torrent_update":
			data := op.Data.(map[string]interface{})
			_, err := torrentStmt.Exec(
				data["seeders"],
				data["leechers"],
				time.Now().Unix(),
				data["info_hash"],
			)
			if err != nil {
				bw.logger.Error("failed to execute torrent statement", err)
			}
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		bw.logger.Error("failed to commit batch transaction", err)
		bw.metrics.RecordDBQuery("batch_commit", time.Since(start), err)
		return
	}

	duration := time.Since(start)
	bw.logger.Debug("flushed batch",
		"operations", len(batch),
		"duration_ms", duration.Milliseconds(),
	)
	bw.metrics.RecordDBQuery("batch_flush", duration, nil)
}

// BatchWriterDB adapts BatchWriter + a DatabaseInterface to satisfy
// DatabaseInterface.  Announce-path writes (RecordPeer, RecordTorrent) are
// routed through the BatchWriter's async queue; all other operations delegate
// to the inner DatabaseInterface so reads and admin writes are unaffected.
//
// This is the preferred Worker.DB implementation for cmd/ocelot, which wires
// the BatchWriter's queue methods (QueuePeerAnnounce, QueueTorrentUpdate) that
// were previously unreachable.
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
	b.bw.QueuePeerAnnounce(&PeerAnnounceData{
		PeerID:     peerID,
		IP:         ip,
		Uploaded:   uploaded,
		Downloaded: downloaded,
		Remaining:  left,
		Timestamp:  int64(announceTime),
	})
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

// QueueDepth returns the current pending-write queue depth, matching the
// same method name on BufferedDB so callers can use a type switch.
func (b *BatchWriterDB) QueueDepth() int {
	return b.bw.Size()
}

// Stop gracefully stops the batch writer
func (bw *BatchWriter) Stop() {
	close(bw.stopChan)
	bw.ticker.Stop()
	bw.wg.Wait()
	bw.logger.Info("batch writer stopped")
}

// Size returns the current buffer size
func (bw *BatchWriter) Size() int {
	return len(bw.buffer)
}
