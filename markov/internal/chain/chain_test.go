package chain

import (
	"math"
	"testing"
)

// ---- Req 3: Configurable smoothing α ----

func TestNewWithSmoothing(t *testing.T) {
	// α=2.0 prior: each cell starts at 2.0
	c := NewWithSmoothing(3, 1.0, 2.0)
	counts := c.Counts()
	for i := range counts {
		for j := range counts[i] {
			if math.Abs(counts[i][j]-2.0) > 1e-9 {
				t.Errorf("counts[%d][%d] = %v, want 2.0", i, j, counts[i][j])
			}
		}
	}
	if c.Smoothing() != 2.0 {
		t.Errorf("Smoothing() = %v, want 2.0", c.Smoothing())
	}
}

func TestSmoothedNormalization(t *testing.T) {
	// With α=0.5 and 10 obs 0→1, row 0 counts = [0.5, 10.5, 0.5], sum=11.5
	c := NewWithSmoothing(3, 1.0, 0.5)
	for range 10 {
		c.Observe(0, 1)
	}
	p := c.P()
	wantP01 := 10.5 / 11.5
	if math.Abs(p[0][1]-wantP01) > 1e-9 {
		t.Errorf("P[0][1] = %v, want %v", p[0][1], wantP01)
	}
	// Each row must still sum to 1.
	for i, row := range p {
		var sum float64
		for _, v := range row {
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("row %d sums to %v, want 1.0", i, sum)
		}
	}
}

// ---- Req 3: Effective sample count ----

func TestEffectiveSampleCount(t *testing.T) {
	// α=1.0, n=3: prior = 3×1=3. Add 5 observations to state 0.
	// eff = (1 + 5 + 1 + 1) - 3 = 5
	c := NewWithSmoothing(3, 1.0, 1.0)
	for range 5 {
		c.Observe(0, 1)
	}
	eff := c.EffectiveSampleCount(0)
	if math.Abs(eff-5.0) > 1e-9 {
		t.Errorf("EffectiveSampleCount(0) = %v, want 5.0", eff)
	}
	// Unseen state: eff = 0 - 0 = 0 (all prior, no real obs)
	eff2 := c.EffectiveSampleCount(2)
	if math.Abs(eff2) > 1e-9 {
		t.Errorf("EffectiveSampleCount(2) = %v, want 0.0 (only prior)", eff2)
	}
}

func TestEffectiveSampleCounts(t *testing.T) {
	c := NewWithSmoothing(2, 1.0, 1.0)
	c.Observe(0, 1)
	c.Observe(0, 1)
	// State 0: 1+2+1=4, prior=2, eff=2
	// State 1: 1+1=2, prior=2, eff=0
	escs := c.EffectiveSampleCounts()
	if len(escs) != 2 {
		t.Fatalf("EffectiveSampleCounts len = %d, want 2", len(escs))
	}
	if math.Abs(escs[0]-2.0) > 1e-9 {
		t.Errorf("escs[0] = %v, want 2.0", escs[0])
	}
	if math.Abs(escs[1]) > 1e-9 {
		t.Errorf("escs[1] = %v, want 0.0", escs[1])
	}
}

// ---- Req 4: Multi-horizon Forecast ----

func TestForecast(t *testing.T) {
	// Absorbing chain: state 1 absorbs. With many obs 0→1 and 1→1,
	// forecasting far ahead should concentrate mass at state 1.
	c := NewWithSmoothing(2, 1.0, 1.0)
	for range 1000 {
		c.Observe(0, 1)
		c.Observe(1, 1)
	}
	pi := []float64{1.0, 0.0}
	horizons := []int{1, 10, 100}
	forecasts := c.Forecast(pi, horizons)
	if len(forecasts) != 3 {
		t.Fatalf("Forecast returned %d slices, want 3", len(forecasts))
	}
	// All horizons should have probability sum = 1.
	for h, f := range forecasts {
		var sum float64
		for _, v := range f {
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("Forecast horizon %d: sum = %v, want 1.0", horizons[h], sum)
		}
	}
	// At horizon 100 steps, state 1 should dominate.
	if forecasts[2][1] < 0.99 {
		t.Errorf("Forecast(100 steps)[state 1] = %v, want > 0.99", forecasts[2][1])
	}
	// Horizon 1 should have less or equal mass at state 1 than horizon 10.
	// (With a strongly absorbing chain they may be numerically equal.)
	if forecasts[0][1] > forecasts[1][1]+1e-9 {
		t.Errorf("1-step forecast[1] %v should be ≤ 10-step %v", forecasts[0][1], forecasts[1][1])
	}
}

