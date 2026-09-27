package tracker

import (
	"context"
	"time"

	"github.com/mgdavisxvs/Ocelot/ml"
)

// ThresholdAdapter defines the subset of ml.AnomalyDetector needed for adaptive
// threshold updates.  The concrete type is *ml.AnomalyDetector.
type ThresholdAdapter interface {
	SetThresholds(ml.ThresholdConfig)
}

// AdaptiveThresholdPoller runs a background goroutine that periodically fetches
// population metrics from the Markov engine and adjusts anomaly-detection
// thresholds to match observed swarm behaviour.
//
// Scaling heuristics:
//   - Large swarms (>50k peers) tolerate faster announce rates; relax by 25%.
//   - Very small swarms (<1k peers) tighten the announce rate by 25% to catch
//     ratio cheaters operating on thin cover.
//
// It returns immediately; cancel ctx to stop.
func AdaptiveThresholdPoller(ctx context.Context, client *MarkovClient, adapter ThresholdAdapter, intervalSec int) {
	go func() {
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m, err := client.Metrics(ctx)
				if err != nil {
					continue
				}
				cfg := thresholdsForPopulation(m.TrackedPeers)
				adapter.SetThresholds(cfg)
			}
		}
	}()
}

// baseMaxAnnounceRate is the factory default used by ml.NewAnomalyDetector().
const baseMaxAnnounceRate = 100

// maxThresholdMultiplier caps adaptive relaxation at 3× the base value to
// prevent the Markov→τ→Markov self-calibration loop from diverging.
// RULING-04 mitigation: τ ≤ maxThresholdMultiplier × τ_base.
const maxThresholdMultiplier = 3.0

// thresholdsForPopulation derives threshold adjustments from the live peer count.
func thresholdsForPopulation(peers int64) ml.ThresholdConfig {
	var rate int
	switch {
	case peers > 50_000:
		// Relax announce-rate threshold for large swarms (+25%).
		rate = int(float64(baseMaxAnnounceRate) * 1.25)
	case peers < 1_000:
		// Tighten for thin-cover swarms (−25%).
		rate = int(float64(baseMaxAnnounceRate) * 0.75)
	default:
		return ml.ThresholdConfig{} // no change
	}
	// Apply convergence bound: never exceed maxThresholdMultiplier × base.
	max := int(float64(baseMaxAnnounceRate) * maxThresholdMultiplier)
	if rate > max {
		rate = max
	}
	if rate < 1 {
		rate = 1
	}
	return ml.ThresholdConfig{MaxAnnounceRate: rate}
}
