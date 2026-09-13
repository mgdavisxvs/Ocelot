package tracker

import (
	"sync/atomic"
	"time"
)

// Reaper evicts peers that have not announced within PeersTimeout.
// It runs on a configurable interval, mirroring C++ worker::reap_peers().
type Reaper struct {
	torrents       *TorrentList
	interval       time.Duration
	timeoutNanos   atomic.Int64 // nanoseconds; read by reap() each cycle
	stop           chan struct{}
}

// NewReaper creates a Reaper. intervalSec controls how often it runs;
// timeoutSec is the staleness threshold for peer expiry.
func NewReaper(torrents *TorrentList, intervalSec, timeoutSec int) *Reaper {
	r := &Reaper{
		torrents: torrents,
		interval: time.Duration(intervalSec) * time.Second,
		stop:     make(chan struct{}),
	}
	r.timeoutNanos.Store(int64(time.Duration(timeoutSec) * time.Second))
	return r
}

// SetTimeout updates the peer-expiry timeout without restarting the goroutine.
func (r *Reaper) SetTimeout(d time.Duration) {
	r.timeoutNanos.Store(int64(d))
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
	cutoff := time.Now().Add(-time.Duration(r.timeoutNanos.Load()))
	r.torrents.ForEach(func(_ string, t *Torrent) bool {
		reapPeerList(t.Seeders, cutoff)
		reapPeerList(t.Leechers, cutoff)
		return true
	})
}

// reapPeerList removes all peers whose LastAnnounced is before cutoff.
func reapPeerList(pl *PeerList, cutoff time.Time) {
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
}
