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

// userCount returns the number of tracked users.
func (ue *UserEngine) userCount() int {
	ue.mu.RLock()
	defer ue.mu.RUnlock()
	return len(ue.lastState)
}
