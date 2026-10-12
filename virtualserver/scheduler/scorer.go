package scheduler

import (
	"context"

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
func computeScore(n domain.Node, spec domain.ServiceSpec, catalog ArtifactCatalog, w Weights) float64 {
	health := healthScore(n)
	capacity := capacityFitScore(n, spec)
	accel := acceleratorFitScore(n, spec)
	locality := dataLocalityScore(n, spec)
	artifact := artifactTransferScore(n, spec, catalog)
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

func artifactTransferScore(n domain.Node, spec domain.ServiceSpec, catalog ArtifactCatalog) float64 {
	if catalog == nil {
		return 0
	}
	if spec.Artifact.Type != domain.ArtifactTypeOcelot || spec.Artifact.InfoHash == "" {
		return 0
	}
	status, err := catalog.Lookup(context.Background(), spec.Artifact.InfoHash)
	if err != nil || !status.Exists || !status.Available {
		return 1.0 // penalty: artifact not available
	}
	return 0.0 // no penalty: artifact available
}

func preferredNodeScore(n domain.Node, spec domain.ServiceSpec) float64 {
	for _, pref := range spec.Placement.PreferredNodes {
		if n.Name == pref || n.ID == pref {
			return 1.0
		}
	}
	return 0.0
}
