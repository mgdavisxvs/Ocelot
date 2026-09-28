package tracker

import (
	"context"
	"time"
)

// PreservationMonitor checks that archived objects maintain sufficient replica count.
type PreservationMonitor struct {
	worker      *Worker
	minReplicas int
	intervalSec int
}

// NewPreservationMonitor creates a PreservationMonitor.
func NewPreservationMonitor(w *Worker, minReplicas, intervalSec int) *PreservationMonitor {
	return &PreservationMonitor{
		worker:      w,
		minReplicas: minReplicas,
		intervalSec: intervalSec,
	}
}

// Start runs the preservation monitor in a background goroutine.
func (m *PreservationMonitor) Start(ctx context.Context) {
	go m.run(ctx)
}

func (m *PreservationMonitor) run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(m.intervalSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkReplicaHealth()
		}
	}
}

func (m *PreservationMonitor) checkReplicaHealth() {
	if m.minReplicas <= 0 {
		return
	}
	m.worker.Torrents.ForEach(func(hash string, t *Torrent) bool {
		seeders := t.Seeders.Size()
		if seeders < m.minReplicas {
			deficit := m.minReplicas - seeders
			m.worker.logSiteCommErr("replica_alert",
				m.worker.SiteComm.ReportAnomaly(
					int64(t.ID), float64(deficit)))
		}
		return true
	})
}
