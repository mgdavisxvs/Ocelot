package tracker

import (
	"context"
	"log"
	"sync"
	"time"
)

// freeleechEngineDefaults controls the engine's sensitivity.  These can be
// overridden before calling NewFreeleechEngine.
const (
	freeleechDefaultDeadProbThreshold = 0.55 // dead-probability-in-72h trigger
	freeleechDefaultDemandSurge       = 0.15 // minimum DP-smoothed net-fraction to be "surging"
	freeleechDefaultMinHours          = 2
	freeleechDefaultMaxHours          = 24
	freeleechDefaultCooldown          = 4 * time.Hour
)

// FreeleechEngine (C-03) combines the DemandHeatmap's differential-private
// demand signal with the MarkovShadowCache's dead-probability forecast to
// autonomously trigger freeleech windows.
//
// Logic per tick:
//  1. Pull the modal /16 net from the heatmap; compute its DP-smoothed fraction.
//  2. For each Markov candidate: if demand_surge ∧ dead_prob_72h > threshold,
//     derive a freeleech duration proportional to the decay rate and call
//     SiteComm.NotifyFreeleech.
//  3. Emit FreeleechGrantedEvent on the bus for observability.
//
// Safe for concurrent use.
type FreeleechEngine struct {
	heatmap  *DemandHeatmap
	cache    *MarkovShadowCache
	siteComm SiteCommInterface
	bus      *Bus

	dpEpsilon         float64
	deadProbThreshold float64
	demandSurge       float64
	cooldown          time.Duration

	mu        sync.Mutex
	lastGrant map[TorrentID]time.Time
}

// NewFreeleechEngine creates the engine. dpEpsilon is forwarded to
// DemandHeatmap.NetFractionDP; pass 0 to disable differential privacy.
func NewFreeleechEngine(
	heatmap *DemandHeatmap,
	cache *MarkovShadowCache,
	siteComm SiteCommInterface,
	bus *Bus,
	dpEpsilon float64,
) *FreeleechEngine {
	return &FreeleechEngine{
		heatmap:           heatmap,
		cache:             cache,
		siteComm:          siteComm,
		bus:               bus,
		dpEpsilon:         dpEpsilon,
		deadProbThreshold: freeleechDefaultDeadProbThreshold,
		demandSurge:       freeleechDefaultDemandSurge,
		cooldown:          freeleechDefaultCooldown,
		lastGrant:         make(map[TorrentID]time.Time),
	}
}

// Start launches the background evaluation loop. Cancel ctx to stop it.
func (e *FreeleechEngine) Start(ctx context.Context, intervalSec int) {
	if intervalSec <= 0 {
		intervalSec = 300
	}
	go func() {
		e.evaluate()
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.evaluate()
			}
		}
	}()
}

// evaluate runs one assessment cycle.
func (e *FreeleechEngine) evaluate() {
	// Step 1: demand signal — modal /16 net fraction with DP.
	modalNet, ok := e.heatmap.ModalNet()
	if !ok {
		return
	}
	var fraction float64
	if e.dpEpsilon > 0 {
		fraction = e.heatmap.NetFractionDP(modalNet, e.dpEpsilon)
	} else {
		fraction = e.heatmap.NetFraction(modalNet)
	}
	if fraction < e.demandSurge {
		return
	}

	// Step 2: iterate Markov candidates.
	candidates := e.cache.Candidates()
	now := time.Now()

	for _, c := range candidates {
		if c.DeadProb72h < e.deadProbThreshold {
			continue
		}
		tid := TorrentID(c.TorrentID)

		e.mu.Lock()
		lastGrant, wasGranted := e.lastGrant[tid]
		if wasGranted && now.Sub(lastGrant) < e.cooldown {
			e.mu.Unlock()
			continue
		}
		e.lastGrant[tid] = now
		e.mu.Unlock()

		// Duration proportional to decay: higher dead_prob → longer freeleech.
		hours := freeleechHours(c.DeadProb72h)
		if err := e.siteComm.NotifyFreeleech(c.TorrentID, hours); err != nil {
			log.Printf("freeleech_engine: NotifyFreeleech torrent=%d err=%v", c.TorrentID, err)
			continue
		}
		if e.bus != nil {
			e.bus.Publish(NewFreeleechGrantedEvent("freeleech_engine", tid))
		}
	}
}

// freeleechHours derives a freeleech window from the dead-probability score.
// Maps [0.55, 1.0] → [2, 24] hours linearly.
func freeleechHours(deadProb float64) int {
	lo, hi := freeleechDefaultDeadProbThreshold, 1.0
	t := (deadProb - lo) / (hi - lo)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	hours := freeleechDefaultMinHours + int(t*float64(freeleechDefaultMaxHours-freeleechDefaultMinHours))
	return hours
}
