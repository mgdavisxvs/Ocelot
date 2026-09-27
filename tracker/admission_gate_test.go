package tracker

import (
	"testing"
)

// newTestAdmissionGate builds a gate with a null Markov cache (no PageRank
// contribution) so test results depend only on the Beta reliability posterior.
func newTestAdmissionGate() *AdmissionGate {
	bandit := NewPeerStrategyBandit()
	return NewAdmissionGate(bandit, nil)
}

// TestAdmissionGate_DefaultAdmitsUnknown verifies that a fresh user with no
// history scores ≥ threshold via the uniform Beta(1,1) prior (mean = 0.5,
// blended = 0.6×0.5 = 0.30 > default threshold 0.25).
func TestAdmissionGate_DefaultAdmitsUnknown(t *testing.T) {
	g := newTestAdmissionGate()
	admitted, _ := g.Admit(UserID(1))
	if !admitted {
		t.Fatal("expected unknown user to be admitted via uniform prior")
	}
}

// TestAdmissionGate_LowThresholdAlwaysAdmits checks that setting threshold=0
// ensures every user passes regardless of history.
func TestAdmissionGate_LowThresholdAlwaysAdmits(t *testing.T) {
	g := newTestAdmissionGate()
	g.SetThreshold(0)
	for i := 0; i < 10; i++ {
		admitted, _ := g.Admit(UserID(99))
		if !admitted {
			t.Fatalf("iteration %d: expected admission with zero threshold", i)
		}
	}
}

// TestAdmissionGate_HighThresholdDefers verifies that threshold=1.0 defers
// every user (no real user can score 1.0 with finite observations).
func TestAdmissionGate_HighThresholdDefers(t *testing.T) {
	g := newTestAdmissionGate()
	g.SetThreshold(1.0)
	admitted, _ := g.Admit(UserID(1))
	if admitted {
		t.Fatal("expected user to be deferred with threshold=1.0")
	}
}

// TestAdmissionGate_RewardIncreasesScore confirms that repeated successful
// completions push the Beta mean above the default threshold.
// With no PageRank the blended score is 0.6×betaMean, so the threshold must be
// ≤0.6 (e.g. 0.55) to be reachable by the Beta component alone.
func TestAdmissionGate_RewardIncreasesScore(t *testing.T) {
	g := newTestAdmissionGate()
	g.SetThreshold(0.55) // reachable via Beta alone: 0.6×betaMean > 0.55 when mean→1

	// Accumulate 20 successes for user 2 (Beta mean ≈ 21/22 ≈ 0.954, blended ≈ 0.573).
	for i := 0; i < 20; i++ {
		_, strat := g.Admit(UserID(2))
		g.Reward(UserID(2), strat, true)
	}
	admitted, _ := g.Admit(UserID(2))
	if !admitted {
		t.Fatal("expected user to be admitted after many successful completions")
	}
}

// TestAdmissionGate_FailuresReduceScore verifies that repeated failures push
// a previously-admitted user below threshold.
func TestAdmissionGate_FailuresReduceScore(t *testing.T) {
	g := newTestAdmissionGate()
	g.SetThreshold(0.25)

	uid := UserID(3)
	// Accumulate 30 failures — mean drops well below 0.25.
	for i := 0; i < 30; i++ {
		_, strat := g.Admit(uid)
		g.Reward(uid, strat, false)
	}
	admitted, _ := g.Admit(uid)
	if admitted {
		t.Fatal("expected user to be deferred after many failures")
	}
}

// TestAdmissionGate_Stats checks that counters advance correctly.
func TestAdmissionGate_Stats(t *testing.T) {
	g := newTestAdmissionGate()
	g.SetThreshold(0)

	for i := 0; i < 5; i++ {
		_, strat := g.Admit(UserID(4))
		g.Reward(UserID(4), strat, i%2 == 0)
	}

	admitted, deferred, rewarded := g.Stats()
	if admitted != 5 {
		t.Errorf("admitted = %d, want 5", admitted)
	}
	if deferred != 0 {
		t.Errorf("deferred = %d, want 0", deferred)
	}
	if rewarded != 5 {
		t.Errorf("rewarded = %d, want 5", rewarded)
	}
}

// TestAdmissionGate_PageRankBoost verifies that a user with a positive PageRank
// in the shadow cache scores higher than an equivalent user without it.
func TestAdmissionGate_PageRankBoost(t *testing.T) {
	bandit := NewPeerStrategyBandit()
	cache := NewMarkovShadowCache(0)

	// Inject a high PageRank for user 10.
	pr := map[UserID]float64{UserID(10): 1.0}
	cache.pageRank.Store(pr)

	g := NewAdmissionGate(bandit, cache)
	g.SetThreshold(0.5)

	// User 10 (PageRank=1.0): blended = 0.6×0.5 + 0.4×1.0 = 0.70 → admitted.
	admitted10, _ := g.Admit(UserID(10))
	if !admitted10 {
		t.Error("expected user with high PageRank to be admitted above 0.5 threshold")
	}

	// User 11 (no PageRank): blended = 0.6×0.5 = 0.30 < 0.5 → deferred.
	admitted11, _ := g.Admit(UserID(11))
	if admitted11 {
		t.Error("expected user without PageRank to be deferred at 0.5 threshold")
	}
}

// TestAdmissionGate_Concurrency stress-tests concurrent Admit+Reward calls.
func TestAdmissionGate_Concurrency(t *testing.T) {
	g := newTestAdmissionGate()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(uid UserID) {
			for j := 0; j < 100; j++ {
				_, strat := g.Admit(uid)
				g.Reward(uid, strat, j%3 == 0)
			}
			done <- struct{}{}
		}(UserID(i))
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
