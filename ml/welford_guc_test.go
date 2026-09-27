package ml

import (
	"math"
	"sync"
	"testing"
)

// --- Knuth: algorithmic correctness, loop invariants, data structure invariants ---

func TestGUC_Knuth_CountAccurate(t *testing.T) {
	tests := []struct {
		n    int
		want int64
	}{
		{0, 0},
		{1, 1},
		{10, 10},
		{1000, 1000},
	}
	for _, tc := range tests {
		w := &WelfordAccum{}
		for i := 0; i < tc.n; i++ {
			w.Update(float64(i))
		}
		if got := w.Count(); got != tc.want {
			t.Errorf("Count after %d updates: got %d, want %d", tc.n, got, tc.want)
		}
	}
}

func TestGUC_Knuth_MeanExactForUniform(t *testing.T) {
	tests := []struct {
		vals []float64
		want float64
	}{
		{[]float64{1, 2, 3, 4, 5}, 3.0},
		{[]float64{10, 10, 10, 10}, 10.0},
		{[]float64{-5, 5}, 0.0},
		{[]float64{0, 0, 0}, 0.0},
	}
	for _, tc := range tests {
		w := &WelfordAccum{}
		for _, v := range tc.vals {
			w.Update(v)
		}
		got := w.Mean()
		if math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("Mean(%v): got %v, want %v", tc.vals, got, tc.want)
		}
	}
}

func TestGUC_Knuth_VarianceMatchesFormula(t *testing.T) {
	// Bessel-corrected sample variance: sum((x - mean)^2) / (n-1)
	vals := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	w := &WelfordAccum{}
	for _, v := range vals {
		w.Update(v)
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	var sumSq float64
	for _, v := range vals {
		d := v - mean
		sumSq += d * d
	}
	expected := sumSq / float64(len(vals)-1)
	got := w.Variance()
	if math.Abs(got-expected) > 1e-10 {
		t.Errorf("Variance: got %v, want %v", got, expected)
	}
}

func TestGUC_Knuth_NaiveVarianceComparison(t *testing.T) {
	// Compare Welford variance with naive two-pass sample variance for N=100.
	const n = 100
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = float64(i*i) - float64(i)*3.7 + 42.1
	}
	w := &WelfordAccum{}
	for _, v := range vals {
		w.Update(v)
	}
	// naive two-pass
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / n
	var sumSq float64
	for _, v := range vals {
		d := v - mean
		sumSq += d * d
	}
	naiveVar := sumSq / (n - 1)
	got := w.Variance()
	relErr := math.Abs(got-naiveVar) / naiveVar
	if relErr > 1e-10 {
		t.Errorf("Variance mismatch: Welford=%v naive=%v relErr=%v", got, naiveVar, relErr)
	}
}

func TestGUC_Knuth_NumericalStabilityNearlyEqual(t *testing.T) {
	// All observations near a large base; Welford must not lose precision
	// tracking deviations relative to the running mean.
	base := 1e15
	deltas := []float64{1, 2, 3, 4, 5}
	w := &WelfordAccum{}
	for _, d := range deltas {
		w.Update(base + d)
	}
	// Sample variance of [1,2,3,4,5] = 2.5
	expectedVar := 2.5
	got := w.Variance()
	if math.Abs(got-expectedVar) > 1e-6 {
		t.Errorf("Variance with near-equal values: got %v, want %v", got, expectedVar)
	}
}

// --- Turing: termination conditions, halting behavior, decidability ---

func TestGUC_Turing_N1VarianceZero(t *testing.T) {
	w := &WelfordAccum{}
	w.Update(42.0)
	if got := w.Variance(); got != 0 {
		t.Errorf("Variance after 1 observation: got %v, want 0", got)
	}
}

func TestGUC_Turing_TwoEqualObservationsVarianceZero(t *testing.T) {
	w := &WelfordAccum{}
	w.Update(7.0)
	w.Update(7.0)
	if got := w.Variance(); got != 0 {
		t.Errorf("Variance for two identical observations: got %v, want 0", got)
	}
}

func TestGUC_Turing_MeanAfterN0IsZero(t *testing.T) {
	w := &WelfordAccum{}
	got := w.Mean()
	if got != 0 {
		t.Errorf("Mean after 0 observations: got %v, want 0", got)
	}
}

