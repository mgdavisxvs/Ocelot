package tracker

import (
	"sync"
)

// peerIntervalKey is the composite cache key for per-peer adaptive intervals.
type peerIntervalKey struct {
	torrentID TorrentID
	userID    UserID
}

const (
	minAnnounceInterval = 300  // 5 minutes — floor regardless of Beta score
	maxAnnounceInterval = 3600 // 60 minutes — ceiling for well-trusted peers
)

// IntervalCache stores Markov-recommended announce intervals per torrent, with
// optional per-peer overrides derived from Beta reliability posteriors.
//
// Lookup priority: per-peer override → torrent-wide Markov recommendation → fallback.
// The tracker's interval calculation reads from this cache; Markov writes to it
// via interval.update events.
type IntervalCache struct {
	mu        sync.RWMutex
	byTorrent map[TorrentID]int            // torrent-wide baseline from Markov
	byPeer    map[peerIntervalKey]int       // per-peer overrides (optional)
	bus       *Bus
}

// NewIntervalCache creates the cache and subscribes to interval.update events.
func NewIntervalCache(bus *Bus) *IntervalCache {
	ic := &IntervalCache{
		byTorrent: make(map[TorrentID]int),
		byPeer:    make(map[peerIntervalKey]int),
		bus:       bus,
	}
	if bus != nil {
		bus.Subscribe("interval.update", ic.onUpdate)
	}
	return ic
}

// GetOrDefault returns the cached interval for a torrent, or fallback if none.
// Does not apply per-peer Beta scaling; use GetOrDefaultAdaptive for that.
func (ic *IntervalCache) GetOrDefault(torrentID TorrentID, fallback int) int {
	ic.mu.RLock()
	v, ok := ic.byTorrent[torrentID]
	ic.mu.RUnlock()
	if ok {
		return v
	}
	return fallback
}

// GetOrDefaultAdaptive returns the best interval for a specific (torrent, peer)
// pair, scaled by the peer's Beta reliability posterior.
//
// betaReliability is the peer's Beta(α,β) mean = α/(α+β) ∈ [0,1].
// A reliability of 0.5 returns the baseline unchanged; values above 0.5 lengthen
// the interval (trusted peer can check in less often); values below shorten it.
//
// scale = 0.5 + betaReliability → range [0.5, 1.5]
// result = clamp(baseline × scale, minAnnounceInterval, maxAnnounceInterval)
func (ic *IntervalCache) GetOrDefaultAdaptive(torrentID TorrentID, userID UserID, betaReliability float64, fallback int) int {
	ic.mu.RLock()
	// Check for an explicit per-peer override first.
	if v, ok := ic.byPeer[peerIntervalKey{torrentID, userID}]; ok {
		ic.mu.RUnlock()
		return v
	}
	base, ok := ic.byTorrent[torrentID]
	ic.mu.RUnlock()
	if !ok {
		base = fallback
	}
	if betaReliability < 0 {
		betaReliability = 0
	} else if betaReliability > 1 {
		betaReliability = 1
	}
	scale := 0.5 + betaReliability // [0.5, 1.5]
	result := int(float64(base) * scale)
	if result < minAnnounceInterval {
		return minAnnounceInterval
	}
	if result > maxAnnounceInterval {
		return maxAnnounceInterval
	}
	return result
}

func (ic *IntervalCache) onUpdate(e Event) {
	ev, ok := e.(*IntervalUpdateEvent)
	if !ok {
		return
	}
	ic.mu.Lock()
	ic.byTorrent[ev.TorrentID] = ev.RecommendedInterval
	ic.mu.Unlock()
}

// Set writes a torrent-wide interval (e.g. from tests or admin override).
func (ic *IntervalCache) Set(torrentID TorrentID, interval int) {
	ic.mu.Lock()
	ic.byTorrent[torrentID] = interval
	ic.mu.Unlock()
}

// SetPerPeer writes an explicit per-peer interval override.
func (ic *IntervalCache) SetPerPeer(torrentID TorrentID, userID UserID, interval int) {
	ic.mu.Lock()
	ic.byPeer[peerIntervalKey{torrentID, userID}] = interval
	ic.mu.Unlock()
}

// DeletePerPeer removes a per-peer override, reverting to the torrent-wide value.
func (ic *IntervalCache) DeletePerPeer(torrentID TorrentID, userID UserID) {
	ic.mu.Lock()
	delete(ic.byPeer, peerIntervalKey{torrentID, userID})
	ic.mu.Unlock()
}
