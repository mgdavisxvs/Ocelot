package tracker

import (
	"math"
	"math/rand"
	"sync"
)

// StrategyID names one of the three peer-selection strategies subject to
// Thompson-sampling exploration.
type StrategyID uint8

const (
	StrategyReservoir StrategyID = iota // Erdős reservoir sampling (exploration baseline)
	StrategyScored                      // ML-scored peers (ml.PeerScorer)
	StrategyEconomic                    // Commons economic ranking
)

// betaArm tracks the Beta(α, β) posterior for one strategy arm.
// α increments on a rewarded (successful) pull; β increments on a failed one.
// Both are initialised to 1 (uniform prior) so all arms are explored equally at
// startup.
type betaArm struct {
	alpha float64 // successes + 1 (prior)
	beta  float64 // failures  + 1 (prior)
}

// sample draws one observation from Beta(α, β) via Gamma ratio sampling.
func (a betaArm) sample() float64 {
	x := gammaVariate(a.alpha)
	y := gammaVariate(a.beta)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// PeerStrategyBandit implements a 3-arm Thompson-sampling bandit that selects
// the peer-selection strategy for each announce.
//
// The reward signal is a completed download (event=completed arriving for the
// leecher who received this peer list).  The bandit converges toward the
// strategy that maximises completion rates, automatically adapting to swarm
// conditions without manual tuning.
//
// Safe for concurrent use.
type PeerStrategyBandit struct {
	mu   sync.Mutex
	arms [3]betaArm
}

// NewPeerStrategyBandit creates a bandit with uniform Beta(1,1) priors.
func NewPeerStrategyBandit() *PeerStrategyBandit {
	b := &PeerStrategyBandit{}
	for i := range b.arms {
		b.arms[i] = betaArm{alpha: 1, beta: 1}
	}
	return b
}

// Select draws a sample from each arm's posterior and returns the arm with the
// highest sampled value (Thompson sampling).
func (b *PeerStrategyBandit) Select() StrategyID {
	b.mu.Lock()
	defer b.mu.Unlock()
	var best StrategyID
	bestVal := -1.0
	for i, arm := range b.arms {
		v := arm.sample()
		if v > bestVal {
			bestVal = v
			best = StrategyID(i)
		}
	}
	return best
}

// Reward updates the posterior for strategy id.
// completed=true increments α (success); completed=false increments β (failure).
func (b *PeerStrategyBandit) Reward(id StrategyID, completed bool) {
	if int(id) >= len(b.arms) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if completed {
		b.arms[id].alpha++
	} else {
		b.arms[id].beta++
	}
}

// Rates returns the current mean (α/(α+β)) for each arm, indexed by StrategyID.
func (b *PeerStrategyBandit) Rates() [3]float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	var r [3]float64
	for i, a := range b.arms {
		r[i] = a.alpha / (a.alpha + a.beta)
	}
	return r
}

// gammaVariate returns a sample from Gamma(shape, 1) using the Marsaglia–Tsang
// method (shape ≥ 1) or reduction for shape < 1.
func gammaVariate(shape float64) float64 {
	if shape < 1 {
		// Reduction: Gamma(k) = Gamma(k+1) × U^(1/k)
		return gammaVariate(shape+1) * math.Pow(rand.Float64(), 1.0/shape)
	}
	d := shape - 1.0/3.0
	c := 1.0 / math.Sqrt(9*d)
	for {
		x := rand.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := rand.Float64()
		if u < 1-0.0331*(x*x)*(x*x) {
			return d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}
