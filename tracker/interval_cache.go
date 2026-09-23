package tracker

import (
	"sync"
)

// IntervalCache stores Markov-recommended announce intervals per torrent.
// The tracker's interval calculation reads from this cache; Markov writes to it
// via interval.update events.
type IntervalCache struct {
	mu      sync.RWMutex
	entries map[TorrentID]int // recommended seconds
	bus     *Bus
}

// NewIntervalCache creates the cache and subscribes to interval.update events.
func NewIntervalCache(bus *Bus) *IntervalCache {
	ic := &IntervalCache{
		entries: make(map[TorrentID]int),
		bus:     bus,
	}
	bus.Subscribe("interval.update", ic.onUpdate)
	return ic
}

// GetOrDefault returns the cached interval for a torrent, or fallback if none.
func (ic *IntervalCache) GetOrDefault(torrentID TorrentID, fallback int) int {
	ic.mu.RLock()
	v, ok := ic.entries[torrentID]
	ic.mu.RUnlock()
	if ok {
		return v
	}
	return fallback
}

func (ic *IntervalCache) onUpdate(e Event) {
	ev, ok := e.(*IntervalUpdateEvent)
	if !ok {
		return
	}
	ic.mu.Lock()
	ic.entries[ev.TorrentID] = ev.RecommendedInterval
	ic.mu.Unlock()
}

// Set allows direct writes (e.g. from tests or admin override).
func (ic *IntervalCache) Set(torrentID TorrentID, interval int) {
	ic.mu.Lock()
	ic.entries[torrentID] = interval
	ic.mu.Unlock()
}
