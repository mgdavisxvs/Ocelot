package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/mgdavisxvs/ocelot/markov/internal/chain"
	"github.com/mgdavisxvs/ocelot/markov/internal/config"
	"github.com/mgdavisxvs/ocelot/markov/internal/db"
)

// Engine is the root coordinator for all three Markov chains plus the Beta-Binomial scorer.
//
// Separation of concerns (non-negotiable architectural invariant):
//   - pollTicker: fetches raw data from Ocelot's DB (peer/torrent/user rows). Fast, frequent.
//   - modelClockTicker: fires Markov observations. Independent of announce/poll frequency.
//   - persistTicker: flushes in-memory state and predictions to the DB.
//
// The Markov layer is advisory. It MUST NOT be in the announce hot path and
// MUST NOT affect payload transfer. Shadow mode (default: on) records recommendations
// without applying them to the tracker.
type Engine struct {
	cfg      *config.Config
	db       *db.DB
	peers    *PeerEngine
	users    *UserEngine
	torrents *TorrentEngine
	beta     *BetaEngine

	// Staging area: last fetched rows, shared between poll and modelClock ticks.
	mu           interface{} // unused — staging is protected by Go scheduler (single-writer poll)
	lastPeers    []db.PeerRow
	lastTorrents []db.TorrentRow
	lastUsers    []db.UserRow
	lastFLUIDs   db.FreeleechUID
	lastSnatches []db.SnatchRow

	snatchWatermark int64

	pollCount      int // incremented on each data fetch
	modelClockTick int // incremented on each model-clock fire
	matrixVersion  int // bumped on each decay cycle
	persistCount   int // incremented on each persist() call; governs forecast sampling
}

// New creates and initializes an Engine, loading persisted state from the DB.
func New(cfg *config.Config, database *db.DB) (*Engine, error) {
	e := &Engine{
		cfg:           cfg,
		db:            database,
		// UMM-02: each chain has its own decay factor tuned to its behavioral timescale.
		peers:         newPeerEngine(cfg.PeerDecayFactor, cfg.SmoothingAlpha),
		users:         newUserEngine(cfg.UserDecayFactor, cfg.SmoothingAlpha, cfg.PathHistoryLen),
		torrents:      newTorrentEngine(cfg.TorrentDecayFactor, cfg.SmoothingAlpha),
		beta:          newBetaEngine(),
		matrixVersion: 1,
	}
	if err := e.loadPersistedState(context.Background()); err != nil {
		return nil, err
	}
	return e, nil
}

// loadPersistedState restores chain counts and state snapshots from DB.
func (e *Engine) loadPersistedState(ctx context.Context) error {
	peerCounts, err := e.db.LoadChainCounts(ctx, "peer")
	if err != nil {
		return err
	}
	userCounts, err := e.db.LoadChainCounts(ctx, "user")
	if err != nil {
		return err
	}
	torrentCounts, err := e.db.LoadChainCounts(ctx, "torrent")
	if err != nil {
		return err
	}
	e.peers.loadChainCounts(peerCounts)
	e.users.loadChainCounts(userCounts)
	e.torrents.loadChainCounts(torrentCounts)
	slog.Info("chain counts loaded",
		"peer_rows", len(peerCounts),
		"user_rows", len(userCounts),
		"torrent_rows", len(torrentCounts))

	peerStates, err := e.db.LoadStoredPeerStates(ctx)
	if err != nil {
		return err
	}
	e.peers.loadStoredStates(peerStates)

	torrentStates, err := e.db.LoadStoredTorrentStates(ctx)
	if err != nil {
		return err
	}
	e.torrents.loadStoredStates(torrentStates)

	userStates, err := e.db.LoadStoredUserStates(ctx)
	if err != nil {
		return err
	}
	e.users.loadStoredStates(userStates)

	slog.Info("state snapshots loaded",
		"peer_records", len(peerStates),
		"torrent_records", len(torrentStates),
		"user_records", len(userStates))

	qualityRecs, err := e.db.LoadAllPeerQuality(ctx)
	if err != nil {
		return err
	}
	e.beta.loadFromDB(qualityRecs)
	slog.Info("peer quality loaded", "records", len(qualityRecs))

	return nil
}

