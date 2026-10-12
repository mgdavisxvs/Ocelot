package domain

import (
	"fmt"
	"time"
)

// NodeState represents the lifecycle state of a compute node.
type NodeState string

const (
	NodeDiscovered  NodeState = "discovered"
	NodeReady       NodeState = "ready"
	NodeDegraded    NodeState = "degraded"
	NodeDraining    NodeState = "draining"
	NodeMaintenance NodeState = "maintenance"
	NodeQuarantined NodeState = "quarantined"
	NodeRetired     NodeState = "retired"
)

// allowedNodeTransitions is the authoritative state machine for nodes.
var allowedNodeTransitions = map[NodeState]map[NodeState]bool{
	NodeDiscovered: {
		NodeReady:       true,
		NodeQuarantined: true,
	},
	NodeReady: {
		NodeDegraded:    true,
		NodeDraining:    true,
		NodeMaintenance: true,
		NodeQuarantined: true,
	},
	NodeDegraded: {
		NodeReady:       true,
		NodeDraining:    true,
		NodeQuarantined: true,
	},
	NodeDraining: {
		NodeMaintenance: true,
		NodeQuarantined: true,
		NodeRetired:     true,
	},
	NodeMaintenance: {
		NodeReady:       true,
		NodeQuarantined: true,
	},
	NodeQuarantined: {
		NodeMaintenance: true,
		NodeRetired:     true,
	},
	NodeRetired: {}, // terminal
}

// ErrInvalidTransition is returned when a state change is not permitted.
type ErrInvalidTransition struct {
	From NodeState
	To   NodeState
}

func (e ErrInvalidTransition) Error() string {
	return fmt.Sprintf("invalid node state transition: %s → %s", e.From, e.To)
}

// ValidateNodeTransition returns nil if the transition from→to is legal.
func ValidateNodeTransition(from, to NodeState) error {
	allowed, ok := allowedNodeTransitions[from]
	if !ok {
		return fmt.Errorf("unknown source node state: %q", from)
	}
	if !allowed[to] {
		return ErrInvalidTransition{From: from, To: to}
	}
	return nil
}

// IsProtectedNodeState returns true for states that must not be automatically
// promoted to ready by a heartbeat.
func IsProtectedNodeState(s NodeState) bool {
	switch s {
	case NodeQuarantined, NodeRetired, NodeDraining, NodeMaintenance:
		return true
	}
	return false
}

// GPUDevice represents one physical GPU accelerator on a node.
type GPUDevice struct {
	Index     int    // zero-based device index
	Vendor    string // e.g. "nvidia", "amd"
	Model     string // e.g. "v100", "a100"
	VRAMMiB   int64  // per-device VRAM in mebibytes
	Allocated bool   // true when claimed by an active allocation
}

// Node is a registered compute node in the VirtualServer registry.
type Node struct {
	ID               string
	Name             string
	BackendType      string
	Arch             string
	OS               string
	CPUModel         string
	CPUThreads       int
	AvailCPUThreads  int // available CPU threads (decremented by active allocations)
	TotalRAMMiB      int64
	AvailRAMMiB      int64
	StorageMiB       int64
	AvailStorageMiB  int64
	GPUDevices       []GPUDevice
	Labels           map[string]string
	Location         string
	TrustClass       string
	State            NodeState
	LastHeartbeat    *time.Time
	AgentMeta        map[string]string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ActiveInstances  int // transient: populated by reconciler for load-aware scheduling
}

// EligibleGPUs returns GPU devices that are unallocated and satisfy the minimum VRAM.
func (n *Node) EligibleGPUs(minVRAMMiB int64) []GPUDevice {
	var out []GPUDevice
	for _, g := range n.GPUDevices {
		if !g.Allocated && g.VRAMMiB >= minVRAMMiB {
			out = append(out, g)
		}
	}
	return out
}

// TotalGPUVRAMMiB returns total VRAM across all devices (for informational use only).
// Do NOT use this to satisfy per-device VRAM requirements.
func (n *Node) TotalGPUVRAMMiB() int64 {
	var total int64
	for _, g := range n.GPUDevices {
		total += g.VRAMMiB
	}
	return total
}
