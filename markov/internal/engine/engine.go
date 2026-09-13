package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/mgdavisxvs/ocelot/markov/internal/config"
	"github.com/mgdavisxvs/ocelot/markov/internal/db"
)

// Engine is the root coordinator for all three Markov chains.
// It drives observation, decay, and persistence on configurable schedules.
type Engine struct {
	cfg     *config.Config
	db      *db.DB
	peers   *PeerEngine
	users   *UserEngine
	torrents *TorrentEngine

	// snatch watermark: we only fetch snatches newer than this timestamp.
	snatchWatermark int64

	pollCount int // incremented on each poll; used for decay scheduling
}

// New creates and initializes an Engine, loading persisted state from the DB.
func New(cfg *config.Config, database *db.DB) (*Engine, error) {
	e := &Engine{
		cfg:      cfg,
		db:       database,
		peers:    newPeerEngine(cfg.DecayFactor),
		users:    newUserEngine(cfg.DecayFactor, cfg.PathHistoryLen),
		torrents: newTorrentEngine(cfg.DecayFactor),
	}
	if err := e.loadPersistedState(context.Background()); err != nil {
		return nil, err
	}
	return e, nil
}

// loadPersistedState restores chain counts and state snapshots from DB.
func (e *Engine) loadPersistedState(ctx context.Context) error {
	// Chain counts
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

	// State snapshots
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
	return nil
}

// Run starts the engine's poll and persist loops. It blocks until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	pollTicker := time.NewTicker(time.Duration(e.cfg.PollIntervalSec) * time.Second)
	persistTicker := time.NewTicker(time.Duration(e.cfg.PersistIntervalSec) * time.Second)
	defer pollTicker.Stop()
	defer persistTicker.Stop()

	// Run one poll immediately on startup to populate distributions.
	e.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("engine shutting down, flushing state to DB")
			persistCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			e.persist(persistCtx)
			cancel()
			return
		case <-pollTicker.C:
			e.poll(ctx)
		case <-persistTicker.C:
			e.persist(ctx)
		}
	}
}

// poll fetches current state from Ocelot's tables and emits transitions.
func (e *Engine) poll(ctx context.Context) {
	now := time.Now().Unix()

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

	torrentRows, err := e.db.LoadTorrents(ctx)
	if err != nil {
		slog.Error("LoadTorrents failed", "err", err)
		return
	}

	userRows, err := e.db.LoadUsers(ctx)
	if err != nil {
		slog.Error("LoadUsers failed", "err", err)
		return
	}

	freeleechUIDs, err := e.db.LoadFreeleechUIDs(ctx)
	if err != nil {
		slog.Error("LoadFreeleechUIDs failed", "err", err)
		return
	}

	e.peers.observe(peers, snatches, now, int64(e.cfg.PeersTimeoutSec))
	e.torrents.observe(torrentRows)
	e.users.observe(userRows, freeleechUIDs)

	e.pollCount++
	if e.pollCount%e.cfg.DecayEveryNPolls == 0 {
		e.peers.decay()
		e.users.decay()
		e.torrents.decay()
		slog.Debug("decay applied", "poll", e.pollCount)
	}

	slog.Debug("poll complete",
		"peers", len(peers),
		"torrents", len(torrentRows),
		"users", len(userRows),
		"snatches", len(snatches))
}

// persist flushes all in-memory state and predictions to the DB.
func (e *Engine) persist(ctx context.Context) {
	now := time.Now().Unix()

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

	// Torrent predictions
	predictions := e.torrents.buildPredictions(
		e.cfg.ForecastSteps24h,
		e.cfg.ForecastSteps72h,
		e.cfg.PollIntervalSec,
		e.cfg.AnnounceIntervalSec,
	)
	predRecs := make([]db.TorrentPredictionRecord, len(predictions))
	for i, p := range predictions {
		predRecs[i] = predictionToDB(p, now)
	}
	if err := e.db.UpsertPredictions(ctx, predRecs); err != nil {
		slog.Error("UpsertPredictions", "err", err)
	}

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

	slog.Info("persist complete",
		"predictions", len(predictions),
		"anomalies", len(anomalyRecs),
		"flagged", countFlagged(anomalies),
		"freeleech_candidates", len(candidates))
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
		e.cfg.ForecastSteps24h,
		e.cfg.ForecastSteps72h,
		e.cfg.PollIntervalSec,
		e.cfg.AnnounceIntervalSec,
	)
}

// UserAnomalyForID returns the live anomaly result for a user.
func (e *Engine) UserAnomalyForID(uid int64) *AnomalyResult {
	// Recompute on-the-fly for a single user using the current chain.
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

	nll := e.users.globalChain.PathLogLikelihood(pathCopy)
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

// Stats returns aggregate statistics for the metrics endpoint.
type Stats struct {
	TrackedPeers     int
	TrackedTorrents  int
	TrackedUsers     int
	PollCount        int
	SnatchWatermark  int64
}

func (e *Engine) Stats() Stats {
	return Stats{
		TrackedPeers:    e.peers.peerCount(),
		TrackedTorrents: e.torrents.torrentCount(),
		TrackedUsers:    e.users.userCount(),
		PollCount:       e.pollCount,
		SnatchWatermark: e.snatchWatermark,
	}
}

// PeerChainP returns the current peer transition matrix (for diagnostics).
func (e *Engine) PeerChainP() [][]float64 {
	return e.peers.globalChain.P()
}

// UserChainP returns the current user transition matrix (for diagnostics).
func (e *Engine) UserChainP() [][]float64 {
	return e.users.globalChain.P()
}

// TorrentChainP returns the current torrent health transition matrix.
func (e *Engine) TorrentChainP() [][]float64 {
	return e.torrents.globalChain.P()
}
