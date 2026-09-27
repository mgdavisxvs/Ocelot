package tracker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── Knuth: algorithmic correctness, loop invariants, data-structure invariants ─

// TestGUC_InitialState_IsClosed verifies the constructor invariant: every
// newly created CircuitBreaker must start in StateClosed regardless of config.
func TestGUC_InitialState_IsClosed(t *testing.T) {
	tests := []struct {
		name   string
		config CircuitBreakerConfig
	}{
		{"zero-value config", CircuitBreakerConfig{}},
		{"explicit MaxFailures", CircuitBreakerConfig{MaxFailures: 3}},
		{"explicit ResetTimeout", CircuitBreakerConfig{ResetTimeout: 10 * time.Second}},
		{"all fields set", CircuitBreakerConfig{MaxFailures: 5, ResetTimeout: 30 * time.Second, HalfOpenMax: 2}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cb := NewCircuitBreaker(tc.config)
			if got := cb.GetState(); got != StateClosed {
				t.Errorf("want StateClosed, got %v", got)
			}
		})
	}
}

// TestGUC_TripThreshold_ExactNFailures verifies the loop invariant: exactly
// MaxFailures consecutive failures (no more, no less) trip CLOSED → OPEN.
func TestGUC_TripThreshold_ExactNFailures(t *testing.T) {
	tests := []struct {
		maxFailures uint32
	}{
		{1}, {3}, {5},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(fmt.Sprintf("maxFailures=%d", tc.maxFailures), func(t *testing.T) {
			cb := NewCircuitBreaker(CircuitBreakerConfig{
				MaxFailures:  tc.maxFailures,
				ResetTimeout: time.Hour,
			})

			// N-1 failures must NOT trip
			for i := uint32(0); i < tc.maxFailures-1; i++ {
				_ = cb.Execute(func() error { return fmt.Errorf("fail") })
				if cb.GetState() != StateClosed {
					t.Errorf("after %d/%d failures, expected StateClosed", i+1, tc.maxFailures)
				}
			}

			// N-th failure must trip
			_ = cb.Execute(func() error { return fmt.Errorf("fail") })
			if got := cb.GetState(); got != StateOpen {
				t.Errorf("after %d failures, expected StateOpen, got %v", tc.maxFailures, got)
			}
		})
	}
}

// TestGUC_GetStateString_AllStates verifies the string representation for
// every valid CircuitState enum value.
func TestGUC_GetStateString_AllStates(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: time.Millisecond,
		HalfOpenMax:  1,
	})

	if got := cb.GetStateString(); got != "closed" {
		t.Errorf("initial state string: want %q, got %q", "closed", got)
	}

	// Trip to Open
	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	if got := cb.GetStateString(); got != "open" {
		t.Errorf("after trip, state string: want %q, got %q", "open", got)
	}

	// Transition to HalfOpen; hold the probe in-flight to observe the state
	time.Sleep(5 * time.Millisecond)
	gate := make(chan struct{})
	probeIn := make(chan struct{})
	go func() {
		_ = cb.Execute(func() error {
			close(probeIn)
			<-gate
			return nil
		})
	}()
	<-probeIn // goroutine is inside fn → circuit is in HalfOpen

	got := cb.GetStateString()
	close(gate)
	if got != "half-open" {
		t.Errorf("during probe, state string: want %q, got %q", "half-open", got)
	}
}

// TestGUC_DefaultConfig_Defaults verifies the data-structure invariant that
// zero-value fields are filled with documented defaults (MaxFailures=5).
func TestGUC_DefaultConfig_Defaults(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{}) // all defaults

	// 4 failures below the default threshold of 5 must not trip
	for i := 0; i < 4; i++ {
		_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	}
	if cb.GetState() != StateClosed {
		t.Error("default MaxFailures=5: four failures must not trip the circuit")
	}

	// 5th failure crosses the threshold
	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	if cb.GetState() != StateOpen {
		t.Error("default MaxFailures=5: five failures must trip the circuit")
	}
}

