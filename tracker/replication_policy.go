package tracker

import (
	"math"
	"sync"
)

// ReplicationConfig holds all tunable parameters for the replication policy.
// All fields are configurable at runtime; none are hardcoded.
type ReplicationConfig struct {
	// EMA smoothing factor for popularity-based replica scaling.
	// α = 1/(1 + τ/Δt) where τ is the characteristic demand decay time
	// and Δt is the evaluation period. Default 0.3 is reasonable to start;
	// calibrate from the autocorrelation of D_24h over 30 days of real data.
	EMAAlpha float64 `yaml:"ema_alpha"`

	// ScaleDownThreshold is the number of consecutive evaluation periods
	// where desired < current before a scale-down is applied.
	// This hysteresis prevents replica churn during brief demand dips.
	ScaleDownThreshold int `yaml:"scale_down_threshold"`

	// PartitionPolicy controls controller behavior when nodes become unreachable.
	// Valid values: "replicate_in_majority" | "alert_and_halt" | "wait"
	// Default: "replicate_in_majority" (CP semantics — prefer consistency).
	PartitionPolicy string `yaml:"partition_policy"`

	// HeartbeatInterval is how often the controller pings registered nodes.
	// A node silent for 2× HeartbeatInterval is marked UNREACHABLE.
	HeartbeatIntervalSec int `yaml:"heartbeat_interval_sec"`
}

// DefaultReplicationConfig is the baseline configuration.
var DefaultReplicationConfig = ReplicationConfig{
	EMAAlpha:             0.3,
	ScaleDownThreshold:   3,
	PartitionPolicy:      "replicate_in_majority",
	HeartbeatIntervalSec: 30,
}

// PopularityReplicaCounter computes and smooths the desired replica count
// for an artifact based on its 24-hour download count.
//
// Formula (Erdős PE-4 amendment):
//
//	D̄_24h  = α · D_24h + (1−α) · D̄_prev   [EMA]
//	R_d     = min(R_max, R_min + ⌈log₂(1 + D̄_24h)⌉)
//
// Scale-down only after ScaleDownThreshold consecutive below-threshold
// evaluation periods — prevents a quiet weekend from triggering unnecessary
// replica deletion.
type PopularityReplicaCounter struct {
	mu             sync.Mutex
	smoothed       float64
	currentDesired int
	belowCount     int
	cfg            ReplicationConfig
}

func NewPopularityReplicaCounter(cfg ReplicationConfig) *PopularityReplicaCounter {
	return &PopularityReplicaCounter{cfg: cfg}
}

// Evaluate returns the new desired replica count given the raw 24h demand
// and the artifact's static policy bounds.
func (p *PopularityReplicaCounter) Evaluate(d24h float64, minReplicas, maxReplicas int) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	// EMA smoothing
	p.smoothed = p.cfg.EMAAlpha*d24h + (1-p.cfg.EMAAlpha)*p.smoothed

	// Log-scaled desired count
	desired := minReplicas + int(math.Ceil(math.Log2(1+p.smoothed)))
	if desired > maxReplicas {
		desired = maxReplicas
	}
	if desired < minReplicas {
		desired = minReplicas
	}

	// Hysteresis: scale UP immediately, scale DOWN only after N consecutive periods
	if desired < p.currentDesired {
		p.belowCount++
		if p.belowCount >= p.cfg.ScaleDownThreshold {
			p.currentDesired = desired
			p.belowCount = 0
		}
	} else {
		p.currentDesired = desired
		p.belowCount = 0
	}

	return p.currentDesired
}

// PartitionSemantics describes how the replication controller behaves
// when nodes are unreachable (network partition).
//
// Adopted policy (M-05): replicate_in_majority — CP semantics.
//
//  1. Controller pings all registered nodes every HeartbeatInterval.
//  2. Nodes silent for > 2× HeartbeatInterval are marked UNREACHABLE.
//  3. Replication decisions are made against the REACHABLE set only.
//  4. Health is evaluated against reachable replicas:
//     - reachable >= desired           → HEALTHY
//     - min <= reachable < desired     → UNDER_REPLICATED (alert)
//     - 0 < reachable < min           → DEGRADED (page)
//     - reachable == 0                → UNAVAILABLE (critical)
//  5. On partition heal, controller reconciles automatically on next tick.
//
// In-flight job persistence (crash recovery):
//
//	Jobs are written to SQLite with columns:
//	  artifact_id, source_node_id, dest_node_id, started_at, status, retry_count
//	On startup: any job with started_at > 10 min ago and status=RUNNING is
//	  marked FAILED, retry_count incremented, and re-queued.
const (
	PartitionPolicyReplicateInMajority = "replicate_in_majority"
	PartitionPolicyAlertAndHalt        = "alert_and_halt"
	PartitionPolicyWait                = "wait"

	// NodeUnreachableMultiplier: a node is UNREACHABLE after this many
	// missed heartbeat intervals.
	NodeUnreachableMultiplier = 2
)

// ReplicationJobStatus tracks the state of an in-flight replication job
// for crash recovery. Persisted to SQLite.
type ReplicationJobStatus string

const (
	JobStatusPending   ReplicationJobStatus = "PENDING"
	JobStatusRunning   ReplicationJobStatus = "RUNNING"
	JobStatusComplete  ReplicationJobStatus = "COMPLETE"
	JobStatusFailed    ReplicationJobStatus = "FAILED"
)

// ReplicationJob represents a single artifact replication operation.
// Persisted to SQLite so the controller can recover orphaned jobs on restart.
type ReplicationJob struct {
	ID           int64
	ArtifactID   uint64
	SourceNodeID uint64
	DestNodeID   uint64
	StartedAt    int64 // unix seconds
	Status       ReplicationJobStatus
	RetryCount   int
}
