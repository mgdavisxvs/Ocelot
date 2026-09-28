package tracker

import (
	"encoding/json"
	"math"
	"net/http"
)

// handleComputeScale processes GET /compute/scale?job_ref=<hash>.
// Returns a scaling signal for the compute orchestrator.
func (s *Server) handleComputeScale(w http.ResponseWriter, r *http.Request) {
	jobRef := r.URL.Query().Get("job_ref")
	if jobRef == "" {
		http.Error(w, `{"error":"missing job_ref"}`, http.StatusBadRequest)
		return
	}
	t, ok := s.worker.Torrents.Get(jobRef)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"scale_up": false,
			"ratio":    0.0,
			"seeders":  0,
			"leechers": 0,
		})
		return
	}
	seeders := t.Seeders.Size()
	leechers := t.Leechers.Size()
	ratio := float64(leechers) / math.Max(1, float64(seeders))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"scale_up": ratio > 4,
		"ratio":    ratio,
		"seeders":  seeders,
		"leechers": leechers,
	})
}

// handleComputeJobETA processes GET /compute/eta?job_ref=<hash>.
func (s *Server) handleComputeJobETA(w http.ResponseWriter, r *http.Request) {
	jobRef := r.URL.Query().Get("job_ref")
	if jobRef == "" {
		http.Error(w, `{"error":"missing job_ref"}`, http.StatusBadRequest)
		return
	}
	t, ok := s.worker.Torrents.Get(jobRef)
	if !ok {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	seeders := t.Seeders.Size()
	leechers := t.Leechers.Size()
	var etaSec float64
	if s.worker.SwarmPredictor != nil && t.Size > 0 {
		const avgSpeed = 100 * 1024 * 1024 // 100 MB/s cluster interconnect
		eta := s.worker.SwarmPredictor.PredictCompletionTime(
			seeders, leechers, avgSpeed, float64(t.Size))
		etaSec = eta.Seconds()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"job_ref":     jobRef,
		"eta_seconds": etaSec,
		"scale_up":    float64(leechers)/math.Max(1, float64(seeders)) > 4,
	})
}