func TestForecastUnsortedHorizons(t *testing.T) {
	c := NewWithSmoothing(2, 1.0, 1.0)
	for range 100 {
		c.Observe(0, 1)
		c.Observe(1, 1)
	}
	pi := []float64{0.5, 0.5}
	// Pass horizons out of order: [100, 1, 10]
	forecasts := c.Forecast(pi, []int{100, 1, 10})
	if len(forecasts) != 3 {
		t.Fatalf("len = %d, want 3", len(forecasts))
	}
	// Index 1 is horizon 1, index 2 is horizon 10, index 0 is horizon 100.
	// horizon 1 < horizon 10 < horizon 100 in terms of state-1 probability.
	if forecasts[1][1] >= forecasts[2][1] {
		t.Errorf("1-step[1]=%v should be < 10-step[1]=%v", forecasts[1][1], forecasts[2][1])
	}
}

func TestForecastEmpty(t *testing.T) {
	c := New(3, 1.0)
	pi := []float64{1.0, 0.0, 0.0}
	f := c.Forecast(pi, nil)
	if f != nil {
		t.Errorf("Forecast with nil horizons should return nil, got %v", f)
	}
}

// ---- Req 4: Entropy ----

func TestEntropy(t *testing.T) {
	// Uniform distribution over 4 states has entropy = log2(4) = 2.0 bits.
	pi := []float64{0.25, 0.25, 0.25, 0.25}
	h := Entropy(pi)
	if math.Abs(h-2.0) > 1e-9 {
		t.Errorf("entropy = %v, want 2.0", h)
	}

	// Point mass has entropy = 0.
	pi2 := []float64{1.0, 0.0, 0.0, 0.0}
	h2 := Entropy(pi2)
	if math.Abs(h2) > 1e-9 {
		t.Errorf("entropy = %v, want 0.0", h2)
	}
}

func TestMaxEntropy(t *testing.T) {
	if math.Abs(MaxEntropy(4)-2.0) > 1e-9 {
		t.Errorf("MaxEntropy(4) = %v, want 2.0", MaxEntropy(4))
	}
	if MaxEntropy(1) != 0 {
		t.Errorf("MaxEntropy(1) = %v, want 0", MaxEntropy(1))
	}
}

// ---- Req 6: NLL normalization ----

func TestPathLogLikelihood(t *testing.T) {
	c := New(3, 1.0)
	for range 10 {
		c.Observe(0, 1)
		c.Observe(1, 2)
	}
	nll := c.PathLogLikelihood([]int{0, 1, 2})
	if math.IsInf(nll, 1) || math.IsNaN(nll) {
		t.Errorf("PathLogLikelihood([0,1,2]) = %v, want finite value", nll)
	}
	if nll <= 0 {
		t.Errorf("NLL should be positive, got %v", nll)
	}

	// Short path (1 state) → NLL = 0.
	nll2 := c.PathLogLikelihood([]int{0})
	if nll2 != 0 {
		t.Errorf("single-state NLL = %v, want 0", nll2)
	}
}

func TestNormalizedPathNLL(t *testing.T) {
	c := New(3, 1.0)
	for range 10 {
		c.Observe(0, 1)
		c.Observe(1, 2)
	}
	// Paths of different lengths should give comparable normalized NLL.
	path2 := []int{0, 1}         // 1 transition
	path3 := []int{0, 1, 2}      // 2 transitions
	nll2 := c.NormalizedPathNLL(path2)
	nll3 := c.NormalizedPathNLL(path3)

	if math.IsInf(nll2, 1) || math.IsNaN(nll2) {
		t.Errorf("NormalizedNLL([0,1]) = %v", nll2)
	}
	if math.IsInf(nll3, 1) || math.IsNaN(nll3) {
		t.Errorf("NormalizedNLL([0,1,2]) = %v", nll3)
	}

	// Normalized NLL should be per-transition, roughly equal for same transition pattern.
	// nll3 divides 2 transitions, nll2 divides 1 — they should be in the same ballpark.
	if math.Abs(nll2-nll3) > 2.0 {
		t.Errorf("normalized NLL diverges too much: path2=%v, path3=%v", nll2, nll3)
	}

	// Single-state path returns 0.
	n0 := c.NormalizedPathNLL([]int{0})
	if n0 != 0 {
		t.Errorf("single-state normalized NLL = %v, want 0", n0)
	}
}

// ---- Req 9: Brier score and log loss (calibration) ----

