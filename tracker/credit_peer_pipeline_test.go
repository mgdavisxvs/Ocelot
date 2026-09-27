package tracker

import (
	"testing"
)

func newTestCreditPipeline() (*CreditPeerPipeline, *IntervalCache) {
	cache := NewMarkovShadowCache(0)
	ic := NewIntervalCache(nil)
	return NewCreditPeerPipeline(cache, ic), ic
}

// TestCreditPipeline_BlendedPageRankUnknownUser verifies that a user with no
// PageRank and no credits scores 0 (base 0 × 0.6 + 0 × 0.4 = 0).
func TestCreditPipeline_BlendedPageRankUnknownUser(t *testing.T) {
	p, _ := newTestCreditPipeline()
	score := p.BlendedPageRank(UserID(999))
	if score != 0 {
		t.Errorf("unknown user score = %v, want 0", score)
	}
}

// TestCreditPipeline_CreditWeightRaisesScore confirms that setting a credit
// weight increases the blended score proportionally.
func TestCreditPipeline_CreditWeightRaisesScore(t *testing.T) {
	p, _ := newTestCreditPipeline()
	p.SetCreditWeights(map[UserID]float64{UserID(1): 1.0})
	score := p.BlendedPageRank(UserID(1))
	// 0 × 0.6 + 1.0 × 0.4 = 0.4
	want := 0.4
	if abs64(score-want) > 1e-9 {
		t.Errorf("score = %v, want %v", score, want)
	}
}

// TestCreditPipeline_PageRankPlusCredit verifies the full blending formula:
// blended = base×0.6 + creditBoost×0.4.
func TestCreditPipeline_PageRankPlusCredit(t *testing.T) {
	cache := NewMarkovShadowCache(0)
	cache.pageRank.Store(map[UserID]float64{UserID(5): 0.8})
	ic := NewIntervalCache(nil)
	p := NewCreditPeerPipeline(cache, ic)
	p.SetCreditWeights(map[UserID]float64{UserID(5): 0.5})

	score := p.BlendedPageRank(UserID(5))
	want := 0.8*0.6 + 0.5*0.4 // = 0.68
	if abs64(score-want) > 1e-9 {
		t.Errorf("score = %v, want %v", score, want)
	}
}

// TestCreditPipeline_ScoreCappedAt1 confirms the blended score never exceeds 1.
func TestCreditPipeline_ScoreCappedAt1(t *testing.T) {
	cache := NewMarkovShadowCache(0)
	cache.pageRank.Store(map[UserID]float64{UserID(6): 1.0})
	ic := NewIntervalCache(nil)
	p := NewCreditPeerPipeline(cache, ic)
	p.SetCreditWeights(map[UserID]float64{UserID(6): 1.0})

	score := p.BlendedPageRank(UserID(6))
	if score > 1.0 {
		t.Errorf("blended score %v exceeds 1.0", score)
	}
}

// TestCreditPipeline_UpdateFromAnnounceIncreasesWeight confirms that uploading
// increases the stored credit weight.
func TestCreditPipeline_UpdateFromAnnounceIncreasesWeight(t *testing.T) {
	p, _ := newTestCreditPipeline()
	uid := UserID(10)

	// No upload — weight stays 0.
	p.UpdateCreditFromAnnounce(uid, 0)
	before := p.BlendedPageRank(uid)

	// Large upload — weight should increase.
	p.UpdateCreditFromAnnounce(uid, 10_000_000_000) // 10 GB
	after := p.BlendedPageRank(uid)

	if after <= before {
		t.Errorf("expected score to increase after upload: before=%v after=%v", before, after)
	}
}

// TestCreditPipeline_SetWeightsDeepCopies verifies that mutating the map after
// SetCreditWeights does not affect the stored value.
func TestCreditPipeline_SetWeightsDeepCopies(t *testing.T) {
	p, _ := newTestCreditPipeline()
	m := map[UserID]float64{UserID(20): 0.9}
	p.SetCreditWeights(m)

	// Mutate the original map.
	m[UserID(20)] = 0.0

	score := p.BlendedPageRank(UserID(20))
	want := 0.9 * creditWeight
	if abs64(score-want) > 1e-9 {
		t.Errorf("SetCreditWeights did not deep-copy: score=%v, want %v", score, want)
	}
}

// TestCreditPipeline_AnnounceIntervalFallback verifies that with no stored
// interval, AnnounceInterval returns the fallback.
func TestCreditPipeline_AnnounceIntervalFallback(t *testing.T) {
	p, _ := newTestCreditPipeline()
	got := p.AnnounceInterval(TorrentID(1), UserID(1), 1800)
	if got < 300 || got > 3600 {
		t.Errorf("interval %d outside expected [300,3600]", got)
	}
}

// TestCreditPipeline_NilIntervalCacheFallback confirms no panic when
// IntervalCache is nil.
func TestCreditPipeline_NilIntervalCacheFallback(t *testing.T) {
	cache := NewMarkovShadowCache(0)
	p := NewCreditPeerPipeline(cache, nil)
	got := p.AnnounceInterval(TorrentID(1), UserID(1), 900)
	if got != 900 {
		t.Errorf("nil cache: interval = %d, want 900", got)
	}
}

// TestCreditPipeline_Concurrency stress-tests concurrent weight updates.
func TestCreditPipeline_Concurrency(t *testing.T) {
	p, _ := newTestCreditPipeline()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(uid UserID) {
			for j := 0; j < 100; j++ {
				p.UpdateCreditFromAnnounce(uid, int64(j)*1_000_000)
				p.BlendedPageRank(uid)
			}
			done <- struct{}{}
		}(UserID(i + 1))
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
