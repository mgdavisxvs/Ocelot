package tracker

import (
	"sync"
	"time"
)

// PeerRef identifies a peer row for bulk deactivation.
type PeerRef struct {
	UserID    UserID
	TorrentID TorrentID
}

// ReapResult reports what one sweep removed.
type ReapResult struct {
	SeedersRemoved  int
	LeechersRemoved int
	TorrentsSwept   int
	Duration        time.Duration
}

// Reaper removes peers that stopped announcing.
//
// Most peers never send event=stopped: clients crash, lose connectivity, or
// are killed. Without a reaper those peers stay in the swarm forever, so the
// peer lists grow without bound and announces hand out addresses that no
// longer serve anything.
type Reaper struct {
	torrents *TorrentList
	db       DatabaseInterface
	stats    *Stats
	metrics  *MetricsRecorder
	logger   *Logger

	timeout  time.Duration
	interval time.Duration

	stopChan chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewReaper creates a reaper that drops peers silent for longer than timeout,
// sweeping every interval. db may be nil to reap in memory only.
func NewReaper(torrents *TorrentList, db DatabaseInterface, stats *Stats, timeout, interval time.Duration) *Reaper {
	return &Reaper{
		torrents: torrents,
		db:       db,
		stats:    stats,
		metrics:  GetMetricsRecorder(),
		logger:   GetDefaultLogger(),
		timeout:  timeout,
		interval: interval,
		stopChan: make(chan struct{}),
	}
}

// Start begins sweeping in the background.
func (r *Reaper) Start() {
	r.wg.Add(1)

	go func() {
		defer r.wg.Done()

		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()

		for {
			select {
			case <-r.stopChan:
				return
			case <-ticker.C:
				result := r.ReapOnce()
				if result.SeedersRemoved+result.LeechersRemoved > 0 {
					r.logger.Info("reaped inactive peers",
						"seeders", result.SeedersRemoved,
						"leechers", result.LeechersRemoved,
						"torrents", result.TorrentsSwept,
						"duration_ms", result.Duration.Milliseconds(),
					)
				}
			}
		}
	}()

	r.logger.Info("peer reaper started",
		"timeout_seconds", int(r.timeout.Seconds()),
		"interval_seconds", int(r.interval.Seconds()),
	)
}

// Stop halts sweeping. It is safe to call more than once.
func (r *Reaper) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopChan)
		r.wg.Wait()
		r.logger.Info("peer reaper stopped")
	})
}

// ReapOnce runs a single sweep and reports what it removed. Exported so the
// sweep can be driven directly rather than waited on.
func (r *Reaper) ReapOnce() ReapResult {
	start := time.Now()
	cutoff := start.Add(-r.timeout)

	var result ReapResult
	var deactivate []PeerRef
	var liveSeeders, liveLeechers int

	r.torrents.ForEach(func(_ string, torrent *Torrent) bool {
		// Held for the whole torrent so a concurrent announce cannot revive a
		// peer between the expiry check and the delete. This matches the lock
		// order in Announce: torrent first, then the peer lists.
		torrent.mu.Lock()

		expiredSeeders := collectExpired(torrent.Seeders, cutoff)
		expiredLeechers := collectExpired(torrent.Leechers, cutoff)

		for _, e := range expiredSeeders {
			torrent.Seeders.Delete(e.key)
			deactivate = append(deactivate, PeerRef{UserID: e.userID, TorrentID: torrent.ID})
		}
		for _, e := range expiredLeechers {
			torrent.Leechers.Delete(e.key)
			deactivate = append(deactivate, PeerRef{UserID: e.userID, TorrentID: torrent.ID})
		}

		seeders := torrent.Seeders.Size()
		leechers := torrent.Leechers.Size()

		torrent.mu.Unlock()

		result.SeedersRemoved += len(expiredSeeders)
		result.LeechersRemoved += len(expiredLeechers)
		result.TorrentsSwept++
		liveSeeders += seeders
		liveLeechers += leechers

		// Keep the stored swarm size honest for torrents that changed.
		if r.db != nil && len(expiredSeeders)+len(expiredLeechers) > 0 {
			r.db.RecordTorrent(torrent.ID, uint32(seeders), uint32(leechers), 0, torrent.Balance)
		}

		return true
	})

	r.decrementStats(result)

	// Mark the rows inactive so a restart does not reload peers that are gone.
	if r.db != nil && len(deactivate) > 0 {
		if deactivator, ok := r.db.(PeerDeactivator); ok {
			if err := deactivator.DeactivatePeers(deactivate); err != nil {
				r.logger.Error("failed to deactivate reaped peers", err,
					"count", len(deactivate))
			}
		}
	}

	r.metrics.UpdatePeerCounts(liveSeeders, liveLeechers)
	r.metrics.UpdateTorrentCount(result.TorrentsSwept)

	result.Duration = time.Since(start)
	return result
}

// TorrentInfoHashRecorder persists the info_hash a torrent is announced under.
// It is optional so an in-memory database can skip it.
type TorrentInfoHashRecorder interface {
	RecordTorrentInfoHash(torrentID TorrentID, infoHash string) error
}

// UserIdentityRecorder persists the passkey an announce authenticates
// against, and its removal. Optional, like the other storage hooks.
type UserIdentityRecorder interface {
	RecordUserPasskey(userID UserID, passkey string, canLeech, protectIP bool) error
	MarkUserDeleted(userID UserID) error
}

// PeerDeactivator marks reaped peers inactive in storage. It is optional: a
// database that does not implement it is reaped in memory only.
type PeerDeactivator interface {
	DeactivatePeers(refs []PeerRef) error
}

type expiredPeer struct {
	key    string
	userID UserID
}

// collectExpired gathers peers silent since cutoff. Deletion cannot happen
// inside ForEach, which holds a read lock that Delete would try to upgrade.
func collectExpired(peers *PeerList, cutoff time.Time) []expiredPeer {
	var expired []expiredPeer

	peers.ForEach(func(key string, peer *Peer) bool {
		// A peer that has not completed a first announce has a zero timestamp;
		// leave it alone rather than reaping it immediately.
		if !peer.LastAnnounced.IsZero() && peer.LastAnnounced.Before(cutoff) {
			expired = append(expired, expiredPeer{key: key, userID: peer.UserID})
		}
		return true
	})

	return expired
}

// decrementStats mirrors the counters Announce increments.
func (r *Reaper) decrementStats(result ReapResult) {
	if r.stats == nil {
		return
	}

	for i := 0; i < result.SeedersRemoved; i++ {
		r.stats.Seeders.Add(^uint32(0))
	}
	for i := 0; i < result.LeechersRemoved; i++ {
		r.stats.Leechers.Add(^uint32(0))
	}
}