// Run starts the engine's tickers. It blocks until ctx is done.
// Three independent tickers:
//   - pollTicker: data fetch from Ocelot tables
//   - modelClockTicker: emit Markov observations (fixed timestep)
//   - persistTicker: flush state to DB
func (e *Engine) Run(ctx context.Context) {
	pollTicker := time.NewTicker(time.Duration(e.cfg.PollIntervalSec) * time.Second)
	modelTicker := time.NewTicker(time.Duration(e.cfg.ModelClockSec) * time.Second)
	persistTicker := time.NewTicker(time.Duration(e.cfg.PersistIntervalSec) * time.Second)
	defer pollTicker.Stop()
	defer modelTicker.Stop()
	defer persistTicker.Stop()

	// Immediate data fetch on startup.
	e.fetchData(ctx)
	// Immediate model clock tick.
	e.emitObservations(ctx)

	if e.cfg.ShadowMode {
		slog.Info("engine running in SHADOW MODE — recommendations recorded but not applied")
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("engine shutting down, flushing state to DB")
			persistCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			e.persist(persistCtx)
			cancel()
			return
		case <-pollTicker.C:
			e.fetchData(ctx)
		case <-modelTicker.C:
			e.emitObservations(ctx)
		case <-persistTicker.C:
			e.persist(ctx)
		}
	}
}

// fetchData loads current state from Ocelot's tables into the staging area.
// Does NOT emit Markov observations — that is the model clock's job.
func (e *Engine) fetchData(ctx context.Context) {
	peers, err := e.db.LoadPeers(ctx)
	if err != nil {
		slog.Error("LoadPeers failed", "err", err)
		return
	}
	snatches, err := e.db.LoadSnatches(ctx, e.snatchWatermark)
	if err != nil {
		slog.Error("LoadSnatches failed", "err", err)
		return
	}
	if len(snatches) > 0 {
		e.snatchWatermark = snatches[len(snatches)-1].Tstamp
	}
	torrents, err := e.db.LoadTorrents(ctx)
	if err != nil {
		slog.Error("LoadTorrents failed", "err", err)
		return
	}
	users, err := e.db.LoadUsers(ctx)
	if err != nil {
		slog.Error("LoadUsers failed", "err", err)
		return
	}
	flUIDs, err := e.db.LoadFreeleechUIDs(ctx)
	if err != nil {
		slog.Error("LoadFreeleechUIDs failed", "err", err)
		return
	}

	// Atomic staging: replace all at once so model clock sees consistent snapshot.
	e.lastPeers = peers
	e.lastTorrents = torrents
	e.lastUsers = users
	e.lastFLUIDs = flUIDs
	e.lastSnatches = snatches
	e.pollCount++

	slog.Debug("data fetch complete",
		"peers", len(peers),
		"torrents", len(torrents),
		"users", len(users),
		"snatches", len(snatches))
}

// emitObservations fires a Markov observation tick using the most recently
// fetched staging data. This is the only place transitions are recorded,
// ensuring the chain's timestep equals ModelClockSec regardless of announce
// or poll frequency.
func (e *Engine) emitObservations(ctx context.Context) {
	now := time.Now().Unix()

	e.peers.observe(e.lastPeers, e.lastSnatches, now, int64(e.cfg.PeersTimeoutSec))
	e.torrents.observe(e.lastTorrents)
	e.users.observe(e.lastUsers, e.lastFLUIDs)

	leechersByTorrent := make(map[int64]int, len(e.lastTorrents))
	for _, t := range e.lastTorrents {
		leechersByTorrent[t.ID] = int(t.Leechers)
	}
	e.beta.observe(e.lastPeers, leechersByTorrent, int64(e.cfg.SuccessThresholdBytes))

	e.modelClockTick++
	// Decay is keyed to model clock ticks, not poll ticks, for a consistent λ.
	if e.modelClockTick%e.cfg.DecayEveryNPolls == 0 {
		e.peers.decay()
		e.users.decay()
		e.torrents.decay()
		e.matrixVersion++
		slog.Debug("decay applied", "model_tick", e.modelClockTick, "matrix_version", e.matrixVersion)
	}

	slog.Debug("model clock tick", "tick", e.modelClockTick)
}

