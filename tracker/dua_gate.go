package tracker

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// EnsureDUATable creates the dua_grants table if absent.
func EnsureDUATable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS dua_grants (
		user_id     INTEGER NOT NULL,
		dataset_id  TEXT NOT NULL,
		approved    INTEGER NOT NULL DEFAULT 0,
		approved_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user_id, dataset_id)
	)`)
	return err
}

// checkDUA returns true if the user has an approved DUA for the given dataset.
// Returns true (permissive) when no DB is configured.
func checkDUA(db *sql.DB, userID UserID, infoHash string) bool {
	if db == nil {
		return true
	}
	var approved int
	err := db.QueryRow(
		`SELECT approved FROM dua_grants WHERE user_id=? AND dataset_id=?`,
		userID, infoHash,
	).Scan(&approved)
	if err != nil {
		return false
	}
	return approved == 1
}

// handleDUAGrant processes POST /admin/dua/grant — approves a DUA.
func (s *Server) handleDUAGrant(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req struct {
		UserID    uint32 `json:"user_id"`
		DatasetID string `json:"dataset_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DatasetID == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO dua_grants (user_id, dataset_id, approved, approved_at)
			 VALUES (?, ?, 1, ?)`,
			req.UserID, req.DatasetID, time.Now().Unix(),
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleDUARevoke processes POST /admin/dua/revoke — revokes a DUA.
func (s *Server) handleDUARevoke(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req struct {
		UserID    uint32 `json:"user_id"`
		DatasetID string `json:"dataset_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DatasetID == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if db != nil {
		db.Exec(
			`UPDATE dua_grants SET approved=0 WHERE user_id=? AND dataset_id=?`,
			req.UserID, req.DatasetID,
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
