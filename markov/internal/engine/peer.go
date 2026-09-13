package engine

import (
	"sync"

	"github.com/mgdavisxvs/ocelot/markov/internal/chain"
	"github.com/mgdavisxvs/ocelot/markov/internal/db"
)

// peerKey uniquely identifies a peer in a swarm.
type peerKey struct {
	TorrentID int64
	UID       int64
}

// PeerEngine tracks peer lifecycle transitions across all swarms using a
// single global Markov chain learned from all observed transitions.
// Per-torrent state distributions are maintained separately.
type PeerEngine struct {
	mu           sync.RWMutex
	globalChain  *chain.Chain
	lastState    map[peerKey]int     // previous observed state per (torrent, user)
	lastStateAt  map[peerKey]int64   // Unix timestamp of last state observation
	distributions map[int64][]float64 // current π per torrent_id
}

func newPeerEngine(decay, alpha float64) *PeerEngine {
	return &PeerEngine{
		globalChain:   chain.NewWithSmoothing(chain.NumPeerStates, decay, alpha),
		lastState:     make(map[peerKey]int),
		lastStateAt:   make(map[peerKey]int64),
		distributions: make(map[int64][]float64),
	}
}

// loadStoredStates restores peer state snapshots from a previous run.
func (pe *PeerEngine) loadStoredStates(recs []db.StoredPeerState) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	for _, r := range recs {
		k := peerKey{r.TorrentID, r.UID}
		pe.lastState[k] = r.State
		pe.lastStateAt[k] = r.ObservedAt
	}
}

// loadChainCounts restores persisted chain counts.
func (pe *PeerEngine) loadChainCounts(rows []db.ChainCountRow) {
	n := chain.NumPeerStates
	alpha := pe.globalChain.Smoothing()
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
	pe.globalChain.LoadCounts(counts)
}

// observe processes a batch of current peer observations, emits transitions,
// and updates the stored last-state map. nowUnix is the current Unix timestamp.
// peersTimeout is Ocelot's peers_timeout configuration value.
func (pe *PeerEngine) observe(peers []db.PeerRow, snatches []db.SnatchRow, nowUnix, peersTimeout int64) {
	// Build snatch set for O(1) lookup: (uid, fid)
	type snatchKey struct{ uid, fid int64 }
	snatchSet := make(map[snatchKey]struct{}, len(snatches))
	for _, s := range snatches {
		snatchSet[snatchKey{s.UID, s.FID}] = struct{}{}
	}

	// Build set of currently active peers.
	currentPeers := make(map[peerKey]struct{}, len(peers))
	newStates := make(map[peerKey]int, len(peers))

	for _, p := range peers {
		k := peerKey{p.FID, p.UID}
		currentPeers[k] = struct{}{}

		// Determine state: snatched takes priority if the snatch is recent.
		var newState int
		if _, snatched := snatchSet[snatchKey{p.UID, p.FID}]; snatched && !p.Active {
			newState = chain.PeerSnatched
		} else {
			newState = chain.PeerActivityState(p.Active, p.Remaining, p.Mtime, nowUnix, peersTimeout)
		}
		newStates[k] = newState
	}

	pe.mu.Lock()
	defer pe.mu.Unlock()

	// Emit transitions for known peers.
	for k, newState := range newStates {
		if prev, ok := pe.lastState[k]; ok && prev != newState {
			pe.globalChain.Observe(prev, newState)
		}
		pe.lastState[k] = newState
		pe.lastStateAt[k] = nowUnix
	}

	// Peers that disappeared → DEAD transition.
	for k, prevState := range pe.lastState {
		if _, exists := currentPeers[k]; !exists {
			if prevState != chain.PeerDead {
				if _, snatched := snatchSet[snatchKey{k.UID, k.TorrentID}]; snatched {
					pe.globalChain.Observe(prevState, chain.PeerSnatched)
					pe.lastState[k] = chain.PeerSnatched
				} else {
					pe.globalChain.Observe(prevState, chain.PeerDead)
				}
			}
			delete(pe.lastState, k)
			delete(pe.lastStateAt, k)
		}
	}

	// Recompute per-torrent distributions from current peer states.
	torrentStateCounts := make(map[int64][chain.NumPeerStates]int64)
	for k, s := range newStates {
		tc := torrentStateCounts[k.TorrentID]
		tc[s]++
		torrentStateCounts[k.TorrentID] = tc
	}
	for tid, counts := range torrentStateCounts {
		var total int64
		for _, c := range counts {
			total += c
		}
		pi := make([]float64, chain.NumPeerStates)
		if total > 0 {
			for s, c := range counts {
				pi[s] = float64(c) / float64(total)
			}
		} else {
			for s := range pi {
				pi[s] = 1.0 / float64(chain.NumPeerStates)
			}
		}
		pe.distributions[tid] = pi
	}
}

