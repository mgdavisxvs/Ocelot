package tracker

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// EnsurePackageRegistryTable creates the package_registry table if absent.
func EnsurePackageRegistryTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS package_registry (
        package_ref  TEXT PRIMARY KEY,
        info_hash    TEXT UNIQUE NOT NULL,
        publisher_id INTEGER NOT NULL DEFAULT 0,
        published_at INTEGER NOT NULL
    )`)
	return err
}

// packageInfoHash derives a deterministic info_hash from a package reference.
func packageInfoHash(ref string) string {
	h := sha256.Sum256([]byte("pkg:" + ref))
	return hex.EncodeToString(h[:])
}

// handlePkgResolve processes GET /packages/resolve?name=<n>&version=<v>.
func (s *Server) handlePkgResolve(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	name := r.URL.Query().Get("name")
	version := r.URL.Query().Get("version")
	if name == "" {
		http.Error(w, `{"error":"missing name"}`, http.StatusBadRequest)
		return
	}
	ref := name
	if version != "" {
		ref = name + "@" + version
	}
	var infoHash string
	if db != nil {
		err := db.QueryRow(
			`SELECT info_hash FROM package_registry WHERE package_ref=?`, ref,
		).Scan(&infoHash)
		if err == sql.ErrNoRows {
			http.Error(w, `{"error":"package not found"}`, http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, `{"error":"db error"}`, http.StatusInternalServerError)
			return
		}
	} else {
		infoHash = packageInfoHash(ref)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"package_ref":  ref,
		"info_hash":    infoHash,
		"announce_url": "/announce",
		"tracker_url":  "/announce",
	})
}

// handlePkgPublish processes POST /packages/publish — CI pipeline registers a package.
func (s *Server) handlePkgPublish(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		InfoHash    string `json:"info_hash"`
		PublisherID uint32 `json:"publisher_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	ref := req.Name + "@" + req.Version
	if req.InfoHash == "" {
		req.InfoHash = packageInfoHash(ref)
	}

	// Register torrent in memory.
	if _, ok := s.worker.Torrents.Get(req.InfoHash); !ok {
		t := NewTorrent(TorrentID(hashToID(req.InfoHash)))
		s.worker.Torrents.Set(req.InfoHash, t)
	}

	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO package_registry
             (package_ref, info_hash, publisher_id, published_at)
             VALUES (?, ?, ?, ?)`,
			ref, req.InfoHash, req.PublisherID, time.Now().Unix(),
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "info_hash": req.InfoHash})
}