func TestGUC_Turing_VarianceAfterN1IsZero(t *testing.T) {
	cases := []float64{0, 1, -1, 1e100, -1e100}
	for _, v := range cases {
		w := &WelfordAccum{}
		w.Update(v)
		if got := w.Variance(); got != 0 {
			t.Errorf("Variance after N=1 (v=%v): got %v, want 0", v, got)
		}
	}
}

func TestGUC_Turing_LargeNNoOverflow(t *testing.T) {
	// N=10^6 updates must not produce inf or NaN.
	const n = 1_000_000
	w := &WelfordAccum{}
	for i := 0; i < n; i++ {
		w.Update(float64(i))
	}
	if w.Count() != n {
		t.Fatalf("Count: got %d, want %d", w.Count(), n)
	}
	// Mean of 0..n-1 = (n-1)/2
	expectedMean := float64(n-1) / 2.0
	if math.Abs(w.Mean()-expectedMean)/expectedMean > 1e-9 {
		t.Errorf("Mean after %d obs: got %v, want ~%v", n, w.Mean(), expectedMean)
	}
	if math.IsInf(w.Variance(), 0) || math.IsNaN(w.Variance()) {
		t.Errorf("Variance after %d obs is inf or NaN: %v", n, w.Variance())
	}
	if math.IsInf(w.StdDev(), 0) || math.IsNaN(w.StdDev()) {
		t.Errorf("StdDev after %d obs is inf or NaN: %v", n, w.StdDev())
	}
}

// --- Church: functional purity, side-effect isolation, referential transparency ---

func TestGUC_Church_StdDevSqrtVariance(t *testing.T) {
	// StdDev must equal sqrt(Variance) exactly.
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	w := &WelfordAccum{}
	for _, v := range vals {
		w.Update(v)
	}
	variance := w.Variance()
	stddev := w.StdDev()
	expected := math.Sqrt(variance)
	if math.Abs(stddev-expected) > 1e-14 {
		t.Errorf("StdDev=%v != sqrt(Variance=%v)=%v", stddev, variance, expected)
	}
}

func TestGUC_Church_UpdateZeroNoEffect(t *testing.T) {
	// Update(0) is a valid observation; it must increment Count and shift Mean.
	w := &WelfordAccum{}
	for i := 1; i <= 4; i++ {
		w.Update(float64(i))
	}
	prevMean := w.Mean()
	prevCount := w.Count()
	w.Update(0)
	if w.Count() != prevCount+1 {
		t.Errorf("Count after Update(0): got %d, want %d", w.Count(), prevCount+1)
	}
	// Mean of [1,2,3,4] is 2.5; adding 0 gives mean of [0,1,2,3,4]=2.0, which is less.
	if w.Mean() >= prevMean {
		t.Errorf("Mean after Update(0): got %v, expected < %v", w.Mean(), prevMean)
	}
}

func TestGUC_Church_NegativeValues(t *testing.T) {
	tests := []struct {
		vals []float64
		mean float64
	}{
		{[]float64{-1, -2, -3, -4, -5}, -3.0},
		{[]float64{-10, 0, 10}, 0.0},
		{[]float64{-100, -200, -300}, -200.0},
	}
	for _, tc := range tests {
		w := &WelfordAccum{}
		for _, v := range tc.vals {
			w.Update(v)
		}
		got := w.Mean()
		if math.Abs(got-tc.mean) > 1e-12 {
			t.Errorf("Mean(%v): got %v, want %v", tc.vals, got, tc.mean)
		}
	}
}

func TestGUC_Church_ResetClearsState(t *testing.T) {
	w := &WelfordAccum{}
	for i := 0; i < 100; i++ {
		w.Update(float64(i))
	}
	w.Reset()
	if got := w.Count(); got != 0 {
		t.Errorf("Count after Reset: got %d, want 0", got)
	}
	if got := w.Mean(); got != 0 {
		t.Errorf("Mean after Reset: got %v, want 0", got)
	}
	if got := w.Variance(); got != 0 {
		t.Errorf("Variance after Reset: got %v, want 0", got)
	}
	if got := w.StdDev(); got != 0 {
		t.Errorf("StdDev after Reset: got %v, want 0", got)
	}
}