// distribution returns a copy of the current peer-state distribution for torrentID.
func (pe *PeerEngine) distribution(torrentID int64) []float64 {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	if d, ok := pe.distributions[torrentID]; ok {
		out := make([]float64, len(d))
		copy(out, d)
		return out
	}
	// Unknown torrent: uniform.
	pi := make([]float64, chain.NumPeerStates)
	for i := range pi {
		pi[i] = 1.0 / float64(chain.NumPeerStates)
	}
	return pi
}

// adaptiveInterval computes the recommended announce interval given the
// current peer distribution entropy, bounded by [minInterval, maxInterval]
// with hysteresis. This ONLY changes the recommend interval — never the model clock.
func (pe *PeerEngine) adaptiveInterval(torrentID int64, minInterval, maxInterval int, hysteresis float64) int {
	pi := pe.distribution(torrentID)
	h := chain.Entropy(pi)
	maxH := chain.MaxEntropy(chain.NumPeerStates)
	return boundedAdaptiveInterval(h, maxH, minInterval, maxInterval, 0, hysteresis)
}

// snapshotStates returns all current peer states for DB persistence.
func (pe *PeerEngine) snapshotStates(nowUnix int64) []db.PeerStateRecord {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	out := make([]db.PeerStateRecord, 0, len(pe.lastState))
	for k, s := range pe.lastState {
		out = append(out, db.PeerStateRecord{
			TorrentID:  k.TorrentID,
			UID:        k.UID,
			State:      s,
			ObservedAt: nowUnix,
		})
	}
	return out
}

func (pe *PeerEngine) counts() [][]float64 {
	return pe.globalChain.Counts()
}

func (pe *PeerEngine) decay() {
	pe.globalChain.Decay()
}

// forecastTorrent returns π at k steps ahead for the given torrent.
func (pe *PeerEngine) forecastTorrent(torrentID int64, k int) []float64 {
	pi := pe.distribution(torrentID)
	return pe.globalChain.Step(pi, k)
}

// entropy returns the current entropy of the torrent's peer distribution.
func (pe *PeerEngine) entropy(torrentID int64) float64 {
	return chain.Entropy(pe.distribution(torrentID))
}

// torrentIDs returns all torrent IDs with a tracked distribution.
func (pe *PeerEngine) torrentIDs() []int64 {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	ids := make([]int64, 0, len(pe.distributions))
	for id := range pe.distributions {
		ids = append(ids, id)
	}
	return ids
}

// expectedAbsorptionSteps returns E[steps to PeerDead] for each state.
func (pe *PeerEngine) expectedAbsorptionSteps() []float64 {
	return pe.globalChain.ExpectedAbsorptionSteps(chain.PeerDead)
}

// expectedDeadHours returns the expected hours to peer death from the
// current distribution of a given torrent.
func (pe *PeerEngine) expectedDeadHours(torrentID int64, pollIntervalSec int) float64 {
	pi := pe.distribution(torrentID)
	absSteps := pe.expectedAbsorptionSteps()
	var expected float64
	for s, prob := range pi {
		expected += prob * absSteps[s]
	}
	return expected * float64(pollIntervalSec) / 3600.0
}

// lastObservedAt returns the most recent observation timestamp for any peer
// in the given torrent swarm, or zero if none.
func (pe *PeerEngine) lastObservedAt(torrentID int64) int64 {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	var latest int64
	for k, t := range pe.lastStateAt {
		if k.TorrentID == torrentID && t > latest {
			latest = t
		}
	}
	return latest
}

// peerCount returns the number of currently tracked peers across all swarms.
func (pe *PeerEngine) peerCount() int {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return len(pe.lastState)
}

