package tracker

import (
	"context"
	"time"
)

// SLAMonitor checks that all members of a feed torrent announce within SLA.
type SLAMonitor struct {
	worker      *Worker
	slaSec      int // maximum seconds between announces before alert
	intervalSec int
}

// NewSLAMonitor creates a SLAMonitor.
func NewSLAMonitor(w *Worker, slaSec, intervalSec int) *SLAMonitor {
	return &SLAMonitor{worker: w, slaSec: slaSec, intervalSec: intervalSec}
}

// Start runs the SLA monitor in a background goroutine until ctx is cancelled.
func (m *SLAMonitor) Start(ctx context.Context) {
	go m.run(ctx)
}

func (m *SLAMonitor) run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(m.intervalSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkFeedSLAs()
		}
	}
}

func (m *SLAMonitor) checkFeedSLAs() {
	if m.slaSec <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(m.slaSec) * time.Second)
	m.worker.Torrents.ForEach(func(hash string, t *Torrent) bool {
		t.Leechers.ForEach(func(_ string, p *Peer) bool {
			if p.LastAnnounced.Before(cutoff) {
				// Member is behind SLA — report via SiteComm.
				m.worker.logSiteCommErr("sla_breach",
					m.worker.SiteComm.ReportAnomaly(int64(p.UserID), 0.5))
			}
			return true
		})
		return true
	})
}
