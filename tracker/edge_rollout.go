package tracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// RolloutStage describes one phase of a staged model rollout.
type RolloutStage struct {
	PctTarget    uint8
	DwellSeconds int
	AnomalyGate  float64 // abort if anomaly_rate > this
}

// EdgeRollout manages a multi-stage model deployment rollout.
type EdgeRollout struct {
	mu           sync.Mutex
	ModelID      string
	InfoHash     string
	Stages       []RolloutStage
	CurrentStage int
	StageStart   time.Time
	Halted       bool
	HaltReason   string
}

// EnsureEdgeRolloutTable creates the edge_rollouts table if absent.
func EnsureEdgeRolloutTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS edge_rollouts (
		model_id      TEXT PRIMARY KEY,
		info_hash     TEXT NOT NULL,
		current_stage INTEGER NOT NULL DEFAULT 0,
		halted        INTEGER NOT NULL DEFAULT 0,
		halt_reason   TEXT NOT NULL DEFAULT '',
		created_at    INTEGER NOT NULL
	)`)
	return err
}

// EdgeRolloutManager manages all active edge rollouts.
type EdgeRolloutManager struct {
	mu       sync.RWMutex
	rollouts map[string]*EdgeRollout // model_id → rollout
	worker   *Worker
}

var globalEdgeRollouts = &EdgeRolloutManager{
	rollouts: make(map[string]*EdgeRollout),
}

func (m *EdgeRolloutManager) register(r *EdgeRollout) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollouts[r.ModelID] = r
}

// handleEdgeRolloutCreate processes POST /edge/rollout/create.
func (s *Server) handleEdgeRolloutCreate(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var req struct {
		ModelID  string         `json:"model_id"`
		InfoHash string         `json:"info_hash"`
		Stages   []RolloutStage `json:"stages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ModelID == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if len(req.Stages) == 0 {
		req.Stages = []RolloutStage{
			{PctTarget: 1, DwellSeconds: 300, AnomalyGate: 0.05},
			{PctTarget: 10, DwellSeconds: 600, AnomalyGate: 0.03},
			{PctTarget: 50, DwellSeconds: 1800, AnomalyGate: 0.02},
			{PctTarget: 100, DwellSeconds: 0, AnomalyGate: 0.01},
		}
	}
	if req.InfoHash == "" {
		req.InfoHash = ModelInfoHash(req.ModelID)
	}

	t, ok := s.worker.Torrents.Get(req.InfoHash)
	if !ok {
		t = NewTorrent(TorrentID(hashToID(req.InfoHash)))
		t.Category = CategoryEdgeAI
		s.worker.Torrents.Set(req.InfoHash, t)
	}
	t.RolloutPct = req.Stages[0].PctTarget

	rollout := &EdgeRollout{
		ModelID:      req.ModelID,
		InfoHash:     req.InfoHash,
		Stages:       req.Stages,
		CurrentStage: 0,
		StageStart:   time.Now(),
	}
	globalEdgeRollouts.register(rollout)

	if db != nil {
		db.Exec(
			`INSERT OR REPLACE INTO edge_rollouts
			 (model_id, info_hash, current_stage, halted, halt_reason, created_at)
			 VALUES (?, ?, 0, 0, '', ?)`,
			req.ModelID, req.InfoHash, time.Now().Unix(),
		)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":            true,
		"model_id":      req.ModelID,
		"current_stage": 0,
		"rollout_pct":   req.Stages[0].PctTarget,
	})
}

// handleEdgeRolloutStatus processes GET /edge/rollout/status?model_id=<id>.
func (s *Server) handleEdgeRolloutStatus(w http.ResponseWriter, r *http.Request) {
	modelID := r.URL.Query().Get("model_id")
	if modelID == "" {
		http.Error(w, `{"error":"missing model_id"}`, http.StatusBadRequest)
		return
	}
	globalEdgeRollouts.mu.RLock()
	rollout, ok := globalEdgeRollouts.rollouts[modelID]
	globalEdgeRollouts.mu.RUnlock()
	if !ok {
		http.Error(w, `{"error":"rollout not found"}`, http.StatusNotFound)
		return
	}
	rollout.mu.Lock()
	defer rollout.mu.Unlock()
	var pct uint8
	if rollout.CurrentStage < len(rollout.Stages) {
		pct = rollout.Stages[rollout.CurrentStage].PctTarget
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"model_id":      rollout.ModelID,
		"current_stage": rollout.CurrentStage,
		"rollout_pct":   pct,
		"halted":        rollout.Halted,
		"halt_reason":   rollout.HaltReason,
		"stage_age_sec": time.Since(rollout.StageStart).Seconds(),
	})
}

// RunEdgeRolloutAdvancer runs a background goroutine that advances rollout stages.
func RunEdgeRolloutAdvancer(ctx context.Context, worker *Worker, db *sql.DB) {
	globalEdgeRollouts.worker = worker
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				globalEdgeRollouts.advanceAll(worker, db)
			}
		}
	}()
}

func (m *EdgeRolloutManager) advanceAll(worker *Worker, db *sql.DB) {
	m.mu.RLock()
	rollouts := make([]*EdgeRollout, 0, len(m.rollouts))
	for _, r := range m.rollouts {
		rollouts = append(rollouts, r)
	}
	m.mu.RUnlock()

	for _, r := range rollouts {
		r.tryAdvance(worker, db)
	}
}

func (r *EdgeRollout) tryAdvance(worker *Worker, db *sql.DB) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Halted || r.CurrentStage >= len(r.Stages) {
		return
	}
	stage := r.Stages[r.CurrentStage]
	if stage.DwellSeconds > 0 &&
		time.Since(r.StageStart) < time.Duration(stage.DwellSeconds)*time.Second {
		return // dwell not elapsed
	}

	// Check swarm health gate.
	if worker.SwarmPredictor != nil {
		if t, ok := worker.Torrents.Get(r.InfoHash); ok {
			score := worker.SwarmPredictor.HealthScore(
				t.Seeders.Size(), t.Leechers.Size())
			if score < 2 {
				r.Halted = true
				r.HaltReason = "swarm health below gate"
				worker.logSiteCommErr("edge_rollout_halt",
					worker.SiteComm.ReportAnomaly(int64(hashToID(r.InfoHash)), 1.0))
				return
			}
		}
	}

	// Advance to next stage.
	r.CurrentStage++
	r.StageStart = time.Now()
	if r.CurrentStage < len(r.Stages) {
		nextPct := r.Stages[r.CurrentStage].PctTarget
		if t, ok := worker.Torrents.Get(r.InfoHash); ok {
			t.RolloutPct = nextPct
		}
		if db != nil {
			db.Exec(
				`UPDATE edge_rollouts SET current_stage=? WHERE model_id=?`,
				r.CurrentStage, r.ModelID,
			)
		}
		GetDefaultLogger().Info("edge rollout advanced",
			"model_id", r.ModelID,
			"stage", r.CurrentStage,
			"rollout_pct", nextPct,
		)
	}
}
