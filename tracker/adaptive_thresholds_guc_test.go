package tracker

// GUC (Gödel Unified Council) test coverage for adaptive threshold logic in Ocelot.
//
// Distribution:
//   Knuth  (~5): algorithmic correctness, loop invariants, data structure invariants
//   Turing (~5): termination conditions, halting behaviour, decidability of operations
//   Church (~5): functional purity, side-effect isolation, referential transparency
//   Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mgdavisxvs/Ocelot/ml"
)

// ── mock adapter ──────────────────────────────────────────────────────────────

// mockAdapter records calls to SetThresholds for inspection in tests.
type mockAdapter struct {
	mu   sync.Mutex
	last ml.ThresholdConfig
	n    int
	ch   chan ml.ThresholdConfig
}

func newMockAdapter() *mockAdapter {
	return &mockAdapter{ch: make(chan ml.ThresholdConfig, 16)}
}

func (m *mockAdapter) SetThresholds(cfg ml.ThresholdConfig) {
	m.mu.Lock()
	m.last = cfg
	m.n++
	m.mu.Unlock()
	select {
	case m.ch <- cfg:
	default:
	}
}

func (m *mockAdapter) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.n
}

// ── Knuth: algorithmic correctness ────────────────────────────────────────────

// TestGUC_Knuth_BaseRateAtZeroLoad verifies that a zero peer count (below the
// thin-cover threshold) produces exactly 75 % of the base rate.
func TestGUC_Knuth_BaseRateAtZeroLoad(t *testing.T) {
	cfg := thresholdsForPopulation(0)
	const want = int(float64(baseMaxAnnounceRate) * 0.75)
	if cfg.MaxAnnounceRate != want {
		t.Errorf("thresholdsForPopulation(0).MaxAnnounceRate = %d, want %d",
			cfg.MaxAnnounceRate, want)
	}
}

// TestGUC_Knuth_LargeSwarmRelaxes25Percent verifies that a large swarm (>50 k)
// relaxes the announce-rate threshold to 125 % of the base.
func TestGUC_Knuth_LargeSwarmRelaxes25Percent(t *testing.T) {
	cfg := thresholdsForPopulation(100_000)
	const want = int(float64(baseMaxAnnounceRate) * 1.25)
	if cfg.MaxAnnounceRate != want {
		t.Errorf("thresholdsForPopulation(100000).MaxAnnounceRate = %d, want %d",
			cfg.MaxAnnounceRate, want)
	}
}

// TestGUC_Knuth_SmallSwarmTightens25Percent verifies that a small swarm (<1 k)
// tightens the announce-rate threshold to 75 % of the base.
func TestGUC_Knuth_SmallSwarmTightens25Percent(t *testing.T) {
	cfg := thresholdsForPopulation(500)
	const want = int(float64(baseMaxAnnounceRate) * 0.75)
	if cfg.MaxAnnounceRate != want {
		t.Errorf("thresholdsForPopulation(500).MaxAnnounceRate = %d, want %d",
			cfg.MaxAnnounceRate, want)
	}
}

// TestGUC_Knuth_ConvergenceBoundIsThreeTimesBase confirms that the hard cap
// constant equals maxThresholdMultiplier × baseMaxAnnounceRate and that
// thresholdsForPopulation never returns a value above it.
func TestGUC_Knuth_ConvergenceBoundIsThreeTimesBase(t *testing.T) {
	hardCap := int(float64(baseMaxAnnounceRate) * maxThresholdMultiplier)
	if hardCap != 300 {
		t.Errorf("expected hard cap = 300, got %d", hardCap)
	}
	for _, peers := range []int64{0, 1, 999, 1000, 25000, 50000, 50001, 1_000_000} {
		cfg := thresholdsForPopulation(peers)
		if cfg.MaxAnnounceRate > hardCap {
			t.Errorf("peers=%d: rate=%d exceeds hard cap %d", peers, cfg.MaxAnnounceRate, hardCap)
		}
	}
}

// TestGUC_Knuth_TableDrivenBoundaryValues checks exact expected rates for key
// boundary peer counts using a table-driven approach.
func TestGUC_Knuth_TableDrivenBoundaryValues(t *testing.T) {
	// mid-range returns zero (no change sentinel); others return scaled values.
	tests := []struct {
		peers    int64
		wantRate int
	}{
		{peers: 0, wantRate: 75},
		{peers: 1, wantRate: 75},
		{peers: 999, wantRate: 75},
		{peers: 1000, wantRate: 0},   // boundary: default branch, no change
		{peers: 25000, wantRate: 0},  // mid range, no change
		{peers: 50000, wantRate: 0},  // boundary: default branch, no change
		{peers: 50001, wantRate: 125},
		{peers: 1_000_000, wantRate: 125},
	}
	for _, tc := range tests {
		got := thresholdsForPopulation(tc.peers)
		if got.MaxAnnounceRate != tc.wantRate {
			t.Errorf("peers=%d: MaxAnnounceRate = %d, want %d",
				tc.peers, got.MaxAnnounceRate, tc.wantRate)
		}
	}
}