// persist flushes all in-memory state and predictions to the DB.
func (e *Engine) persist(ctx context.Context) {
	now := time.Now().Unix()
	e.persistCount++

	// Chain counts
	if err := e.db.UpsertChainCounts(ctx, "peer", e.peers.counts()); err != nil {
		slog.Error("UpsertChainCounts peer", "err", err)
	}
	if err := e.db.UpsertChainCounts(ctx, "user", e.users.counts()); err != nil {
		slog.Error("UpsertChainCounts user", "err", err)
	}
	if err := e.db.UpsertChainCounts(ctx, "torrent", e.torrents.counts()); err != nil {
		slog.Error("UpsertChainCounts torrent", "err", err)
	}

	// State snapshots
	if err := e.db.UpsertPeerStates(ctx, e.peers.snapshotStates(now)); err != nil {
		slog.Error("UpsertPeerStates", "err", err)
	}
	if err := e.db.UpsertTorrentStates(ctx, e.torrents.snapshotStates(now)); err != nil {
		slog.Error("UpsertTorrentStates", "err", err)
	}
	if err := e.db.UpsertUserStates(ctx, e.users.snapshotStates(now)); err != nil {
		slog.Error("UpsertUserStates", "err", err)
	}

	// Torrent predictions (UMM-05: evidence-gated adaptive interval)
	predictions := e.torrents.buildPredictions(
		e.cfg.ForecastSteps1h,
		e.cfg.ForecastSteps6h,
		e.cfg.ForecastSteps24h,
		e.cfg.ForecastSteps72h,
		e.cfg.ModelClockSec,
		e.cfg.MinAnnounceIntervalSec,
		e.cfg.MaxAnnounceIntervalSec,
		e.cfg.HysteresisFactor,
		e.cfg.MinEvidenceForAdaptiveInterval,
	)
	predRecs := make([]db.TorrentPredictionRecord, len(predictions))
	for i, p := range predictions {
		predRecs[i] = predictionToDB(p, now)
	}
	if err := e.db.UpsertPredictions(ctx, predRecs); err != nil {
		slog.Error("UpsertPredictions", "err", err)
	}

	// Forecast calibration: snapshot periodically, evaluate elapsed rows every cycle.
	if e.cfg.ForecastSampleEveryN > 0 && e.persistCount%e.cfg.ForecastSampleEveryN == 0 {
		e.storeForecastEvals(ctx, predictions, now)
	}
	e.evaluateForecastEvals(ctx, now)

	// User anomalies
	anomalies := e.users.computeAnomalies(e.cfg.FraudThresholdSigma, e.cfg.FraudPathMinLen)
	anomalyRecs := make([]db.UserAnomalyRecord, 0, len(anomalies))
	for _, a := range anomalies {
		anomalyRecs = append(anomalyRecs, db.UserAnomalyRecord{
			UID:               a.UID,
			AnomalyScore:      a.AnomalyScore,
			PathLogLikelihood: a.PathLogLikelihood,
			Flagged:           a.Flagged,
			UpdatedAt:         now,
		})
	}
	if err := e.db.UpsertUserAnomalies(ctx, anomalyRecs); err != nil {
		slog.Error("UpsertUserAnomalies", "err", err)
	}

	// Log flagged anomalies as recommendations (advisory, shadow-mode-aware).
	e.logAnomalyRecommendations(ctx, anomalies, now)

	// Freeleech candidates
	candidates := e.torrents.buildFreeleechCandidates(predictions, e.cfg.FreeleechTopN)
	candidateRecs := make([]db.FreeleechCandidateRecord, len(candidates))
	for i, c := range candidates {
		candidateRecs[i] = db.FreeleechCandidateRecord{
			TorrentID:     c.TorrentID,
			PriorityScore: c.PriorityScore,
			DeadProb72h:   c.DeadProb72h,
			Recommended:   true,
			UpdatedAt:     now,
		}
	}
	if err := e.db.UpsertFreeleechCandidates(ctx, candidateRecs); err != nil {
		slog.Error("UpsertFreeleechCandidates", "err", err)
	}

	// Beta-Binomial peer quality
	if err := e.beta.persist(ctx, e.db); err != nil {
		slog.Error("beta persist", "err", err)
	}

	// FR-008: expire dead peer state rows older than 7 days
	if err := e.db.ExpireDeadPeerStates(ctx, now-7*86400); err != nil {
		slog.Error("ExpireDeadPeerStates", "err", err)
	}

	// Persist model governance metadata.
	e.persistModelMetadata(ctx, now)

	slog.Info("persist complete",
		"predictions", len(predictions),
		"anomalies", len(anomalyRecs),
		"flagged", countFlagged(anomalies),
		"freeleech_candidates", len(candidates),
		"shadow_mode", e.cfg.ShadowMode)
}

