package tracker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPool_SubmitUnderCapacity(t *testing.T) {
	pool := NewWorkerPool(10)

	var wg sync.WaitGroup
	var count atomic.Int32
	for i := 0; i < 5; i++ {
		wg.Add(1)
		if err := pool.Submit(func() {
			defer wg.Done()
			count.Add(1)
		}); err != nil {
			t.Fatalf("Submit returned error: %v", err)
		}
	}
	wg.Wait()
	if count.Load() != 5 {
		t.Errorf("executed %d tasks, want 5", count.Load())
	}
}

func TestWorkerPool_SubmitAtCapacity(t *testing.T) {
	const cap = 3
	pool := NewWorkerPool(cap)

	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < cap; i++ {
		wg.Add(1)
		pool.Submit(func() { //nolint:errcheck
			defer wg.Done()
			<-release
		})
	}

	// Pool is now full. A new submit should block until a slot frees.
	done := make(chan struct{})
	go func() {
		pool.Submit(func() {}) //nolint:errcheck
		close(done)
	}()

	select {
	case <-done:
		t.Error("Submit should have blocked on a full pool")
	case <-time.After(30 * time.Millisecond):
		// Expected: still waiting.
	}

	close(release) // free all slots
	wg.Wait()

	select {
	case <-done:
		// Submit unblocked
	case <-time.After(200 * time.Millisecond):
		t.Error("Submit did not unblock after slots freed")
	}
}

func TestWorkerPool_TrySubmit_Full(t *testing.T) {
	pool := NewWorkerPool(1)

	release := make(chan struct{})
	pool.TrySubmit(func() { <-release }) // fills the single slot

	accepted := pool.TrySubmit(func() {})
	close(release)
	pool.Wait()

	if accepted {
		t.Error("TrySubmit should return false when pool is full")
	}
}

func TestWorkerPool_TrySubmit_HasRoom(t *testing.T) {
	pool := NewWorkerPool(5)
	if !pool.TrySubmit(func() {}) {
		t.Error("TrySubmit should return true when pool has capacity")
	}
	pool.Wait()
}

func TestWorkerPool_ActiveAndCapacity(t *testing.T) {
	pool := NewWorkerPool(4)
	if pool.Capacity() != 4 {
		t.Errorf("Capacity = %d, want 4", pool.Capacity())
	}

	release := make(chan struct{})
	for i := 0; i < 2; i++ {
		pool.Submit(func() { <-release }) //nolint:errcheck
	}

	time.Sleep(20 * time.Millisecond)
	active := pool.Active()
	close(release)
	pool.Wait()

	if active < 1 || active > 2 {
		t.Errorf("Active mid-run = %d, want 1 or 2", active)
	}
}

func TestWorkerPool_Shutdown_DrainsInflight(t *testing.T) {
	pool := NewWorkerPool(5)

	var count atomic.Int32
	for i := 0; i < 3; i++ {
		pool.Submit(func() { //nolint:errcheck
			time.Sleep(20 * time.Millisecond)
			count.Add(1)
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	if count.Load() != 3 {
		t.Errorf("Shutdown: %d tasks completed, want 3", count.Load())
	}
}

func TestWorkerPool_Shutdown_Timeout(t *testing.T) {
	pool := NewWorkerPool(1)

	release := make(chan struct{})
	pool.Submit(func() { <-release }) //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := pool.Shutdown(ctx)
	if err == nil {
		t.Error("Shutdown should return an error when context times out")
	}
	close(release)
	pool.Wait()
}

func TestWorkerPool_Wait_CompletesAll(t *testing.T) {
	pool := NewWorkerPool(10)
	var count atomic.Int32
	for i := 0; i < 10; i++ {
		pool.Submit(func() { count.Add(1) }) //nolint:errcheck
	}
	pool.Wait()
	if count.Load() != 10 {
		t.Errorf("Wait: %d tasks completed, want 10", count.Load())
	}
}
