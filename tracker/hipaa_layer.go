package tracker

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// EnsureBAATable creates the baa_grants table if absent.
func EnsureBAATable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS baa_grants (
        user_id     INTEGER NOT NULL,
        dataset_id  TEXT NOT NULL,
        approved    INTEGER NOT NULL DEFAULT 0,
        approved_at INTEGER NOT NULL DEFAULT 0,
        PRIMARY KEY (user_id, dataset_id)
    )`)
	return err
}

// checkBAA returns true if the user has an approved BAA for the dataset.
func checkBAA(db *sql.DB, userID UserID, infoHash string) bool {
	if db == nil {
		return true
	}
	var approved int
	err := db.QueryRow(
		`SELECT approved FROM baa_grants WHERE user_id=? AND dataset_id=?`,
		userID, infoHash,
	).Scan(&approved)
	if err != nil {
		return false
	}
	return approved == 1
}

// handleBAAGrant processes POST /admin/baa/grant.
func (s *Server) handleBAAGrant(w http.ResponseWriter, r *http.Request, db *sql.DB) {
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
			`INSERT OR REPLACE INTO baa_grants (user_id, dataset_id, approved, approved_at)
             VALUES (?, ?, 1, ?)`,
			req.UserID, req.DatasetID, time.Now().Unix(),
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleBAARevoke processes POST /admin/baa/revoke.
func (s *Server) handleBAARevoke(w http.ResponseWriter, r *http.Request, db *sql.DB) {
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
			`UPDATE baa_grants SET approved=0 WHERE user_id=? AND dataset_id=?`,
			req.UserID, req.DatasetID,
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// stripPeerIPs returns a zeroed-IP compact peer list for HIPAA compliance.
// All IP bytes are replaced with 0.0.0.0 to prevent IP address logging.
func stripPeerIPs(compact []byte) []byte {
	out := make([]byte, len(compact))
	copy(out, compact)
	for i := 0; i+5 < len(out); i += 6 {
		out[i] = 0
		out[i+1] = 0
		out[i+2] = 0
		out[i+3] = 0
		// port bytes [i+4], [i+5] are kept for connectivity
	}
	return out
}