// logAnomalyRecommendations records flagged anomalies in the recommendation audit log.
// In shadow mode, policy_action remains empty (no tracker action taken).
func (e *Engine) logAnomalyRecommendations(ctx context.Context, anomalies []AnomalyResult, now int64) {
	for _, a := range anomalies {
		if !a.Flagged {
			continue
		}
		predMap := map[string]any{
			"nll_zscore": a.AnomalyScore,
			"nll":        a.PathLogLikelihood,
			"state":      a.State,
		}
		predJSON, _ := json.Marshal(predMap)

		policyAction := ""
		if !e.cfg.ShadowMode {
			policyAction = "watch"
		}

		rec := db.RecommendationRecord{
			ChainName:         "user",
			EntityID:          a.UID,
			PredictionJSON:    string(predJSON),
			Entropy:           0, // path entropy not computed per-user
			EvidenceStrength:  a.PathLogLikelihood,
			ModelVersion:      e.matrixVersion,
			RecommendedAction: "watch",
			PolicyAction:      policyAction,
			ShadowMode:        e.cfg.ShadowMode,
			CreatedAt:         now,
		}
		if err := e.db.InsertRecommendation(ctx, rec); err != nil {
			slog.Error("InsertRecommendation", "err", err)
		}
	}
}

// persistModelMetadata writes current chain governance metadata to the DB.
// UMM-02: decay_lambda is stored per-chain using the chain-specific factor.
func (e *Engine) persistModelMetadata(ctx context.Context, now int64) {
	chains := []struct {
		name  string
		n     int
		efn   func(int) float64
		decay float64
	}{
		{"peer", 5, e.peers.globalChain.EffectiveSampleCount, e.cfg.PeerDecayFactor},
		{"user", 5, e.users.globalChain.EffectiveSampleCount, e.cfg.UserDecayFactor},
		{"torrent", 5, e.torrents.globalChain.EffectiveSampleCount, e.cfg.TorrentDecayFactor},
	}
	for _, ch := range chains {
		eff := make([]float64, ch.n)
		for i := range eff {
			eff[i] = ch.efn(i)
		}
		meta := db.ModelMetadata{
			ChainName:      ch.name,
			SchemaVersion:  e.cfg.ModelSchemaVersion,
			DecayLambda:    ch.decay,
			SmoothingAlpha: e.cfg.SmoothingAlpha,
			ObsIntervalSec: e.cfg.ModelClockSec,
			MatrixVersion:  e.matrixVersion,
			EffSamples:     eff,
			UpdatedAt:      now,
		}
		if err := e.db.UpsertModelMetadata(ctx, meta); err != nil {
			slog.Error("UpsertModelMetadata", "chain", ch.name, "err", err)
		}
	}
}

// GetDeploymentStage returns the current deployment stage for a named chain (UMM-06).
func (e *Engine) GetDeploymentStage(ctx context.Context, chainName string) (string, error) {
	stage, err := e.db.GetDeploymentStage(ctx, chainName)
	return string(stage), err
}

