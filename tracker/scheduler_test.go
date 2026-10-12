package tracker

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type stubSchedulerDB struct {
	checkpoints int32
	rotations   int32
	cpErr       error
	rotErr      error
}

func (s *stubSchedulerDB) CheckpointWAL() error {
	atomic.AddInt32(&s.checkpoints, 1)
	return s.cpErr
}

func (s *stubSchedulerDB) CheckRotation() error {
	atomic.AddInt32(&s.rotations, 1)
	return s.rotErr
}

func newTestScheduler(db schedulerDB) (*Scheduler, *fakeClock) {
	fc := newFakeClock()
	s := &Scheduler{db: db, clock: fc, stop: make(chan struct{})}
	return s, fc
}

func TestSchedulerFiresBothMethods(t *testing.T) {
	db := &stubSchedulerDB{}
	s, fc := newTestScheduler(db)
	s.Start()
	defer s.Stop()

	fc.Fire()
	time.Sleep(20 * time.Millisecond)

	if atomic.LoadInt32(&db.checkpoints) != 1 {
		t.Errorf("want 1 checkpoint, got %d", atomic.LoadInt32(&db.checkpoints))
	}
	if atomic.LoadInt32(&db.rotations) != 1 {
		t.Errorf("want 1 rotation, got %d", atomic.LoadInt32(&db.rotations))
	}
}

func TestSchedulerMultipleTicks(t *testing.T) {
	db := &stubSchedulerDB{}
	s, fc := newTestScheduler(db)
	s.Start()
	defer s.Stop()

	const n = 5
	for i := 0; i < n; i++ {
		fc.Fire()
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&db.checkpoints) != n {
		t.Errorf("want %d checkpoints, got %d", n, atomic.LoadInt32(&db.checkpoints))
	}
	if atomic.LoadInt32(&db.rotations) != n {
		t.Errorf("want %d rotations, got %d", n, atomic.LoadInt32(&db.rotations))
	}
}

func TestSchedulerCheckpointErrorContinues(t *testing.T) {
	db := &stubSchedulerDB{cpErr: errors.New("disk full")}
	s, fc := newTestScheduler(db)
	s.Start()
	defer s.Stop()

	fc.Fire()
	time.Sleep(20 * time.Millisecond)
	fc.Fire()
	time.Sleep(20 * time.Millisecond)

	if atomic.LoadInt32(&db.checkpoints) < 2 {
		t.Error("scheduler must continue after CheckpointWAL error")
	}
}

func TestSchedulerRotationErrorContinues(t *testing.T) {
	db := &stubSchedulerDB{rotErr: errors.New("rotation failed")}
	s, fc := newTestScheduler(db)
	s.Start()
	defer s.Stop()

	fc.Fire()
	time.Sleep(20 * time.Millisecond)
	fc.Fire()
	time.Sleep(20 * time.Millisecond)

	if atomic.LoadInt32(&db.rotations) < 2 {
		t.Error("scheduler must continue after CheckRotation error")
	}
}

func TestSchedulerStopDrainsCleanly(t *testing.T) {
	db := &stubSchedulerDB{}
	s, _ := newTestScheduler(db)
	s.Start()
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("Stop() did not return within 500ms")
	}
}