// TestGUC_SuccessInClosed_ResetsFailures verifies that a successful call in
// CLOSED resets the failure accumulator so the full threshold is needed again.
func TestGUC_SuccessInClosed_ResetsFailures(t *testing.T) {
	const maxFail = uint32(3)
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  maxFail,
		ResetTimeout: time.Hour,
	})

	// Accumulate maxFail-1 failures
	for i := uint32(0); i < maxFail-1; i++ {
		_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	}

	// A success should reset the counter
	_ = cb.Execute(func() error { return nil })
	if cb.GetState() != StateClosed {
		t.Fatal("circuit should remain Closed after success")
	}

	// Need full maxFail failures again after reset
	for i := uint32(0); i < maxFail-1; i++ {
		_ = cb.Execute(func() error { return fmt.Errorf("fail") })
		if cb.GetState() != StateClosed {
			t.Errorf("post-reset: failure %d/%d should not trip", i+1, maxFail)
		}
	}
	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	if cb.GetState() != StateOpen {
		t.Error("should trip after reset + maxFail consecutive failures")
	}
}

// ── Turing: termination conditions and halting behavior ───────────────────────

// TestGUC_OpenState_RejectsWithoutCallingFn verifies that Execute returns
// immediately without invoking fn when the circuit is OPEN.
func TestGUC_OpenState_RejectsWithoutCallingFn(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: time.Hour,
	})
	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	if cb.GetState() != StateOpen {
		t.Fatal("prerequisite: expected StateOpen")
	}

	var called bool
	err := cb.Execute(func() error {
		called = true
		return nil
	})

	if called {
		t.Error("fn must not be called when circuit is Open")
	}
	if err == nil {
		t.Error("Execute in Open state must return a non-nil error")
	}
}

// TestGUC_OpenState_BeforeTimeout_StillRejectsAll verifies that all calls
// in OPEN state are rejected before the reset timeout elapses.
func TestGUC_OpenState_BeforeTimeout_StillRejectsAll(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: time.Hour, // will not expire during test
	})
	_ = cb.Execute(func() error { return fmt.Errorf("fail") })

	for i := 0; i < 5; i++ {
		err := cb.Execute(func() error { return nil })
		if err == nil {
			t.Errorf("call %d: expected rejection in Open state, got nil error", i+1)
		}
	}
}

// TestGUC_OpenToHalfOpen_AfterTimeout verifies that after resetTimeout the
// circuit allows a probe and transitions to HALF_OPEN.
func TestGUC_OpenToHalfOpen_AfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: 2 * time.Millisecond,
		HalfOpenMax:  1,
	})

	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	if cb.GetState() != StateOpen {
		t.Fatal("prerequisite: expected StateOpen after failure")
	}

	// Immediately – before timeout – must still be rejected
	if err := cb.Execute(func() error { return nil }); err == nil {
		t.Error("Execute before timeout must be rejected")
	}

	time.Sleep(10 * time.Millisecond) // ensure timeout has elapsed

	gate := make(chan struct{})
	probeIn := make(chan struct{})
	go func() {
		_ = cb.Execute(func() error {
			close(probeIn)
			<-gate
			return nil
		})
	}()

	select {
	case <-probeIn:
		// probe admitted → circuit is now in HalfOpen
		if got := cb.GetState(); got != StateHalfOpen {
			close(gate)
			t.Errorf("during post-timeout probe, expected StateHalfOpen, got %v", got)
			return
		}
		close(gate)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("probe was never admitted after resetTimeout elapsed")
	}
}

// TestGUC_HalfOpenMax_LimitsProbes verifies that only (1 transition probe +
// HalfOpenMax) requests are admitted in HALF_OPEN; additional ones are rejected.
func TestGUC_HalfOpenMax_LimitsProbes(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: time.Millisecond,
		HalfOpenMax:  1,
	})

	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	time.Sleep(5 * time.Millisecond)

	// admitted counts goroutines that reached inside fn
	var admitted int32
	gate := make(chan struct{})
	allIn := make(chan struct{})
	var once sync.Once

	var wg sync.WaitGroup
	// Launch 2 goroutines: 1 transition probe + 1 HalfOpenMax probe
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cb.Execute(func() error {
				n := atomic.AddInt32(&admitted, 1)
				if n == 2 {
					once.Do(func() { close(allIn) })
				}
				<-gate
				return nil
			})
		}()
	}

	select {
	case <-allIn:
		// both probes are inside fn; halfOpenCount is now at HalfOpenMax
	case <-time.After(500 * time.Millisecond):
		close(gate)
		wg.Wait()
		t.Fatal("timed out waiting for probes to be admitted into HalfOpen state")
	}

	// A third Execute must be rejected
	err := cb.Execute(func() error { return nil })
	close(gate)
	wg.Wait()

	if err == nil {
		t.Error("third probe must be rejected when HalfOpenMax is exhausted")
	}
}

