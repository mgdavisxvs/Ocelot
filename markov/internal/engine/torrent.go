package engine

import (
	"encoding/json"
	"math"
	"sort"
	"sync"

	"github.com/mgdavisxvs/ocelot/markov/internal/chain"
	"github.com/mgdavisxvs/ocelot/markov/internal/db"
)

// TorrentEngine tracks torrent swarm health transitions and generates
// predictions including expected time-to-death and freeleech recommendations.
type TorrentEngine struct {
	mu            sync.RWMutex
	globalChain   *chain.Chain
	lastState     map[int64]int     // torrent_id → last health state
	distributions map[int64][]float64 // torrent_id → current π
	lastInterval  map[int64]int     // torrent_id → last recommended interval (for hysteresis)
}

func newTorrentEngine(decay, alpha float64) *TorrentEngine {
	return &TorrentEngine{
		globalChain:   chain.NewWithSmoothing(chain.NumTorrentStates, decay, alpha),
		lastState:     make(map[int64]int),
		distributions: make(map[int64][]float64),
		lastInterval:  make(map[int64]int),
	}
}

func (te *TorrentEngine) loadStoredStates(recs []db.StoredTorrentState) {
	te.mu.Lock()
	defer te.mu.Unlock()
	for _, r := range recs {
		te.lastState[r.TorrentID] = r.State
		// Initialize π as a point mass on the stored state.
		pi := make([]float64, chain.NumTorrentStates)
		pi[r.State] = 1.0
		te.distributions[r.TorrentID] = pi
	}
}

func (te *TorrentEngine) loadChainCounts(rows []db.ChainCountRow) {
	n := chain.NumTorrentStates
	alpha := te.globalChain.Smoothing()
	counts := make([][]float64, n)
	for i := range counts {
		counts[i] = make([]float64, n)
		for j := range counts[i] {
			counts[i][j] = alpha
		}
	}
	for _, r := range rows {
		if r.From < n && r.To < n {
			counts[r.From][r.To] = r.Count
		}
	}
	te.globalChain.LoadCounts(counts)
}

// observe processes current torrent seeder/leecher counts.
func (te *TorrentEngine) observe(torrents []db.TorrentRow) {
	te.mu.Lock()
	defer te.mu.Unlock()

	seenIDs := make(map[int64]struct{}, len(torrents))
	for _, t := range torrents {
		seenIDs[t.ID] = struct{}{}
		newState := chain.TorrentHealthState(t.Seeders, t.Leechers)

		if prev, ok := te.lastState[t.ID]; ok && prev != newState {
			te.globalChain.Observe(prev, newState)
		}
		te.lastState[t.ID] = newState

		// π is updated as a soft mixture: 80% point mass on observed state,
		// 20% carry-forward from previous π. This prevents wild swings on
		// single-announce observations while tracking real changes.
		pi := te.distributions[t.ID]
		if pi == nil {
			pi = make([]float64, chain.NumTorrentStates)
		}
		for s := range pi {
			pi[s] *= 0.2
		}
		pi[newState] += 0.8
		te.distributions[t.ID] = pi
	}

	// Remove torrents no longer present.
	for id := range te.lastState {
		if _, ok := seenIDs[id]; !ok {
			delete(te.lastState, id)
			delete(te.distributions, id)
			delete(te.lastInterval, id)
		}
	}
}

func (te *TorrentEngine) counts() [][]float64 {
	return te.globalChain.Counts()
}

func (te *TorrentEngine) decay() {
	te.globalChain.Decay()
}

// distribution returns a copy of the current health distribution for torrentID.
func (te *TorrentEngine) distribution(torrentID int64) []float64 {
	te.mu.RLock()
	defer te.mu.RUnlock()
	if d, ok := te.distributions[torrentID]; ok {
		out := make([]float64, len(d))
		copy(out, d)
		return out
	}
	pi := make([]float64, chain.NumTorrentStates)
	for i := range pi {
		pi[i] = 1.0 / float64(chain.NumTorrentStates)
	}
	return pi
}

// TorrentPrediction is a fully computed prediction for one torrent.
type TorrentPrediction struct {
	TorrentID           int64
	HealthState         int
	Pi                  []float64
	Pi1h                []float64
	Pi6h                []float64
	Pi24h               []float64
	Pi72h               []float64
	DeadProb1h          float64
	DeadProb6h          float64
	DeadProb24h         float64
	DeadProb72h         float64
	ExpectedDeadHours   float64
	Entropy             float64
	EffectiveSamples    float64 // evidence strength: effective obs for current state
	RecommendedInterval int
}

