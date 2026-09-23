package tracker

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"
)

// bufferedOp is a deferred DatabaseInterface write captured as a closure.
type bufferedOp struct {
	call func() error
}

// BufferedDB wraps a DatabaseInterface with an async write buffer and an
// optional CircuitBreaker (features A + D).
//
// Announce-path writes (RecordPeer, RecordPeerLight, RecordUserStats,
// RecordTorrent, RecordSnatch, RecordToken) are enqueued and flushed
// asynchronously by a background goroutine.  Admin writes and all reads
// pass through synchronously so they are never silently deferred.
//
// On graceful shutdown: call Flush() to drain the queue, then Close()
// to close the underlying database.
type BufferedDB struct {
	inner   DatabaseInterface
	breaker *CircuitBreaker // may be nil
	queue   chan bufferedOp
	stop    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
	logger  *Logger

	dropped atomic.Uint64 // ops silently dropped because the queue was full
}

const (
	defaultBufCap       = 4096
	defaultFlushTick    = 200 * time.Millisecond
)

// NewBufferedDB wraps inner with an async write queue of bufCap entries and
// a flush tick of flushInterval.  Pass a non-nil breaker to wrap every write
// (queued or synchronous) with circuit-breaker protection.
func NewBufferedDB(inner DatabaseInterface, breaker *CircuitBreaker, bufCap int, flushInterval time.Duration) *BufferedDB {
	if bufCap <= 0 {
		bufCap = defaultBufCap
	}
	if flushInterval <= 0 {
		flushInterval = defaultFlushTick
	}
	b := &BufferedDB{
		inner:   inner,
		breaker: breaker,
		queue:   make(chan bufferedOp, bufCap),
		stop:    make(chan struct{}),
		logger:  GetDefaultLogger(),
	}
	b.wg.Add(1)
	go b.drain(flushInterval)
	return b
}

// drain is the background goroutine that executes queued ops.
func (b *BufferedDB) drain(interval time.Duration) {
	defer b.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case op := <-b.queue:
			b.execute(op)
		case <-ticker.C:
			// Drain any residual items without blocking.
			for done := false; !done; {
				select {
				case op := <-b.queue:
					b.execute(op)
				default:
					done = true
				}
			}
		case <-b.stop:
			// Flush all remaining items before exiting.
			for {
				select {
				case op := <-b.queue:
					b.execute(op)
				default:
					return
				}
			}
		}
	}
}

// execute runs one op, wrapping it in the circuit breaker when present.
func (b *BufferedDB) execute(op bufferedOp) {
	ctx := context.Background()
	_, span := TraceDBQuery(ctx, "async_write")
	var err error
	if b.breaker != nil {
		err = b.breaker.ExecuteWithContext(ctx, func(_ context.Context) error {
			return op.call()
		})
	} else {
		err = op.call()
	}
	span.End()
	if err != nil {
		b.logger.Warn("buffered db write failed", "error", err.Error())
	}
}

// enqueue adds fn to the async queue.  If the queue is full the op is
// silently dropped and the dropped counter is incremented.
func (b *BufferedDB) enqueue(fn func() error) {
	select {
	case b.queue <- bufferedOp{call: fn}:
	default:
		b.dropped.Add(1)
		b.logger.Warn("buffered db queue full, dropping write")
	}
}

// syncWrite runs fn synchronously, routing through the circuit breaker when
// present.  Used for infrequent admin writes that must not be silently lost.
func (b *BufferedDB) syncWrite(fn func() error) error {
	ctx := context.Background()
	_, span := TraceDBQuery(ctx, "sync_write")
	defer span.End()
	if b.breaker != nil {
		return b.breaker.ExecuteWithContext(ctx, func(_ context.Context) error {
			return fn()
		})
	}
	return fn()
}

// QueueDepth returns the number of ops currently waiting in the queue.
func (b *BufferedDB) QueueDepth() int { return len(b.queue) }

// DroppedOps returns the total number of ops dropped due to a full queue.
func (b *BufferedDB) DroppedOps() uint64 { return b.dropped.Load() }

// Flush blocks until the async queue is fully drained.  Safe to call once;
// subsequent calls are no-ops.
func (b *BufferedDB) Flush() {
	b.once.Do(func() {
		close(b.stop)
		b.wg.Wait()
	})
}

// ── DatabaseInterface: announce-path writes (async) ──────────────────────────