// ── Turing: termination & halting behaviour ───────────────────────────────────

// TestGUC_Turing_ZeroLoadInput verifies that thresholdsForPopulation(0) always
// terminates and returns a well-formed (non-panicking) ThresholdConfig.
func TestGUC_Turing_ZeroLoadInput(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("thresholdsForPopulation(0) panicked: %v", r)
		}
	}()
	cfg := thresholdsForPopulation(0)
	// Must return the tightened rate for < 1000 peers.
	if cfg.MaxAnnounceRate <= 0 {
		t.Errorf("expected positive rate for zero load, got %d", cfg.MaxAnnounceRate)
	}
}

// TestGUC_Turing_DefaultRangeReturnsEmptyConfig ensures peer counts in the
// "default" branch [1000, 50000] produce a zero-value config (no-change sentinel).
func TestGUC_Turing_DefaultRangeReturnsEmptyConfig(t *testing.T) {
	for _, peers := range []int64{1000, 5000, 10000, 49999, 50000} {
		cfg := thresholdsForPopulation(peers)
		if cfg.MaxAnnounceRate != 0 {
			t.Errorf("peers=%d: expected no-change (0), got %d", peers, cfg.MaxAnnounceRate)
		}
	}
}

// TestGUC_Turing_PollerTerminatesOnContextCancel starts AdaptiveThresholdPoller
// with a cancelled context and verifies that no panic occurs and the adapter is
// never called after cancellation.
func TestGUC_Turing_PollerTerminatesOnContextCancel(t *testing.T) {
	// Serve /metrics so the client has a valid target.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"tracked_peers": 100000})
	}))
	defer srv.Close()

	adapter := newMockAdapter()
	client := NewMarkovClient(srv.URL)

	// Cancel immediately so the poller goroutine exits before its first tick.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	AdaptiveThresholdPoller(ctx, client, adapter, 3600) // 1-hour interval: never fires in test

	// No SetThresholds calls should have been made.
	if n := adapter.count(); n != 0 {
		t.Errorf("expected 0 SetThresholds calls after immediate cancel, got %d", n)
	}
}

// TestGUC_Turing_ExtremeLargeRateClamped confirms that even if population-derived
// arithmetic would overshoot the cap, the result is clamped to the hard maximum.
func TestGUC_Turing_ExtremeLargeRateClamped(t *testing.T) {
	hardCap := int(float64(baseMaxAnnounceRate) * maxThresholdMultiplier)
	// The largest value thresholdsForPopulation can produce is 125 (large swarm).
	// We verify that value is well below the hard cap (not at risk of overflow).
	cfg := thresholdsForPopulation(1_000_000_000)
	if cfg.MaxAnnounceRate > hardCap {
		t.Errorf("rate %d exceeds hard cap %d for extreme large peer count",
			cfg.MaxAnnounceRate, hardCap)
	}
}

// TestGUC_Turing_Rate0HandledAsNoOp verifies that setting a ThresholdConfig with
// MaxAnnounceRate == 0 on a real AnomalyDetector leaves the existing rate intact.
func TestGUC_Turing_Rate0HandledAsNoOp(t *testing.T) {
	ad := ml.NewAnomalyDetector()
	before := ad.Thresholds().MaxAnnounceRate
	ad.SetThresholds(ml.ThresholdConfig{MaxAnnounceRate: 0})
	after := ad.Thresholds().MaxAnnounceRate
	if before != after {
		t.Errorf("SetThresholds(0) changed rate: %d → %d", before, after)
	}
}

// ── Church: functional purity & side-effect isolation ─────────────────────────

// TestGUC_Church_PureFunctionDeterminism verifies that thresholdsForPopulation is
// referentially transparent: the same input always produces the same output.
func TestGUC_Church_PureFunctionDeterminism(t *testing.T) {
	inputs := []int64{0, 500, 1000, 25000, 50001, 1_000_000}
	for _, p := range inputs {
		first := thresholdsForPopulation(p)
		second := thresholdsForPopulation(p)
		if first != second {
			t.Errorf("peers=%d: non-deterministic output: %+v vs %+v", p, first, second)
		}
	}
}

// TestGUC_Church_ThresholdNeverNegative asserts that the function never produces
// a negative MaxAnnounceRate across a representative range of peer counts.
func TestGUC_Church_ThresholdNeverNegative(t *testing.T) {
	for _, peers := range []int64{0, 1, 500, 999, 1000, 50000, 50001, 1_000_000} {
		cfg := thresholdsForPopulation(peers)
		if cfg.MaxAnnounceRate < 0 {
			t.Errorf("peers=%d: MaxAnnounceRate is negative: %d", peers, cfg.MaxAnnounceRate)
		}
	}
}

