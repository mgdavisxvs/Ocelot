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

// thresholdsForPopulation derives threshold adjustments from the live peer count.
func thresholdsForPopulation(peers int64) ml.ThresholdConfig {
	switch {
	case peers > 50_000:
		// Relax announce-rate threshold for large swarms.
		return ml.ThresholdConfig{MaxAnnounceRate: 125}
	case peers < 1_000:
		// Tighten for thin-cover swarms.
		return ml.ThresholdConfig{MaxAnnounceRate: 75}
	default:
		return ml.ThresholdConfig{} // no change
	}
}
