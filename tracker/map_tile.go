package tracker

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// EnsureMapTileTable creates the map_tiles table if absent.
func EnsureMapTileTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS map_tiles (
        tile_key    TEXT PRIMARY KEY,
        info_hash   TEXT UNIQUE NOT NULL,
        map_version TEXT NOT NULL DEFAULT '',
        created_at  INTEGER NOT NULL
    )`)
	return err
}

// mapTileKey returns the canonical key for a tile.
func mapTileKey(lat, lon int, version string) string {
	return fmt.Sprintf("%d_%d_%s", lat, lon, version)
}

// mapTileInfoHash derives an info_hash from a tile key.
func mapTileInfoHash(key string) string {
	h := sha256.Sum256([]byte("maptile:" + key))
	return hex.EncodeToString(h[:])
}

// handleMapTileResolve processes GET /map/tile?lat=<n>&lon=<n>&ver=<v>.
func (s *Server) handleMapTileResolve(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	lat := r.URL.Query().Get("lat")
	lon := r.URL.Query().Get("lon")
	ver := r.URL.Query().Get("ver")
	if lat == "" || lon == "" {
		http.Error(w, `{"error":"missing lat/lon"}`, http.StatusBadRequest)
		return
	}
	key := mapTileKey(parseInt(lat), parseInt(lon), ver)
	infoHash := mapTileInfoHash(key)

	if _, ok := s.worker.Torrents.Get(infoHash); !ok {
		t := NewTorrent(TorrentID(hashToID(infoHash)))
		t.FreeType = FreeFree
		s.worker.Torrents.Set(infoHash, t)
	}
	if db != nil {
		db.Exec(
			`INSERT OR IGNORE INTO map_tiles (tile_key, info_hash, map_version, created_at)
             VALUES (?, ?, ?, ?)`,
			key, infoHash, ver, time.Now().Unix(),
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"tile_key":    key,
		"info_hash":   infoHash,
		"tracker_url": "/announce",
	})
}

// handleVehicleAttest processes POST /admin/vehicle/attest — sets health attestation.
func (s *Server) handleVehicleAttest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Passkey string `json:"passkey"`
		Healthy bool   `json:"healthy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Passkey == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	u, ok := s.worker.Users.Get(req.Passkey)
	if !ok {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	atomic.StoreUint32(&u.HealthAttestation, boolToUint32(req.Healthy))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func parseInt(s string) int {
	v := 0
	neg := false
	start := 0
	if len(s) > 0 && s[0] == '-' {
		neg = true
		start = 1
	}
	for i := start; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			v = v*10 + int(s[i]-'0')
		}
	}
	if neg {
		return -v
	}
	return v
}

func boolToUint32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}
