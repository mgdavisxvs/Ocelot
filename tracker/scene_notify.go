package tracker

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// EnsureSceneRegistryTable creates the scene_registry table if absent.
func EnsureSceneRegistryTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS scene_registry (
		scene_ref   TEXT PRIMARY KEY,
		info_hash   TEXT UNIQUE NOT NULL,
		license     TEXT NOT NULL DEFAULT 'proprietary',
		size_bytes  INTEGER NOT NULL DEFAULT 0,
		created_at  INTEGER NOT NULL
	)`)
	return err
}

// publishSceneReady publishes a Redis pub/sub event when the first seeder arrives.
func (w *Worker) publishSceneReady(sceneRef string) {
	if w.Redis == nil {
		return
	}
	w.logSiteCommErr("scene_ready_publish",
		w.Redis.PublishUpdate("scene_ready", map[string]string{
			"scene_ref": sceneRef,
		}))
}

// handleSceneRegister processes POST /imagery/scene/register.
func (s *Server) handleSceneRegister(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req struct {
		SceneRef  string `json:"scene_ref"`
		InfoHash  string `json:"info_hash"`
		License   string `json:"license"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SceneRef == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.InfoHash == "" {
		req.InfoHash = sceneInfoHash(req.SceneRef)
	}
	if req.License == "" {
		req.License = "proprietary"
	}

	t, ok := s.worker.Torrents.Get(req.InfoHash)
	if !ok {
		t = NewTorrent(TorrentID(hashToID(req.InfoHash)))
		t.Size = req.SizeBytes
		t.Category = CategoryImagery
		// Open-licensed imagery (Copernicus, Landsat) is freeleech.
		if req.License == "open" {
			t.FreeType = FreeFree
		}
		s.worker.Torrents.Set(req.InfoHash, t)
	}

	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO scene_registry
			 (scene_ref, info_hash, license, size_bytes, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			req.SceneRef, req.InfoHash, req.License, req.SizeBytes, time.Now().Unix(),
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "info_hash": req.InfoHash})
}

// handleSceneResolve processes GET /imagery/scene/resolve?scene_ref=<r>.
func (s *Server) handleSceneResolve(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	sceneRef := r.URL.Query().Get("scene_ref")
	if sceneRef == "" {
		http.Error(w, `{"error":"missing scene_ref"}`, http.StatusBadRequest)
		return
	}
	var infoHash string
	var sizeBytes int64
	if db != nil {
		db.QueryRow(
			`SELECT info_hash, size_bytes FROM scene_registry WHERE scene_ref=?`, sceneRef,
		).Scan(&infoHash, &sizeBytes)
	}
	if infoHash == "" {
		infoHash = sceneInfoHash(sceneRef)
	}
	seeders, leechers := 0, 0
	if t, ok := s.worker.Torrents.Get(infoHash); ok {
		seeders = t.Seeders.Size()
		leechers = t.Leechers.Size()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"scene_ref":   sceneRef,
		"info_hash":   infoHash,
		"size_bytes":  sizeBytes,
		"seeders":     seeders,
		"leechers":    leechers,
		"tracker_url": "/announce",
	})
}

// sceneInfoHash returns a deterministic hex-encoded info_hash for a scene reference.
// Format: sha256("scene:" + sceneRef), hex-encoded.
func sceneInfoHash(sceneRef string) string {
	h := sha256.Sum256([]byte("scene:" + sceneRef))
	return hex.EncodeToString(h[:])
}
