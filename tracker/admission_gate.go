package tracker

import (
	"sync"
	"sync/atomic"
)

// AdmissionGate (C-01) combines the PeerStrategyBandit's Thompson-sampling arm
// selection with per-user Beta reliability scores and Markov PageRank to gate
// swarm admission.  Peers that fall below the computed threshold are deferred
// (returned back to the caller) rather than hard-rejected, giving the swarm
// time to warm up without permanently excluding cold-start peers.
//
// The gate is safe for concurrent use.
type AdmissionGate struct {
	bandit *PeerStrategyBandit
	cache  *MarkovShadowCache

	mu        sync.RWMutex
	betaAlpha map[UserID]float64 // per-user Beta(α,β) reliability posterior
	betaBeta  map[UserID]float64

	// admitThreshold is the minimum blended score to admit a peer.
	// Tune via SetThreshold; default 0.25.
	admitThreshold atomic.Value // float64

	// stats
	admitCount  atomic.Uint64
	deferCount  atomic.Uint64
	rewardCount atomic.Uint64
}

// NewAdmissionGate creates a gate backed by bandit and the Markov shadow cache.
func NewAdmissionGate(bandit *PeerStrategyBandit, cache *MarkovShadowCache) *AdmissionGate {
	g := &AdmissionGate{
		bandit:    bandit,
		cache:     cache,
		betaAlpha: make(map[UserID]float64),
		betaBeta:  make(map[UserID]float64),
	}
	g.admitThreshold.Store(float64(0.25))
	return g
}

// SetThreshold overrides the admission threshold (default 0.25).
func (g *AdmissionGate) SetThreshold(t float64) {
	g.admitThreshold.Store(t)
}

// Admit evaluates whether userID should be admitted to the swarm.
// Returns true (admit) or false (defer).  The selected strategy arm is also
// returned so the caller can later call Reward once outcome is known.
func (g *AdmissionGate) Admit(userID UserID) (admit bool, strategy StrategyID) {
	strategy = g.bandit.Select()
	score := g.score(userID)
	thresh := g.admitThreshold.Load().(float64)
	if score >= thresh {
		g.admitCount.Add(1)
		return true, strategy
	}
	g.deferCount.Add(1)
	return false, strategy
}

// Reward feeds the outcome of a previously admitted peer back into both the
// bandit and the per-user Beta reliability posterior.
// completed=true on event=completed; false on stopped/timeout without completing.
func (g *AdmissionGate) Reward(userID UserID, strategy StrategyID, completed bool) {
	g.bandit.Reward(strategy, completed)
	g.updateBeta(userID, completed)
	g.rewardCount.Add(1)
}

// Stats returns (admitted, deferred, rewarded) counters.
func (g *AdmissionGate) Stats() (admitted, deferred, rewarded uint64) {
	return g.admitCount.Load(), g.deferCount.Load(), g.rewardCount.Load()
}

// score blends per-user Beta reliability (60%) with Markov PageRank (40%).
func (g *AdmissionGate) score(userID UserID) float64 {
	reliability := g.betaReliability(userID)

	pr := float64(0)
	if g.cache != nil {
		pageRank := g.cache.PageRank()
		if v, ok := pageRank[userID]; ok {
			pr = v
		}
	}
	// normalise PageRank to [0,1] with a soft cap at 1.0
	if pr > 1.0 {
		pr = 1.0
	}

	return 0.6*reliability + 0.4*pr
}

// betaReliability returns the mean of the per-user Beta posterior, defaulting
// to 0.5 (uniform prior) for unknown users.
func (g *AdmissionGate) betaReliability(userID UserID) float64 {
	g.mu.RLock()
	a, aOK := g.betaAlpha[userID]
	b := g.betaBeta[userID]
	g.mu.RUnlock()
	if !aOK {
		return 0.5
	}
	if a+b == 0 {
		return 0.5
	}
	return a / (a + b)
}

// updateBeta increments the appropriate Beta parameter for the user.
func (g *AdmissionGate) updateBeta(userID UserID, success bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.betaAlpha[userID]; !ok {
		g.betaAlpha[userID] = 1.0 // uniform prior
		g.betaBeta[userID] = 1.0
	}
	if success {
		g.betaAlpha[userID]++
	} else {
		g.betaBeta[userID]++
	}
}
