package tracker

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreakerClosed(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test-cb",
		MaxFailures:  3,
		ResetTimeout: 100 * time.Millisecond,
	})

	// Circuit should start closed
	if cb.GetState() != StateClosed {
		t.Errorf("Expected initial state to be Closed, got %s", cb.GetStateString())
	}

	// Successful executions should keep circuit closed
	for i := 0; i < 5; i++ {
		err := cb.Execute(func() error {
			return nil
		})

		if err != nil {
			t.Errorf("Execution %d failed: %v", i, err)
		}

		if cb.GetState() != StateClosed {
			t.Errorf("Circuit opened unexpectedly after success")
		}
	}
}

func TestCircuitBreakerOpens(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test-cb",
		MaxFailures:  3,
		ResetTimeout: 100 * time.Millisecond,
	})

	testErr := errors.New("test error")

	// Execute failing operations
	for i := 0; i < 3; i++ {
		err := cb.Execute(func() error {
			return testErr
		})

		if err != testErr {
			t.Errorf("Expected error %v, got %v", testErr, err)
		}
	}

	// Circuit should now be open
	if cb.GetState() != StateOpen {
		t.Errorf("Expected circuit to be Open after %d failures, got %s",
			3, cb.GetStateString())
	}

	// Further executions should fail immediately
	err := cb.Execute(func() error {
		t.Error("Function should not be called when circuit is open")
		return nil
	})

	if err == nil {
		t.Error("Expected error when circuit is open")
	}
}

func TestCircuitBreakerHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test-cb",
		MaxFailures:  2,
		ResetTimeout: 50 * time.Millisecond,
		HalfOpenMax:  3,
	})

	testErr := errors.New("test error")

	// Open the circuit
	for i := 0; i < 2; i++ {
		cb.Execute(func() error { return testErr })
	}

	if cb.GetState() != StateOpen {
		t.Fatal("Circuit should be open")
	}

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Next execution should transition to half-open
	successCount := 0
	err := cb.Execute(func() error {
		successCount++
		return nil
	})

	if err != nil {
		t.Errorf("Half-open execution failed: %v", err)
	}

	if cb.GetState() != StateHalfOpen {
		t.Errorf("Expected Half-Open state, got %s", cb.GetStateString())
	}
}

func TestCircuitBreakerHalfOpenToClosedTransition(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test-cb",
		MaxFailures:  2,
		ResetTimeout: 50 * time.Millisecond,
		HalfOpenMax:  3,
	})

	testErr := errors.New("test error")

	// Open the circuit
	for i := 0; i < 2; i++ {
		cb.Execute(func() error { return testErr })
	}

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Execute successful half-open requests
	for i := 0; i < 3; i++ {
		err := cb.Execute(func() error {
			return nil
		})

		if err != nil {
			t.Fatalf("Half-open execution %d failed: %v", i, err)
		}
	}

	// Circuit should now be closed
	if cb.GetState() != StateClosed {
		t.Errorf("Expected circuit to be Closed after successful half-open tests, got %s",
			cb.GetStateString())
	}
}

func TestCircuitBreakerHalfOpenToOpenTransition(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test-cb",
		MaxFailures:  2,
		ResetTimeout: 50 * time.Millisecond,
		HalfOpenMax:  3,
	})

	testErr := errors.New("test error")

	// Open the circuit
	for i := 0; i < 2; i++ {
		cb.Execute(func() error { return testErr })
	}

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// First half-open request succeeds
	cb.Execute(func() error { return nil })

	if cb.GetState() != StateHalfOpen {
		t.Fatal("Circuit should be half-open")
	}

	// Next half-open request fails
	cb.Execute(func() error { return testErr })

	// Circuit should immediately reopen
	if cb.GetState() != StateOpen {
		t.Errorf("Expected circuit to reopen after half-open failure, got %s",
			cb.GetStateString())
	}
}

