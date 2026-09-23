package chain

import (
	"math"
	"testing"
)

func TestObserveAndNormalize(t *testing.T) {
	c := New(3, 1.0)
	c.Observe(0, 1)
	c.Observe(0, 1)
	c.Observe(0, 2)

	p := c.P()
	// Row 0: 2 observations to state 1, 1 to state 2, plus Laplace prior (1 each)
	// counts: [1, 3, 2], sum=6
	want0to1 := 3.0 / 6.0
	// Epsilon regularization (chainEpsilon) shifts each cell by ~1e-6 before
	// renormalization, so we allow 1e-4 instead of exact equality.
	if math.Abs(p[0][1]-want0to1) > 1e-4 {
		t.Errorf("P[0][1] = %v, want ~%v (±1e-4)", p[0][1], want0to1)
	}
	// Each row must sum to 1 (renormalization guarantees this exactly).
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

func TestStepConvergesToAbsorbing(t *testing.T) {
	// State 2 is absorbing. With enough observations and steps, the vast
	// majority of probability mass should accumulate there.
	c := New(3, 1.0)
	// Make state 2 strongly absorbing: many self-transitions dominate the prior.
	for range 1000 {
		c.Observe(2, 2)
	}
	// Transient states transition strongly toward state 2.
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

func TestPathLogLikelihood(t *testing.T) {
	c := New(3, 1.0)
	// Observe 10 transitions 0→1 and 10 transitions 1→2.
	for range 10 {
		c.Observe(0, 1)
		c.Observe(1, 2)
	}
	// Path 0→1→2 should have finite, non-infinite NLL.
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

func TestExpectedAbsorptionSteps(t *testing.T) {
	// 2-state chain: state 0 is transient, state 1 is absorbing.
	// P[0][0]=0, P[0][1]=1 → E[steps from 0] = 1.
	c := New(2, 1.0)
	// Override with deterministic transition 0→1.
	// Use many observations to dominate Laplace prior.
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
	// E[steps from 0] ≈ 1 (slightly off due to Laplace prior).
	if math.Abs(times[0]-1.0) > 0.05 {
		t.Errorf("E[steps from 0] = %v, want ≈1.0", times[0])
	}
}

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
		up, down   int64
		canLeech   bool
		freeleech  bool
		want       int
	}{
		{100, 0, true, false, UserHealthy},    // seed-only user
		{60, 100, true, false, UserHealthy},   // ratio 0.6
		{45, 100, true, false, UserWarning},   // ratio 0.45
		{15, 100, true, false, UserProbation}, // ratio 0.15
		{5, 100, true, false, UserBanned},     // ratio 0.05
		{0, 0, false, false, UserBanned},      // banned flag
		{5, 100, true, true, UserFreeleech},   // freeleech trumps ratio
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
		{false, 0, now - 100, PeerDormant},     // inactive but recent
		{false, 0, now - 8000, PeerDead},       // inactive and past timeout
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
