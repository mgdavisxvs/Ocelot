package tracker

import (
	"context"
	"sync"
)

// WorkerPool implements a bounded goroutine pool using semaphore pattern
// This prevents resource exhaustion under connection floods
// System enhancement from gap analysis Phase 5
type WorkerPool struct {
	sem chan struct{}
	wg  sync.WaitGroup
	ctx context.Context
}

// NewWorkerPool creates a new bounded worker pool
// capacity: Maximum number of concurrent workers (goroutines)
func NewWorkerPool(capacity int) *WorkerPool {
	return &WorkerPool{
		sem: make(chan struct{}, capacity),
		ctx: context.Background(),
	}
}

// Submit submits a task to the pool
// Blocks if pool is at capacity until a slot becomes available
func (p *WorkerPool) Submit(task func()) error {
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.sem <- struct{}{}:
		// Got a slot
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer func() { <-p.sem }() // Release slot
			task()
		}()
		return nil
	}
}

// TrySubmit attempts to submit a task without blocking
// Returns false if pool is at capacity
func (p *WorkerPool) TrySubmit(task func()) bool {
	select {
	case p.sem <- struct{}{}:
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer func() { <-p.sem }()
			task()
		}()
		return true
	default:
		return false
	}
}

// Wait waits for all tasks to complete
func (p *WorkerPool) Wait() {
	p.wg.Wait()
}

// Shutdown gracefully shuts down the pool
func (p *WorkerPool) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Active returns the number of active workers
func (p *WorkerPool) Active() int {
	return len(p.sem)
}

// Capacity returns the total capacity of the pool
func (p *WorkerPool) Capacity() int {
	return cap(p.sem)
}
