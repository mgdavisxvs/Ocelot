// Package node defines the Node registry types and the agent wire protocol
// structs (request/response bodies for AGENT_PROTOCOL.md §4).
package node

import "encoding/json"

// ─── Node registry ────────────────────────────────────────────────────────────

type Status string

const (
	StatusJoining  Status = "joining"
	StatusReady    Status = "ready"
	StatusDraining Status = "draining"
	StatusBusy     Status = "busy"
	StatusDegraded Status = "degraded"
	StatusLost     Status = "lost"
	StatusDead     Status = "dead"
)

type GPUHealth string

const (
	GPUHealthReady    GPUHealth = "ready"
	GPUHealthDegraded GPUHealth = "degraded"
	GPUHealthFailed   GPUHealth = "failed"
)

type GPU struct {
	ID          string    `json:"id"`
	NodeID      string    `json:"node_id"`
	DeviceIndex int       `json:"device_index"`
	Model       string    `json:"model"`
	VRAMMb      int       `json:"vram_mb"`
	CUDACap     string    `json:"cuda_cap,omitempty"`
	Health      GPUHealth `json:"health"`
	VRAMReserved int      `json:"vram_reserved"`
	WorkloadID  string    `json:"workload_id,omitempty"`
}

type Node struct {
	ID            string            `json:"id"`
	Hostname      string            `json:"hostname"`
	DisplayName   string            `json:"display_name,omitempty"`
	Arch          string            `json:"arch"`
	CPUCores      int               `json:"cpu_cores"`
	RAMMb         int               `json:"ram_mb"`
	StorageGb     int               `json:"storage_gb"`
	Labels        map[string]string `json:"labels"`
	Status        Status            `json:"status"`
	LastHeartbeat int64             `json:"last_heartbeat"` // unix epoch ms
	AgentVersion  string            `json:"agent_version"`
	CreatedAt     int64             `json:"created_at"`
	GPUs          []GPU             `json:"gpus,omitempty"`
}

// ─── Agent → Control Plane: Registration ─────────────────────────────────────

