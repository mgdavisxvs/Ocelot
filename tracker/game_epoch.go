package tracker

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// GameEpoch represents a rolling world-state delta torrent.
type GameEpoch struct {
	WorldID   string    `json:"world_id"`
	EpochID   uint64    `json:"epoch_id"`
	InfoHash  string    `json:"info_hash"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GameEpochRegistry manages active epochs per world.
type GameEpochRegistry struct {
	mu     sync.RWMutex
	epochs map[string]*GameEpoch // world_id → current epoch
}

var globalGameRegistry = &GameEpochRegistry{
	epochs: make(map[string]*GameEpoch),
}

func (gr *GameEpochRegistry) set(worldID string, epoch *GameEpoch) {
	gr.mu.Lock()
	defer gr.mu.Unlock()
	gr.epochs[worldID] = epoch
}

func (gr *GameEpochRegistry) get(worldID string) (*GameEpoch, bool) {
	gr.mu.RLock()
	defer gr.mu.RUnlock()
	e, ok := gr.epochs[worldID]
	return e, ok
}

// EnsureGameEpochTable creates the game_epochs table if absent.
func EnsureGameEpochTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS game_epochs (
		world_id   TEXT NOT NULL,
		epoch_id   INTEGER NOT NULL,
		info_hash  TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (world_id, epoch_id)
	)`)
	return err
}

// handleGameEpoch processes POST /game/epoch — game server registers a new world-state delta.
func (s *Server) handleGameEpoch(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req struct {
		WorldID  string `json:"world_id"`
		EpochID  uint64 `json:"epoch_id"`
		InfoHash string `json:"info_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.WorldID == "" || req.InfoHash == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	// Retire previous epoch torrent.
	if prev, ok := globalGameRegistry.get(req.WorldID); ok {
		s.worker.Torrents.Delete(prev.InfoHash)
	}

	// Register new epoch torrent with short TTL.
	now := time.Now()
	t := NewTorrent(TorrentID(hashToID(req.InfoHash)))
	t.FreeType = FreeFree
	t.Category = CategoryGame
	t.TTLSeconds = 90
	t.CreatedAt = now
	s.worker.Torrents.Set(req.InfoHash, t)

	epoch := &GameEpoch{
		WorldID:   req.WorldID,
		EpochID:   req.EpochID,
		InfoHash:  req.InfoHash,
		ExpiresAt: now.Add(90 * time.Second),
	}
	globalGameRegistry.set(req.WorldID, epoch)

	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO game_epochs (world_id, epoch_id, info_hash, created_at)
			 VALUES (?, ?, ?, ?)`,
			req.WorldID, req.EpochID, req.InfoHash, now.Unix(),
		)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"world_id":  req.WorldID,
		"epoch_id":  req.EpochID,
		"info_hash": req.InfoHash,
	})
}

// detectGameCheat returns true if a peer's corrupt-to-downloaded ratio exceeds the
// given threshold. Called during announce processing for game torrents.
func detectGameCheat(peer *Peer, threshold float64) bool {
	if peer.Downloaded <= 0 {
		return false
	}
	ratio := float64(peer.Corrupt) / float64(peer.Downloaded)
	return ratio > threshold
}
