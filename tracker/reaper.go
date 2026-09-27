package tracker

import (
	"sort"
	"time"
)

// Reaper evicts peers that have not announced within PeersTimeout.
// It runs on a configurable interval, mirroring C++ worker::reap_peers().
type Reaper struct {
	torrents     *TorrentList
	stats        *Stats
	interval     time.Duration
	timeout      time.Duration
	maxSwarmSize int // 0 = no cap
	stop         chan struct{}
}

// NewReaper creates a Reaper. intervalSec controls how often it runs;
// timeoutSec is the staleness threshold for peer expiry.
func NewReaper(torrents *TorrentList, stats *Stats, intervalSec, timeoutSec int) *Reaper {
	return &Reaper{
		torrents: torrents,
		stats:    stats,
		interval: time.Duration(intervalSec) * time.Second,
		timeout:  time.Duration(timeoutSec) * time.Second,
		stop:     make(chan struct{}),
	}
}

// WithMaxSwarmSize sets a per-torrent peer count cap. When a torrent has more
// peers than cap across seeders+leechers, the oldest (by LastAnnounced) are
// evicted first until the swarm fits.  Call before Start.
func (r *Reaper) WithMaxSwarmSize(cap int) *Reaper {
	r.maxSwarmSize = cap
	return r
}

// Start launches the reaper goroutine.
func (r *Reaper) Start() {
	go r.run()
}

// Stop signals the reaper to exit on its next tick.
func (r *Reaper) Stop() {
	close(r.stop)
}

func (r *Reaper) run() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.reap()
		case <-r.stop:
			return
		}
	}
}

func (r *Reaper) reap() {
	cutoff := time.Now().Add(-r.timeout)
	var totalEvicted uint64
	r.torrents.ForEach(func(_ string, t *Torrent) bool {
		totalEvicted += reapPeerList(t.Seeders, cutoff)
		totalEvicted += reapPeerList(t.Leechers, cutoff)
		if r.maxSwarmSize > 0 {
			totalEvicted += capPeerLists(t.Seeders, t.Leechers, r.maxSwarmSize)
		}
		return true
	})
	if totalEvicted > 0 && r.stats != nil {
		r.stats.EvictedPeers.Add(totalEvicted)
	}
}

type peerEntry struct {
	key  string
	list *PeerList
	t    time.Time
}

// capPeerLists evicts the oldest peers (by LastAnnounced) across seeders and
// leechers until the combined swarm size is at or below maxSize.
func capPeerLists(seeders, leechers *PeerList, maxSize int) uint64 {
	total := seeders.Size() + leechers.Size()
	if total <= maxSize {
		return 0
	}
	excess := total - maxSize

	entries := make([]peerEntry, 0, total)
	seeders.ForEach(func(key string, p *Peer) bool {
		entries = append(entries, peerEntry{key: key, list: seeders, t: p.LastAnnounced})
		return true
	})
	leechers.ForEach(func(key string, p *Peer) bool {
		entries = append(entries, peerEntry{key: key, list: leechers, t: p.LastAnnounced})
		return true
	})

	// Sort ascending so oldest peers are at the front.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].t.Before(entries[j].t)
	})

	var evicted uint64
	for i := 0; i < excess && i < len(entries); i++ {
		entries[i].list.Delete(entries[i].key)
		evicted++
	}
	return evicted
}

// reapPeerList removes all peers whose LastAnnounced is before cutoff and
// returns the number of peers removed.
func reapPeerList(pl *PeerList, cutoff time.Time) uint64 {
	var dead []string
	pl.ForEach(func(key string, p *Peer) bool {
		if !p.LastAnnounced.IsZero() && p.LastAnnounced.Before(cutoff) {
			dead = append(dead, key)
		}
		return true
	})
	for _, k := range dead {
		pl.Delete(k)
	}
	return uint64(len(dead))
}
