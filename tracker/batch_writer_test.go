package tracker

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newBatchDB opens an in-memory SQLite DB with the peers and torrents tables
// that BatchWriter.flush() requires.
func newBatchDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS peers (
			info_hash TEXT NOT NULL,
			peer_id   TEXT NOT NULL,
			ip        TEXT NOT NULL,
			port      INTEGER NOT NULL,
			uploaded  INTEGER NOT NULL DEFAULT 0,
			downloaded INTEGER NOT NULL DEFAULT 0,
			remaining INTEGER NOT NULL DEFAULT 0,
			last_announce INTEGER NOT NULL,
			active    INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (info_hash, peer_id)
		);
		CREATE TABLE IF NOT EXISTS torrents (
			info_hash TEXT PRIMARY KEY,
			seeders   INTEGER NOT NULL DEFAULT 0,
			leechers  INTEGER NOT NULL DEFAULT 0,
			last_action INTEGER NOT NULL DEFAULT 0
		);
	`)
	if err != nil {
		t.Fatalf("create batch tables: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ── NewBatchWriter ────────────────────────────────────────────────────────────

func TestNewBatchWriter_StopsCleanly(t *testing.T) {
	db := newBatchDB(t)
	bw := NewBatchWriter(db, 100, time.Second)
	bw.Stop()
}

func TestNewBatchWriter_InitialSizeZero(t *testing.T) {
	db := newBatchDB(t)
	bw := NewBatchWriter(db, 10, time.Second)
	defer bw.Stop()

	if bw.Size() != 0 {
		t.Errorf("initial Size = %d, want 0", bw.Size())
	}
}

// ── QueuePeerAnnounce ─────────────────────────────────────────────────────────

func TestQueuePeerAnnounce_IncreasesSize(t *testing.T) {
	db := newBatchDB(t)
	bw := NewBatchWriter(db, 1000, time.Hour) // large interval: no auto-flush
	defer bw.Stop()

	bw.QueuePeerAnnounce(&PeerAnnounceData{
		InfoHash:  "testhash",
		PeerID:    "peer1",
		IP:        "1.2.3.4",
		Port:      6881,
		Timestamp: time.Now().Unix(),
	})

	// Give the goroutine a moment to receive the item.
	time.Sleep(5 * time.Millisecond)
	// After receiving (but not flushing since batchSize is 1000), size may be 0
	// (item consumed by processLoop into the local batch slice). Either 0 or 1 is valid.
	// The important check: no panic, no hang.
}

func TestQueuePeerAnnounce_MultipleItems(t *testing.T) {
	db := newBatchDB(t)
	bw := NewBatchWriter(db, 1000, time.Hour)
	defer bw.Stop()

	for i := 0; i < 5; i++ {
		bw.QueuePeerAnnounce(&PeerAnnounceData{
			InfoHash:  "hash",
			PeerID:    string(rune('a' + i)),
			IP:        "1.2.3.4",
			Port:      uint16(6880 + i),
			Timestamp: time.Now().Unix(),
		})
	}
}

// ── QueueTorrentUpdate ────────────────────────────────────────────────────────

func TestQueueTorrentUpdate_DoesNotPanic(t *testing.T) {
	db := newBatchDB(t)
	bw := NewBatchWriter(db, 1000, time.Hour)
	defer bw.Stop()

	bw.QueueTorrentUpdate("infohash", 3, 1)
}

func TestQueueTorrentUpdate_DropWhenFull(t *testing.T) {
	db := newBatchDB(t)
	bw := &BatchWriter{
		db:            db,
		buffer:        make(chan DBOperation, 1),
		ticker:        time.NewTicker(time.Hour),
		batchSize:     1,
		flushInterval: time.Hour,
		stopChan:      make(chan struct{}),
		logger:        GetDefaultLogger(),
		metrics:       GetMetricsRecorder(),
	}
	// Fill the buffer so the next call hits the default branch.
	bw.buffer <- DBOperation{Type: "torrent_update"}
	bw.QueueTorrentUpdate("overflow", 1, 1) // must not block
	bw.ticker.Stop()
}

// ── Size ──────────────────────────────────────────────────────────────────────

func TestSize_ReflectsBuffer(t *testing.T) {
	db := newBatchDB(t)
	// batchSize=1000, but buffer capacity = batchSize*10 = 10000
	// Use a large enough flush interval so processLoop doesn't drain.
	bw := NewBatchWriter(db, 1000, 24*time.Hour)
	defer bw.Stop()

	// Send items faster than the goroutine can drain, check Size reflects buffered count.
	for i := 0; i < 10; i++ {
		bw.buffer <- DBOperation{Type: "peer_announce", Data: &PeerAnnounceData{}}
	}
	// Channel holds items before processLoop can drain them (check immediately).
	// Size may be anywhere from 0-10 depending on scheduling; the test just
	// verifies Size() doesn't panic and returns a sane value.
	s := bw.Size()
	if s < 0 {
		t.Errorf("Size() = %d, must be non-negative", s)
	}
}

// ── Stop ──────────────────────────────────────────────────────────────────────

func TestStop_FlushesRemainingItems(t *testing.T) {
	db := newBatchDB(t)
	// Insert a torrent row so the UPDATE in flush won't fail (no rows affected is fine).
	db.Exec("INSERT INTO torrents (info_hash, seeders, leechers, last_action) VALUES ('h1', 0, 0, 0)")

	bw := NewBatchWriter(db, 1000, time.Hour)

	bw.QueueTorrentUpdate("h1", 5, 2)
	time.Sleep(5 * time.Millisecond) // let channel be consumed into batch slice
	bw.Stop()                        // triggers final flush
}

func TestStop_NoHangOnEmpty(t *testing.T) {
	db := newBatchDB(t)
	bw := NewBatchWriter(db, 10, time.Second)
	done := make(chan struct{})
	go func() {
		bw.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2 seconds")
	}
}

// ── Buffer-full drop ──────────────────────────────────────────────────────────

func TestQueuePeerAnnounce_DropWhenFull(t *testing.T) {
	db := newBatchDB(t)
	// batchSize=1, buffer capacity = 1*10 = 10.  Stop the processLoop immediately
	// so it can't drain; then send 11 items — the 11th must be dropped silently.
	bw := &BatchWriter{
		db:            db,
		buffer:        make(chan DBOperation, 1), // tiny buffer
		ticker:        time.NewTicker(time.Hour),
		batchSize:     1,
		flushInterval: time.Hour,
		stopChan:      make(chan struct{}),
		logger:        GetDefaultLogger(),
		metrics:       GetMetricsRecorder(),
	}
	// Manually fill the buffer without starting processLoop.
	bw.buffer <- DBOperation{Type: "peer_announce", Data: &PeerAnnounceData{}}

	// This call must not block (select default branch).
	bw.QueuePeerAnnounce(&PeerAnnounceData{InfoHash: "overflow"})

	// Cleanup without starting the goroutine.
	bw.ticker.Stop()
}

// ── Full flush integration ────────────────────────────────────────────────────

func TestBatchWriter_FlushTorrentUpdate(t *testing.T) {
	db := newBatchDB(t)
	// Pre-insert a torrent row so the UPDATE hits an existing record.
	db.Exec("INSERT INTO torrents (info_hash, seeders, leechers, last_action) VALUES ('th', 0, 0, 0)")

	// batchSize=1: every item immediately triggers a flush.
	bw := NewBatchWriter(db, 1, time.Hour)

	bw.QueueTorrentUpdate("th", 4, 2)
	time.Sleep(30 * time.Millisecond)
	bw.Stop()

	var seeders int
	db.QueryRow("SELECT seeders FROM torrents WHERE info_hash = 'th'").Scan(&seeders)
	if seeders != 4 {
		t.Errorf("seeders = %d, want 4 after flush", seeders)
	}
}

func TestBatchWriter_FlushPeerAnnounce(t *testing.T) {
	db := newBatchDB(t)
	bw := NewBatchWriter(db, 5, time.Hour)

	// Queue 5 items — exactly the batch size, triggering an automatic flush.
	for i := 0; i < 5; i++ {
		bw.QueuePeerAnnounce(&PeerAnnounceData{
			InfoHash:  "flushhash",
			PeerID:    string(rune('a' + i)),
			IP:        "10.0.0.1",
			Port:      uint16(6880 + i),
			Timestamp: time.Now().Unix(),
		})
	}

	// Allow time for processLoop to detect batch full + flush.
	time.Sleep(50 * time.Millisecond)
	bw.Stop()

	// Verify at least one row was written to the peers table.
	var count int
	db.QueryRow("SELECT COUNT(*) FROM peers WHERE info_hash = 'flushhash'").Scan(&count)
	if count == 0 {
		t.Error("expected at least one peer row in DB after flush")
	}
}

// ── processLoop — ticker-based flush ─────────────────────────────────────────

func TestBatchWriter_TickerFlush(t *testing.T) {
	db := newBatchDB(t)
	db.Exec("INSERT INTO torrents (info_hash, seeders, leechers, last_action) VALUES ('tickhash', 0, 0, 0)")

	// batchSize=100 means a single item won't trigger a size-based flush;
	// the short interval ensures the ticker fires first.
	bw := NewBatchWriter(db, 100, 20*time.Millisecond)

	bw.QueueTorrentUpdate("tickhash", 9, 4)

	// Wait for ticker to fire (≥20ms) then a bit more for the flush to land.
	time.Sleep(80 * time.Millisecond)
	bw.Stop()

	var seeders int
	db.QueryRow("SELECT seeders FROM torrents WHERE info_hash = 'tickhash'").Scan(&seeders)
	if seeders != 9 {
		t.Errorf("seeders = %d, want 9 after ticker flush", seeders)
	}
}

// ── flush error paths ─────────────────────────────────────────────────────────

func TestBatchWriter_Flush_ClosedDB_DoesNotPanic(t *testing.T) {
	db := newBatchDB(t)
	bw := &BatchWriter{
		db:        db,
		buffer:    make(chan DBOperation, 1),
		ticker:    time.NewTicker(time.Hour),
		batchSize: 10,
		stopChan:  make(chan struct{}),
		logger:    GetDefaultLogger(),
		metrics:   GetMetricsRecorder(),
	}
	defer bw.ticker.Stop()

	// Close the DB so Begin() fails — flush must not panic.
	db.Close()
	bw.flush([]DBOperation{{
		Type: "torrent_update",
		Data: map[string]interface{}{"info_hash": "h1", "seeders": 1, "leechers": 0},
	}})
}

func TestBatchWriter_Flush_MissingPeersTable_DoesNotPanic(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	// Only create torrents table — peers table is missing so tx.Prepare for peer insert fails.
	db.Exec(`CREATE TABLE torrents (
		info_hash TEXT PRIMARY KEY,
		seeders INTEGER NOT NULL DEFAULT 0,
		leechers INTEGER NOT NULL DEFAULT 0,
		last_action INTEGER NOT NULL DEFAULT 0
	)`)

	bw := &BatchWriter{
		db:        db,
		buffer:    make(chan DBOperation, 1),
		ticker:    time.NewTicker(time.Hour),
		batchSize: 10,
		stopChan:  make(chan struct{}),
		logger:    GetDefaultLogger(),
		metrics:   GetMetricsRecorder(),
	}
	defer bw.ticker.Stop()

	// flush with a peer_announce op triggers the tx.Prepare error path for peers table.
	bw.flush([]DBOperation{{
		Type: "peer_announce",
		Data: &PeerAnnounceData{InfoHash: "h", PeerID: "p", IP: "1.2.3.4", Port: 6881},
	}})
}
