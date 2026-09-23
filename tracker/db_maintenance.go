package tracker

import (
	"log"
	"time"
)

// Scheduler wraps DBMaintainer and exposes the same Start/Stop interface.
// It exists to satisfy callers that use the NewScheduler constructor.
type Scheduler struct {
	*DBMaintainer
}

// NewScheduler creates a Scheduler that runs WAL checkpoint and shard rotation
// every intervalSec seconds. delReasonLifetimeSec is stored for future use.
func NewScheduler(db *SQLiteShardManager, intervalSec, delReasonLifetimeSec int) *Scheduler {
	_ = delReasonLifetimeSec // reserved
	return &Scheduler{DBMaintainer: NewDBMaintainer(db, intervalSec)}
}

// DBMaintainer runs periodic SQLite maintenance tasks: WAL checkpointing and
// shard rotation. It replaces the C++ schedule.cpp logic.
//
// NOTE: This type is intentionally named DBMaintainer, not Scheduler.
// The name "Scheduler" is reserved for the compute-plane workload scheduler
// that lives in the compute/ package.
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