// boundedAdaptiveInterval computes a recommend announce interval using entropy
// as a proxy for certainty, bounded by [minInterval, maxInterval] with hysteresis
// to prevent oscillation. The model clock is NOT changed by this function.
func boundedAdaptiveInterval(h, maxH float64, minInterval, maxInterval, lastInterval int, hysteresis float64) int {
	certainty := 0.0
	if maxH > 0 {
		certainty = 1.0 - h/maxH
	}
	// Map certainty [0,1] → interval [maxInterval, minInterval].
	// High certainty = long interval; low certainty = short interval.
	raw := float64(maxInterval) - certainty*float64(maxInterval-minInterval)
	proposed := int(raw)
	if proposed < minInterval {
		proposed = minInterval
	}
	if proposed > maxInterval {
		proposed = maxInterval
	}
	if lastInterval == 0 {
		return proposed
	}
	// Hysteresis: only update if fractional change exceeds threshold.
	delta := math.Abs(float64(proposed-lastInterval)) / float64(lastInterval)
	if delta < hysteresis {
		return lastInterval
	}
	return proposed
}

// buildPredictions computes predictions for all tracked torrents.
// steps{1h,6h,24h,72h} are numbers of model-clock steps for each horizon.
// modelClockSec is the model clock period (NOT the poll interval).
// minInterval/maxInterval/hysteresis govern adaptive announce bounds.
func (te *TorrentEngine) buildPredictions(
	steps1h, steps6h, steps24h, steps72h, modelClockSec int,
	minInterval, maxInterval int, hysteresis float64,
) []TorrentPrediction {
	absSteps := te.globalChain.ExpectedAbsorptionSteps(chain.TorrentDead)
	maxH := chain.MaxEntropy(chain.NumTorrentStates)

	te.mu.RLock()
	ids := make([]int64, 0, len(te.distributions))
	for id := range te.distributions {
		ids = append(ids, id)
	}
	te.mu.RUnlock()

	out := make([]TorrentPrediction, 0, len(ids))
	for _, id := range ids {
		pi := te.distribution(id)
		forecasts := te.globalChain.Forecast(pi, []int{steps1h, steps6h, steps24h, steps72h})
		pi1h := forecasts[0]
		pi6h := forecasts[1]
		pi24 := forecasts[2]
		pi72 := forecasts[3]

		h := chain.Entropy(pi)
		effSamples := te.globalChain.EffectiveSampleCount(te.currentState(id))

		te.mu.RLock()
		last := te.lastInterval[id]
		hs := te.lastState[id]
		te.mu.RUnlock()

		interval := boundedAdaptiveInterval(h, maxH, minInterval, maxInterval, last, hysteresis)
		te.mu.Lock()
		te.lastInterval[id] = interval
		te.mu.Unlock()

		var expectedDeadSteps float64
		for s, prob := range pi {
			expectedDeadSteps += prob * absSteps[s]
		}
		expectedDeadHours := expectedDeadSteps * float64(modelClockSec) / 3600.0

		out = append(out, TorrentPrediction{
			TorrentID:           id,
			HealthState:         hs,
			Pi:                  pi,
			Pi1h:                pi1h,
			Pi6h:                pi6h,
			Pi24h:               pi24,
			Pi72h:               pi72,
			DeadProb1h:          pi1h[chain.TorrentDead],
			DeadProb6h:          pi6h[chain.TorrentDead],
			DeadProb24h:         pi24[chain.TorrentDead],
			DeadProb72h:         pi72[chain.TorrentDead],
			ExpectedDeadHours:   expectedDeadHours,
			Entropy:             h,
			EffectiveSamples:    effSamples,
			RecommendedInterval: interval,
		})
	}
	return out
}

// currentState returns the last health state for a torrent (0 if unknown).
func (te *TorrentEngine) currentState(torrentID int64) int {
	te.mu.RLock()
	defer te.mu.RUnlock()
	return te.lastState[torrentID]
}

