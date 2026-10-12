package markov

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Knuth lens: algorithmic correctness, loop invariants, data-structure invariants
// ---------------------------------------------------------------------------

// TestGUC_Knuth_BaseIntervalInitially verifies the loop invariant that
// Current() and Next() both return the base interval before any Failure() or
// Success() call is made.
func TestGUC_Knuth_BaseIntervalInitially(t *testing.T) {
	tests := []struct {
		base   time.Duration
		factor float64
		max    time.Duration
	}{
		{time.Second, 2.0, 10 * time.Second},
		{30 * time.Second, 1.5, 5 * time.Minute},
		{100 * time.Millisecond, 3.0, 0},
	}
	for _, tc := range tests {
		ai := NewAdaptiveInterval(tc.base, tc.factor, tc.max, 0)
		got := ai.Current()
		if got != tc.base {
			t.Errorf("base=%v: Current()=%v before any call, want %v", tc.base, got, tc.base)
		}
		// Next() with jitter=0 must equal base exactly.
		n := ai.Next()
		if n != tc.base {
			t.Errorf("base=%v: Next()=%v before any call, want %v", tc.base, n, tc.base)
		}
	}
}

// TestGUC_Knuth_ExponentialBackoffFactor verifies the algorithmic correctness
// of the backoff: after k failures the interval equals base * factor^k (capped
// at max when max > 0).
func TestGUC_Knuth_ExponentialBackoffFactor(t *testing.T) {
	base := time.Second
	factor := 2.0
	ai := NewAdaptiveInterval(base, factor, 0, 0) // no cap, no jitter

	expected := base
	for k := 1; k <= 5; k++ {
		ai.Failure()
		expected = time.Duration(float64(expected) * factor)
		got := ai.Current()
		if got != expected {
			t.Errorf("after %d failures: Current()=%v, want %v", k, got, expected)
		}
	}
}

// TestGUC_Knuth_MaxCapEnforced verifies the data-structure invariant that the
// interval never exceeds max when max > 0, regardless of how many failures occur.
func TestGUC_Knuth_MaxCapEnforced(t *testing.T) {
	tests := []struct {
		base     time.Duration
		factor   float64
		max      time.Duration
		failures int
	}{
		{time.Second, 2.0, 8 * time.Second, 10},
		{time.Minute, 3.0, 5 * time.Minute, 8},
		{100 * time.Millisecond, 10.0, time.Second, 20},
	}
	for _, tc := range tests {
		ai := NewAdaptiveInterval(tc.base, tc.factor, tc.max, 0)
		for i := 0; i < tc.failures; i++ {
			ai.Failure()
			got := ai.Current()
			if got > tc.max {
				t.Errorf("base=%v factor=%.1f max=%v: after %d failures Current()=%v > max",
					tc.base, tc.factor, tc.max, i+1, got)
			}
		}
	}
}

// TestGUC_Knuth_SuccessiveFailuresApproachMax verifies that with a large
// factor the interval reaches the cap within a logarithmic number of failures
// (i.e., the algorithmic bound holds: ceil(log_factor(max/base)) steps).
func TestGUC_Knuth_SuccessiveFailuresApproachMax(t *testing.T) {
	base := time.Second
	max := 64 * time.Second
	factor := 2.0
	// log2(64) = 6 failures needed to reach max
	ai := NewAdaptiveInterval(base, factor, max, 0)
	for i := 0; i < 6; i++ {
		ai.Failure()
	}
	got := ai.Current()
	if got != max {
		t.Errorf("after 6 failures with factor=2, max=64s: Current()=%v, want %v", got, max)
	}
	// Additional failures must not exceed max.
	for i := 0; i < 10; i++ {
		ai.Failure()
		got = ai.Current()
		if got > max {
			t.Errorf("after extra failure %d: Current()=%v > max=%v", i+1, got, max)
		}
	}
}