// SetDeploymentStage advances the deployment stage for a named chain (UMM-06).
// Validation of promotion gates is the caller's responsibility; this layer only
// persists the requested stage. Never call this from automated code paths.
func (e *Engine) SetDeploymentStage(ctx context.Context, chainName, stage string) error {
	return e.db.SetDeploymentStage(ctx, chainName, db.DeploymentStage(stage))
}

// storeForecastEvals snapshots the current 24h and 72h torrent predictions into
// the calibration log for later scoring when the horizon elapses.
// Only the torrent chain is sampled here; peer and user chain forecasts are
// not yet stored (extend by adding horizons to this loop).
func (e *Engine) storeForecastEvals(ctx context.Context, predictions []TorrentPrediction, now int64) {
	type horizon struct {
		steps int
		dist  []float64
	}
	for _, p := range predictions {
		for _, h := range []horizon{
			{e.cfg.ForecastSteps24h, p.Pi24h},
			{e.cfg.ForecastSteps72h, p.Pi72h},
		} {
			b, err := json.Marshal(h.dist)
			if err != nil {
				continue
			}
			rec := db.ForecastEvalRecord{
				ChainName:    "torrent",
				EntityID:     p.TorrentID,
				HorizonSteps: h.steps,
				ForecastJSON: string(b),
				OutcomeState: -1,
				CreatedAt:    now,
			}
			if err := e.db.InsertForecastEval(ctx, rec); err != nil {
				slog.Error("InsertForecastEval", "torrent_id", p.TorrentID, "err", err)
			}
		}
	}
}

// evaluateForecastEvals resolves calibration rows whose horizon has elapsed.
// For each row, if the torrent is still tracked, the current health state is
// used as the ground-truth outcome and Brier/log-loss scores are recorded.
// Rows for torrents no longer in the active set are skipped (unresolvable outcome).
func (e *Engine) evaluateForecastEvals(ctx context.Context, now int64) {
	// Load all rows older than the minimum horizon (24h). Rows for longer
	// horizons that aren't ready yet are filtered below by per-row elapsed check.
	minHorizonSec := int64(e.cfg.ForecastSteps24h) * int64(e.cfg.ModelClockSec)
	pending, err := e.db.LoadPendingForecastEvals(ctx, now-minHorizonSec, 500)
	if err != nil {
		slog.Error("LoadPendingForecastEvals", "err", err)
		return
	}
	var evaluated, skipped int
	for _, row := range pending {
		horizonSec := int64(row.HorizonSteps) * int64(e.cfg.ModelClockSec)
		if now-row.CreatedAt < horizonSec {
			continue // this row's specific horizon hasn't elapsed yet
		}
		actualState, ok := e.torrents.currentStateOk(row.EntityID)
		if !ok {
			skipped++
			continue // torrent no longer tracked; outcome unresolvable
		}
		var forecast []float64
		if err := json.Unmarshal([]byte(row.ForecastJSON), &forecast); err != nil {
			slog.Warn("ForecastEval unmarshal", "id", row.ID, "err", err)
			continue
		}
		brier := chain.BrierScore(forecast, actualState)
		logLoss := chain.LogLoss(forecast, actualState)
		if err := e.db.UpdateForecastOutcome(ctx, row.ID, actualState, brier, logLoss, now); err != nil {
			slog.Error("UpdateForecastOutcome", "id", row.ID, "err", err)
		} else {
			evaluated++
		}
	}
	if evaluated > 0 || skipped > 0 {
		slog.Debug("forecast calibration evaluated", "evaluated", evaluated, "skipped_untracked", skipped)
	}
}

func countFlagged(a []AnomalyResult) int {
	n := 0
	for _, r := range a {
		if r.Flagged {
			n++
		}
	}
	return n
}

// --- API accessor methods (called from api.go) ---

// TorrentPredictionForID returns a live prediction for a single torrent.
func (e *Engine) TorrentPredictionForID(torrentID int64) *TorrentPrediction {
	return e.torrents.getPrediction(
		torrentID,
		e.cfg.ForecastSteps1h,
		e.cfg.ForecastSteps6h,
		e.cfg.ForecastSteps24h,
		e.cfg.ForecastSteps72h,
		e.cfg.ModelClockSec,
		e.cfg.MinAnnounceIntervalSec,
		e.cfg.MaxAnnounceIntervalSec,
		e.cfg.HysteresisFactor,
		e.cfg.MinEvidenceForAdaptiveInterval,
	)
}

