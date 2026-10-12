package engine

import (
	"encoding/json"
	"math"
	"sort"
	"sync"

	"github.com/mgdavisxvs/ocelot/markov/internal/chain"
	"github.com/mgdavisxvs/ocelot/markov/internal/db"
)

// UserEngine tracks user ratio-state transitions and detects anomalous
// trajectories indicative of ratio gaming.
type UserEngine struct {
	mu          sync.RWMutex
	globalChain *chain.Chain
	lastState   map[int64]int    // uid → last ratio state
	pathHistory map[int64][]int  // uid → recent state path (capped at pathHistoryLen)
	pathMaxLen  int
}

func newUserEngine(decay float64, pathMaxLen int) *UserEngine {
	return &UserEngine{
		globalChain: chain.New(chain.NumUserStates, decay),
		lastState:   make(map[int64]int),
		pathHistory: make(map[int64][]int),
		pathMaxLen:  pathMaxLen,
	}
}

func (ue *UserEngine) loadStoredStates(recs []db.StoredUserState) {
	ue.mu.Lock()
	defer ue.mu.Unlock()
	for _, r := range recs {
		ue.lastState[r.UID] = r.State
		if r.PathJSON != "" {
			var path []int
			if err := json.Unmarshal([]byte(r.PathJSON), &path); err == nil {
				ue.pathHistory[r.UID] = path
			}
		}
	}
}

func (ue *UserEngine) loadChainCounts(rows []db.ChainCountRow) {
	n := chain.NumUserStates
	counts := make([][]float64, n)
	for i := range counts {
		counts[i] = make([]float64, n)
		for j := range counts[i] {
			counts[i][j] = 1.0
		}
	}
	for _, r := range rows {
		if r.From < n && r.To < n {
			counts[r.From][r.To] = r.Count
		}
	}
	ue.globalChain.LoadCounts(counts)
}

// observe processes a batch of user rows and freeleech token set.
func (ue *UserEngine) observe(users []db.UserRow, freeleechUIDs db.FreeleechUID) {
	ue.mu.Lock()
	defer ue.mu.Unlock()

	seenUIDs := make(map[int64]struct{}, len(users))
	for _, u := range users {
		seenUIDs[u.ID] = struct{}{}
		_, hasFreeleech := freeleechUIDs[u.ID]
		newState := chain.UserRatioState(u.Uploaded, u.Downloaded, u.CanLeech, hasFreeleech)

		if prev, ok := ue.lastState[u.ID]; ok && prev != newState {
			ue.globalChain.Observe(prev, newState)
		}
		ue.lastState[u.ID] = newState

		// Append to path history.
		path := append(ue.pathHistory[u.ID], newState)
		if len(path) > ue.pathMaxLen {
			path = path[len(path)-ue.pathMaxLen:]
		}
		ue.pathHistory[u.ID] = path
	}

	// Remove users that are no longer in the enabled set.
	for uid := range ue.lastState {
		if _, ok := seenUIDs[uid]; !ok {
			delete(ue.lastState, uid)
			delete(ue.pathHistory, uid)
		}
	}
}

func (ue *UserEngine) counts() [][]float64 {
	return ue.globalChain.Counts()
}

func (ue *UserEngine) decay() {
	ue.globalChain.Decay()
}

// AnomalyResult holds fraud detection output for one user.
type AnomalyResult struct {
	UID               int64
	State             int
	PathLogLikelihood float64
	AnomalyScore      float64 // z-score relative to population mean
	Flagged           bool
}

// computeAnomalies scores all users with sufficient path history.
// threshold is the z-score above which a user is flagged.
// minPathLen is the minimum number of path observations required.
func (ue *UserEngine) computeAnomalies(threshold float64, minPathLen int) []AnomalyResult {
	ue.mu.RLock()
	paths := make(map[int64][]int, len(ue.pathHistory))
	states := make(map[int64]int, len(ue.lastState))
	for uid, p := range ue.pathHistory {
		c := make([]int, len(p))
		copy(c, p)
		paths[uid] = c
	}
	for uid, s := range ue.lastState {
		states[uid] = s
	}
	ue.mu.RUnlock()

	// Compute NLL for each eligible user.
	type scored struct {
		uid int64
		nll float64
	}
	candidates := make([]scored, 0, len(paths))
	for uid, path := range paths {
		if len(path) < minPathLen {
			continue
		}
		nll := ue.globalChain.PathLogLikelihood(path)
		if !math.IsInf(nll, 1) {
			candidates = append(candidates, scored{uid, nll})
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Compute population mean and stddev.
	var sum, sumSq float64
	for _, c := range candidates {
		sum += c.nll
		sumSq += c.nll * c.nll
	}
	n := float64(len(candidates))
	mean := sum / n
	variance := sumSq/n - mean*mean
	if variance < 0 {
		variance = 0
	}
	stddev := math.Sqrt(variance)

	results := make([]AnomalyResult, 0, len(candidates))
	for _, c := range candidates {
		var z float64
		if stddev > 1e-9 {
			z = (c.nll - mean) / stddev
		}
		results = append(results, AnomalyResult{
			UID:               c.uid,
			State:             states[c.uid],
			PathLogLikelihood: c.nll,
			AnomalyScore:      z,
			Flagged:           z > threshold,
		})
	}
	// Sort by anomaly score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].AnomalyScore > results[j].AnomalyScore
	})
	return results
}

