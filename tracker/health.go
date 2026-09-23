package tracker

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// TorrentHealthStatus classifies a torrent's swarm health.
type TorrentHealthStatus string

const (
	HealthDark     TorrentHealthStatus = "dark"     // active leechers, zero seeders
	HealthSPOF     TorrentHealthStatus = "spof"     // single seeder on a snatched torrent
	HealthDegraded TorrentHealthStatus = "degraded" // seeders below MinReplicas floor
	HealthOK       TorrentHealthStatus = "ok"
)

// TorrentHealthRecord describes a single torrent's health at scan time.
type TorrentHealthRecord struct {
	InfoHash    string              `json:"info_hash"`
	TorrentID   TorrentID           `json:"torrent_id"`
	Seeders     int                 `json:"seeders"`
	Leechers    int                 `json:"leechers"`
	Completed   uint32              `json:"completed"`
	MinReplicas uint32              `json:"min_replicas,omitempty"`
	Status      TorrentHealthStatus `json:"status"`
}

// SwarmHealthSnapshot is the output of one full catalog scan.
type SwarmHealthSnapshot struct {
	ScannedAt     time.Time             `json:"scanned_at"`
	TotalTorrents int                   `json:"total_torrents"`
	HealthyCount  int                   `json:"healthy_count"`
	Dark          []TorrentHealthRecord `json:"dark"`
	SPOF          []TorrentHealthRecord `json:"spof"`
	Degraded      []TorrentHealthRecord `json:"degraded"`
}

// SwarmHealthDaemon scans the torrent catalog on a fixed interval and:
//   - publishes EventTorrentHealth to the event bus each cycle
//   - serves the latest snapshot at /health/swarms
//
// Classification rules (evaluated in priority order):
//  1. dark     — leechers > 0 AND seeders == 0  (active swarm with no supply)
//  2. spof     — seeders == 1 AND completed > 0 (proven-value torrent on one seed)
//  3. degraded — MinReplicas > 0 AND seeders < MinReplicas (below replica floor)
//  4. ok       — everything else
type SwarmHealthDaemon struct {
	worker   *Worker
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
	latest   atomic.Pointer[SwarmHealthSnapshot]
}

func NewSwarmHealthDaemon(worker *Worker, interval time.Duration) *SwarmHealthDaemon {
	return &SwarmHealthDaemon{
		worker:   worker,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

func (d *SwarmHealthDaemon) Start() {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(d.interval)
		defer ticker.Stop()
		d.scan() // initial scan on start
		for {
			select {
			case <-d.stopCh:
				return
			case <-ticker.C:
				d.scan()
			}
		}
	}()
}

func (d *SwarmHealthDaemon) Stop() {
	close(d.stopCh)
	d.wg.Wait()
}

// Latest returns the most recent snapshot, or nil before the first scan completes.
func (d *SwarmHealthDaemon) Latest() *SwarmHealthSnapshot {
	return d.latest.Load()
}

func (d *SwarmHealthDaemon) scan() {
	snap := &SwarmHealthSnapshot{
		ScannedAt: time.Now(),
		Dark:      make([]TorrentHealthRecord, 0),
		SPOF:      make([]TorrentHealthRecord, 0),
		Degraded:  make([]TorrentHealthRecord, 0),
	}

	d.worker.Torrents.ForEach(func(hash string, t *Torrent) bool {
		snap.TotalTorrents++

		t.mu.RLock()
		seeders := t.Seeders.Size()
		leechers := t.Leechers.Size()
		completed := t.Completed
		minReplicas := t.MinReplicas
		t.mu.RUnlock()

		rec := TorrentHealthRecord{
			InfoHash:    hash,
			TorrentID:   t.ID,
			Seeders:     seeders,
			Leechers:    leechers,
			Completed:   completed,
			MinReplicas: minReplicas,
		}

		switch {
		case seeders == 0 && leechers > 0:
			rec.Status = HealthDark
			snap.Dark = append(snap.Dark, rec)
		case seeders == 1 && completed > 0:
			rec.Status = HealthSPOF
			snap.SPOF = append(snap.SPOF, rec)
		case minReplicas > 0 && uint32(seeders) < minReplicas:
			rec.Status = HealthDegraded
			snap.Degraded = append(snap.Degraded, rec)
		default:
			rec.Status = HealthOK
			snap.HealthyCount++
		}
		return true
	})

	d.latest.Store(snap)
	d.worker.publish(BusEvent{
		Type:    EventTorrentHealth,
		Payload: snap,
		Time:    snap.ScannedAt,
	})
}

// SwarmHealthHandler serves GET /health/swarms — returns the latest scan as JSON.
func SwarmHealthHandler(daemon *SwarmHealthDaemon) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := daemon.Latest()
		if snap == nil {
			http.Error(w, "no scan completed yet", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snap) //nolint:errcheck
	}
}