func (b *BufferedDB) RecordPeer(userID UserID, torrentID TorrentID, active int,
	uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64,
	announceTime, announces uint32, ip, peerID, userAgent string, invalidIP bool) error {
	b.enqueue(func() error {
		return b.inner.RecordPeer(userID, torrentID, active,
			uploaded, downloaded, upSpeed, downSpeed, left, corrupt,
			announceTime, announces, ip, peerID, userAgent, invalidIP)
	})
	return nil
}

func (b *BufferedDB) RecordPeerLight(userID UserID, torrentID TorrentID, announceTime, announces uint32, peerID string) error {
	b.enqueue(func() error {
		return b.inner.RecordPeerLight(userID, torrentID, announceTime, announces, peerID)
	})
	return nil
}

func (b *BufferedDB) RecordUserStats(userID UserID, uploaded, downloaded int64) error {
	b.enqueue(func() error {
		return b.inner.RecordUserStats(userID, uploaded, downloaded)
	})
	return nil
}

func (b *BufferedDB) RecordTorrent(torrentID TorrentID, seeders, leechers uint32, snatched int, balance int64) error {
	b.enqueue(func() error {
		return b.inner.RecordTorrent(torrentID, seeders, leechers, snatched, balance)
	})
	return nil
}

func (b *BufferedDB) RecordSnatch(userID UserID, torrentID TorrentID, t time.Time, ip string) error {
	b.enqueue(func() error {
		return b.inner.RecordSnatch(userID, torrentID, t, ip)
	})
	return nil
}

func (b *BufferedDB) RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error {
	b.enqueue(func() error {
		return b.inner.RecordToken(userID, torrentID, downloaded)
	})
	return nil
}

// ── DatabaseInterface: admin writes (synchronous) ────────────────────────────

func (b *BufferedDB) RecordTorrentHash(id TorrentID, infoHash string) error {
	return b.syncWrite(func() error { return b.inner.RecordTorrentHash(id, infoHash) })
}

func (b *BufferedDB) RecordUserPasskey(id UserID, passkey string, canLeech, protectIP bool) error {
	return b.syncWrite(func() error { return b.inner.RecordUserPasskey(id, passkey, canLeech, protectIP) })
}

func (b *BufferedDB) AddWhitelistEntry(prefix string) error {
	return b.syncWrite(func() error { return b.inner.AddWhitelistEntry(prefix) })
}

func (b *BufferedDB) RemoveWhitelistEntry(prefix string) error {
	return b.syncWrite(func() error { return b.inner.RemoveWhitelistEntry(prefix) })
}

func (b *BufferedDB) DeleteToken(userID UserID, torrentID TorrentID) error {
	return b.syncWrite(func() error { return b.inner.DeleteToken(userID, torrentID) })
}

func (b *BufferedDB) DeleteTorrentHash(infoHash string) error {
	return b.syncWrite(func() error { return b.inner.DeleteTorrentHash(infoHash) })
}

func (b *BufferedDB) DeleteUserPasskey(passkey string) error {
	return b.syncWrite(func() error { return b.inner.DeleteUserPasskey(passkey) })
}

func (b *BufferedDB) LoadRecommendedInterval(torrentID TorrentID) (int, bool) {
	return b.inner.LoadRecommendedInterval(torrentID)
}

// ── DatabaseInterface: reads (always pass-through) ───────────────────────────

func (b *BufferedDB) LoadTorrents() ([]torrentLoadRow, error) { return b.inner.LoadTorrents() }
func (b *BufferedDB) LoadUsers() ([]userLoadRow, error)       { return b.inner.LoadUsers() }
func (b *BufferedDB) LoadWhitelist() ([]string, error)         { return b.inner.LoadWhitelist() }
func (b *BufferedDB) LoadTokens() (map[string][]UserID, error) { return b.inner.LoadTokens() }

// ── DatabaseInterface: maintenance (pass-through) ────────────────────────────

func (b *BufferedDB) CheckpointWAL() error { return b.inner.CheckpointWAL() }
func (b *BufferedDB) CheckRotation() error { return b.inner.CheckRotation() }

// CurrentDB returns the active *sql.DB from the underlying shard manager,
// satisfying virtualserver.DBProvider (and catalog.DBProvider).
func (b *BufferedDB) CurrentDB() *sql.DB {
	type currentDBer interface{ CurrentDB() *sql.DB }
	if c, ok := b.inner.(currentDBer); ok {
		return c.CurrentDB()
	}
	return nil
}

// Close flushes pending writes, then closes the underlying database.
func (b *BufferedDB) Close() error {
	b.Flush()
	return b.inner.Close()
}
