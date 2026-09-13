package tracker

import (
	"log"
	"time"
)

// schedulerClock is the tick source. Replaced in tests with a fakeClock.
type schedulerClock interface {
	Tick() <-chan time.Time
	Stop()
}

type realClock struct{ t *time.Ticker }

func (r *realClock) Tick() <-chan time.Time { return r.t.C }
func (r *realClock) Stop()                  { r.t.Stop() }

// fakeClock lets tests fire ticks manually without sleeping.
type fakeClock struct{ ch chan time.Time }

func newFakeClock() *fakeClock              { return &fakeClock{ch: make(chan time.Time, 1)} }
func (f *fakeClock) Tick() <-chan time.Time { return f.ch }
func (f *fakeClock) Stop()                  {}
func (f *fakeClock) Fire()                  { f.ch <- time.Now() }

// schedulerDB is the subset of SQLiteShardManager used by Scheduler.
// Defined as an interface so tests can inject a stub without a real database.
type schedulerDB interface {
	CheckpointWAL() error
	CheckRotation() error
}

// Scheduler runs periodic SQLite maintenance tasks: WAL checkpointing and
// shard rotation. It replaces the C++ schedule.cpp logic.
type Scheduler struct {
	db       schedulerDB
	interval time.Duration
	clock    schedulerClock
	stop     chan struct{}
}

// NewScheduler creates a Scheduler that fires every intervalSec seconds.
func NewScheduler(db *SQLiteShardManager, intervalSec int) *Scheduler {
	d := time.Duration(intervalSec) * time.Second
	return &Scheduler{
		db:       db,
		interval: d,
		clock:    &realClock{t: time.NewTicker(d)},
		stop:     make(chan struct{}),
	}
}

// Start launches the scheduler goroutine.
func (s *Scheduler) Start() {
	go s.run()
}

// Stop signals the scheduler to exit on its next tick.
func (s *Scheduler) Stop() {
	close(s.stop)
}

func (s *Scheduler) run() {
	defer s.clock.Stop()
	for {
		select {
		case <-s.clock.Tick():
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