// TestGUC_Knuth_IntervalNonDecreasing verifies the loop invariant that
// Current() is non-decreasing across successive Failure() calls (no
// backtracking before a Success() reset).
func TestGUC_Knuth_IntervalNonDecreasing(t *testing.T) {
	ai := NewAdaptiveInterval(time.Second, 1.5, 0, 0)
	prev := ai.Current()
	for i := 0; i < 15; i++ {
		ai.Failure()
		got := ai.Current()
		if got < prev {
			t.Errorf("step %d: Current()=%v < previous %v (non-decreasing violated)", i+1, got, prev)
		}
		prev = got
	}
}

// ---------------------------------------------------------------------------
// Turing lens: termination conditions, halting behavior
// ---------------------------------------------------------------------------

// TestGUC_Turing_TerminatesAtMaxCap verifies that growth halts (terminates)
// once the cap is reached: additional Failure() calls produce no change.
func TestGUC_Turing_TerminatesAtMaxCap(t *testing.T) {
	max := 4 * time.Second
	ai := NewAdaptiveInterval(time.Second, 2.0, max, 0)
	// Drive to cap.
	for i := 0; i < 20; i++ {
		ai.Failure()
	}
	first := ai.Current()
	if first != max {
		t.Fatalf("expected interval at cap %v, got %v", max, first)
	}
	// Further failures must not change the value — growth has halted.
	for i := 0; i < 5; i++ {
		ai.Failure()
		got := ai.Current()
		if got != max {
			t.Errorf("failure %d beyond cap: Current()=%v, want %v (halting violated)", i+1, got, max)
		}
	}
}

// TestGUC_Turing_ZeroBaseHandled verifies that a zero base interval does not
// panic or loop and returns zero from Next() and Current() without failure calls.
func TestGUC_Turing_ZeroBaseHandled(t *testing.T) {
	ai := NewAdaptiveInterval(0, 2.0, time.Minute, 0)
	if got := ai.Current(); got != 0 {
		t.Errorf("zero base: Current()=%v, want 0", got)
	}
	if got := ai.Next(); got != 0 {
		t.Errorf("zero base: Next()=%v, want 0", got)
	}
	// Failure on zero base stays zero (0 * factor == 0).
	ai.Failure()
	if got := ai.Current(); got != 0 {
		t.Errorf("zero base after Failure: Current()=%v, want 0", got)
	}
}

// TestGUC_Turing_ZeroMaxCapMeansUnlimited verifies that when max == 0 the
// interval grows without bound across many failures (i.e., growth does not
// halt prematurely).
func TestGUC_Turing_ZeroMaxCapMeansUnlimited(t *testing.T) {
	ai := NewAdaptiveInterval(time.Second, 2.0, 0, 0)
	for i := 0; i < 30; i++ {
		ai.Failure()
	}
	got := ai.Current()
	// After 30 doublings: base * 2^30 ≈ 1073741824s — far above any reasonable max.
	threshold := time.Duration(1<<29) * time.Second
	if got < threshold {
		t.Errorf("zero max: after 30 failures Current()=%v, want >= %v (unlimited growth)", got, threshold)
	}
}

// TestGUC_Turing_SuccessAfterFailuresResetsCleanly verifies that Success()
// terminates backoff growth and restores the base — the reset halting condition.
func TestGUC_Turing_SuccessAfterFailuresResetsCleanly(t *testing.T) {
	base := 5 * time.Second
	ai := NewAdaptiveInterval(base, 2.0, time.Minute, 0)
	for i := 0; i < 6; i++ {
		ai.Failure()
	}
	if ai.Current() <= base {
		t.Fatalf("pre-condition: expected interval > base after failures, got %v", ai.Current())
	}
	ai.Success()
	got := ai.Current()
	if got != base {
		t.Errorf("after Success(): Current()=%v, want base=%v", got, base)
	}
}

// TestGUC_Turing_NegativeFactorNormalized verifies that a negative factor is
// clamped to 1.0 so Failure() does not cause interval shrinkage or sign flip,
// and the algorithm terminates normally.
func TestGUC_Turing_NegativeFactorNormalized(t *testing.T) {
	base := time.Second
	ai := NewAdaptiveInterval(base, -5.0, 0, 0)
	prev := ai.Current()
	for i := 0; i < 5; i++ {
		ai.Failure()
		got := ai.Current()
		if got < prev {
			t.Errorf("negative factor not normalized: step %d Current()=%v < %v", i+1, got, prev)
		}
		// With factor clamped to 1.0 the interval must stay at base.
		if got != base {
			t.Errorf("negative factor not normalized to 1.0: Current()=%v, want %v", got, base)
		}
		prev = got
	}
}

