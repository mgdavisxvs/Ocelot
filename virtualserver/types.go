package virtualserver

import "time"

// ── Node ──────────────────────────────────────────────────────────────────────

// NodeState is the administrative state of a registered compute node.
type NodeState string

const (
	NodeStateReady    NodeState = "ready"
	NodeStateDraining NodeState = "draining"
	NodeStateOffline  NodeState = "offline"
)

// GPU describes one physical accelerator on a node.
type GPU struct {
	DeviceID string `json:"device_id"`
	Model    string `json:"model"`
	VRAMBytes int64 `json:"vram_bytes"`
}

// Node represents a registered compute node.
type Node struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Arch          string            `json:"arch"` // amd64, arm64, …
	Labels        map[string]string `json:"labels"`
	State         NodeState         `json:"state"`
	CPUMillicores int               `json:"cpu_millicores"` // total
	RAMBytes      int64             `json:"ram_bytes"`      // total
	GPUs          []GPU             `json:"gpus"`
	BearerToken   string            `json:"-"` // node auth credential
	LastHeartbeat time.Time         `json:"last_heartbeat"`
	CreatedAt     time.Time         `json:"created_at"`
}

// ── Service ───────────────────────────────────────────────────────────────────

// RestartPolicy governs how the reconciler reacts when an instance exits.
type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "Always"
	RestartOnFailure RestartPolicy = "OnFailure"
	RestartNever     RestartPolicy = "Never"
)

// GPURequirement declares how many accelerators a service needs.
type GPURequirement struct {
	Count         int    `json:"count"`
	VRAMPerDevice int64  `json:"vram_per_device"` // bytes per GPU
	ModelFilter   string `json:"model_filter"`    // optional substring match on GPU.Model
}

// VolumeMount declares a named volume binding for a service.
type VolumeMount struct {
	Name      string `json:"name"`
	VolumeID  string `json:"volume_id"`
	MountPath string `json:"mount_path"`
	ReadOnly  bool   `json:"read_only"`
}

// Service is a desired-state manifest for a workload.
type Service struct {
	ID              string            `json:"id"`
	Namespace       string            `json:"namespace"`
	Name            string            `json:"name"`
	Image           string            `json:"image"`
	CPUMillicores   int               `json:"cpu_millicores"`
	RAMBytes        int64             `json:"ram_bytes"`
	GPUs            []GPURequirement  `json:"gpus"`
	NodeSelector    map[string]string `json:"node_selector"`
	NodeExclusions  []string          `json:"node_exclusions"`
	RestartPolicy   RestartPolicy     `json:"restart_policy"`
	VolumeMounts    []VolumeMount     `json:"volume_mounts"`
	ArtifactHash    string            `json:"artifact_hash,omitempty"` // torrent info-hash required before provisioning
	Env             map[string]string `json:"env"`
	Annotations     map[string]string `json:"annotations"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// ── Instance ──────────────────────────────────────────────────────────────────

// InstanceState is the state-machine state of a running workload instance.
type InstanceState string

const (
	InstanceStateScheduled    InstanceState = "scheduled"
	InstanceStateProvisioning InstanceState = "provisioning"
	InstanceStateStarting     InstanceState = "starting"
	InstanceStateRunning      InstanceState = "running"
	InstanceStateStopping     InstanceState = "stopping"
	InstanceStateStopped      InstanceState = "stopped"
	InstanceStateFailed       InstanceState = "failed"
)

// Instance is a single running (or recently terminated) copy of a Service.
type Instance struct {
	ID         string        `json:"id"`
	ServiceID  string        `json:"service_id"`
	Namespace  string        `json:"namespace"`
	NodeID     string        `json:"node_id"`
	State      InstanceState `json:"state"`
	Message    string        `json:"message"`
	RuntimeID  string        `json:"runtime_id"` // opaque adapter identifier
	Restarts   int           `json:"restarts"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// ── Allocation ────────────────────────────────────────────────────────────────

// Allocation records the resource accounting for one instance on one node.
type Allocation struct {
	ID            string    `json:"id"`
	InstanceID    string    `json:"instance_id"`
	NodeID        string    `json:"node_id"`
	CPUMillicores int       `json:"cpu_millicores"`
	RAMBytes      int64     `json:"ram_bytes"`
	GPUDeviceIDs  []string  `json:"gpu_device_ids"`
	CreatedAt     time.Time `json:"created_at"`
}

// ── Volume ────────────────────────────────────────────────────────────────────

// Volume is a persistent storage resource.
type Volume struct {
	ID          string    `json:"id"`
	Namespace   string    `json:"namespace"`
	Name        string    `json:"name"`
	DriverName  string    `json:"driver_name"`
	NodeAffinity string   `json:"node_affinity"` // nodeID this volume is pinned to
	SizeBytes   int64     `json:"size_bytes"`
	Handle      string    `json:"handle"` // opaque VolumeHandle value stored in DB
	CreatedAt   time.Time `json:"created_at"`
}

// Snapshot is a point-in-time copy of a Volume.
type Snapshot struct {
	ID            string    `json:"id"`
	VolumeID      string    `json:"volume_id"`
	SnapshotHandle string   `json:"snapshot_handle"`
	SizeBytes     int64     `json:"size_bytes"`
	CreatedAt     time.Time `json:"created_at"`
}

// VolumeBinding links an Instance to a Volume for the duration of a run.
type VolumeBinding struct {
	ID         string    `json:"id"`
	InstanceID string    `json:"instance_id"`
	VolumeID   string    `json:"volume_id"`
	MountPath  string    `json:"mount_path"`
	ReadOnly   bool      `json:"read_only"`
	HostPath   string    `json:"host_path"` // resolved MountPoint.HostPath
	CreatedAt  time.Time `json:"created_at"`
}

// ── Operation ─────────────────────────────────────────────────────────────────

// OperationState is the lifecycle state of an async operation.
type OperationState string

const (
	OpStatePending   OperationState = "pending"
	OpStateRunning   OperationState = "running"
	OpStateSucceeded OperationState = "succeeded"
	OpStateFailed    OperationState = "failed"
)

// Operation tracks an async control-plane action (snapshot, restore, etc.).
type Operation struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	ResourceID string         `json:"resource_id"`
	State      OperationState `json:"state"`
	Message    string         `json:"message"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// ── Placement ─────────────────────────────────────────────────────────────────

// PlacementDecision records the outcome of the scheduler for one instance.
type PlacementDecision struct {
	InstanceID string    `json:"instance_id"`
	NodeID     string    `json:"node_id"`
	Score      float64   `json:"score"`
	Reason     string    `json:"reason"`
	DecidedAt  time.Time `json:"decided_at"`
}

// ── Namespace ─────────────────────────────────────────────────────────────────

// Namespace is a logical isolation boundary for services and volumes.
type Namespace struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