func TestBrierScore(t *testing.T) {
	// Perfect prediction.
	pi := []float64{0.0, 1.0, 0.0}
	if s := BrierScore(pi, 1); math.Abs(s) > 1e-9 {
		t.Errorf("perfect Brier = %v, want 0.0", s)
	}

	// Uniform prediction: Brier = Σ(1/3 - o_i)² = (1/3)²×2 + (2/3)² = 2/9 + 4/9 = 2/3
	pi2 := []float64{1.0 / 3, 1.0 / 3, 1.0 / 3}
	want := 2.0/3.0
	if s := BrierScore(pi2, 0); math.Abs(s-want) > 1e-9 {
		t.Errorf("uniform Brier = %v, want %v", s, want)
	}
}

func TestLogLoss(t *testing.T) {
	// Perfect prediction: p[actual]=1.0 → log loss = -log(1) = 0
	pi := []float64{0.0, 1.0}
	if ll := LogLoss(pi, 1); math.Abs(ll) > 1e-6 {
		t.Errorf("perfect log loss = %v, want ~0", ll)
	}

	// Random guess p=0.5: log loss = -log(0.5) ≈ 0.693
	pi2 := []float64{0.5, 0.5}
	wantLL := -math.Log(0.5)
	if ll := LogLoss(pi2, 0); math.Abs(ll-wantLL) > 1e-9 {
		t.Errorf("log loss = %v, want %v", ll, wantLL)
	}

	// Invalid state returns +Inf.
	if !math.IsInf(LogLoss(pi, 5), 1) {
		t.Errorf("out-of-bounds LogLoss should return +Inf")
	}
}

// ---- Req 2: Exponential decay ----

func TestDecay(t *testing.T) {
	c := New(2, 0.5)
	c.Observe(0, 1) // count[0][1] = 2.0 (prior 1 + 1 observation)
	before := c.Counts()[0][1]
	c.Decay()
	after := c.Counts()[0][1]
	if math.Abs(after-before*0.5) > 1e-9 {
		t.Errorf("after decay: count[0][1] = %v, want %v", after, before*0.5)
	}
}

func TestDecayPreservesProportions(t *testing.T) {
	// Decay should not change the normalized transition probabilities.
	c := New(3, 0.99)
	for range 100 {
		c.Observe(0, 1)
	}
	for range 50 {
		c.Observe(0, 2)
	}
	pBefore := c.P()
	c.Decay()
	c.Decay()
	pAfter := c.P()
	for i := range pBefore {
		for j := range pBefore[i] {
			if math.Abs(pBefore[i][j]-pAfter[i][j]) > 1e-6 {
				t.Errorf("P[%d][%d] changed after decay: %v → %v", i, j, pBefore[i][j], pAfter[i][j])
			}
		}
	}
}

// ---- Req 3: Sparse-data behavior ----

func TestSparseDataSmoothing(t *testing.T) {
	// With α=1.0 and zero real observations, the matrix should be uniform (all 1/n).
	c := NewWithSmoothing(4, 1.0, 1.0)
	p := c.P()
	want := 0.25
	for i, row := range p {
		for j, v := range row {
			if math.Abs(v-want) > 1e-9 {
				t.Errorf("sparse P[%d][%d] = %v, want %v (uniform prior)", i, j, v, want)
			}
		}
	}
}

func TestSparseDataEffectiveSamples(t *testing.T) {
	// No real observations: effective samples should be ≤ 0.
	c := NewWithSmoothing(3, 1.0, 1.0)
	for i := 0; i < 3; i++ {
		if e := c.EffectiveSampleCount(i); e > 0 {
			t.Errorf("state %d eff samples = %v, want ≤ 0 (no real obs)", i, e)
		}
	}
}

// ---- Req 4: P^k forecasting correctness ----

func TestStepConvergesToAbsorbing(t *testing.T) {
	c := New(3, 1.0)
	for range 1000 {
		c.Observe(2, 2)
	}
	for range 100 {
		c.Observe(0, 2)
		c.Observe(1, 2)
	}
	pi := []float64{0.5, 0.5, 0.0}
	result := c.Step(pi, 500)
	if result[2] < 0.99 {
		t.Errorf("expected convergence to state 2, got %v", result)
	}
}