type RegisterRequest struct {
	Hostname     string            `json:"hostname"`
	DisplayName  string            `json:"display_name,omitempty"`
	Arch         string            `json:"arch"`
	AgentVersion string            `json:"agent_version"`
	CPUCores     int               `json:"cpu_cores"`
	RAMMb        int               `json:"ram_mb"`
	StorageGb    int               `json:"storage_gb"`
	GPUs         []GPUSpec         `json:"gpus,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

type GPUSpec struct {
	DeviceIndex int    `json:"device_index"`
	Model       string `json:"model"`
	VRAMMb      int    `json:"vram_mb"`
	CUDACap     string `json:"cuda_cap,omitempty"`
}

type RegisterResponse struct {
	NodeID                 string `json:"node_id"`
	NodeSecret             string `json:"node_secret"`
	HeartbeatIntervalSec   int    `json:"heartbeat_interval_sec"`
	InventoryIntervalSec   int    `json:"inventory_interval_sec"`
	DesiredPollIntervalSec int    `json:"desired_poll_interval_sec"`
}

// ─── Agent → Control Plane: Heartbeat ────────────────────────────────────────

type HeartbeatRequest struct {
	Ts          int64   `json:"ts"`
	Status      Status  `json:"status"`
	LoadAvg1m   float64 `json:"load_avg_1m"`
	CPUUsedPct  float64 `json:"cpu_used_pct"`
	RAMUsedMb   int     `json:"ram_used_mb"`
	UptimeSec   int64   `json:"uptime_sec"`
}

type HeartbeatResponse struct {
	OK           bool  `json:"ok"`
	ServerTimeMs int64 `json:"server_time_ms"`
}

// ─── Agent → Control Plane: Inventory ────────────────────────────────────────

type InventoryGPU struct {
	DeviceIndex  int       `json:"device_index"`
	Model        string    `json:"model"`
	VRAMMb       int       `json:"vram_mb"`
	VRAMFreeMb   int       `json:"vram_free_mb"`
	CUDACap      string    `json:"cuda_cap,omitempty"`
	Health       GPUHealth `json:"health"`
	WorkloadID   string    `json:"workload_id,omitempty"`
}

type InventoryRequest struct {
	Ts           int64          `json:"ts"`
	CPUCores     int            `json:"cpu_cores"`
	RAMMb        int            `json:"ram_mb"`
	StorageGb    int            `json:"storage_gb"`
	GPUs         []InventoryGPU `json:"gpus,omitempty"`
	CPUFreePct   float64        `json:"cpu_free_pct"`
	RAMFreeMb    int            `json:"ram_free_mb"`
	DiskFreeGb   int            `json:"disk_free_gb"`
	WattsCurrent float64        `json:"watts_current,omitempty"`
	AgentVersion string         `json:"agent_version"`
}

// ─── Control Plane → Agent: Desired State ────────────────────────────────────

type WorkloadAction string

const (
	ActionRun        WorkloadAction = "run"
	ActionStop       WorkloadAction = "stop"
	ActionCheckpoint WorkloadAction = "checkpoint"
)

type ResourceSpec struct {
	CPUMillicores int `json:"cpu_millicores"`
	RAMMb         int `json:"ram_mb"`
	GPUCount      int `json:"gpu_count,omitempty"`
	GPUVRAMMb     int `json:"gpu_vram_mb,omitempty"`
}

type VolumeMount struct {
	VolumeID   string `json:"volume_id"`
	MountPoint string `json:"mount_point"`
	Driver     string `json:"driver"`
	ReadOnly   bool   `json:"read_only"`
}

type WorkloadManifest struct {
	Name                  string            `json:"name"`
	Type                  string            `json:"type"`
	ImageRef              string            `json:"image_ref,omitempty"`
	Entrypoint            string            `json:"entrypoint,omitempty"`
	Args                  []string          `json:"args,omitempty"`
	Env                   map[string]string `json:"env,omitempty"`
	Resources             ResourceSpec      `json:"resources"`
	Volumes               []VolumeMount     `json:"volumes,omitempty"`
	CheckpointIntervalSec int               `json:"checkpoint_interval_sec,omitempty"`
	TimeoutSec            int               `json:"timeout_sec,omitempty"`
}

type DesiredWorkload struct {
	ID       string           `json:"id"`
	Action   WorkloadAction   `json:"action"`
	Manifest WorkloadManifest `json:"manifest"`
}

type PrefetchItem struct {
	Infohash string `json:"infohash"`
	Priority int    `json:"priority"`
}

type VolumeSpec struct {
	VolumeID   string `json:"volume_id"`
	Driver     string `json:"driver"`
	SourcePath string `json:"source_path,omitempty"`
}

type DesiredStateResponse struct {
	SchemaVersion int               `json:"schema_version"`
	Workloads     []DesiredWorkload `json:"workloads"`
	Prefetch      []PrefetchItem    `json:"prefetch,omitempty"`
	Volumes       []VolumeSpec      `json:"volumes,omitempty"`
}

// ─── Agent → Control Plane: Health events ────────────────────────────────────

type EventKind string

const (
	EventStarted     EventKind = "started"
	EventCheckpoint  EventKind = "checkpoint"
	EventFailed      EventKind = "failed"
	EventCompleted   EventKind = "completed"
	EventOOMKilled   EventKind = "oom_killed"
	EventTimedOut    EventKind = "timed_out"
	EventStopped     EventKind = "stopped"
)

type HealthEvent struct {
	WorkloadID string    `json:"workload_id"`
	Kind       EventKind `json:"kind"`
	Ts         int64     `json:"ts"`
	PID        int       `json:"pid,omitempty"`
	ExitCode   *int      `json:"exit_code"`
	Message    string    `json:"message,omitempty"`
}

type HealthRequest struct {
	Ts             int64         `json:"ts"`
	Events         []HealthEvent `json:"events"`
	BufferOverflow bool          `json:"buffer_overflow"`
}

// ─── Agent → Control Plane: Metrics ──────────────────────────────────────────

type WorkloadMetrics struct {
	WorkloadID       string `json:"workload_id"`
	CPUMillicoreSec  int64  `json:"cpu_millicore_sec"`
	RAMMbSec         int64  `json:"ram_mb_sec"`
	GPUVRAMMbSec     int64  `json:"gpu_vram_mb_sec,omitempty"`
	BytesRead        int64  `json:"bytes_read"`
	BytesWritten     int64  `json:"bytes_written"`
	NetBytesIn       int64  `json:"net_bytes_in"`
	NetBytesOut      int64  `json:"net_bytes_out"`
}

type NodeMetrics struct {
	CPUMillicoreSec int64   `json:"cpu_millicore_sec"`
	RAMMbSec        int64   `json:"ram_mb_sec"`
	WattsSec        float64 `json:"watts_sec,omitempty"`
}

type MetricsRequest struct {
	Ts          int64             `json:"ts"`
	IntervalSec int               `json:"interval_sec"`
	Workloads   []WorkloadMetrics `json:"workloads,omitempty"`
	Node        NodeMetrics       `json:"node"`
}

// ─── Error envelope ───────────────────────────────────────────────────────────

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Retry   bool   `json:"retry"`
}

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// MarshalJSON is provided so callers can safely round-trip DesiredStateResponse.
func (d DesiredStateResponse) MarshalJSON() ([]byte, error) {
	type alias DesiredStateResponse
	return json.Marshal(alias(d))
}
