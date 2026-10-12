package tracker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var errFake = errors.New("fake error")

func TestCircuitBreaker_InitialStateClosed(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "test", MaxFailures: 3, ResetTimeout: time.Second, HalfOpenMax: 2})
	if cb.GetState() != StateClosed {
		t.Errorf("initial state = %v, want StateClosed", cb.GetState())
	}
}

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "test", MaxFailures: 3, ResetTimeout: time.Second, HalfOpenMax: 1})

	for i := 0; i < 3; i++ {
		cb.Execute(func() error { return errFake }) //nolint:errcheck
	}

	if cb.GetState() != StateOpen {
		t.Errorf("state after %d failures = %v, want StateOpen", 3, cb.GetState())
	}
}

func TestCircuitBreaker_OpenRejectsRequests(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "test", MaxFailures: 1, ResetTimeout: time.Hour, HalfOpenMax: 1})
	cb.Execute(func() error { return errFake }) //nolint:errcheck

	err := cb.Execute(func() error { return nil })
	if err == nil {
		t.Error("open circuit should reject with error, got nil")
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  1,
		ResetTimeout: 30 * time.Millisecond,
		HalfOpenMax:  1,
	})
	cb.Execute(func() error { return errFake }) //nolint:errcheck

	if cb.GetState() != StateOpen {
		t.Fatal("circuit should be open")
	}

	time.Sleep(60 * time.Millisecond)

	// First Execute after timeout: Open→HalfOpen probe (halfOpenCount not yet incremented).
	cb.Execute(func() error { return nil }) //nolint:errcheck

	// State should be HalfOpen after probe succeeds (count=0 < max=1, not yet closed).
	if cb.GetState() != StateHalfOpen {
		t.Errorf("after probe success state = %v, want StateHalfOpen", cb.GetState())
	}

	// Second success: halfOpenCount reaches halfOpenMax=1 → circuit closes.
	cb.Execute(func() error { return nil }) //nolint:errcheck
	if cb.GetState() != StateClosed {
		t.Errorf("after counted half-open success state = %v, want StateClosed", cb.GetState())
	}
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  1,
		ResetTimeout: 30 * time.Millisecond,
		HalfOpenMax:  2,
	})
	cb.Execute(func() error { return errFake }) //nolint:errcheck
	time.Sleep(60 * time.Millisecond)

	// One request gets through in half-open; fail it.
	cb.Execute(func() error { return errFake }) //nolint:errcheck

	if cb.GetState() != StateOpen {
		t.Errorf("half-open failure: state = %v, want StateOpen", cb.GetState())
	}
}

func TestCircuitBreaker_HalfOpenMaxLimitsRequests(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  1,
		ResetTimeout: 30 * time.Millisecond,
		HalfOpenMax:  1,
	})
	cb.Execute(func() error { return errFake }) //nolint:errcheck
	time.Sleep(60 * time.Millisecond)

	// Probe (Open→HalfOpen) + one counted success → circuit closes.
	cb.Execute(func() error { return nil }) //nolint:errcheck
	cb.Execute(func() error { return nil }) //nolint:errcheck

	// Circuit should be closed now; further requests succeed.
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Errorf("closed circuit should allow requests, got %v", err)
	}
}

func TestCircuitBreaker_SuccessResetFailures(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "test", MaxFailures: 5, ResetTimeout: time.Second, HalfOpenMax: 1})
	for i := 0; i < 3; i++ {
		cb.Execute(func() error { return errFake }) //nolint:errcheck
	}
	cb.Execute(func() error { return nil }) //nolint:errcheck
	if cb.GetState() != StateClosed {
		t.Error("success should keep circuit closed after partial failures")
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "test", MaxFailures: 1, ResetTimeout: time.Hour, HalfOpenMax: 1})
	cb.Execute(func() error { return errFake }) //nolint:errcheck

	if cb.GetState() != StateOpen {
		t.Fatal("circuit should be open")
	}
	cb.Reset()
	if cb.GetState() != StateClosed {
		t.Errorf("after Reset state = %v, want StateClosed", cb.GetState())
	}
	// Should now allow requests
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Errorf("after Reset Execute failed: %v", err)
	}
}

func TestCircuitBreaker_GetStateString(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "test", MaxFailures: 1, ResetTimeout: time.Hour, HalfOpenMax: 1})
	if s := cb.GetStateString(); s != "closed" {
		t.Errorf("state string = %q, want \"closed\"", s)
	}
	cb.Execute(func() error { return errFake }) //nolint:errcheck
	if s := cb.GetStateString(); s != "open" {
		t.Errorf("state string after failure = %q, want \"open\"", s)
	}
}

func TestCircuitBreaker_ConcurrentExecute(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "concurrent", MaxFailures: 100, ResetTimeout: time.Second, HalfOpenMax: 10})

	var wg sync.WaitGroup
	const goroutines = 50
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.Execute(func() error { return nil }) //nolint:errcheck
		}()
	}
	wg.Wait()
	if cb.GetState() != StateClosed {
		t.Errorf("after 50 concurrent successes state = %v, want StateClosed", cb.GetState())
	}
}
