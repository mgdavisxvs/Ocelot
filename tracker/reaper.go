package tracker

import "time"

// Reaper evicts peers that have not announced within PeersTimeout.
// It runs on a configurable interval, mirroring C++ worker::reap_peers().
type Reaper struct {
	torrents *TorrentList
	interval time.Duration
	timeout  time.Duration
	stop     chan struct{}
}

// NewReaper creates a Reaper. intervalSec controls how often it runs;
// timeoutSec is the staleness threshold for peer expiry.
func NewReaper(torrents *TorrentList, intervalSec, timeoutSec int) *Reaper {
	return &Reaper{
		torrents: torrents,
		interval: time.Duration(intervalSec) * time.Second,
		timeout:  time.Duration(timeoutSec) * time.Second,
		stop:     make(chan struct{}),
	}
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
