package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mgdavisxvs/ocelot/markov/internal/chain"
	"github.com/mgdavisxvs/ocelot/markov/internal/engine"
)

// Server exposes the Markov service over HTTP for Gazelle integration.
type Server struct {
	eng    *engine.Engine
	server *http.Server
}

func New(addr string, eng *engine.Engine) *Server {
	s := &Server{eng: eng}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/chain/peer", s.handleChainPeer)
	mux.HandleFunc("/chain/user", s.handleChainUser)
	mux.HandleFunc("/chain/torrent", s.handleChainTorrent)
	mux.HandleFunc("/torrent/", s.handleTorrent)
	mux.HandleFunc("/user/", s.handleUser)
	mux.HandleFunc("/peer/quality/", s.handlePeerQuality)
	mux.HandleFunc("/freeleech", s.handleFreeleech)
	s.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe() error {
	slog.Info("API server listening", "addr", s.server.Addr)
	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// --- Handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	stats := s.eng.Stats()
	jsonOK(w, map[string]any{
		"tracked_peers":    stats.TrackedPeers,
		"tracked_torrents": stats.TrackedTorrents,
		"tracked_users":    stats.TrackedUsers,
		"poll_count":       stats.PollCount,
		"snatch_watermark": stats.SnatchWatermark,
		"time":             time.Now().Unix(),
	})
}

// GET /chain/peer  — returns the raw 5×5 peer transition matrix
func (s *Server) handleChainPeer(w http.ResponseWriter, r *http.Request) {
	s.handleChain(w, s.eng.PeerChainP(), chain.PeerStateNames[:])
}

// GET /chain/user
func (s *Server) handleChainUser(w http.ResponseWriter, r *http.Request) {
	s.handleChain(w, s.eng.UserChainP(), chain.UserStateNames[:])
}

// GET /chain/torrent
func (s *Server) handleChainTorrent(w http.ResponseWriter, r *http.Request) {
	s.handleChain(w, s.eng.TorrentChainP(), chain.TorrentStateNames[:])
}

func (s *Server) handleChain(w http.ResponseWriter, p [][]float64, names []string) {
	rows := make([]map[string]any, len(p))
	for i, row := range p {
		transitions := make(map[string]float64, len(row))
		for j, v := range row {
			transitions[names[j]] = v
		}
		rows[i] = map[string]any{
			"from":        names[i],
			"transitions": transitions,
		}
	}
	jsonOK(w, rows)
}

// GET /torrent/{id}
// Returns health prediction for a single torrent.
func (s *Server) handleTorrent(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r.URL.Path, "/torrent/")
	if err != nil {
		http.Error(w, "invalid torrent id", http.StatusBadRequest)
		return
	}
	pred := s.eng.TorrentPredictionForID(id)
	if pred == nil {
		http.Error(w, "torrent not tracked", http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]any{
		"torrent_id":           pred.TorrentID,
		"health_state":         chain.TorrentStateNames[pred.HealthState],
		"health_state_id":      pred.HealthState,
		"pi_current":           pred.Pi,
		"pi_24h":               pred.Pi24h,
		"pi_72h":               pred.Pi72h,
		"dead_prob_24h":        pred.DeadProb24h,
		"dead_prob_72h":        pred.DeadProb72h,
		"expected_dead_hours":  pred.ExpectedDeadHours,
		"entropy_bits":         pred.Entropy,
		"recommended_interval": pred.RecommendedInterval,
	})
}

// GET /user/{id}/anomaly  — fraud anomaly score
// GET /user/{id}/cheat    — FR-011: decomposed cheat confidence
func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "use /user/{id}/anomaly or /user/{id}/cheat", http.StatusBadRequest)
		return
	}
	uid, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	switch parts[2] {
	case "anomaly":
		s.handleUserAnomaly(w, uid)
	case "cheat":
		s.handleUserCheat(w, uid)
	default:
		http.Error(w, "use /user/{id}/anomaly or /user/{id}/cheat", http.StatusBadRequest)
	}
}

func (s *Server) handleUserAnomaly(w http.ResponseWriter, uid int64) {
	result := s.eng.UserAnomalyForID(uid)
	if result == nil {
		http.Error(w, "user not tracked", http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]any{
		"uid":                 result.UID,
		"ratio_state":         chain.UserStateNames[result.State],
		"ratio_state_id":      result.State,
		"path_log_likelihood": result.PathLogLikelihood,
		"anomaly_score":       result.AnomalyScore,
		"flagged":             result.Flagged,
	})
}

// handleUserCheat implements FR-011: decomposed cheat_confidence.
// Formula: cheat_confidence = 0.6×(1−global_p) + 0.4×clamp(nll_zscore/3, 0, 1)
func (s *Server) handleUserCheat(w http.ResponseWriter, uid int64) {
	result := s.eng.UserAnomalyForID(uid)
	if result == nil {
		http.Error(w, "user not tracked", http.StatusNotFound)
		return
	}
	globalP, obsCount := s.eng.BetaUserReliability(uid)
	nllZscore := result.AnomalyScore
	clampedZ := math.Min(1, math.Max(0, nllZscore/3))
	cheatConf := 0.6*(1-globalP) + 0.4*clampedZ
	jsonOK(w, map[string]any{
		"uid":              uid,
		"cheat_confidence": cheatConf,
		"nll_zscore":       nllZscore,
		"global_p":         globalP,
		"obs_count":        obsCount,
		"flagged":          cheatConf > 0.6,
	})
}

// GET /peer/quality/{uid}/{torrent_id} — FR-009: Beta-Binomial peer quality
func (s *Server) handlePeerQuality(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// path: ["peer", "quality", uid, torrent_id]
	if len(parts) < 4 {
		http.Error(w, "use /peer/quality/{uid}/{torrent_id}", http.StatusBadRequest)
		return
	}
	uid, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "invalid uid", http.StatusBadRequest)
		return
	}
	tid, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		http.Error(w, "invalid torrent_id", http.StatusBadRequest)
		return
	}
	alpha, betaV, obsCount, ok := s.eng.BetaPeerQuality(uid, tid)
	if !ok {
		http.Error(w, "peer not tracked", http.StatusNotFound)
		return
	}
	globalP, _ := s.eng.BetaUserReliability(uid)
	lo, hi := engine.BetaCI95(alpha, betaV)
	jsonOK(w, map[string]any{
		"uid":        uid,
		"torrent_id": tid,
		"alpha":      alpha,
		"beta":       betaV,
		"obs_count":  obsCount,
		"global_p":   globalP,
		"ci_low":     lo,
		"ci_high":    hi,
	})
}

// GET /freeleech
// Returns the current top freeleech candidates.
func (s *Server) handleFreeleech(w http.ResponseWriter, r *http.Request) {
	candidates, err := s.eng.FreeleechCandidates(r.Context())
	if err != nil {
		slog.Error("FreeleechCandidates", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rows := make([]map[string]any, len(candidates))
	for i, c := range candidates {
		rows[i] = map[string]any{
			"torrent_id":     c.TorrentID,
			"priority_score": c.PriorityScore,
			"dead_prob_72h":  c.DeadProb72h,
		}
	}
	jsonOK(w, rows)
}

// --- helpers ---

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("json encode", "err", err)
	}
}

func parseIDFromPath(path, prefix string) (int64, error) {
	trimmed := strings.TrimPrefix(path, prefix)
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		return 0, fmt.Errorf("empty id")
	}
	return strconv.ParseInt(trimmed, 10, 64)
}
