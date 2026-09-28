package tracker

import (
	"encoding/json"
	"net/http"
)

// handleCDNStatus processes GET /cdn/status?info_hash=<hash>.
// Returns whether the swarm is healthy enough to serve peers without CDN fallback.
func (s *Server) handleCDNStatus(w http.ResponseWriter, r *http.Request) {
	hash := r.URL.Query().Get("info_hash")
	if hash == "" {
		http.Error(w, `{"error":"missing info_hash"}`, http.StatusBadRequest)
		return
	}
	t, ok := s.worker.Torrents.Get(hash)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"use_peers": false,
			"health":    0,
			"seeders":   0,
			"leechers":  0,
		})
		return
	}
	seeders := t.Seeders.Size()
	leechers := t.Leechers.Size()
	health := 0
	if s.worker.SwarmPredictor != nil {
		health = s.worker.SwarmPredictor.HealthScore(seeders, leechers)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"use_peers": health >= 3,
		"health":    health,
		"seeders":   seeders,
		"leechers":  leechers,
	})
}
