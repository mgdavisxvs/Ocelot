package tracker

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// PatchRelease describes a staged software/game patch rollout.
type PatchRelease struct {
	InfoHash   string   `json:"info_hash"`
	Version    string   `json:"version"`
	FreeType   FreeType `json:"free_type"`
	RolloutPct uint8    `json:"rollout_pct"` // 0-100
}

// EnsurePatchTable creates the patches table if absent.
func EnsurePatchTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS patches (
		info_hash   TEXT PRIMARY KEY,
		version     TEXT NOT NULL DEFAULT '',
		rollout_pct INTEGER NOT NULL DEFAULT 100,
		created_at  INTEGER NOT NULL
	)`)
	return err
}

// handlePatchRelease processes POST /patch/release — registers a patch torrent
// and applies rollout + freeleech settings.
func (s *Server) handlePatchRelease(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req PatchRelease
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.InfoHash == "" {
		http.Error(w, `{"error":"missing info_hash"}`, http.StatusBadRequest)
		return
	}
	if req.RolloutPct == 0 {
		req.RolloutPct = 100
	}

	// Register or update torrent in memory.
	t, ok := s.worker.Torrents.Get(req.InfoHash)
	if !ok {
		t = NewTorrent(TorrentID(hashToID(req.InfoHash)))
		s.worker.Torrents.Set(req.InfoHash, t)
	}
	t.FreeType = req.FreeType
	t.RolloutPct = req.RolloutPct

	// Persist to patches table.
	if db != nil {
		_, err := db.Exec(
			`INSERT OR REPLACE INTO patches (info_hash, version, rollout_pct, created_at)
			 VALUES (?, ?, ?, ?)`,
			req.InfoHash, req.Version, req.RolloutPct, time.Now().Unix(),
		)
		if err != nil {
			GetDefaultLogger().Warn("patch_release: db insert", "err", err)
		}
	}

	// Notify site about freeleech activation.
	if req.FreeType != FreeNormal {
		s.worker.logSiteCommErr("patch_freeleech",
			s.worker.SiteComm.NotifyFreeleech(int64(t.ID),
				s.worker.Config.FreeleechNotifyHours))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"info_hash":   req.InfoHash,
		"rollout_pct": req.RolloutPct,
	})
}

// handlePatchRolloutAdvance advances the rollout percentage for a patch.
// POST /patch/rollout?info_hash=<h>&pct=<n>
func (s *Server) handlePatchRolloutAdvance(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	hash := r.URL.Query().Get("info_hash")
	pctStr := r.URL.Query().Get("pct")
	pct64, err := strconv.ParseUint(pctStr, 10, 8)
	if err != nil || hash == "" {
		http.Error(w, `{"error":"missing info_hash or invalid pct"}`, http.StatusBadRequest)
		return
	}
	pct := uint8(pct64)
	t, ok := s.worker.Torrents.Get(hash)
	if !ok {
		http.Error(w, `{"error":"torrent not found"}`, http.StatusNotFound)
		return
	}
	t.RolloutPct = pct
	if db != nil {
		db.Exec(`UPDATE patches SET rollout_pct=? WHERE info_hash=?`, pct, hash)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "rollout_pct": pct})
}

// checkRolloutGate returns true if the user is in the rollout cohort.
// Gate is deterministic per user: user.ID % 100 < pct.
func checkRolloutGate(user *User, pct uint8) bool {
	if pct >= 100 {
		return true
	}
	return uint32(user.ID)%100 < uint32(pct)
}

// hashToID derives a uint32 TorrentID from an info_hash string (first 4 bytes).
func hashToID(h string) uint32 {
	var id uint32
	for i := 0; i < len(h) && i < 4; i++ {
		id = id<<8 | uint32(h[i])
	}
	return id
}

// patchRolloutGateErr is returned when a user is not in the rollout cohort.
var patchRolloutGateErr = fmt.Errorf("not in rollout cohort")
