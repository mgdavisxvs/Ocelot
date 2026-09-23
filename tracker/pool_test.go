package tracker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// ── NewWorkerPool ─────────────────────────────────────────────────────────────

func TestNewWorkerPool_CapacityAndActive(t *testing.T) {
	p := NewWorkerPool(5)
	if p.Capacity() != 5 {
		t.Errorf("Capacity = %d, want 5", p.Capacity())
	}
	if p.Active() != 0 {
		t.Errorf("Active = %d, want 0 initially", p.Active())
	}
}

// ── Submit ────────────────────────────────────────────────────────────────────

func TestWorkerPool_Submit_RunsTask(t *testing.T) {
	p := NewWorkerPool(4)
	var ran atomic.Int64
	for i := 0; i < 10; i++ {
		if err := p.Submit(func() { ran.Add(1) }); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	p.Wait()
	if ran.Load() != 10 {
		t.Errorf("ran = %d, want 10", ran.Load())
	}
}

func TestWorkerPool_Submit_CancelledContext(t *testing.T) {
	// Create a pool with a cancelled context by reaching in via the struct.
	ctx, cancel := context.WithCancel(context.Background())
	p := &WorkerPool{
		sem: make(chan struct{}, 1),
		ctx: ctx,
	}
	cancel()

	// Fill the semaphore so Submit must select on ctx.Done().
	p.sem <- struct{}{}

	err := p.Submit(func() {})
	if err == nil {
		t.Error("expected error when pool context is cancelled")
	}
}

// ── TrySubmit ─────────────────────────────────────────────────────────────────

func TestWorkerPool_TrySubmit_WhenAvailable(t *testing.T) {
	p := NewWorkerPool(2)
	var ran atomic.Int64
	if !p.TrySubmit(func() { ran.Add(1) }) {
		t.Error("TrySubmit should return true when capacity is available")
	}
	p.Wait()
	if ran.Load() != 1 {
		t.Errorf("ran = %d, want 1", ran.Load())
	}
}

func TestWorkerPool_TrySubmit_WhenFull(t *testing.T) {
	p := NewWorkerPool(1)
	// Manually saturate the semaphore.
	p.sem <- struct{}{}

	if p.TrySubmit(func() {}) {
		t.Error("TrySubmit should return false when pool is full")
	}
	<-p.sem // drain so nothing leaks
}

// ── Wait ──────────────────────────────────────────────────────────────────────

func TestWorkerPool_Wait_AllTasksComplete(t *testing.T) {
	p := NewWorkerPool(10)
	var count atomic.Int64
	for i := 0; i < 20; i++ {
		p.Submit(func() {
			time.Sleep(time.Millisecond)
			count.Add(1)
		})
	}
	p.Wait()
	if count.Load() != 20 {
		t.Errorf("count = %d, want 20", count.Load())
	}
}

// ── Shutdown ──────────────────────────────────────────────────────────────────

func TestWorkerPool_Shutdown_NoTasks(t *testing.T) {
	p := NewWorkerPool(4)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestWorkerPool_Shutdown_WaitsForTasks(t *testing.T) {
	p := NewWorkerPool(2)
	var done atomic.Int64
	p.Submit(func() {
		time.Sleep(20 * time.Millisecond)
		done.Add(1)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if done.Load() != 1 {
		t.Error("Shutdown returned before task completed")
	}
}

func TestWorkerPool_Shutdown_ContextDeadline(t *testing.T) {
	p := NewWorkerPool(1)
	p.Submit(func() { time.Sleep(500 * time.Millisecond) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := p.Shutdown(ctx)
	if err == nil {
		t.Error("expected context deadline error when tasks take too long")
	}
}

// ── Active ────────────────────────────────────────────────────────────────────

func TestWorkerPool_Active_ReflectsRunning(t *testing.T) {
	p := NewWorkerPool(10)
	started := make(chan struct{})
	done := make(chan struct{})
	p.Submit(func() {
		close(started)
		<-done
	})
	<-started
	if p.Active() == 0 {
		t.Error("Active should be > 0 while task is running")
	}
	close(done)
	p.Wait()
}
