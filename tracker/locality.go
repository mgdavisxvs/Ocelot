package tracker

import (
	"fmt"
	"math"
)

// LocalityWeights holds the five scoring factors for job-to-node assignment.
// S = w_C·C + w_A·A + w_Q·Q + w_N·N + w_R·R
// All weights must sum to 1.0 (±0.001 tolerance).
//
// These defaults are from the GUC Phase II analysis and are intentionally
// arbitrary. Calibrate against real job latency data before treating any
// set as canonical. Make changes at runtime via config, not recompilation.
var DefaultLocalityWeights = LocalityWeights{
	Compute:     0.30,
	Artifact:    0.25,
	Queue:       0.20,
	Network:     0.15,
	Reliability: 0.10,
}

// LocalityWeights is the configurable weight vector for locality scoring.
type LocalityWeights struct {
	Compute     float64 `yaml:"compute"`     // C: hardware suitability for job type
	Artifact    float64 `yaml:"artifact"`    // A: artifact already present on node
	Queue       float64 `yaml:"queue"`       // Q: node queue availability (inverse occupancy)
	Network     float64 `yaml:"network"`     // N: network quality / bandwidth to node
	Reliability float64 `yaml:"reliability"` // R: historical node uptime ratio
}

// Validate returns an error if weights don't sum to 1.0 within tolerance.
func (w LocalityWeights) Validate() error {
	sum := w.Compute + w.Artifact + w.Queue + w.Network + w.Reliability
	if math.Abs(sum-1.0) > 0.001 {
		return fmt.Errorf("locality weights sum to %.4f, must be 1.0 ±0.001", sum)
	}
	return nil
}

// NodeScoreInput holds per-node values for each scoring factor.
// All values are in [0.0, 1.0]. A value of -1 signals unavailability.
//
// Safe pessimistic fallbacks when a distributed input is unreachable:
//   - Q (queue):   0.0 — assume queue is full
//   - N (network): 0.0 — assume poor network quality
//
// These fallbacks ensure the formula always produces a defined result even
// when the target node cannot be queried (e.g., during a network partition).
type NodeScoreInput struct {
	Compute     float64 // C ∈ [0,1]
	Artifact    float64 // A ∈ [0,1]: 1.0 if artifact fully present
	Queue       float64 // Q ∈ [0,1]: 1.0 = empty queue; -1 = unavailable
	Network     float64 // N ∈ [0,1]: link quality; -1 = unavailable
	Reliability float64 // R ∈ [0,1]: historical uptime ratio
}

// Score computes the weighted locality score for a node.
// Unavailable inputs (value == -1) use their pessimistic fallback (0.0).
func (w LocalityWeights) Score(in NodeScoreInput) float64 {
	q := in.Queue
	if q < 0 {
		q = 0
	}
	n := in.Network
	if n < 0 {
		n = 0
	}
	return w.Compute*in.Compute +
		w.Artifact*in.Artifact +
		w.Queue*q +
		w.Network*n +
		w.Reliability*in.Reliability
}

// NodeCandidate pairs a node identifier with its computed locality score.
type NodeCandidate struct {
	NodeID        uint64
	FailureDomain string // rack, datacenter, or region identifier
	Score         float64
}

// RankNodes returns candidates sorted descending by locality score.
// Callers should implement greedy placement on the returned slice.
func RankNodes(weights LocalityWeights, inputs map[uint64]NodeScoreInput, domains map[uint64]string) []NodeCandidate {
	out := make([]NodeCandidate, 0, len(inputs))
	for id, in := range inputs {
		out = append(out, NodeCandidate{
			NodeID:        id,
			FailureDomain: domains[id],
			Score:         weights.Score(in),
		})
	}
	// Insertion sort — candidate sets are small (tens of nodes)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Score > out[j-1].Score; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// GreedyPlace selects up to needed nodes from ranked candidates,
// preferring nodes in uncovered failure domains first (Erdős PE-2).
// This is the greedy set-cover approximation, within ln(M) of optimal.
func GreedyPlace(ranked []NodeCandidate, needed int) []NodeCandidate {
	covered := make(map[string]bool)
	selected := make([]NodeCandidate, 0, needed)

	// First pass: one node per failure domain
	for _, c := range ranked {
		if len(selected) >= needed {
			break
		}
		if !covered[c.FailureDomain] {
			selected = append(selected, c)
			covered[c.FailureDomain] = true
		}
	}
	// Second pass: fill remaining slots from highest-scored candidates
	for _, c := range ranked {
		if len(selected) >= needed {
			break
		}
		alreadySelected := false
		for _, s := range selected {
			if s.NodeID == c.NodeID {
				alreadySelected = true
				break
			}
		}
		if !alreadySelected {
			selected = append(selected, c)
		}
	}
	return selected
}
