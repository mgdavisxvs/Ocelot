package tracker

import (
	"log"
	"strings"
	"time"
)

// Scheduler runs periodic SQLite maintenance tasks: WAL checkpointing,
// shard rotation, and stale-record pruning.
type Scheduler struct {
	db                *SQLiteShardManager
	interval          time.Duration
	delReasonLifetime int // seconds; 0 disables pruning
	stop              chan struct{}
}

// NewScheduler creates a Scheduler that fires every intervalSec seconds.
// delReasonLifetime is the maximum age (seconds) of deletion-reason rows; 0 disables pruning.
func NewScheduler(db *SQLiteShardManager, intervalSec int, delReasonLifetime ...int) *Scheduler {
	lifetime := 0
	if len(delReasonLifetime) > 0 {
		lifetime = delReasonLifetime[0]
	}
	return &Scheduler{
		db:                db,
		interval:          time.Duration(intervalSec) * time.Second,
		delReasonLifetime: lifetime,
		stop:              make(chan struct{}),
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
			if s.delReasonLifetime > 0 {
				cutoff := time.Now().Unix() - int64(s.delReasonLifetime)
				if _, err := s.db.CurrentDB().Exec(
					`DELETE FROM del_reasons WHERE created_at < ?`, cutoff,
				); err != nil && !isNoTableError(err) {
					log.Printf("scheduler: del_reason prune: %v", err)
				}
			}
		case <-s.stop:
			return
		}
	}
}

// isNoTableError returns true when err indicates the table does not exist yet.
func isNoTableError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no such table")
}
