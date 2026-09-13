package tracker

import (
	"log"
	"sync/atomic"
	"time"
)

// Scheduler runs periodic SQLite maintenance tasks: WAL checkpointing and
// shard rotation. It replaces the C++ schedule.cpp logic.
type Scheduler struct {
	db             *SQLiteShardManager
	intervalNanos  atomic.Int64 // nanoseconds; supports hot-reload via SetInterval
	stop           chan struct{}
}

// NewScheduler creates a Scheduler that fires every intervalSec seconds.
func NewScheduler(db *SQLiteShardManager, intervalSec int) *Scheduler {
	s := &Scheduler{
		db:   db,
		stop: make(chan struct{}),
	}
	s.intervalNanos.Store(int64(time.Duration(intervalSec) * time.Second))
	return s
}

// SetInterval updates the schedule interval without restarting the goroutine.
// The new interval takes effect on the next tick.
func (s *Scheduler) SetInterval(d time.Duration) {
	s.intervalNanos.Store(int64(d))
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
	current := time.Duration(s.intervalNanos.Load())
	ticker := time.NewTicker(current)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.db.CheckpointWAL(); err != nil {
				log.Printf("scheduler: WAL checkpoint: %v", err)
			}
			if err := s.db.CheckRotation(); err != nil {
				log.Printf("scheduler: DB rotation check: %v", err)
			}
			// Re-arm ticker if interval changed via SetInterval
			if next := time.Duration(s.intervalNanos.Load()); next != current {
				current = next
				ticker.Reset(current)
			}
		case <-s.stop:
			return
		}
	}
}