func TestCircuitBreakerHalfOpenLimit(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test-cb",
		MaxFailures:  2,
		ResetTimeout: 50 * time.Millisecond,
		HalfOpenMax:  2,
	})

	testErr := errors.New("test error")

	// Open the circuit
	for i := 0; i < 2; i++ {
		cb.Execute(func() error { return testErr })
	}

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Execute max half-open requests (should succeed and close circuit)
	for i := 0; i < 2; i++ {
		err := cb.Execute(func() error { return nil })
		if err != nil {
			t.Fatalf("Half-open execution %d failed: %v", i, err)
		}
	}

	// After halfOpenMax successful requests, circuit should be closed
	if cb.GetState() != StateClosed {
		t.Errorf("Expected circuit to be Closed after %d successful half-open tests, got %s",
			2, cb.GetStateString())
	}

	// Additional requests should now succeed (circuit is closed)
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Errorf("Execution after circuit close failed: %v", err)
	}
}

func TestCircuitBreakerReset(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test-cb",
		MaxFailures:  2,
		ResetTimeout: 100 * time.Millisecond,
	})

	testErr := errors.New("test error")

	// Open the circuit
	for i := 0; i < 2; i++ {
		cb.Execute(func() error { return testErr })
	}

	if cb.GetState() != StateOpen {
		t.Fatal("Circuit should be open")
	}

	// Manual reset
	cb.Reset()

	if cb.GetState() != StateClosed {
		t.Errorf("Expected circuit to be Closed after reset, got %s",
			cb.GetStateString())
	}

	// Should be able to execute now
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Errorf("Execution after reset failed: %v", err)
	}
}

func TestCircuitBreakerStateStrings(t *testing.T) {
	tests := []struct {
		state    CircuitState
		expected string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
	}

	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name: "test-cb",
	})

	for _, tt := range tests {
		// Manually set state for testing
		cb.mu.Lock()
		cb.state = tt.state
		cb.mu.Unlock()

		result := cb.GetStateString()
		if result != tt.expected {
			t.Errorf("GetStateString() for state %d = %s, expected %s",
				tt.state, result, tt.expected)
		}
	}
}

func TestCircuitBreakerDefaults(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name: "test-cb",
		// All values at zero, should use defaults
	})

	if cb.maxFailures != 5 {
		t.Errorf("Expected default MaxFailures 5, got %d", cb.maxFailures)
	}

	if cb.resetTimeout != 30*time.Second {
		t.Errorf("Expected default ResetTimeout 30s, got %v", cb.resetTimeout)
	}

	if cb.halfOpenMax != 3 {
		t.Errorf("Expected default HalfOpenMax 3, got %d", cb.halfOpenMax)
	}
}

func TestCircuitBreakerConcurrency(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test-cb",
		MaxFailures:  10,
		ResetTimeout: 100 * time.Millisecond,
	})

	// Execute many operations concurrently
	done := make(chan bool)
	for i := 0; i < 100; i++ {
		go func() {
			cb.Execute(func() error {
				time.Sleep(1 * time.Millisecond)
				return nil
			})
			done <- true
		}()
	}

	// Wait for all to complete
	for i := 0; i < 100; i++ {
		<-done
	}

	// Circuit should still be closed
	if cb.GetState() != StateClosed {
		t.Errorf("Expected circuit to remain Closed, got %s", cb.GetStateString())
	}
}

func TestCircuitBreakerResetTimeout(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test-cb",
		MaxFailures:  1,
		ResetTimeout: 100 * time.Millisecond,
	})

	testErr := errors.New("test error")

	// Open circuit
	cb.Execute(func() error { return testErr })

	if cb.GetState() != StateOpen {
		t.Fatal("Circuit should be open")
	}

	// Try to execute before timeout - should fail
	err := cb.Execute(func() error {
		t.Error("Should not execute before reset timeout")
		return nil
	})

	if err == nil {
		t.Error("Expected error before reset timeout")
	}

	// Wait for reset timeout
	time.Sleep(110 * time.Millisecond)

	// Should now transition to half-open
	cb.Execute(func() error { return nil })

	if cb.GetState() != StateHalfOpen && cb.GetState() != StateClosed {
		t.Errorf("Expected Half-Open or Closed state after timeout, got %s",
			cb.GetStateString())
	}
}
