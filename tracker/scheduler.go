package tracker

import (
	"log"
	"time"
)

// Scheduler runs periodic SQLite maintenance tasks: WAL checkpointing and
// shard rotation. It replaces the C++ schedule.cpp logic.
type Scheduler struct {
	db       *SQLiteShardManager
	interval time.Duration
	stop     chan struct{}
}

// NewScheduler creates a Scheduler that fires every intervalSec seconds.
func NewScheduler(db *SQLiteShardManager, intervalSec int) *Scheduler {
	return &Scheduler{
		db:       db,
		interval: time.Duration(intervalSec) * time.Second,
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
	ticker := time.NewTicker(s.interval)
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
		case <-s.stop:
			return
		}
	}
}
