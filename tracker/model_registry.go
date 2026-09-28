package tracker

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ModelRegistryEntry maps a model ID to a torrent info_hash.
type ModelRegistryEntry struct {
	ModelID   string `json:"model_id"`
	InfoHash  string `json:"info_hash"`
	SizeBytes int64  `json:"size_bytes"`
	Version   string `json:"version"`
}

// EnsureModelRegistryTable creates the model_registry table if absent.
func EnsureModelRegistryTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_registry (
		model_id    TEXT PRIMARY KEY,
		info_hash   TEXT UNIQUE NOT NULL,
		size_bytes  INTEGER NOT NULL DEFAULT 0,
		version     TEXT NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL
	)`)
	return err
}

// ModelInfoHash derives a canonical info_hash from a model_id string.
func ModelInfoHash(modelID string) string {
	h := sha256.Sum256([]byte(modelID))
	return hex.EncodeToString(h[:])
}

// handleModelRegister processes POST /model/register — registers a model version.
func (s *Server) handleModelRegister(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var entry ModelRegistryEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if entry.ModelID == "" {
		http.Error(w, `{"error":"missing model_id"}`, http.StatusBadRequest)
		return
	}
	if entry.InfoHash == "" {
		entry.InfoHash = ModelInfoHash(entry.ModelID)
	}

	// Register torrent in memory.
	if _, ok := s.worker.Torrents.Get(entry.InfoHash); !ok {
		t := NewTorrent(TorrentID(hashToID(entry.InfoHash)))
		t.Size = entry.SizeBytes
		t.FreeType = FreeFree // ML datasets are always free
		s.worker.Torrents.Set(entry.InfoHash, t)
	}

	// Persist.
	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO model_registry
			 (model_id, info_hash, size_bytes, version, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			entry.ModelID, entry.InfoHash, entry.SizeBytes,
			entry.Version, time.Now().Unix(),
		)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"info_hash": entry.InfoHash,
	})
}

// handleModelETA processes GET /model/eta?model_ref=<hash> — returns ETA.
func (s *Server) handleModelETA(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("model_ref")
	if ref == "" {
		http.Error(w, `{"error":"missing model_ref"}`, http.StatusBadRequest)
		return
	}
	t, ok := s.worker.Torrents.Get(ref)
	if !ok {
		http.Error(w, `{"error":"torrent not found"}`, http.StatusNotFound)
		return
	}
	seeders := t.Seeders.Size()
	leechers := t.Leechers.Size()
	var etaSec float64
	if s.worker.SwarmPredictor != nil && t.Size > 0 {
		const avgUploadSpeedBytesPerSec = 50 * 1024 * 1024 // 50 MB/s assumed
		eta := s.worker.SwarmPredictor.PredictCompletionTime(
			seeders, leechers, avgUploadSpeedBytesPerSec, float64(t.Size))
		etaSec = eta.Seconds()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"model_ref":   ref,
		"seeders":     seeders,
		"leechers":    leechers,
		"eta_seconds": etaSec,
	})
}

// handleModelManifest processes GET /model/manifest?model_id=<id> — resolves info_hash.
func (s *Server) handleModelManifest(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	modelID := r.URL.Query().Get("model_id")
	if modelID == "" {
		http.Error(w, `{"error":"missing model_id"}`, http.StatusBadRequest)
		return
	}
	var infoHash string
	var sizeBytes int64
	var version string
	if db != nil {
		row := db.QueryRow(
			`SELECT info_hash, size_bytes, version FROM model_registry WHERE model_id=?`,
			modelID,
		)
		if err := row.Scan(&infoHash, &sizeBytes, &version); err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
				return
			}
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
	} else {
		infoHash = ModelInfoHash(modelID)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"model_id":    modelID,
		"info_hash":   infoHash,
		"size_bytes":  sizeBytes,
		"version":     version,
		"tracker_url": "/announce",
	})
}
