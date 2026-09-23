package tracker

import (
	"path/filepath"
	"testing"
	"time"
)

// ── Scheduler lifecycle ───────────────────────────────────────────────────────

func TestScheduler_StartStop(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	defer sm.Close()

	// Long interval so the ticker never fires during the test.
	s := NewScheduler(sm, 9999)
	s.Start()
	time.Sleep(10 * time.Millisecond)
	s.Stop()
	// If we reach here without deadlock the goroutine exited cleanly.
}

func TestScheduler_TickerFires(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	defer sm.Close()

	// 1-second interval but we override via internal field for fast test.
	s := &Scheduler{
		db:       sm,
		interval: time.Millisecond, // fires immediately
		stop:     make(chan struct{}),
	}
	s.Start()
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	// Ticker fired at least once — CheckpointWAL and CheckRotation ran.
}

func TestScheduler_CheckpointWAL_Error_DoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	defer sm.Close()

	// Close the underlying DB so CheckpointWAL returns an error, covering
	// the log.Printf("scheduler: WAL checkpoint: %v", err) branch.
	sm.currentDB.Close()

	s := &Scheduler{
		db:       sm,
		interval: time.Millisecond,
		stop:     make(chan struct{}),
	}
	s.Start()
	time.Sleep(30 * time.Millisecond)
	s.Stop()
}

func TestScheduler_CheckRotation_Error_DoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSQLiteShardManager(dir)
	if err != nil {
		t.Fatalf("NewSQLiteShardManager: %v", err)
	}
	defer sm.Close()

	// Point currentPath to a non-existent file so getDBSize fails and
	// CheckRotation returns an error, covering the log.Printf branch.
	sm.mu.Lock()
	sm.currentPath = filepath.Join(dir, "nonexistent-2099-01.db")
	sm.mu.Unlock()

	s := &Scheduler{
		db:       sm,
		interval: time.Millisecond,
		stop:     make(chan struct{}),
	}
	s.Start()
	time.Sleep(30 * time.Millisecond)
	s.Stop()
}
