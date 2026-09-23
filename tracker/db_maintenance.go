package tracker

import (
	"log"
	"sync"
	"time"
)

// ── schedulerDB interface ──────────────────────────────────────────────────────

// schedulerDB is the minimal database interface required by Scheduler.
type schedulerDB interface {
	CheckpointWAL() error
	CheckRotation() error
}

// ── clock abstraction ─────────────────────────────────────────────────────────

// schedulerClock abstracts time.Ticker so that tests can inject a fake clock.
type schedulerClock interface {
	C() <-chan time.Time
	Stop()
}

type realSchedulerClock struct{ t *time.Ticker }

func newRealSchedulerClock(d time.Duration) *realSchedulerClock {
	return &realSchedulerClock{t: time.NewTicker(d)}
}

func (c *realSchedulerClock) C() <-chan time.Time { return c.t.C }
func (c *realSchedulerClock) Stop()              { c.t.Stop() }

// ── Scheduler ─────────────────────────────────────────────────────────────────

// Scheduler runs periodic WAL checkpoint and shard rotation.
type Scheduler struct {
	db    schedulerDB
	clock schedulerClock
	stop  chan struct{}
	wg    sync.WaitGroup
}

// NewScheduler creates a Scheduler that fires every intervalSec seconds.
// delReasonLifetimeSec is accepted for API compatibility and reserved for future use.
func NewScheduler(db *SQLiteShardManager, intervalSec, delReasonLifetimeSec int) *Scheduler {
	_ = delReasonLifetimeSec
	return &Scheduler{
		db:    db,
		clock: newRealSchedulerClock(time.Duration(intervalSec) * time.Second),
		stop:  make(chan struct{}),
	}
}

// Start launches the maintenance goroutine.
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go s.run()
}

// Stop signals the scheduler to exit and waits for it to finish.
func (s *Scheduler) Stop() {
	close(s.stop)
	s.clock.Stop()
	s.wg.Wait()
}

func (s *Scheduler) run() {
	defer s.wg.Done()
	for {
		select {
		case <-s.clock.C():
			if err := s.db.CheckpointWAL(); err != nil {
				log.Printf("scheduler: WAL checkpoint: %v", err)
			}
			if err := s.db.CheckRotation(); err != nil {
				log.Printf("scheduler: DB rotation check: %v", err)
			}
		case <-s.stop:
			return
		}
	}
}

// ── DBMaintainer (legacy) ─────────────────────────────────────────────────────

// DBMaintainer runs periodic SQLite maintenance tasks: WAL checkpointing and
// shard rotation.  New code should prefer Scheduler.
type DBMaintainer struct {
	db       *SQLiteShardManager
	interval time.Duration
	stop     chan struct{}
}

// NewDBMaintainer creates a DBMaintainer that fires every intervalSec seconds.
func NewDBMaintainer(db *SQLiteShardManager, intervalSec int) *DBMaintainer {
	return &DBMaintainer{
		db:       db,
		interval: time.Duration(intervalSec) * time.Second,
		stop:     make(chan struct{}),
	}
}

// Start launches the maintenance goroutine.
func (m *DBMaintainer) Start() {
	go m.run()
}

// Stop signals the maintainer to exit on its next tick.
func (m *DBMaintainer) Stop() {
	close(m.stop)
}

func (m *DBMaintainer) run() {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := m.db.CheckpointWAL(); err != nil {
				log.Printf("db_maintenance: WAL checkpoint: %v", err)
			}
			if err := m.db.CheckRotation(); err != nil {
				log.Printf("db_maintenance: DB rotation check: %v", err)
			}
		case <-m.stop:
			return
		}
	}
}
