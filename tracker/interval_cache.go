package tracker

// IntervalCache stores Markov-recommended announce intervals per torrent.
// The tracker's interval calculation reads from this cache; Markov writes to it
// via interval.update events.
//
// F-K2: backed by IntervalSkipList for O(log n) Get/Set and ordered iteration.
type IntervalCache struct {
	list *IntervalSkipList
	bus  *Bus
}

// NewIntervalCache creates the cache and subscribes to interval.update events.
func NewIntervalCache(bus *Bus) *IntervalCache {
	ic := &IntervalCache{
		list: NewIntervalSkipList(),
		bus:  bus,
	}
	bus.Subscribe("interval.update", ic.onUpdate)
	return ic
}

// GetOrDefault returns the cached interval for a torrent, or fallback if none.
func (ic *IntervalCache) GetOrDefault(torrentID TorrentID, fallback int) int {
	v, ok := ic.list.Get(torrentID)
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
	ic.list.Set(ev.TorrentID, ev.RecommendedInterval)
}

// Set allows direct writes (e.g. from tests or admin override).
func (ic *IntervalCache) Set(torrentID TorrentID, interval int) {
	ic.list.Set(torrentID, interval)
}

// RangeByInterval returns all TorrentIDs whose recommended interval ≤ maxSec,
// in ascending TorrentID order.
func (ic *IntervalCache) RangeByInterval(maxSec int) []TorrentID {
	return ic.list.RangeByInterval(maxSec)
}
