package tracker

import (
	"log"
	"sync"
	"time"
)

// CollusionDetector watches for pairs of users who repeatedly announce the same
// torrents within a short time window — a strong signal of coordinated upload-count
// inflation ("fake swarm" collusion rings).
//
// Detection rule (Turing criterion T-07):
//
//	Two users who co-announce at least collusionMinTorrents distinct torrents with
//	each timestamp within collusionWindowSec of each other are flagged as a
//	collusion ring.  Both users receive a user.flagged event (score 0.85) on the
//	bus, which FraudEnforcer converts to a leech-suspend or ban depending on their
//	cumulative strike count.
type CollusionDetector struct {
	mu sync.Mutex
	// recentAnnounces: torrentID → ring buffer of recent (userID, timestamp) pairs
	recentAnnounces map[TorrentID][]collusionRecord
	// pairCounts: canonical user pair → count of shared co-announces
	pairCounts map[[2]UserID]int

	bus         *Bus
	window      time.Duration
	minTorrents int
}

type collusionRecord struct {
	userID UserID
	at     time.Time
}

const (
	collusionWindowSec  = 60
	collusionMinTorrents = 5
)

// NewCollusionDetector creates a detector and subscribes it to announce.success events.
func NewCollusionDetector(bus *Bus) *CollusionDetector {
	cd := &CollusionDetector{
		recentAnnounces: make(map[TorrentID][]collusionRecord),
		pairCounts:      make(map[[2]UserID]int),
		bus:             bus,
		window:          collusionWindowSec * time.Second,
		minTorrents:     collusionMinTorrents,
	}
	bus.Subscribe("announce.success", cd.onAnnounce)
	return cd
}

func (cd *CollusionDetector) onAnnounce(e Event) {
	ev, ok := e.(*AnnounceEvent)
	if !ok {
		return
	}

	torrentID := ev.TorrentID
	userID := ev.UserID
	now := time.Now()
	cutoff := now.Add(-cd.window)

	cd.mu.Lock()
	defer cd.mu.Unlock()

	// Prune stale records for this torrent (sliding window).
	existing := cd.recentAnnounces[torrentID]
	fresh := existing[:0]
	for _, r := range existing {
		if r.at.After(cutoff) {
			fresh = append(fresh, r)
		}
	}

	// Check for co-announces: count shared torrents with each recent peer.
	for _, r := range fresh {
		if r.userID == userID {
			continue
		}
		pair := collusionPair(r.userID, userID)
		cd.pairCounts[pair]++
		if cd.pairCounts[pair] == cd.minTorrents {
			log.Printf("collusion_detector: ring detected users=%d,%d shared_torrents>=%d window=%s",
				pair[0], pair[1], cd.minTorrents, cd.window)
			cd.bus.Publish(NewUserFlaggedEvent(ev.TraceID(), pair[0], 0.85))
			cd.bus.Publish(NewUserFlaggedEvent(ev.TraceID(), pair[1], 0.85))
		}
	}

	// Append this user's record.
	fresh = append(fresh, collusionRecord{userID: userID, at: now})
	cd.recentAnnounces[torrentID] = fresh
}

// collusionPair returns a canonical (lower-ID-first) pair key.
func collusionPair(a, b UserID) [2]UserID {
	if a < b {
		return [2]UserID{a, b}
	}
	return [2]UserID{b, a}
}
