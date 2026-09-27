package scheduler

import (
	"context"
	"math"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// Weights controls the relative importance of each placement score factor.
type Weights struct {
	Health              float64
	CapacityFit         float64
	AcceleratorFit      float64
	DataLocality        float64
	Reliability         float64
	PreferredNodeBonus  float64
	ExistingLoadPenalty float64
	ArtifactTransfer    float64
}

// DefaultWeights returns the documented default scoring weights.
func DefaultWeights() Weights {
	return Weights{
		Health:              0.20,
		CapacityFit:         0.20,
		AcceleratorFit:      0.20,
		DataLocality:        0.15,
		Reliability:         0.15,
		PreferredNodeBonus:  0.10,
		ExistingLoadPenalty: 0.05,
		ArtifactTransfer:    0.10,
	}
}

// computeScore returns a placement score in [0,1] for a candidate node.
// totalNodes is the full candidate pool size — used by the artifact entropy scorer.
func computeScore(n domain.Node, spec domain.ServiceSpec, catalog ArtifactCatalog, w Weights, totalNodes int) float64 {
	health := healthScore(n)
	capacity := capacityFitScore(n, spec)
	accel := acceleratorFitScore(n, spec)
	locality := dataLocalityScore(n, spec)
	artifact := artifactTransferScore(n, spec, catalog, totalNodes)
	preferred := preferredNodeScore(n, spec)

	load := existingLoadPenaltyScore(n)

	score := w.Health*health +
		w.CapacityFit*capacity +
		w.AcceleratorFit*accel +
		w.DataLocality*locality +
		w.Reliability*1.0 + // reliability requires historical op data; default 1.0
		w.PreferredNodeBonus*preferred -
		w.ArtifactTransfer*artifact -
		w.ExistingLoadPenalty*load

	if score < 0 {
		score = 0
	}
	return score
}

func existingLoadPenaltyScore(n domain.Node) float64 {
	if n.CPUThreads == 0 {
		return 0
	}
	ratio := float64(n.ActiveInstances) / float64(n.CPUThreads)
	if ratio > 1 {
		return 1
	}
	return ratio
}

func healthScore(n domain.Node) float64 {
	if n.State == domain.NodeReady {
		return 1.0
	}
	if n.State == domain.NodeDegraded {
		return 0.5
	}
	return 0.0
}

func capacityFitScore(n domain.Node, spec domain.ServiceSpec) float64 {
	if n.TotalRAMMiB == 0 {
		return 0
	}
	ramRatio := float64(n.AvailRAMMiB) / float64(n.TotalRAMMiB)
	cpuRatio := 1.0
	if n.CPUThreads > 0 && spec.Resources.CPUThreads > 0 {
		remaining := n.AvailCPUThreads - spec.Resources.CPUThreads
		if remaining < 0 {
			remaining = 0
		}
		cpuRatio = float64(remaining) / float64(n.CPUThreads)
	}
	return (ramRatio*0.5 + cpuRatio*0.5)
}

func acceleratorFitScore(n domain.Node, spec domain.ServiceSpec) float64 {
	if !spec.Resources.GPU.Required {
		return 0
	}
	eligible := n.EligibleGPUs(spec.Resources.GPU.MinVRAMMiB)
	if len(eligible) == 0 {
		return 0
	}
	// Score: largest eligible VRAM / total GPU VRAM of node (prefer more headroom)
	totalVRAM := n.TotalGPUVRAMMiB()
	if totalVRAM == 0 {
		return 0
	}
	var maxEligible int64
	for _, g := range eligible {
		if g.VRAMMiB > maxEligible {
			maxEligible = g.VRAMMiB
		}
	}
	return float64(maxEligible) / float64(totalVRAM)
}

func dataLocalityScore(n domain.Node, spec domain.ServiceSpec) float64 {
	if spec.Placement.PreferredLocation == "" {
		return 0.5 // neutral
	}
	if n.Location == spec.Placement.PreferredLocation {
		return 1.0
	}
	return 0.0
}

// artifactTransferScore returns a transfer cost penalty in [0, 1].
// VS-F-S2: the penalty is scaled by H(p) — the Shannon binary entropy of the
// availability fraction p = SeederCount/totalNodes — so rare artifacts impose
// a higher penalty (entropy is low, p → 0, cost is high) and widely-available
// artifacts impose a low penalty regardless of node selection.
//
//	penalty = H(p) * (1 - p)
//
// where H(p) = -p·log₂(p) - (1-p)·log₂(1-p).  H(p) ∈ [0,1]; the product
// ensures penalty→0 as p→1 (artifact on every node, no transfer needed).
func artifactTransferScore(n domain.Node, spec domain.ServiceSpec, catalog ArtifactCatalog, totalNodes int) float64 {
	if catalog == nil {
		return 0
	}
	if spec.Artifact.Type != domain.ArtifactTypeOcelot || spec.Artifact.InfoHash == "" {
		return 0
	}
	status, err := catalog.Lookup(context.Background(), spec.Artifact.InfoHash)
	if err != nil || !status.Exists {
		return 1.0 // artifact unknown: maximum transfer cost
	}
	if totalNodes <= 0 {
		if !status.Available {
			return 1.0
		}
		return 0.0
	}
	p := float64(status.SeederCount) / float64(totalNodes)
	if p > 1.0 {
		p = 1.0
	}
	return binaryEntropyPenalty(p)
}

// binaryEntropyPenalty returns H(p)·(1-p) where H(p) is Shannon binary entropy.
// At p=0 → 0 (no seeders, unavoidable — max transfer cost signalled by caller).
// At p=0.5 → 0.5 (moderate availability, moderate penalty).
// At p=1.0 → 0 (fully available, no penalty).
func binaryEntropyPenalty(p float64) float64 {
	if p <= 0 || p >= 1 {
		return 1 - p // 0→1 (max cost), 1→0 (no cost)
	}
	h := -p*math.Log2(p) - (1-p)*math.Log2(1-p) // ∈ (0,1]
	return h * (1 - p)
}

func preferredNodeScore(n domain.Node, spec domain.ServiceSpec) float64 {
	for _, pref := range spec.Placement.PreferredNodes {
		if n.Name == pref || n.ID == pref {
			return 1.0
		}
	}
	return 0.0
}