func TestStepDistributionSumsToOne(t *testing.T) {
	c := New(5, 0.99)
	for range 200 {
		c.Observe(0, 1)
		c.Observe(1, 2)
		c.Observe(2, 3)
		c.Observe(3, 4)
	}
	pi := []float64{0.2, 0.2, 0.2, 0.2, 0.2}
	for k := 1; k <= 288; k *= 4 {
		result := c.Step(pi, k)
		var sum float64
		for _, v := range result {
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-6 {
			t.Errorf("Step(%d) distribution sum = %v, want 1.0", k, sum)
		}
	}
}

// ---- Req 5: Adaptive interval bounds ----

func TestBoundedAdaptiveInterval(t *testing.T) {
	// imported via engine package; test the logic here via torrent engine tests
	// to keep chain_test focused on the chain package.
	// We verify the entropy-based signal: uniform pi → max entropy → min interval.
	pi := make([]float64, 5)
	for i := range pi {
		pi[i] = 0.2
	}
	h := Entropy(pi)
	maxH := MaxEntropy(5)
	if maxH <= 0 {
		t.Fatal("MaxEntropy(5) must be > 0")
	}
	if math.Abs(h-maxH) > 1e-9 {
		t.Errorf("uniform entropy = %v, want maxH = %v", h, maxH)
	}
}

// ---- Req 7: Model governance via LoadCounts ----

func TestLoadCounts(t *testing.T) {
	c := NewWithSmoothing(2, 1.0, 1.0)
	// Load custom counts.
	loaded := [][]float64{{5.0, 3.0}, {2.0, 8.0}}
	c.LoadCounts(loaded)
	counts := c.Counts()
	for i := range loaded {
		for j := range loaded[i] {
			if math.Abs(counts[i][j]-loaded[i][j]) > 1e-9 {
				t.Errorf("counts[%d][%d] = %v, want %v", i, j, counts[i][j], loaded[i][j])
			}
		}
	}
}

// ---- Existing absorption test ----

func TestExpectedAbsorptionSteps(t *testing.T) {
	c := New(2, 1.0)
	for range 1000 {
		c.Observe(0, 1)
	}
	for range 1000 {
		c.Observe(1, 1)
	}
	times := c.ExpectedAbsorptionSteps(1)
	if times[1] != 0 {
		t.Errorf("absorbing state expected time = %v, want 0", times[1])
	}
	if math.Abs(times[0]-1.0) > 0.05 {
		t.Errorf("E[steps from 0] = %v, want ≈1.0", times[0])
	}
}

// ---- State classification tests (preserved from original) ----

func TestTorrentHealthState(t *testing.T) {
	tests := []struct {
		seeders, leechers int64
		want              int
	}{
		{0, 0, TorrentDead},
		{0, 5, TorrentDying},
		{1, 10, TorrentAtRisk},
		{2, 0, TorrentAtRisk},
		{3, 3, TorrentHealthy},
		{10, 4, TorrentThriving},
		{10, 0, TorrentThriving},
	}
	for _, tt := range tests {
		got := TorrentHealthState(tt.seeders, tt.leechers)
		if got != tt.want {
			t.Errorf("TorrentHealthState(%d, %d) = %v (%s), want %v (%s)",
				tt.seeders, tt.leechers,
				got, TorrentStateNames[got],
				tt.want, TorrentStateNames[tt.want])
		}
	}
}

func TestUserRatioState(t *testing.T) {
	tests := []struct {
		up, down  int64
		canLeech  bool
		freeleech bool
		want      int
	}{
		{100, 0, true, false, UserHealthy},
		{60, 100, true, false, UserHealthy},
		{45, 100, true, false, UserWarning},
		{15, 100, true, false, UserProbation},
		{5, 100, true, false, UserBanned},
		{0, 0, false, false, UserBanned},
		{5, 100, true, true, UserFreeleech},
	}
	for _, tt := range tests {
		got := UserRatioState(tt.up, tt.down, tt.canLeech, tt.freeleech)
		if got != tt.want {
			t.Errorf("UserRatioState(%d, %d, %v, %v) = %s, want %s",
				tt.up, tt.down, tt.canLeech, tt.freeleech,
				UserStateNames[got], UserStateNames[tt.want])
		}
	}
}

func TestPeerActivityState(t *testing.T) {
	now := int64(10000)
	timeout := int64(7200)
	tests := []struct {
		active    bool
		remaining int64
		mtime     int64
		want      int
	}{
		{true, 0, now - 100, PeerSeeding},
		{true, 1000, now - 100, PeerLeeching},
		{false, 0, now - 100, PeerDormant},
		{false, 0, now - 8000, PeerDead},
	}
	for _, tt := range tests {
		got := PeerActivityState(tt.active, tt.remaining, tt.mtime, now, timeout)
		if got != tt.want {
			t.Errorf("PeerActivityState(active=%v, remaining=%d, mtime=%d) = %s, want %s",
				tt.active, tt.remaining, tt.mtime,
				PeerStateNames[got], PeerStateNames[tt.want])
		}
	}
}