// ---------------------------------------------------------------------------
// Church lens: functional purity, side-effect isolation, referential transparency
// ---------------------------------------------------------------------------

// TestGUC_Church_IntervalTypeIsDuration verifies referential transparency of
// the type contract: Next() and Current() always return time.Duration values.
func TestGUC_Church_IntervalTypeIsDuration(t *testing.T) {
	ai := NewAdaptiveInterval(time.Second, 2.0, time.Minute, 0)
	var _ time.Duration = ai.Next()
	var _ time.Duration = ai.Current()
	// If the code compiles and we reach here the type contract is satisfied.
	if ai.Next() == -1 {
		t.Error("unreachable sentinel: type assertion failed")
	}
}

// TestGUC_Church_JitterWithinBounds verifies that Next() with jitter set
// always returns a value in [current, current*(1+jitter)] — pure function
// output respects declared bounds.
func TestGUC_Church_JitterWithinBounds(t *testing.T) {
	base := time.Second
	jitter := 0.2
	ai := NewAdaptiveInterval(base, 1.0, 0, jitter)

	const samples = 500
	for i := 0; i < samples; i++ {
		got := ai.Next()
		lo := base
		hi := base + time.Duration(float64(base)*jitter)
		if got < lo || got > hi {
			t.Errorf("sample %d: Next()=%v outside [%v, %v]", i, got, lo, hi)
		}
	}
}

// TestGUC_Church_ResetOnSuccess verifies side-effect isolation: Success()
// always resets to base regardless of how many Failure() calls preceded it.
func TestGUC_Church_ResetOnSuccess(t *testing.T) {
	base := 10 * time.Second
	tests := []struct{ failures int }{{1}, {5}, {10}, {50}}
	for _, tc := range tests {
		ai := NewAdaptiveInterval(base, 2.0, 0, 0)
		for i := 0; i < tc.failures; i++ {
			ai.Failure()
		}
		ai.Success()
		got := ai.Current()
		if got != base {
			t.Errorf("after %d failures then Success: Current()=%v, want %v", tc.failures, got, base)
		}
	}
}

// TestGUC_Church_IndependentInstances verifies that two AdaptiveInterval
// instances do not share any mutable state: advancing one does not affect the other.
func TestGUC_Church_IndependentInstances(t *testing.T) {
	base := time.Second
	a := NewAdaptiveInterval(base, 2.0, 0, 0)
	b := NewAdaptiveInterval(base, 2.0, 0, 0)

	for i := 0; i < 5; i++ {
		a.Failure()
	}
	if b.Current() != base {
		t.Errorf("instance b.Current()=%v after mutating a, want %v (isolation violated)", b.Current(), base)
	}
}

// TestGUC_Church_SuccessIsIdempotent verifies referential transparency of
// Success(): calling it multiple times in a row always leaves the interval at base.
func TestGUC_Church_SuccessIsIdempotent(t *testing.T) {
	base := 7 * time.Second
	ai := NewAdaptiveInterval(base, 2.0, 0, 0)
	ai.Failure()
	ai.Failure()
	for i := 0; i < 5; i++ {
		ai.Success()
		got := ai.Current()
		if got != base {
			t.Errorf("Success() call %d: Current()=%v, want %v (idempotency violated)", i+1, got, base)
		}
	}
}

// ---------------------------------------------------------------------------
// Gödel lens: formal consistency, invariant preservation, impossible-state detection
// ---------------------------------------------------------------------------