// SeederPageRankResult holds the PageRank score for one user in the seeder graph.
type SeederPageRankResult struct {
	UID   int64
	Score float64
	State int
}

// seederStateWeight maps user health states to their seeder contribution quality.
var seederStateWeight = [chain.NumUserStates]float64{
	1.0, // HEALTHY: full contribution
	0.6, // WARNING: reduced contribution
	0.2, // PROBATION: minimal contribution
	0.0, // BANNED: no contribution
	0.9, // FREELEECH: high contribution (active user)
}

// computeSeederPageRank scores users by their seeder contribution quality using
// the Markov chain's stationary distribution weighted by per-user path health.
// dampingFactor is the standard PageRank damping (0.85 is canonical).
// iterations controls power-iteration convergence (50 is sufficient for 5 states).
func (ue *UserEngine) computeSeederPageRank(dampingFactor float64, iterations int) []SeederPageRankResult {
	ue.mu.RLock()
	paths := make(map[int64][]int, len(ue.pathHistory))
	states := make(map[int64]int, len(ue.lastState))
	for uid, p := range ue.pathHistory {
		c := make([]int, len(p))
		copy(c, p)
		paths[uid] = c
	}
	for uid, s := range ue.lastState {
		states[uid] = s
	}
	counts := ue.globalChain.Counts()
	ue.mu.RUnlock()

	n := chain.NumUserStates

	// Row-normalise transition counts → probability matrix P.
	P := make([][]float64, n)
	for i := range P {
		P[i] = make([]float64, n)
		rowSum := 0.0
		for j := range P[i] {
			rowSum += counts[i][j]
		}
		if rowSum > 0 {
			for j := range P[i] {
				P[i][j] = counts[i][j] / rowSum
			}
		} else {
			P[i][i] = 1.0 // absorbing state
		}
	}

	// Power iteration for stationary distribution π.
	pi := make([]float64, n)
	for i := range pi {
		pi[i] = 1.0 / float64(n)
	}
	next := make([]float64, n)
	for iter := 0; iter < iterations; iter++ {
		for j := range next {
			next[j] = 0
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				next[j] += pi[i] * P[i][j]
			}
		}
		pi, next = next, pi
	}

	// Score each user: stationary probability of their current state ×
	// time-averaged path health score, modulated by damping factor.
	results := make([]SeederPageRankResult, 0, len(states))
	uniform := (1 - dampingFactor) / float64(n)

	for uid, path := range paths {
		state, ok := states[uid]
		if !ok || len(path) == 0 {
			continue
		}
		var healthSum float64
		for _, s := range path {
			if s >= 0 && s < n {
				healthSum += seederStateWeight[s]
			}
		}
		pathHealthScore := healthSum / float64(len(path))

		score := uniform + dampingFactor*pi[state]*pathHealthScore
		results = append(results, SeederPageRankResult{
			UID:   uid,
			Score: score,
			State: state,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// percentile returns the p-th percentile (0.0–1.0) of a float64 slice.
// The slice is sorted in place. p=0.5 gives the median.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	n := float64(len(vals))
	idx := p * (n - 1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(vals) {
		return vals[len(vals)-1]
	}
	frac := idx - float64(lo)
	return vals[lo]*(1-frac) + vals[hi]*frac
}

// snapshotStates returns all current user states for DB persistence.
func (ue *UserEngine) snapshotStates(nowUnix int64) []db.UserStateRecord {
	ue.mu.RLock()
	defer ue.mu.RUnlock()
	out := make([]db.UserStateRecord, 0, len(ue.lastState))
	for uid, s := range ue.lastState {
		pathJSON := "[]"
		if path, ok := ue.pathHistory[uid]; ok {
			if b, err := json.Marshal(path); err == nil {
				pathJSON = string(b)
			}
		}
		out = append(out, db.UserStateRecord{
			UID:        uid,
			State:      s,
			PathJSON:   pathJSON,
			ObservedAt: nowUnix,
		})
	}
	return out
}

// userStates returns a snapshot copy of uid → ratio state for all tracked users.
// Used by buildSeederAssignments for eligibility scoring without holding the lock.
func (ue *UserEngine) userStates() map[int64]int {
	ue.mu.RLock()
	defer ue.mu.RUnlock()
	out := make(map[int64]int, len(ue.lastState))
	for uid, s := range ue.lastState {
		out[uid] = s
	}
	return out
}

// userCount returns the number of tracked users.
func (ue *UserEngine) userCount() int {
	ue.mu.RLock()
	defer ue.mu.RUnlock()
	return len(ue.lastState)
}
