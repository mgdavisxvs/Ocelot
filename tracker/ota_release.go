package tracker

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// OTARelease describes a staged IoT firmware rollout.
type OTARelease struct {
	InfoHash    string `json:"info_hash"`
	FirmwareVer string `json:"firmware_ver"`
	HWTarget    string `json:"hw_target"`
	RolloutPct  uint8  `json:"rollout_pct"`
	HealthGate  int    `json:"health_gate"` // min swarm health score to continue
}

// EnsureOTAReleaseTable creates the ota_releases table if absent.
func EnsureOTAReleaseTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS ota_releases (
        info_hash    TEXT PRIMARY KEY,
        firmware_ver TEXT NOT NULL DEFAULT '',
        hw_target    TEXT NOT NULL DEFAULT '',
        rollout_pct  INTEGER NOT NULL DEFAULT 100,
        health_gate  INTEGER NOT NULL DEFAULT 2,
        created_at   INTEGER NOT NULL
    )`)
	return err
}

// handleOTARegister processes POST /ota/release — registers a firmware torrent.
func (s *Server) handleOTARegister(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req OTARelease
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InfoHash == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.RolloutPct == 0 {
		req.RolloutPct = 5 // start at 5% canary
	}
	if req.HealthGate == 0 {
		req.HealthGate = 2
	}

	t, ok := s.worker.Torrents.Get(req.InfoHash)
	if !ok {
		t = NewTorrent(TorrentID(hashToID(req.InfoHash)))
		s.worker.Torrents.Set(req.InfoHash, t)
	}
	t.FreeType = FreeFree
	t.RolloutPct = req.RolloutPct

	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO ota_releases
             (info_hash, firmware_ver, hw_target, rollout_pct, health_gate, created_at)
             VALUES (?, ?, ?, ?, ?, ?)`,
			req.InfoHash, req.FirmwareVer, req.HWTarget,
			req.RolloutPct, req.HealthGate, time.Now().Unix(),
		)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"info_hash":   req.InfoHash,
		"rollout_pct": req.RolloutPct,
	})
}

// onOTAComplete is called when an IoT device sends a "completed" announce event.
// It logs the device model version update via SiteComm.
func (w *Worker) onOTAComplete(req *AnnounceRequest) {
	// Port field carries device_type as a sentinel; UpdateStats logs the completion.
	w.logSiteCommErr("ota_complete",
		w.SiteComm.UpdateStats(int64(req.Port), 0, 1))
}

// checkOTAHealthGate returns an error string if swarm health is below the gate.
// Returns "" if the gate passes or no predictor is configured.
func (w *Worker) checkOTAHealthGate(infoHash string, gate int) string {
	if w.SwarmPredictor == nil || gate <= 0 {
		return ""
	}
	t, ok := w.Torrents.Get(infoHash)
	if !ok {
		return ""
	}
	score := w.SwarmPredictor.HealthScore(t.Seeders.Size(), t.Leechers.Size())
	if score < gate {
		return "swarm below health gate: OTA rollout paused"
	}
	return ""
}