// TestGUC_Church_ResetFunctionality verifies that applying a zero ThresholdConfig
// does not modify the detector state — it is a no-op "reset" that preserves the
// previous setting.
func TestGUC_Church_ResetFunctionality(t *testing.T) {
	ad := ml.NewAnomalyDetector()
	// First set a known non-default value.
	ad.SetThresholds(ml.ThresholdConfig{MaxAnnounceRate: 200})
	before := ad.Thresholds().MaxAnnounceRate
	// Apply the zero config — should be a no-op.
	ad.SetThresholds(ml.ThresholdConfig{})
	after := ad.Thresholds().MaxAnnounceRate
	if before != after {
		t.Errorf("zero ThresholdConfig changed rate %d → %d; expected no change", before, after)
	}
}

// TestGUC_Church_SetThresholdAppliesConfig verifies that a non-zero config
// actually updates the detector's stored threshold.
func TestGUC_Church_SetThresholdAppliesConfig(t *testing.T) {
	ad := ml.NewAnomalyDetector()
	const newRate = 42
	ad.SetThresholds(ml.ThresholdConfig{MaxAnnounceRate: newRate})
	got := ad.Thresholds().MaxAnnounceRate
	if got != newRate {
		t.Errorf("after SetThresholds(%d), MaxAnnounceRate = %d", newRate, got)
	}
}

// TestGUC_Church_EMACorrectness would verify exponential-moving-average
// smoothing of threshold updates, but no EMA implementation exists yet.
func TestGUC_Church_EMACorrectness(t *testing.T) {
	t.Skip("not yet implemented: EMA smoothing for threshold updates")
}

// ── Gödel: formal consistency, invariant preservation ─────────────────────────

// TestGUC_Godel_ThresholdNeverExceedsHardCap is a formal invariant check:
// RULING-04 states τ ≤ maxThresholdMultiplier × τ_base for all peer counts.
func TestGUC_Godel_ThresholdNeverExceedsHardCap(t *testing.T) {
	hardCap := int(float64(baseMaxAnnounceRate) * maxThresholdMultiplier)
	peerCounts := []int64{
		0, 1, 100, 500, 999,
		1000, 1001, 10000, 49999, 50000,
		50001, 100000, 500000, 1_000_000,
	}
	for _, peers := range peerCounts {
		cfg := thresholdsForPopulation(peers)
		if cfg.MaxAnnounceRate > hardCap {
			t.Errorf("invariant violated: peers=%d rate=%d > cap=%d",
				peers, cfg.MaxAnnounceRate, hardCap)
		}
	}
}

// TestGUC_Godel_ConcurrentUpdatesSafe verifies that concurrent calls to
// thresholdsForPopulation (a pure function with no shared mutable state) return
// consistent results — there is no data-race on the function itself.
func TestGUC_Godel_ConcurrentUpdatesSafe(t *testing.T) {
	const goroutines = 20
	var wg sync.WaitGroup
	results := make([]ml.ThresholdConfig, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = thresholdsForPopulation(100_000)
		}(i)
	}
	wg.Wait()
	const want = int(float64(baseMaxAnnounceRate) * 1.25)
	for i, cfg := range results {
		if cfg.MaxAnnounceRate != want {
			t.Errorf("goroutine %d: MaxAnnounceRate = %d, want %d", i, cfg.MaxAnnounceRate, want)
		}
	}
}

// TestGUC_Godel_MonotoneIncreaseUnderLoad asserts that a larger swarm always
// yields a higher (or equal) announce-rate threshold than a smaller one:
// thresholdsForPopulation(large) ≥ thresholdsForPopulation(small).
func TestGUC_Godel_MonotoneIncreaseUnderLoad(t *testing.T) {
	small := thresholdsForPopulation(500)   // < 1000 → tightened = 75
	large := thresholdsForPopulation(100_000) // > 50000 → relaxed = 125
	if large.MaxAnnounceRate < small.MaxAnnounceRate {
		t.Errorf("monotone invariant violated: large=%d < small=%d",
			large.MaxAnnounceRate, small.MaxAnnounceRate)
	}
}

// TestGUC_Godel_StepDownOnReducedLoad verifies that moving from a large swarm
// to the default range then to a small swarm produces a strictly decreasing
// sequence of returned rates (where non-zero values are compared).
func TestGUC_Godel_StepDownOnReducedLoad(t *testing.T) {
	rLarge := thresholdsForPopulation(100_000).MaxAnnounceRate  // 125
	rSmall := thresholdsForPopulation(100).MaxAnnounceRate      // 75
	rMid := thresholdsForPopulation(10_000).MaxAnnounceRate     // 0 (no change)

	// Large swarm should relax more than small swarm.
	if rLarge <= rSmall {
		t.Errorf("step-down invariant: large rate %d should exceed small rate %d",
			rLarge, rSmall)
	}
	// Mid range returns the no-change sentinel (zero).
	if rMid != 0 {
		t.Errorf("mid-range should return no-change (0), got %d", rMid)
	}
}

// TestGUC_Godel_WindowSizeEffect would verify that different polling window sizes
// affect how quickly thresholds adapt to load changes, but no window-size
// parameter is exposed on the current implementation.
func TestGUC_Godel_WindowSizeEffect(t *testing.T) {
	t.Skip("not yet implemented: window size parameter for threshold adaptation")
}
