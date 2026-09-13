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
	lastState   map[int64]int   // uid → last ratio state
	pathHistory map[int64][]int // uid → recent state path (capped at pathHistoryLen)
	pathMaxLen  int
}

func newUserEngine(decay, alpha float64, pathMaxLen int) *UserEngine {
	return &UserEngine{
		globalChain: chain.NewWithSmoothing(chain.NumUserStates, decay, alpha),
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
	alpha := ue.globalChain.Smoothing()
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
	ue.globalChain.LoadCounts(counts)
}

// observe processes a batch of user rows and freeleech token set.
func (ue *UserEngine) observe(users []db.UserRow, freeleechUIDs db.FreeleechUID) {
	ue.mu.Lock()
	defer ue.mu.Unlock()

	seenUIDs := make(map[int64]struct{}, len(users))
	for _, u := range users {
		seenUIDs[u.ID] = struct{}{}
		// UMM-01: skip freeleech users from chain observations. Accounting-regime
		// changes (freeleech on/off) create artificial ratio jumps that corrupt
		// the chain's model of organic ratio dynamics.
		_, hasFreeleech := freeleechUIDs[u.ID]
		newState := chain.UserRatioState(u.Uploaded, u.Downloaded)

		if prev, ok := ue.lastState[u.ID]; ok && prev != newState && !hasFreeleech {
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
// The model NEVER directly bans users — it only produces advisory risk signals.
// Operators apply sanctions via policy independent of this layer.
type AnomalyResult struct {
	UID               int64
	State             int
	PathLogLikelihood float64
	AnomalyScore      float64 // robust z-score via median/MAD normalization
	Flagged           bool    // advisory: true means WATCH/SUSPICIOUS, not a ban
}

// computeAnomalies scores all users with sufficient path history using
// normalized per-transition NLL and robust median/MAD normalization.
// threshold is the MAD z-score above which a user is flagged as suspicious.
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

	// Compute normalized NLL (per-transition) for each eligible user.
	type scored struct {
		uid64 int64
		nll   float64
	}
	candidates := make([]scored, 0, len(paths))
	for uid, path := range paths {
		if len(path) < minPathLen {
			continue
		}
		// Use normalized NLL for fair cross-user comparison.
		nll := ue.globalChain.NormalizedPathNLL(path)
		if !math.IsInf(nll, 1) {
			candidates = append(candidates, scored{uid64: uid, nll: nll})
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Robust normalization via median and MAD (median absolute deviation).
	// Avoids sensitivity to outliers that plague mean/stddev normalization.
	nlls := make([]float64, len(candidates))
	for i, c := range candidates {
		nlls[i] = c.nll
	}
	median := percentile(nlls, 0.5)
	absDevs := make([]float64, len(nlls))
	for i, v := range nlls {
		absDevs[i] = math.Abs(v - median)
	}
	mad := percentile(absDevs, 0.5)
	// Scale factor 1.4826 makes MAD consistent with stddev for normal data.
	scaledMAD := mad * 1.4826
	if scaledMAD < 1e-9 {
		scaledMAD = 1e-9
	}

	results := make([]AnomalyResult, 0, len(candidates))
	for _, c := range candidates {
		z := (c.nll - median) / scaledMAD
		results = append(results, AnomalyResult{
			UID:               c.uid64,
			State:             states[c.uid64],
			PathLogLikelihood: c.nll,
			AnomalyScore:      z,
			Flagged:           z > threshold, // advisory only; operator decides sanctions
		})
	}
	// Sort by anomaly score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].AnomalyScore > results[j].AnomalyScore
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
