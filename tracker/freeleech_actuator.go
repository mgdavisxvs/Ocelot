package tracker

import (
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

// FreeleechActuator listens for freeleech.recommended events and calls
// SiteComm.GrantFreeleech() when the priority score exceeds the threshold.
// It also revokes freeleech when the 72-hour death probability drops below
// revokeThreshold, indicating the torrent has become dormant.
type FreeleechActuator struct {
	siteComm SiteCommInterface
	bus      *Bus

	mu              sync.Mutex
	lastGrant       map[TorrentID]time.Time // prevent re-granting within cooldown
	activeGrants    map[TorrentID]bool       // tracks which torrents have active grants

	scoreThreshold  float64
	revokeThreshold float64
	cooldown        time.Duration
}

// defaultFreeleechThreshold is used when FREELEECH_THRESHOLD env var is not set.
const defaultFreeleechThreshold = 0.75

// NewFreeleechActuator creates the actuator and subscribes to the bus.
func NewFreeleechActuator(bus *Bus, siteComm SiteCommInterface) *FreeleechActuator {
	threshold := defaultFreeleechThreshold
	if v := os.Getenv("FREELEECH_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			threshold = f
		}
	}

	fa := &FreeleechActuator{
		siteComm:        siteComm,
		bus:             bus,
		lastGrant:       make(map[TorrentID]time.Time),
		activeGrants:    make(map[TorrentID]bool),
		scoreThreshold:  threshold,
		revokeThreshold: 0.25,
		cooldown:        6 * time.Hour,
	}
	bus.Subscribe("freeleech.recommended", fa.onRecommended)
	return fa
}

func (fa *FreeleechActuator) onRecommended(e Event) {
	ev, ok := e.(*FreeleechRecommendedEvent)
	if !ok {
		return
	}

	fa.mu.Lock()
	defer fa.mu.Unlock()

	// Revoke if the torrent has become dormant and we hold an active grant.
	if fa.activeGrants[ev.TorrentID] && ev.DeadProb72h <= fa.revokeThreshold {
		fa.siteComm.RevokeFreeleech(ev.TorrentID)
		delete(fa.activeGrants, ev.TorrentID)
		delete(fa.lastGrant, ev.TorrentID)
		fa.bus.Publish(NewFreeleechRevokedEvent(ev.TraceID(), ev.TorrentID))
		log.Printf("freeleech_actuator: revoked freeleech torrent=%d dead72h=%.3f",
			ev.TorrentID, ev.DeadProb72h)
		return
	}

	if ev.PriorityScore < fa.scoreThreshold {
		return
	}

	// Check cooldown to avoid redundant grants.
	last, exists := fa.lastGrant[ev.TorrentID]
	if exists && time.Since(last) < fa.cooldown {
		return
	}
	fa.lastGrant[ev.TorrentID] = time.Now()
	fa.activeGrants[ev.TorrentID] = true

	fa.siteComm.GrantFreeleech(ev.TorrentID)
	fa.bus.Publish(NewFreeleechGrantedEvent(ev.TraceID(), ev.TorrentID))
	log.Printf("freeleech_actuator: granted freeleech torrent=%d score=%.3f dead72h=%.3f",
		ev.TorrentID, ev.PriorityScore, ev.DeadProb72h)
}
