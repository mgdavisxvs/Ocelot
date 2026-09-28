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

// ChainSnapshot maps a blockchain epoch checkpoint to a torrent.
type ChainSnapshot struct {
	Chain     string `json:"chain"`
	Epoch     uint64 `json:"epoch"`
	InfoHash  string `json:"info_hash"`
	BlockHash string `json:"block_hash"`
	SizeBytes int64  `json:"size_bytes"`
}

// EnsureChainSnapshotTable creates the chain_snapshots table if absent.
func EnsureChainSnapshotTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS chain_snapshots (
        chain       TEXT NOT NULL,
        epoch       INTEGER NOT NULL,
        info_hash   TEXT NOT NULL,
        block_hash  TEXT NOT NULL DEFAULT '',
        size_bytes  INTEGER NOT NULL DEFAULT 0,
        created_at  INTEGER NOT NULL,
        PRIMARY KEY (chain, epoch)
    )`)
	return err
}

// chainSnapshotInfoHash derives an info_hash from chain + epoch.
func chainSnapshotInfoHash(chain string, epoch uint64) string {
	key := []byte(chain)
	key = append(key, byte(epoch>>24), byte(epoch>>16), byte(epoch>>8), byte(epoch))
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:])
}

// handleChainSnapshot processes GET /chain/snapshot?chain=<c>&epoch=<n>.
func (s *Server) handleChainSnapshot(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	chain := r.URL.Query().Get("chain")
	epochStr := r.URL.Query().Get("epoch")
	if chain == "" {
		http.Error(w, `{"error":"missing chain"}`, http.StatusBadRequest)
		return
	}

	var snap ChainSnapshot
	if db != nil && epochStr != "" {
		var epoch uint64
		if _, err := parseEpoch(epochStr, &epoch); err == nil {
			row := db.QueryRow(
				`SELECT chain, epoch, info_hash, block_hash, size_bytes
                 FROM chain_snapshots WHERE chain=? AND epoch=?`,
				chain, epoch,
			)
			if err := row.Scan(&snap.Chain, &snap.Epoch, &snap.InfoHash,
				&snap.BlockHash, &snap.SizeBytes); err == nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(snap)
				return
			}
		}
	}
	// Return latest snapshot for chain.
	if db != nil {
		row := db.QueryRow(
			`SELECT chain, epoch, info_hash, block_hash, size_bytes
             FROM chain_snapshots WHERE chain=? ORDER BY epoch DESC LIMIT 1`,
			chain,
		)
		row.Scan(&snap.Chain, &snap.Epoch, &snap.InfoHash, &snap.BlockHash, &snap.SizeBytes)
	}
	if snap.InfoHash == "" {
		http.Error(w, `{"error":"snapshot not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snap)
}

// handleChainSnapshotRegister processes POST /chain/snapshot/register.
func (s *Server) handleChainSnapshotRegister(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var snap ChainSnapshot
	if err := json.NewDecoder(r.Body).Decode(&snap); err != nil || snap.Chain == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if snap.InfoHash == "" {
		snap.InfoHash = chainSnapshotInfoHash(snap.Chain, snap.Epoch)
	}
	if _, ok := s.worker.Torrents.Get(snap.InfoHash); !ok {
		t := NewTorrent(TorrentID(hashToID(snap.InfoHash)))
		t.Size = snap.SizeBytes
		s.worker.Torrents.Set(snap.InfoHash, t)
	}
	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO chain_snapshots
             (chain, epoch, info_hash, block_hash, size_bytes, created_at)
             VALUES (?, ?, ?, ?, ?, ?)`,
			snap.Chain, snap.Epoch, snap.InfoHash,
			snap.BlockHash, snap.SizeBytes, time.Now().Unix(),
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "info_hash": snap.InfoHash})
}

// parseEpoch parses a decimal string into a uint64.
// Returns the number of characters consumed and any parse error.
func parseEpoch(s string, out *uint64) (int, error) {
	var v uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid epoch")
		}
		v = v*10 + uint64(c-'0')
	}
	*out = v
	return len(s), nil
}
