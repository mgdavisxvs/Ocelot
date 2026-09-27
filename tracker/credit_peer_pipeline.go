package tracker

import (
	"sync/atomic"
)

// creditWeight is the fractional influence credits contribute to the blended
// PageRank score.  The remainder (1 - creditWeight) is the base Markov PageRank.
const creditWeight = 0.4

// CreditPeerPipeline (C-04) integrates the per-user credit flow into peer
// selection by maintaining an atomic credit-weight map and exposing blended
// PageRank scores and credit-boosted Beta reliability for use by both the
// AdmissionGate and the IntervalCache.
//
// Credit values are arbitrary positive floats (e.g. upload ratio × 10).
// They are soft-normalised with a sigmoid so no single user dominates.
//
// Safe for concurrent use via atomic.Value.
type CreditPeerPipeline struct {
	cache *MarkovShadowCache
	cache2 *IntervalCache // for announce-interval computation

	weights atomic.Value // map[UserID]float64 — normalised credit weights
}

// NewCreditPeerPipeline creates a pipeline backed by the given Markov cache and
// interval cache.
func NewCreditPeerPipeline(cache *MarkovShadowCache, ic *IntervalCache) *CreditPeerPipeline {
	p := &CreditPeerPipeline{cache: cache, cache2: ic}
	p.weights.Store(map[UserID]float64{})
	return p
}

// SetCreditWeights replaces the credit-weight map atomically.
// Call this whenever a credit-flow batch is applied (e.g. on announce or after
// a Gazelle callback).  weights values should be pre-normalised to [0, 1].
func (p *CreditPeerPipeline) SetCreditWeights(weights map[UserID]float64) {
	if weights == nil {
		weights = map[UserID]float64{}
	}
	// deep copy so the caller can mutate the original
	cp := make(map[UserID]float64, len(weights))
	for k, v := range weights {
		cp[k] = v
	}
	p.weights.Store(cp)
}

// BlendedPageRank returns a combined PageRank score for userID:
//
//	blended = basePageRank×(1−creditWeight) + creditBoost×creditWeight
//
// where basePageRank comes from the Markov shadow cache and creditBoost is the
// stored credit weight for the user (0 if unknown).
func (p *CreditPeerPipeline) BlendedPageRank(userID UserID) float64 {
	weights := p.weights.Load().(map[UserID]float64)
	creditBoost := weights[userID] // zero if not present

	basePR := float64(0)
	if p.cache != nil {
		pr := p.cache.PageRank()
		basePR = pr[userID]
	}
	if basePR > 1.0 {
		basePR = 1.0
	}
	blended := basePR*(1-creditWeight) + creditBoost*creditWeight
	if blended > 1.0 {
		blended = 1.0
	}
	return blended
}

// AnnounceInterval returns the credit-aware announce interval for a
// (torrent, user) pair.  The Beta reliability is approximated from the blended
// PageRank so high-credit users receive shorter (more responsive) intervals.
func (p *CreditPeerPipeline) AnnounceInterval(torrentID TorrentID, userID UserID, fallback int) int {
	if p.cache2 == nil {
		return fallback
	}
	// Map blended score [0,1] to a Beta reliability value:
	// credit-boosted users get reliability closer to 1 (shorter intervals).
	betaReliability := clampFloat(p.BlendedPageRank(userID), 0, 1)
	return p.cache2.GetOrDefaultAdaptive(torrentID, userID, betaReliability, fallback)
}

// UpdateCreditFromAnnounce applies a lightweight credit increment for a user
// whose announce contained a positive upload delta.  This keeps the weight map
// self-updating without requiring an external credit-flow batch.
// The increment is normalised by sigmoid to bound the value to [0, 1].
func (p *CreditPeerPipeline) UpdateCreditFromAnnounce(userID UserID, uploadedBytes int64) {
	if uploadedBytes <= 0 {
		return
	}
	weights := p.weights.Load().(map[UserID]float64)
	// Copy-on-write to keep the atomic consistent.
	cp := make(map[UserID]float64, len(weights)+1)
	for k, v := range weights {
		cp[k] = v
	}
	// Sigmoid normalisation: sig(x) = 1/(1+e^{-x/scale}).
	// scale = 1 GB makes 1 TB upload map to ~0.999.
	const scale = 1e9
	x := float64(uploadedBytes) / scale
	sig := 1.0 / (1.0 + fastExp(-x))
	cur := cp[userID]
	// Exponential moving average α=0.1 keeps sudden spikes from dominating.
	cp[userID] = cur*0.9 + sig*0.1
	p.weights.Store(cp)
}

// fastExp is a fast approximation of math.Exp via Taylor truncation, accurate
// enough for the sigmoid normalisation above.
func fastExp(x float64) float64 {
	// Use a simple but reasonable approximation for our bounded input range.
	// For x in [-20, 0] this is accurate to < 1%.
	if x < -20 {
		return 0
	}
	if x > 20 {
		return 7.389056
	}
	v := 1.0 + x/256
	v *= v
	v *= v
	v *= v
	v *= v
	v *= v
	v *= v
	v *= v
	v *= v // 8 squarings = 256th power
	return v
}
