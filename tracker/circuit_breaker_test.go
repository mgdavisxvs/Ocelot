package tracker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestCB(maxFailures uint32) *CircuitBreaker {
	return NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  maxFailures,
		ResetTimeout: 50 * time.Millisecond,
		HalfOpenMax:  2,
	})
}

// ── NewCircuitBreaker ─────────────────────────────────────────────────────────

func TestNewCircuitBreaker_Defaults(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "defaults"})
	if cb.GetState() != StateClosed {
		t.Error("initial state should be Closed")
	}
	if cb.maxFailures != 5 {
		t.Errorf("default maxFailures = %d, want 5", cb.maxFailures)
	}
	if cb.resetTimeout != 30*time.Second {
		t.Errorf("default resetTimeout = %v, want 30s", cb.resetTimeout)
	}
	if cb.halfOpenMax != 3 {
		t.Errorf("default halfOpenMax = %d, want 3", cb.halfOpenMax)
	}
}

// ── GetStateString ────────────────────────────────────────────────────────────

func TestGetStateString_Closed(t *testing.T) {
	cb := newTestCB(5)
	if cb.GetStateString() != "closed" {
		t.Errorf("GetStateString = %q, want \"closed\"", cb.GetStateString())
	}
}

func TestGetStateString_Open(t *testing.T) {
	cb := newTestCB(1)
	cb.Execute(func() error { return errors.New("fail") })
	if cb.GetStateString() != "open" {
		t.Errorf("GetStateString = %q, want \"open\"", cb.GetStateString())
	}
}

func TestGetStateString_HalfOpen(t *testing.T) {
	cb := newTestCB(1)
	cb.Execute(func() error { return errors.New("fail") })
	// Manually set state to half-open (simulate timeout elapsed)
	cb.mu.Lock()
	cb.state = StateHalfOpen
	cb.mu.Unlock()
	if cb.GetStateString() != "half-open" {
		t.Errorf("GetStateString = %q, want \"half-open\"", cb.GetStateString())
	}
}

func TestGetStateString_Unknown(t *testing.T) {
	cb := newTestCB(1)
	cb.mu.Lock()
	cb.state = CircuitState(99)
	cb.mu.Unlock()
	if cb.GetStateString() != "unknown" {
		t.Errorf("GetStateString = %q, want \"unknown\"", cb.GetStateString())
	}
}

// ── Execute: success path ─────────────────────────────────────────────────────

func TestCircuitBreaker_ExecuteSuccess_KeepsClosed(t *testing.T) {
	cb := newTestCB(3)
	for i := 0; i < 5; i++ {
		if err := cb.Execute(func() error { return nil }); err != nil {
			t.Fatalf("Execute returned error on success: %v", err)
		}
	}
	if cb.GetState() != StateClosed {
		t.Error("repeated successes should keep circuit closed")
	}
}

// ── Execute: failure → open transition ───────────────────────────────────────

func TestCircuitBreaker_OpensAfterMaxFailures(t *testing.T) {
	cb := newTestCB(3)
	fail := errors.New("boom")
	for i := 0; i < 3; i++ {
		cb.Execute(func() error { return fail })
	}
	if cb.GetState() != StateOpen {
		t.Errorf("expected Open after 3 failures, got %s", cb.GetStateString())
	}
}

func TestCircuitBreaker_OpenRejectsImmediately(t *testing.T) {
	cb := newTestCB(1)
	cb.Execute(func() error { return errors.New("fail") })

	called := false
	err := cb.Execute(func() error {
		called = true
		return nil
	})
	if called {
		t.Error("open circuit should not call the function")
	}
	if err == nil {
		t.Error("expected error from open circuit")
	}
}

// ── Half-open transition (time-based) ─────────────────────────────────────────

func TestCircuitBreaker_HalfOpenAfterResetTimeout(t *testing.T) {
	cb := newTestCB(1)
	cb.Execute(func() error { return errors.New("fail") })
	if cb.GetState() != StateOpen {
		t.Fatal("circuit should be open")
	}

	// Wait for reset timeout (50ms).
	time.Sleep(80 * time.Millisecond)

	// The next canExecute call should flip to half-open.
	executed := false
	cb.Execute(func() error {
		executed = true
		return nil
	})
	if !executed {
		t.Error("expected function to be called during half-open probe")
	}
}

