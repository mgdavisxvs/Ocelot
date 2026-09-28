package tracker

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"time"
)

// EnsureContainerLayerTable creates the container_layers table if absent.
func EnsureContainerLayerTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS container_layers (
        digest      TEXT PRIMARY KEY,
        info_hash   TEXT UNIQUE NOT NULL,
        size_bytes  INTEGER NOT NULL DEFAULT 0,
        created_at  INTEGER NOT NULL
    )`)
	return err
}

// containerLayerInfoHash maps an OCI layer digest to a BT info_hash.
func containerLayerInfoHash(digest string) string {
	h := sha256.Sum256([]byte("oci:" + digest))
	return hex.EncodeToString(h[:])
}

// sameAZBonus returns an extra peer score when two IPs share a /16 prefix.
// Used by PeerScorer to prefer same-rack/same-AZ peers.
func sameAZBonus(a, b net.IP) int {
	av4 := a.To4()
	bv4 := b.To4()
	if av4 == nil || bv4 == nil {
		return 0
	}
	if av4[0] == bv4[0] && av4[1] == bv4[1] {
		return 20
	}
	return 0
}

// handleLayerResolve processes GET /container/layer?digest=sha256:<hex>.
func (s *Server) handleLayerResolve(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	digest := r.URL.Query().Get("digest")
	if digest == "" {
		http.Error(w, `{"error":"missing digest"}`, http.StatusBadRequest)
		return
	}
	infoHash := containerLayerInfoHash(digest)
	seederCount := 0
	health := 0
	if t, ok := s.worker.Torrents.Get(infoHash); ok {
		seederCount = t.Seeders.Size()
		if s.worker.SwarmPredictor != nil {
			health = s.worker.SwarmPredictor.HealthScore(seederCount, t.Leechers.Size())
		}
	}

	if db != nil {
		db.Exec(
			`INSERT OR IGNORE INTO container_layers (digest, info_hash, created_at)
             VALUES (?, ?, ?)`,
			digest, infoHash, time.Now().Unix(),
		)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"digest":       digest,
		"info_hash":    infoHash,
		"seeder_count": seederCount,
		"health_score": health,
		"announce_url": "/announce",
	})
}

// handleLayerRegister processes POST /container/layer/register — snapshotter calls this.
func (s *Server) handleLayerRegister(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req struct {
		Digest    string `json:"digest"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Digest == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	infoHash := containerLayerInfoHash(req.Digest)
	if _, ok := s.worker.Torrents.Get(infoHash); !ok {
		t := NewTorrent(TorrentID(hashToID(infoHash)))
		t.Size = req.SizeBytes
		t.FreeType = FreeFree
		s.worker.Torrents.Set(infoHash, t)
	}
	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO container_layers (digest, info_hash, size_bytes, created_at)
             VALUES (?, ?, ?, ?)`,
			req.Digest, infoHash, req.SizeBytes, time.Now().Unix(),
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "info_hash": infoHash})
}
