package tracker

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/mgdavisxvs/Ocelot/ml"
)

// ThresholdAdapter defines the subset of ml.AnomalyDetector needed for adaptive
// threshold updates.  The concrete type is *ml.AnomalyDetector.
type ThresholdAdapter interface {
	SetThresholds(ml.ThresholdConfig)
}

// minPollsForStability is the number of Markov engine polls that must have
// elapsed between consecutive threshold applications (D-G3 stability gate).
// Prevents applying thresholds derived from an unconverged chain.
const minPollsForStability = 5

// AdaptiveThresholdPoller runs a background goroutine that periodically fetches
// population metrics from the Markov engine and adjusts anomaly-detection
// thresholds to match observed swarm behaviour.
//
// Stability gate (D-G3): thresholds are applied only when the Markov engine
// has completed at least minPollsForStability polls since the last application,
// preventing threshold churn from unconverged chain distributions.
//
// It returns immediately; cancel ctx to stop.
func AdaptiveThresholdPoller(ctx context.Context, client *MarkovClient, adapter ThresholdAdapter, intervalSec int) {
	var lastAppliedPollCount atomic.Int64
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
				// F-G2: publish convergence distances as Prometheus gauges so operators
				// can observe chain learning progress without querying the Markov API.
				markovConvergencePeer.Set(m.ConvergencePeer)
				markovConvergenceUser.Set(m.ConvergenceUser)
				markovConvergenceTorrent.Set(m.ConvergenceTorrent)

				// Stability gate: require at least minPollsForStability new polls.
				last := lastAppliedPollCount.Load()
				if m.PollCount-last < minPollsForStability {
					continue
				}
				cfg := thresholdsForPopulation(m.TrackedPeers)
				adapter.SetThresholds(cfg)
				lastAppliedPollCount.Store(m.PollCount)
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