// TestGUC_Godel_IntervalAlwaysPositive verifies the formal invariant that
// Next() never returns a negative duration — an impossible state to rule out.
func TestGUC_Godel_IntervalAlwaysPositive(t *testing.T) {
	cases := []struct {
		base   time.Duration
		factor float64
		max    time.Duration
		jitter float64
	}{
		{time.Second, 2.0, 0, 0},
		{0, 2.0, time.Minute, 0},
		{time.Second, 2.0, time.Minute, 0.5},
		{time.Millisecond, 1.0, 0, 1.0},
	}
	for _, c := range cases {
		ai := NewAdaptiveInterval(c.base, c.factor, c.max, c.jitter)
		for i := 0; i < 10; i++ {
			if n := ai.Next(); n < 0 {
				t.Errorf("base=%v factor=%.1f jitter=%.1f: Next()=%v < 0 (impossible state)",
					c.base, c.factor, c.jitter, n)
			}
			ai.Failure()
		}
	}
}

// TestGUC_Godel_FactorLessThanOneNormalized verifies formal consistency:
// a factor in (0,1) is clamped to 1.0, so Failure() can never decrease the
// interval below base (a contradiction in the backoff contract).
func TestGUC_Godel_FactorLessThanOneNormalized(t *testing.T) {
	tests := []struct{ factor float64 }{{0.1}, {0.5}, {0.9999}, {0.0}}
	base := 10 * time.Second
	for _, tc := range tests {
		ai := NewAdaptiveInterval(base, tc.factor, 0, 0)
		ai.Failure()
		got := ai.Current()
		if got < base {
			t.Errorf("factor=%.4f: after Failure Current()=%v < base=%v (factor<1 must be clamped)",
				tc.factor, got, base)
		}
	}
}

// TestGUC_Godel_ConcurrentNextSafe verifies formal consistency under concurrent
// access: many goroutines calling Next(), Failure(), and Success() simultaneously
// must not produce a data race or impossible (negative) value. Run with -race.
func TestGUC_Godel_ConcurrentNextSafe(t *testing.T) {
	const goroutines = 8
	const iters = 200
	ai := NewAdaptiveInterval(time.Second, 2.0, 10*time.Second, 0.1)

	var wg sync.WaitGroup
	var negCount atomic.Int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		g := g
		go func() {
			defer wg.Done()
			for k := 0; k < iters; k++ {
				switch (g + k) % 3 {
				case 0:
					if n := ai.Next(); n < 0 {
						negCount.Add(1)
					}
				case 1:
					ai.Failure()
				case 2:
					ai.Success()
				}
			}
		}()
	}
	wg.Wait()
	if negCount.Load() > 0 {
		t.Errorf("%d concurrent Next() calls returned negative duration (impossible state)",
			negCount.Load())
	}
}

// TestGUC_Godel_MaxCapConsistency verifies that once the max cap is reached
// the system stays in a formally consistent state: interval == max after any
// number of subsequent Failure() calls (no overflow or exceeding of max).
func TestGUC_Godel_MaxCapConsistency(t *testing.T) {
	max := 16 * time.Second
	ai := NewAdaptiveInterval(time.Second, 2.0, max, 0)
	// Saturate.
	for i := 0; i < 50; i++ {
		ai.Failure()
	}
	if ai.Current() != max {
		t.Fatalf("pre-condition: interval not at max after 50 failures, got %v", ai.Current())
	}
	// Consistency check: 20 more failures must not change the value.
	for i := 0; i < 20; i++ {
		ai.Failure()
		got := ai.Current()
		if got != max {
			t.Errorf("failure %d past cap: Current()=%v, want %v (max consistency violated)", i+1, got, max)
		}
	}
}

// TestGUC_Godel_CurrentBeforeFirstNext verifies the formal invariant that
// Current() returns base before any Next() call, ruling out the impossible
// state where the interval is initialized to something other than base.
func TestGUC_Godel_CurrentBeforeFirstNext(t *testing.T) {
	tests := []struct {
		base   time.Duration
		factor float64
		max    time.Duration
	}{
		{time.Second, 2.0, 10 * time.Second},
		{500 * time.Millisecond, 3.0, time.Minute},
		{0, 1.0, 0},
	}
	for _, tc := range tests {
		ai := NewAdaptiveInterval(tc.base, tc.factor, tc.max, 0)
		got := ai.Current()
		if got != tc.base {
			t.Errorf("base=%v: Current() before Next()=%v, want base (invariant violated)", tc.base, got)
		}
	}
}
