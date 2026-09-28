package tracker

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// EnsureCIArtifactTable creates the ci_artifacts table if absent.
func EnsureCIArtifactTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS ci_artifacts (
        pipeline_id  TEXT NOT NULL,
        git_sha      TEXT NOT NULL,
        info_hash    TEXT UNIQUE NOT NULL,
        ttl_seconds  INTEGER NOT NULL DEFAULT 14400,
        created_at   INTEGER NOT NULL,
        PRIMARY KEY (pipeline_id, git_sha)
    )`)
	return err
}

// handleCIArtifactRegister processes POST /ci/artifact/register.
func (s *Server) handleCIArtifactRegister(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req struct {
		PipelineID string `json:"pipeline_id"`
		GitSHA     string `json:"git_sha"`
		InfoHash   string `json:"info_hash"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InfoHash == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.TTLSeconds == 0 {
		req.TTLSeconds = 14400 // 4 hours default
	}

	t, ok := s.worker.Torrents.Get(req.InfoHash)
	if !ok {
		t = NewTorrent(TorrentID(hashToID(req.InfoHash)))
		s.worker.Torrents.Set(req.InfoHash, t)
	}
	t.TTLSeconds = req.TTLSeconds
	t.CreatedAt = time.Now()

	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO ci_artifacts
             (pipeline_id, git_sha, info_hash, ttl_seconds, created_at)
             VALUES (?, ?, ?, ?, ?)`,
			req.PipelineID, req.GitSHA, req.InfoHash,
			req.TTLSeconds, time.Now().Unix(),
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "info_hash": req.InfoHash})
}

// handleCIArtifactReady processes GET /ci/artifact/ready?pipeline_id=<id>&git_sha=<s>.
// Returns true when the build node has started seeding (seeder_count >= 1).
func (s *Server) handleCIArtifactReady(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	pipelineID := r.URL.Query().Get("pipeline_id")
	gitSHA := r.URL.Query().Get("git_sha")
	if pipelineID == "" && gitSHA == "" {
		http.Error(w, `{"error":"missing pipeline_id or git_sha"}`, http.StatusBadRequest)
		return
	}
	var infoHash string
	if db != nil {
		err := db.QueryRow(
			`SELECT info_hash FROM ci_artifacts
             WHERE pipeline_id=? AND git_sha=?`,
			pipelineID, gitSHA,
		).Scan(&infoHash)
		if err == sql.ErrNoRows {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"ready": false})
			return
		}
	}
	ready := false
	if infoHash != "" {
		if t, ok := s.worker.Torrents.Get(infoHash); ok {
			ready = t.Seeders.Size() >= 1
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ready":     ready,
		"info_hash": infoHash,
	})
}

// purgeCIArtifacts removes CI artifact torrents past their TTL.
// Called by Scheduler on each tick.
func PurgeCIArtifacts(db *sql.DB, torrents *TorrentList) {
	if db == nil {
		return
	}
	now := time.Now().Unix()
	rows, err := db.Query(
		`SELECT info_hash FROM ci_artifacts
         WHERE ttl_seconds > 0 AND created_at + ttl_seconds < ?`, now,
	)
	if err != nil {
		return
	}
	defer rows.Close()
	var expired []string
	for rows.Next() {
		var h string
		if rows.Scan(&h) == nil {
			expired = append(expired, h)
		}
	}
	for _, h := range expired {
		torrents.Delete(h)
		db.Exec(`DELETE FROM ci_artifacts WHERE info_hash=?`, h)
	}
}
