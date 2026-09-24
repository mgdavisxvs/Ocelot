package virtualserver

import (
	"fmt"
	"strings"
	"time"
)

// Scheduler filters candidate nodes through eight hard constraints then scores
// survivors on five dimensions to produce a single PlacementDecision.
type Scheduler struct {
	db *db
}

func newScheduler(d *db) *Scheduler { return &Scheduler{db: d} }

// Schedule attempts to place svc on one of the available nodes.
// It returns an error if no node satisfies all hard constraints.
func (s *Scheduler) Schedule(svc *Service) (*PlacementDecision, error) {
	nodes, err := s.db.listNodes()
	if err != nil {
		return nil, fmt.Errorf("scheduler: list nodes: %w", err)
	}

	// Collect the set of volume-affinity node requirements for this service.
	affinityNodes, err := s.volumeAffinityConstraint(svc)
	if err != nil {
		return nil, err
	}

	var candidates []*Node
	for _, n := range nodes {
		if ok, _ := s.passesHardConstraints(n, svc, affinityNodes); ok {
			candidates = append(candidates, n)
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("scheduler: no node satisfies all constraints for service %q", svc.ID)
	}

	best, bestScore := candidates[0], s.score(candidates[0], svc)
	for _, n := range candidates[1:] {
		if sc := s.score(n, svc); sc > bestScore {
			best, bestScore = n, sc
		}
	}

	decision := &PlacementDecision{
		NodeID:    best.ID,
		Score:     bestScore,
		Reason:    fmt.Sprintf("%d candidate(s); winner %s score=%.3f", len(candidates), best.Name, bestScore),
		DecidedAt: time.Now().UTC(),
	}
	return decision, nil
}

// ── eight hard constraints ────────────────────────────────────────────────────

// passesHardConstraints returns (true, nil) if n passes all 8 hard constraints
// for svc. On false it returns a human-readable reason.
func (s *Scheduler) passesHardConstraints(n *Node, svc *Service, affinityNodes map[string]struct{}) (bool, string) {
	// 1 – node state must be ready
	if n.State != NodeStateReady {
		return false, fmt.Sprintf("node %s state=%s", n.ID, n.State)
	}

	// 2 – architecture label must match if svc specifies "arch"
	if want, ok := svc.NodeSelector["arch"]; ok && n.Arch != want {
		return false, fmt.Sprintf("arch mismatch: node=%s want=%s", n.Arch, want)
	}

	// 3 – label selectors
	for k, want := range svc.NodeSelector {
		if k == "arch" {
			continue
		}
		if got, ok := n.Labels[k]; !ok || got != want {
			return false, fmt.Sprintf("label %s=%s not satisfied", k, want)
		}
	}

	// 4 – node exclusions
	for _, excl := range svc.NodeExclusions {
		if n.ID == excl || n.Name == excl {
			return false, fmt.Sprintf("node %s is in exclusion list", n.ID)
		}
	}

	// 5 – CPU headroom
	freeCPU, freeRAM, err := s.db.nodeHeadroom(n.ID, n.CPUMillicores, n.RAMBytes)
	if err != nil {
		return false, fmt.Sprintf("headroom query error: %v", err)
	}
	if freeCPU < svc.CPUMillicores {
		return false, fmt.Sprintf("insufficient CPU: free=%dm need=%dm", freeCPU, svc.CPUMillicores)
	}

	// 6 – RAM headroom
	if freeRAM < svc.RAMBytes {
		return false, fmt.Sprintf("insufficient RAM: free=%d need=%d", freeRAM, svc.RAMBytes)
	}

	// 7 – per-device GPU VRAM
	if len(svc.GPUs) > 0 {
		usedGPUs, _ := s.db.nodeAllocatedGPUs(n.ID)
		for _, req := range svc.GPUs {
			if ok, reason := gpuConstraintSatisfied(n.GPUs, usedGPUs, req); !ok {
				return false, reason
			}
		}
	}

	// 8 – single-node volume affinity
	if len(affinityNodes) > 0 {
		if _, ok := affinityNodes[n.ID]; !ok {
			return false, fmt.Sprintf("volume affinity requires node in %v", affinityNodes)
		}
	}

	return true, ""
}

// gpuConstraintSatisfied checks that node has enough free GPUs matching req.
func gpuConstraintSatisfied(nodeGPUs []GPU, usedIDs map[string]struct{}, req GPURequirement) (bool, string) {
	var free []GPU
	for _, g := range nodeGPUs {
		if _, used := usedIDs[g.DeviceID]; used {
			continue
		}
		if req.VRAMPerDevice > 0 && g.VRAMBytes < req.VRAMPerDevice {
			continue
		}
		if req.ModelFilter != "" && !strings.Contains(g.Model, req.ModelFilter) {
			continue
		}
		free = append(free, g)
	}
	if len(free) < req.Count {
		return false, fmt.Sprintf("GPU constraint unsatisfied: need %d got %d free", req.Count, len(free))
	}
	return true, ""
}

// volumeAffinityConstraint returns the set of node IDs that all volume mounts
// in svc are already pinned to.  If volumes have mixed affinities the constraint
// is unsatisfiable and returns an error.  An empty map means no affinity pinning.
func (s *Scheduler) volumeAffinityConstraint(svc *Service) (map[string]struct{}, error) {
	pinned := make(map[string]struct{})
	for _, vm := range svc.VolumeMounts {
		affNode, err := s.db.volumeAffinityNode(vm.VolumeID)
		if err != nil || affNode == "" {
			continue
		}
		pinned[affNode] = struct{}{}
	}
	if len(pinned) > 1 {
		return nil, fmt.Errorf("scheduler: service %q volumes are pinned to different nodes", svc.ID)
	}
	return pinned, nil
}

// ── five scoring dimensions ────────────────────────────────────────────────────

// score returns a [0,1] value for n w.r.t. svc.  Higher is better.
func (s *Scheduler) score(n *Node, svc *Service) float64 {
	return (s.scoreHealth(n) +
		s.scoreCapacityFit(n, svc) +
		s.scoreAcceleratorFit(n, svc) +
		s.scoreDataLocality(n, svc) +
		s.scoreReliability(n)) / 5.0
}

// 1. Health: penalise nodes whose heartbeat is stale (> 60 s).
func (s *Scheduler) scoreHealth(n *Node) float64 {
	age := time.Since(n.LastHeartbeat)
	if age < 30*time.Second {
		return 1.0
	}
	if age > 120*time.Second {
		return 0.0
	}
	return 1.0 - float64(age-30*time.Second)/float64(90*time.Second)
}

// 2. Capacity fit: prefer tightly packed nodes (less remaining headroom).
func (s *Scheduler) scoreCapacityFit(n *Node, svc *Service) float64 {
	freeCPU, freeRAM, err := s.db.nodeHeadroom(n.ID, n.CPUMillicores, n.RAMBytes)
	if err != nil || n.CPUMillicores == 0 {
		return 0.5
	}
	cpuUtil := 1.0 - float64(freeCPU-svc.CPUMillicores)/float64(n.CPUMillicores)
	ramUtil := 0.5
	if n.RAMBytes > 0 {
		ramUtil = 1.0 - float64(freeRAM-svc.RAMBytes)/float64(n.RAMBytes)
	}
	return clamp01((cpuUtil + ramUtil) / 2.0)
}

// 3. Accelerator fit: prefer nodes whose free GPU models match the service request.
func (s *Scheduler) scoreAcceleratorFit(n *Node, svc *Service) float64 {
	if len(svc.GPUs) == 0 {
		return 1.0
	}
	usedGPUs, _ := s.db.nodeAllocatedGPUs(n.ID)
	var totalFree, matchingFree int
	for _, g := range n.GPUs {
		if _, used := usedGPUs[g.DeviceID]; used {
			continue
		}
		totalFree++
		for _, req := range svc.GPUs {
			if req.ModelFilter == "" || strings.Contains(g.Model, req.ModelFilter) {
				matchingFree++
				break
			}
		}
	}
	if totalFree == 0 {
		return 0.0
	}
	return clamp01(float64(matchingFree) / float64(totalFree))
}

// 4. Data locality: prefer nodes that already host the required volumes.
func (s *Scheduler) scoreDataLocality(n *Node, svc *Service) float64 {
	if len(svc.VolumeMounts) == 0 {
		return 1.0
	}
	local := 0
	for _, vm := range svc.VolumeMounts {
		aff, _ := s.db.volumeAffinityNode(vm.VolumeID)
		if aff == n.ID {
			local++
		}
	}
	return clamp01(float64(local) / float64(len(svc.VolumeMounts)))
}

// 5. Reliability: penalise nodes with recent instance failures (last 24 h).
func (s *Scheduler) scoreReliability(n *Node) float64 {
	failures, err := s.db.nodeRecentFailures(n.ID, time.Now().Add(-24*time.Hour))
	if err != nil {
		return 0.5
	}
	if failures == 0 {
		return 1.0
	}
	if failures >= 5 {
		return 0.0
	}
	return 1.0 - float64(failures)/5.0
}

// ── helper ────────────────────────────────────────────────────────────────────

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