// UserAnomalyForID returns the live anomaly result for a user.
func (e *Engine) UserAnomalyForID(uid int64) *AnomalyResult {
	e.users.mu.RLock()
	path, hasPath := e.users.pathHistory[uid]
	state, hasState := e.users.lastState[uid]
	e.users.mu.RUnlock()
	if !hasState {
		return nil
	}
	pathCopy := make([]int, len(path))
	copy(pathCopy, path)
	_ = hasPath

	nll := e.users.globalChain.NormalizedPathNLL(pathCopy)
	return &AnomalyResult{
		UID:               uid,
		State:             state,
		PathLogLikelihood: nll,
		AnomalyScore:      0, // z-score requires population stats; use batch compute
		Flagged:           false,
	}
}

// FreeleechCandidates returns the current top freeleech candidates from DB.
func (e *Engine) FreeleechCandidates(ctx context.Context) ([]FreeleechCandidate, error) {
	recs, err := e.db.LoadFreeleechCandidates(ctx, e.cfg.FreeleechTopN)
	if err != nil {
		return nil, err
	}
	out := make([]FreeleechCandidate, len(recs))
	for i, r := range recs {
		out[i] = FreeleechCandidate{
			TorrentID:     r.TorrentID,
			PriorityScore: r.PriorityScore,
			DeadProb72h:   r.DeadProb72h,
		}
	}
	return out, nil
}

// ModelMetadata returns governance metadata for all chains.
func (e *Engine) ModelMetadata(ctx context.Context) ([]db.ModelMetadata, error) {
	return e.db.LoadModelMetadata(ctx)
}

// ShadowMode returns whether the engine is in shadow mode.
func (e *Engine) ShadowMode() bool { return e.cfg.ShadowMode }

// Stats returns aggregate statistics for the metrics endpoint.
type Stats struct {
	TrackedPeers     int
	TrackedTorrents  int
	TrackedUsers     int
	PollCount        int
	ModelClockTick   int
	MatrixVersion    int
	SnatchWatermark  int64
	ShadowMode       bool
}

func (e *Engine) Stats() Stats {
	return Stats{
		TrackedPeers:    e.peers.peerCount(),
		TrackedTorrents: e.torrents.torrentCount(),
		TrackedUsers:    e.users.userCount(),
		PollCount:       e.pollCount,
		ModelClockTick:  e.modelClockTick,
		MatrixVersion:   e.matrixVersion,
		SnatchWatermark: e.snatchWatermark,
		ShadowMode:      e.cfg.ShadowMode,
	}
}

// PeerChainP returns the current peer transition matrix.
func (e *Engine) PeerChainP() [][]float64 { return e.peers.globalChain.P() }

// UserChainP returns the current user transition matrix.
func (e *Engine) UserChainP() [][]float64 { return e.users.globalChain.P() }

// TorrentChainP returns the current torrent health transition matrix.
func (e *Engine) TorrentChainP() [][]float64 { return e.torrents.globalChain.P() }

// BetaUserReliability returns E[p] = Σα/Σ(α+β) and total obs for a user.
func (e *Engine) BetaUserReliability(uid int64) (globalP float64, obsCount int) {
	return e.beta.UserGlobalReliability(uid)
}

// BetaPeerQuality returns the Beta parameters for a specific (uid, torrentID) pair.
func (e *Engine) BetaPeerQuality(uid, torrentID int64) (alpha, betaV float64, obsCount int, ok bool) {
	return e.beta.PeerQuality(uid, torrentID)
}

// CalibrationSummary returns aggregate Brier/log-loss calibration metrics.
func (e *Engine) CalibrationSummary(ctx context.Context) ([]db.CalibrationSummary, error) {
	return e.db.LoadCalibrationSummary(ctx)
}