func TestGUC_Church_IndependentInstances(t *testing.T) {
	// Two WelfordAccum instances must not share any state.
	a := &WelfordAccum{}
	b := &WelfordAccum{}
	for i := 0; i < 10; i++ {
		a.Update(float64(i))
	}
	if b.Count() != 0 {
		t.Errorf("b.Count affected by updates to a: got %d, want 0", b.Count())
	}
	if b.Mean() != 0 {
		t.Errorf("b.Mean affected by updates to a: got %v, want 0", b.Mean())
	}
	if b.Variance() != 0 {
		t.Errorf("b.Variance affected by updates to a: got %v, want 0", b.Variance())
	}
}

// --- Gödel: formal consistency, invariant preservation, impossible-state detection ---

func TestGUC_Godel_ConcurrentUpdateSafe(t *testing.T) {
	// WelfordAccum is not goroutine-safe; protect with a mutex and verify
	// that under -race the result is still correct.
	var mu sync.Mutex
	w := &WelfordAccum{}
	const goroutines = 10
	const perGoroutine = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				mu.Lock()
				w.Update(1.0)
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	want := int64(goroutines * perGoroutine)
	if w.Count() != want {
		t.Errorf("Count after concurrent updates: got %d, want %d", w.Count(), want)
	}
	// All observations are 1.0, so mean must equal 1.0.
	if math.Abs(w.Mean()-1.0) > 1e-12 {
		t.Errorf("Mean after concurrent updates: got %v, want 1.0", w.Mean())
	}
}

func TestGUC_Godel_VarianceNonNegative(t *testing.T) {
	// Variance must never be negative for any sequence of inputs.
	sequences := [][]float64{
		{1},
		{1, 2},
		{1, 1, 1},
		{0, 0, 0, 0},
		{-3, -1, 0, 1, 3},
		{1e100, 2e100, 3e100},
		{-1e100, 0, 1e100},
	}
	for _, seq := range sequences {
		w := &WelfordAccum{}
		for _, v := range seq {
			w.Update(v)
		}
		if got := w.Variance(); got < 0 {
			t.Errorf("Variance(%v): got negative value %v", seq, got)
		}
	}
}

func TestGUC_Godel_StdDevNonNegative(t *testing.T) {
	// StdDev must never be negative.
	sequences := [][]float64{
		{1, 2, 3},
		{-5, -4, -3},
		{0, 0, 0},
		{1e-300, 2e-300},
		{1, 1},
	}
	for _, seq := range sequences {
		w := &WelfordAccum{}
		for _, v := range seq {
			w.Update(v)
		}
		if got := w.StdDev(); got < 0 {
			t.Errorf("StdDev(%v): got negative value %v", seq, got)
		}
	}
}

func TestGUC_Godel_MeanBoundedByObservations(t *testing.T) {
	// Mean must lie within [min, max] of all observations.
	tests := [][]float64{
		{1, 2, 3, 4, 5},
		{-10, 5, 3, -2},
		{100, 100, 100},
		{-1, 0, 1},
	}
	for _, vals := range tests {
		w := &WelfordAccum{}
		minV, maxV := vals[0], vals[0]
		for _, v := range vals {
			w.Update(v)
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
		got := w.Mean()
		if got < minV-1e-12 || got > maxV+1e-12 {
			t.Errorf("Mean(%v)=%v out of bounds [%v, %v]", vals, got, minV, maxV)
		}
	}
}

func TestGUC_Godel_ZScoreConsistency(t *testing.T) {
	// When all observations are identical, ZScore of that value must be 0.
	w := &WelfordAccum{}
	for i := 0; i < 5; i++ {
		w.Update(3.0)
	}
	if got := w.ZScore(3.0); got != 0 {
		t.Errorf("ZScore of constant observation: got %v, want 0", got)
	}

	// ZScore of the mean must be ~0 for any distribution.
	w2 := &WelfordAccum{}
	for _, v := range []float64{1, 2, 3, 4, 5} {
		w2.Update(v)
	}
	zOfMean := w2.ZScore(w2.Mean())
	if math.Abs(zOfMean) > 1e-12 {
		t.Errorf("ZScore(mean): got %v, want ~0", zOfMean)
	}
}