// TestGUC_HalfOpenFailure_ReopensCircuit verifies that a single failure in
// HALF_OPEN immediately transitions back to OPEN.
func TestGUC_HalfOpenFailure_ReopensCircuit(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: 2 * time.Millisecond,
		HalfOpenMax:  3,
	})

	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	time.Sleep(10 * time.Millisecond)

	// Failure during the half-open probe
	_ = cb.Execute(func() error { return fmt.Errorf("probe fail") })

	if got := cb.GetState(); got != StateOpen {
		t.Errorf("after HalfOpen failure, expected StateOpen, got %v", got)
	}
}

// ── Church: functional purity, side-effect isolation, referential transparency ─

// TestGUC_Execute_ReturnsNilOnSuccess verifies that Execute propagates a nil
// return value from fn without modification.
func TestGUC_Execute_ReturnsNilOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{MaxFailures: 5})
	if err := cb.Execute(func() error { return nil }); err != nil {
		t.Errorf("Execute with successful fn must return nil, got %v", err)
	}
}

// TestGUC_Reset_RestoresClosed verifies that Reset() from OPEN transitions
// the circuit back to CLOSED and allows subsequent execution.
func TestGUC_Reset_RestoresClosed(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: time.Hour,
	})
	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	if cb.GetState() != StateOpen {
		t.Fatal("prerequisite: expected StateOpen before Reset")
	}

	cb.Reset()

	if got := cb.GetState(); got != StateClosed {
		t.Errorf("after Reset, expected StateClosed, got %v", got)
	}
	if err := cb.Execute(func() error { return nil }); err != nil {
		t.Errorf("after Reset, Execute must succeed, got %v", err)
	}
}

// TestGUC_Reset_FromHalfOpen_RestoresClosed verifies that Reset() called
// during a HALF_OPEN probe also returns the circuit to CLOSED.
func TestGUC_Reset_FromHalfOpen_RestoresClosed(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: 2 * time.Millisecond,
		HalfOpenMax:  1,
	})

	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	time.Sleep(10 * time.Millisecond)

	gate := make(chan struct{})
	probeIn := make(chan struct{})
	go func() {
		_ = cb.Execute(func() error {
			close(probeIn)
			<-gate
			return nil
		})
	}()
	<-probeIn // circuit is in HalfOpen

	cb.Reset()
	got := cb.GetState()
	close(gate)

	if got != StateClosed {
		t.Errorf("Reset from HalfOpen: expected StateClosed, got %v", got)
	}
}

// TestGUC_ExecuteWithContext_PassesContext verifies that ExecuteWithContext
// forwards the caller's context to fn without modification.
func TestGUC_ExecuteWithContext_PassesContext(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{MaxFailures: 5})

	type ctxKey string
	const key ctxKey = "sentinel"
	ctx := context.WithValue(context.Background(), key, "42")

	var received context.Context
	err := cb.ExecuteWithContext(ctx, func(c context.Context) error {
		received = c
		return nil
	})
	if err != nil {
		t.Fatalf("ExecuteWithContext returned unexpected error: %v", err)
	}
	if received == nil {
		t.Fatal("fn received nil context")
	}
	if v, _ := received.Value(key).(string); v != "42" {
		t.Errorf("context value not forwarded: want %q, got %q", "42", v)
	}
}

// TestGUC_MultipleResets_Idempotent verifies that calling Reset() repeatedly
// is safe and always results in StateClosed with no state corruption.
func TestGUC_MultipleResets_Idempotent(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  3,
		ResetTimeout: time.Hour,
	})
	for i := 0; i < 3; i++ {
		_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	}
	if cb.GetState() != StateOpen {
		t.Fatal("prerequisite: expected StateOpen before idempotent-reset test")
	}

	for i := 0; i < 5; i++ {
		cb.Reset()
		if got := cb.GetState(); got != StateClosed {
			t.Errorf("after reset call #%d, expected StateClosed, got %v", i+1, got)
		}
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state detection ─

// TestGUC_ConcurrentFailures_TripSafe verifies that concurrent failures from
// many goroutines result in a consistent Open state without data races.
func TestGUC_ConcurrentFailures_TripSafe(t *testing.T) {
	const goroutines = 50
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  5,
		ResetTimeout: time.Hour,
	})

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = cb.Execute(func() error { return fmt.Errorf("concurrent fail") })
		}()
	}

	close(start)
	wg.Wait()

	// With 50 failures and maxFailures=5, circuit MUST be Open
	if got := cb.GetState(); got != StateOpen {
		t.Errorf("after %d concurrent failures (threshold 5), expected StateOpen, got %v", goroutines, got)
	}
}

