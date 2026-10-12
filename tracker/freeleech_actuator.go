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
type FreeleechActuator struct {
	siteComm SiteCommInterface
	bus      *Bus

	mu        sync.Mutex
	lastGrant map[TorrentID]time.Time // prevent re-granting within cooldown

	scoreThreshold float64
	cooldown       time.Duration
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
		siteComm:       siteComm,
		bus:            bus,
		lastGrant:      make(map[TorrentID]time.Time),
		scoreThreshold: threshold,
		cooldown:       6 * time.Hour,
	}
	bus.Subscribe("freeleech.recommended", fa.onRecommended)
	return fa
}

func (fa *FreeleechActuator) onRecommended(e Event) {
	ev, ok := e.(*FreeleechRecommendedEvent)
	if !ok {
		return
	}

	if ev.PriorityScore < fa.scoreThreshold {
		return
	}

	// Check cooldown to avoid redundant grants.
	fa.mu.Lock()
	last, exists := fa.lastGrant[ev.TorrentID]
	if exists && time.Since(last) < fa.cooldown {
		fa.mu.Unlock()
		return
	}
	fa.lastGrant[ev.TorrentID] = time.Now()
	fa.mu.Unlock()

	fa.siteComm.GrantFreeleech(ev.TorrentID)
	fa.bus.Publish(NewFreeleechGrantedEvent(ev.TraceID(), ev.TorrentID))
	log.Printf("freeleech_actuator: granted freeleech torrent=%d score=%.3f dead72h=%.3f",
		ev.TorrentID, ev.PriorityScore, ev.DeadProb72h)
}
