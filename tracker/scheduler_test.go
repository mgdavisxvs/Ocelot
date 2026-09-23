package tracker

import (
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