// TestGUC_HalfOpenSuccess_ClosesCircuit verifies the Gödel invariant: after
// the transition probe plus HalfOpenMax successful probes the circuit closes.
func TestGUC_HalfOpenSuccess_ClosesCircuit(t *testing.T) {
	// halfOpenMax=1: 1 transition probe (uncounted) + 1 counted probe → Closed
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: 2 * time.Millisecond,
		HalfOpenMax:  1,
	})

	_ = cb.Execute(func() error { return fmt.Errorf("fail") })
	time.Sleep(10 * time.Millisecond)

	// Transition probe: Open → HalfOpen (halfOpenCount stays 0 in this path)
	_ = cb.Execute(func() error { return nil })

	// Counted probe: halfOpenCount 0→1; onSuccess sees 1 >= 1 → Closed
	_ = cb.Execute(func() error { return nil })

	if got := cb.GetState(); got != StateClosed {
		t.Errorf("after successful half-open probes, expected StateClosed, got %v", got)
	}
}

// TestGUC_OpenError_HasCircuitBreakerType verifies the formal error contract:
// the error returned from an Open circuit is *TrackerError with
// Type == "circuit_breaker".
func TestGUC_OpenError_HasCircuitBreakerType(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "guc-test",
		MaxFailures:  1,
		ResetTimeout: time.Hour,
	})

	_ = cb.Execute(func() error { return fmt.Errorf("fail") })

	err := cb.Execute(func() error { return nil })
	if err == nil {
		t.Fatal("expected non-nil error from Open circuit")
	}

	te, ok := err.(*TrackerError)
	if !ok {
		t.Fatalf("expected *TrackerError, got %T: %v", err, err)
	}
	if te.Type != "circuit_breaker" {
		t.Errorf("error Type: want %q, got %q", "circuit_breaker", te.Type)
	}
}

// TestGUC_StateAlwaysValid_NeverUnknown verifies the consistency invariant:
// the state is always one of the three valid enum values under concurrent
// read/write pressure, and the "unknown" branch is never reached.
func TestGUC_StateAlwaysValid_NeverUnknown(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  3,
		ResetTimeout: 2 * time.Millisecond,
		HalfOpenMax:  2,
	})

	validStates := map[CircuitState]bool{
		StateClosed:   true,
		StateOpen:     true,
		StateHalfOpen: true,
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer goroutines: interleave failures, successes, and resets
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if n%2 == 0 {
						_ = cb.Execute(func() error { return fmt.Errorf("err") })
					} else {
						_ = cb.Execute(func() error { return nil })
					}
					cb.Reset()
				}
			}
		}(i)
	}

	// Reader goroutine: every observed state must be valid
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s := cb.GetState()
				if !validStates[s] {
					t.Errorf("invalid state observed: %v", s)
				}
			}
		}
	}()

	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestGUC_FailureCount_BelowThreshold_NoTrip is a table-driven Gödel boundary
// test: fewer than MaxFailures consecutive failures must never open the circuit.
func TestGUC_FailureCount_BelowThreshold_NoTrip(t *testing.T) {
	tests := []struct {
		maxFailures uint32
		attempts    uint32 // always < maxFailures
	}{
		{maxFailures: 1, attempts: 0},
		{maxFailures: 2, attempts: 1},
		{maxFailures: 5, attempts: 4},
		{maxFailures: 10, attempts: 9},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(fmt.Sprintf("max=%d_attempts=%d", tc.maxFailures, tc.attempts), func(t *testing.T) {
			cb := NewCircuitBreaker(CircuitBreakerConfig{
				MaxFailures:  tc.maxFailures,
				ResetTimeout: time.Hour,
			})
			for i := uint32(0); i < tc.attempts; i++ {
				_ = cb.Execute(func() error { return fmt.Errorf("fail") })
			}
			if cb.GetState() != StateClosed {
				t.Errorf("%d/%d failures: circuit must remain Closed below threshold",
					tc.attempts, tc.maxFailures)
			}
		})
	}
}
