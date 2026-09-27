package tracker

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/mgdavisxvs/Ocelot/ml"
)

// MarkovShadowCache holds the last-known Markov engine results in atomics so
// the hot announce path never blocks on a remote RPC.  A background goroutine
// refreshes the cache on the configured interval; if the Markov service is
// unavailable the stale values remain in use.
type MarkovShadowCache struct {
	candidates atomic.Value // []markovCandidate
	metrics    atomic.Value // *markovMetrics
	pageRank   atomic.Value // map[UserID]float64
	lastFetch  atomic.Int64 // unix-nano of last successful fetch
	staleTTL   time.Duration
}

// NewMarkovShadowCache creates a cache with the given stale TTL.
// A zero staleTTL defaults to 5 minutes.
func NewMarkovShadowCache(staleTTL time.Duration) *MarkovShadowCache {
	if staleTTL <= 0 {
		staleTTL = 5 * time.Minute
	}
	c := &MarkovShadowCache{staleTTL: staleTTL}
	// initialise atomics with zero-value sentinel so loads never return nil
	c.candidates.Store([]markovCandidate{})
	c.metrics.Store(&markovMetrics{})
	c.pageRank.Store(map[UserID]float64{})
	return c
}

// StartRefresh launches the background refresh goroutine.
// Cancel ctx to stop it.  intervalSec is the poll cadence in seconds.
func (c *MarkovShadowCache) StartRefresh(ctx context.Context, client *MarkovClient, intervalSec int) {
	if intervalSec <= 0 {
		intervalSec = 60
	}
	go func() {
		c.refresh(ctx, client) // fetch immediately on start
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.refresh(ctx, client)
			}
		}
	}()
}

func (c *MarkovShadowCache) refresh(ctx context.Context, client *MarkovClient) {
	tctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	cands, err := client.FreeleechCandidates(tctx)
	if err == nil {
		c.candidates.Store(cands)
		c.lastFetch.Store(time.Now().UnixNano())
	}

	tctx2, cancel2 := context.WithTimeout(ctx, 4*time.Second)
	defer cancel2()
	m, err := client.Metrics(tctx2)
	if err == nil {
		c.metrics.Store(m)
	}

	tctx3, cancel3 := context.WithTimeout(ctx, 4*time.Second)
	defer cancel3()
	pr, err := client.SeederPageRank(tctx3)
	if err == nil {
		c.pageRank.Store(pr)
	}
}

// Candidates returns the cached freeleech candidate list (never nil).
func (c *MarkovShadowCache) Candidates() []markovCandidate {
	return c.candidates.Load().([]markovCandidate)
}

// Metrics returns the cached engine metrics (never nil).
func (c *MarkovShadowCache) Metrics() *markovMetrics {
	return c.metrics.Load().(*markovMetrics)
}

// PageRank returns the cached seeder PageRank map (never nil).
func (c *MarkovShadowCache) PageRank() map[UserID]float64 {
	return c.pageRank.Load().(map[UserID]float64)
}

// IsFresh reports whether the cache was successfully refreshed within staleTTL.
func (c *MarkovShadowCache) IsFresh() bool {
	last := c.lastFetch.Load()
	if last == 0 {
		return false
	}
	return time.Since(time.Unix(0, last)) < c.staleTTL
}

// Age returns how long ago the cache was last successfully refreshed.
// Returns a large duration if never fetched.
func (c *MarkovShadowCache) Age() time.Duration {
	last := c.lastFetch.Load()
	if last == 0 {
		return 999 * time.Hour
	}
	return time.Since(time.Unix(0, last))
}

// ThresholdsForCache derives ml.ThresholdConfig from cached metrics.
// Falls back to no-change config when cache is stale.
func (c *MarkovShadowCache) ThresholdsForCache() ml.ThresholdConfig {
	if !c.IsFresh() {
		return ml.ThresholdConfig{}
	}
	return thresholdsForPopulation(c.Metrics().TrackedPeers)
}

// ShadowFreeleechPoller replaces FreeleechPoller; it reads from the cache
// rather than issuing a live RPC, so the announce path is never blocked.
func ShadowFreeleechPoller(ctx context.Context, cache *MarkovShadowCache, siteComm SiteCommInterface, intervalSec, notifyHours int) {
	go func() {
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !cache.IsFresh() {
					continue
				}
				for _, cand := range cache.Candidates() {
					if err := siteComm.NotifyFreeleech(cand.TorrentID, notifyHours); err != nil {
						GetDefaultLogger().Warn("freeleech notify failed",
							"torrent_id", cand.TorrentID, "err", err)
					}
				}
			}
		}
	}()
}

// ShadowAdaptiveThresholdPoller replaces AdaptiveThresholdPoller; reads thresholds
// from cache rather than issuing a live RPC on every tick.
func ShadowAdaptiveThresholdPoller(ctx context.Context, cache *MarkovShadowCache, adapter ThresholdAdapter, intervalSec int) {
	go func() {
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				adapter.SetThresholds(cache.ThresholdsForCache())
			}
		}
	}()
}