// toDB converts a TorrentPrediction to a DB record.
func predictionToDB(p TorrentPrediction, nowUnix int64) db.TorrentPredictionRecord {
	piJSON, _ := json.Marshal(p.Pi)
	pi24JSON, _ := json.Marshal(p.Pi24h)
	pi72JSON, _ := json.Marshal(p.Pi72h)
	return db.TorrentPredictionRecord{
		TorrentID:           p.TorrentID,
		HealthState:         p.HealthState,
		PiJSON:              string(piJSON),
		Pi24hJSON:           string(pi24JSON),
		Pi72hJSON:           string(pi72JSON),
		DeadProb24h:         p.DeadProb24h,
		DeadProb72h:         p.DeadProb72h,
		ExpectedDeadHours:   p.ExpectedDeadHours,
		Entropy:             p.Entropy,
		RecommendedInterval: p.RecommendedInterval,
		UpdatedAt:           nowUnix,
	}
}

// FreeleechCandidate is a torrent that benefits from a system freeleech event.
type FreeleechCandidate struct {
	TorrentID     int64
	PriorityScore float64 // higher = more urgently needs new leechers/seeders
	DeadProb72h   float64
}

// buildFreeleechCandidates returns the top-N torrents to recommend for freeleech.
// Priority = DeadProb72h × (1 + Entropy). At-risk and dying torrents score highest.
func (te *TorrentEngine) buildFreeleechCandidates(predictions []TorrentPrediction, topN int) []FreeleechCandidate {
	candidates := make([]FreeleechCandidate, 0, len(predictions))
	for _, p := range predictions {
		// Only consider non-dead, non-thriving torrents.
		if p.HealthState == chain.TorrentDead || p.HealthState == chain.TorrentThriving {
			continue
		}
		score := p.DeadProb72h * (1.0 + p.Entropy)
		candidates = append(candidates, FreeleechCandidate{
			TorrentID:     p.TorrentID,
			PriorityScore: score,
			DeadProb72h:   p.DeadProb72h,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].PriorityScore > candidates[j].PriorityScore
	})
	if len(candidates) > topN {
		candidates = candidates[:topN]
	}
	return candidates
}

// snapshotStates returns all current torrent health states for DB persistence.
func (te *TorrentEngine) snapshotStates(nowUnix int64) []db.TorrentStateRecord {
	te.mu.RLock()
	defer te.mu.RUnlock()
	out := make([]db.TorrentStateRecord, 0, len(te.lastState))
	for id, s := range te.lastState {
		out = append(out, db.TorrentStateRecord{
			TorrentID:  id,
			State:      s,
			ObservedAt: nowUnix,
		})
	}
	return out
}

// torrentCount returns the number of tracked torrents.
func (te *TorrentEngine) torrentCount() int {
	te.mu.RLock()
	defer te.mu.RUnlock()
	return len(te.lastState)
}

// getPrediction retrieves a live prediction for a single torrent.
func (te *TorrentEngine) getPrediction(
	torrentID int64,
	steps1h, steps6h, steps24h, steps72h, modelClockSec int,
	minInterval, maxInterval int, hysteresis float64,
) *TorrentPrediction {
	absSteps := te.globalChain.ExpectedAbsorptionSteps(chain.TorrentDead)
	maxH := chain.MaxEntropy(chain.NumTorrentStates)

	te.mu.RLock()
	hs, ok := te.lastState[torrentID]
	last := te.lastInterval[torrentID]
	te.mu.RUnlock()
	if !ok {
		return nil
	}

	pi := te.distribution(torrentID)
	forecasts := te.globalChain.Forecast(pi, []int{steps1h, steps6h, steps24h, steps72h})
	pi1h := forecasts[0]
	pi6h := forecasts[1]
	pi24 := forecasts[2]
	pi72 := forecasts[3]

	h := chain.Entropy(pi)
	effSamples := te.globalChain.EffectiveSampleCount(hs)
	interval := boundedAdaptiveInterval(h, maxH, minInterval, maxInterval, last, hysteresis)

	var expectedDeadSteps float64
	for s, prob := range pi {
		expectedDeadSteps += prob * absSteps[s]
	}

	return &TorrentPrediction{
		TorrentID:           torrentID,
		HealthState:         hs,
		Pi:                  pi,
		Pi1h:                pi1h,
		Pi6h:                pi6h,
		Pi24h:               pi24,
		Pi72h:               pi72,
		DeadProb1h:          pi1h[chain.TorrentDead],
		DeadProb6h:          pi6h[chain.TorrentDead],
		DeadProb24h:         pi24[chain.TorrentDead],
		DeadProb72h:         pi72[chain.TorrentDead],
		ExpectedDeadHours:   expectedDeadSteps * float64(modelClockSec) / 3600.0,
		Entropy:             h,
		EffectiveSamples:    effSamples,
		RecommendedInterval: interval,
	}
}
