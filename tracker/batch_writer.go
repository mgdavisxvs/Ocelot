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
	stopOnce      sync.Once
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
			// Drain whatever is still queued before exiting. Without this,
			// select picks randomly between a ready buffer and a closed
			// stopChan, silently dropping pending writes on shutdown.
		drain:
			for {
				select {
				case op := <-bw.buffer:
					batch = append(batch, op)
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

// Stop gracefully stops the batch writer, flushing anything still queued.
// It is safe to call more than once.
func (bw *BatchWriter) Stop() {
	bw.stopOnce.Do(func() {
		close(bw.stopChan)
		bw.ticker.Stop()
		bw.wg.Wait()
		bw.logger.Info("batch writer stopped")
	})
}

// Size returns the current buffer size
func (bw *BatchWriter) Size() int {
	return len(bw.buffer)
}