// ── Half-open: failure reopens ────────────────────────────────────────────────

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	cb := newTestCB(1)
	cb.Execute(func() error { return errors.New("fail") })
	time.Sleep(80 * time.Millisecond)

	// First call after timeout transitions to half-open and executes.
	cb.Execute(func() error { return errors.New("still failing") })

	if cb.GetState() != StateOpen {
		t.Errorf("half-open failure should reopen circuit, got %s", cb.GetStateString())
	}
}

// ── Half-open: enough successes closes ────────────────────────────────────────

func TestCircuitBreaker_HalfOpenSuccessesClose(t *testing.T) {
	cb := newTestCB(1)
	cb.Execute(func() error { return errors.New("fail") })
	time.Sleep(80 * time.Millisecond)

	// HalfOpenMax is 2; the first call transitions Open→HalfOpen (count stays 0),
	// then 2 more calls in StateHalfOpen increment the counter to 2 and close.
	cb.Execute(func() error { return nil }) // Open→HalfOpen transition; count=0
	cb.Execute(func() error { return nil }) // count→1
	cb.Execute(func() error { return nil }) // count→2 → should close

	if cb.GetState() != StateClosed {
		t.Errorf("circuit should be closed after all half-open successes, got %s", cb.GetStateString())
	}
}

// ── Reset ─────────────────────────────────────────────────────────────────────

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := newTestCB(1)
	cb.Execute(func() error { return errors.New("fail") })
	if cb.GetState() != StateOpen {
		t.Fatal("circuit should be open")
	}

	cb.Reset()
	if cb.GetState() != StateClosed {
		t.Errorf("after Reset, state = %s, want closed", cb.GetStateString())
	}
}

func TestCircuitBreaker_Reset_ClearsCounters(t *testing.T) {
	cb := newTestCB(3)
	cb.Execute(func() error { return errors.New("fail") })
	cb.Execute(func() error { return errors.New("fail") })
	cb.Reset()

	// After reset, 3 more failures should open (not 1, since counter is cleared).
	if err := cb.Execute(func() error { return errors.New("fail") }); err != nil {
		// Execute ran but returned the function error — that's fine.
	}
	if cb.GetState() == StateOpen {
		t.Error("1 failure after reset should not open a circuit with maxFailures=3")
	}
}

// ── ExecuteWithContext ────────────────────────────────────────────────────────

func TestCircuitBreaker_ExecuteWithContext_Success(t *testing.T) {
	cb := newTestCB(3)
	err := cb.ExecuteWithContext(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("ExecuteWithContext returned error: %v", err)
	}
}

func TestCircuitBreaker_ExecuteWithContext_OpenRejects(t *testing.T) {
	cb := newTestCB(1)
	cb.Execute(func() error { return errors.New("fail") })

	err := cb.ExecuteWithContext(context.Background(), func(_ context.Context) error {
		return nil
	})
	if err == nil {
		t.Error("expected error from ExecuteWithContext on open circuit")
	}
}

// ── canExecute edge cases ─────────────────────────────────────────────────────

func TestCircuitBreaker_HalfOpen_ExhaustsMax(t *testing.T) {
	cb := newTestCB(1)
	cb.mu.Lock()
	cb.state = StateHalfOpen
	cb.halfOpenCount = 2 // == halfOpenMax(2); no slots remain
	cb.mu.Unlock()

	err := cb.Execute(func() error { return nil })
	if err == nil {
		t.Error("expected ErrCircuitOpen when HalfOpen slots exhausted")
	}
}

func TestCircuitBreaker_UnknownState_Rejects(t *testing.T) {
	cb := newTestCB(1)
	cb.mu.Lock()
	cb.state = CircuitState(99) // hits default branch
	cb.mu.Unlock()

	err := cb.Execute(func() error { return nil })
	if err == nil {
		t.Error("expected rejection for unknown circuit state")
	}
}
