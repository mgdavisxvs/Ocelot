package scheduler

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	vsmetrics "github.com/mgdavisxvs/Ocelot/virtualserver/metrics"
)

// Scheduler performs constraint-first placement decisions.
// It is safe for concurrent use; a mutex prevents concurrent allocations
// from claiming the same resources.
type Scheduler struct {
	mu      sync.Mutex
	weights Weights
}

// New creates a Scheduler with the given weight configuration.
func New(w Weights) *Scheduler {
	if w == (Weights{}) {
		w = DefaultWeights()
	}
	return &Scheduler{weights: w}
}

// Schedule selects the best eligible node for a service manifest.
// It applies hard constraints first (eliminating nodes), then scores survivors.
// The allocation must be committed by the caller via VSStore.AllocateResources.
func (s *Scheduler) Schedule(ctx context.Context, m domain.ServiceManifest, nodes []domain.Node, catalog ArtifactCatalog) (*domain.PlacementDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	decision := &domain.PlacementDecision{
		RejectionReasons: make(map[string][]string),
	}

	spec := m.Spec
	var candidates []scoredNode

	for _, n := range nodes {
		reasons := hardFilter(n, spec)
		if len(reasons) > 0 {
			decision.RejectionReasons[n.Name] = reasons
			continue
		}
		// Pass all hard constraints — score this node
		score := computeScore(n, spec, catalog, s.weights)
		candidates = append(candidates, scoredNode{node: n, score: score})
	}

	if len(candidates) == 0 {
		reason := "no_candidates"
		if len(nodes) > 0 {
			reason = "resource_exhausted"
		}
		vsmetrics.PlacementFailures.WithLabelValues(reason).Inc()
		return decision, fmt.Errorf("no eligible node found for %s/%s: all %d candidates rejected",
			m.Metadata.Namespace, m.Metadata.Name, len(nodes))
	}

	// Deterministic sort: score DESC, then node name ASC for tie-breaking (Knuth K1)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].node.Name < candidates[j].node.Name
	})

	best := candidates[0]
	decision.SelectedNodeID = best.node.ID
	decision.Score = best.score
	decision.Reasons = []string{
		fmt.Sprintf("architecture satisfied (%s)", best.node.Arch),
		fmt.Sprintf("RAM satisfied (avail %d MiB)", best.node.AvailRAMMiB),
	}
	if spec.Resources.GPU.Required {
		eligible := best.node.EligibleGPUs(spec.Resources.GPU.MinVRAMMiB)
		if len(eligible) > 0 {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("GPU device %s satisfies %d MiB VRAM", eligible[0].Model, spec.Resources.GPU.MinVRAMMiB))
		}
	}
	for k, v := range spec.Placement.RequiredLabels {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("label %s=%s satisfied", k, v))
	}

	return decision, nil
}

// hardFilter returns rejection reasons for a node. Empty slice means the node passes.
func hardFilter(n domain.Node, spec domain.ServiceSpec) []string {
	var reasons []string

	// HC-01: node must be ready
	if n.State != domain.NodeReady {
		reasons = append(reasons, fmt.Sprintf("node state is %s, required ready", n.State))
		return reasons // no point checking further if not ready
	}

	// HC-02: architecture
	if spec.Placement.RequiredArch != "" && n.Arch != spec.Placement.RequiredArch {
		reasons = append(reasons, fmt.Sprintf("architecture mismatch: node is %s, required %s", n.Arch, spec.Placement.RequiredArch))
	}

	// HC-03: required labels
	for k, v := range spec.Placement.RequiredLabels {
		if n.Labels[k] != v {
			reasons = append(reasons, fmt.Sprintf("required label %s=%s not present", k, v))
		}
	}

	// HC-04: excluded nodes
	for _, excluded := range spec.Placement.ExcludedNodes {
		if n.Name == excluded || n.ID == excluded {
			reasons = append(reasons, "node explicitly excluded")
		}
	}

	// HC-05: available CPU (best effort — we store total, not avail)
	if spec.Resources.CPUThreads > 0 && n.CPUThreads < spec.Resources.CPUThreads {
		reasons = append(reasons, fmt.Sprintf("insufficient CPU: node has %d threads, need %d", n.CPUThreads, spec.Resources.CPUThreads))
	}

	// HC-06: available RAM
	if spec.Resources.RAMMiB > 0 && n.AvailRAMMiB < spec.Resources.RAMMiB {
		reasons = append(reasons, fmt.Sprintf("insufficient RAM: available %d MiB, required %d MiB", n.AvailRAMMiB, spec.Resources.RAMMiB))
	}

	// HC-07: GPU — per-device VRAM (MUST NOT sum across devices)
	if spec.Resources.GPU.Required {
		eligible := n.EligibleGPUs(spec.Resources.GPU.MinVRAMMiB)
		if len(eligible) < spec.Resources.GPU.Count {
			if spec.Resources.GPU.MinVRAMMiB > 0 {
				reasons = append(reasons, fmt.Sprintf(
					"no individual GPU satisfies %d MiB VRAM requirement", spec.Resources.GPU.MinVRAMMiB))
			}
			if len(n.GPUDevices) > 0 && len(eligible) < spec.Resources.GPU.Count {
				reasons = append(reasons, fmt.Sprintf(
					"insufficient unallocated GPU devices: need %d, have %d eligible", spec.Resources.GPU.Count, len(eligible)))
			} else if len(n.GPUDevices) == 0 {
				reasons = append(reasons, "node has no GPU devices")
			}
		}
	}

	return reasons
}

type scoredNode struct {
	node  domain.Node
	score float64
}

// ArtifactCatalog is the narrow interface the scheduler uses to assess artifact availability.
// Defined here to avoid circular imports; implemented by catalog.OcelotCatalogAdapter.
type ArtifactCatalog interface {
	Lookup(ctx context.Context, infoHash string) (domain.ArtifactStatus, error)
}
