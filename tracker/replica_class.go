package tracker

import (
	"fmt"
	"sync"
	"time"
)

// ReplicaClass classifies the retention and eviction priority of a replica on a node.
// Classes are ordered: EPHEMERAL (lowest priority) → ARCHIVAL (highest priority).
type ReplicaClass uint8

const (
	ReplicaEphemeral ReplicaClass = iota // Transient cache; evict first; no SLA
	ReplicaCache                         // Warm cache; evict after EPHEMERAL
	ReplicaStandard                      // Normal managed replica; SLA-counted
	ReplicaPinned                        // Operator-pinned; never auto-evicted
	ReplicaArchival                      // Long-term cold storage; never auto-evicted
)

func (rc ReplicaClass) String() string {
	switch rc {
	case ReplicaEphemeral:
		return "EPHEMERAL"
	case ReplicaCache:
		return "CACHE"
	case ReplicaStandard:
		return "STANDARD"
	case ReplicaPinned:
		return "PINNED"
	case ReplicaArchival:
		return "ARCHIVAL"
	default:
		return "UNKNOWN"
	}
}

// CountsTowardSLA returns true when this class contributes to replica-coverage SLA.
// Only STANDARD and PINNED count; EPHEMERAL/CACHE don't satisfy min-replica targets
// and ARCHIVAL is excluded from active-swarm guarantees.
func (rc ReplicaClass) CountsTowardSLA() bool {
	return rc == ReplicaStandard || rc == ReplicaPinned
}

// ── Per-node replica state machine ──────────────────────────────────────────
//
// The per-node replica FSM models the full lifecycle of one artifact copy
// on one managed node. States are orthogonal to the artifact-level FSM in
// verify_state.go, which tracks artifact identity verification status.
//
//	ABSENT → REQUESTED → SWARMING → BT_COMPLETE → HASH_VERIFY → VERIFIED → SEEDING
//	                                                    ↓
//	                                                 INVALID → PURGE → ABSENT (rejoin)
//	                                                              ↓
//	                                                          ABSENT (terminal clear)

type NodeReplicaState uint8

const (
	ReplicaStateAbsent     NodeReplicaState = iota // Not present on node; eligible for JOIN_SWARM
	ReplicaStateRequested                          // JOIN_SWARM sent; waiting for agent ACK
	ReplicaStateSwarming                           // Agent joined swarm; BT in progress
	ReplicaStateBTComplete                         // BT download 100%; awaiting hash check
	ReplicaStateHashVerify                         // SHA-256 verification running on node
	ReplicaStateVerified                           // Hash confirmed; not yet seeding
	ReplicaStateSeeding                            // Active seeder; counts toward SLA
	ReplicaStateInvalid                            // Hash failed; replica is corrupt
	ReplicaStatePurge                              // Being deleted from node
)

func (s NodeReplicaState) String() string {
	switch s {
	case ReplicaStateAbsent:
		return "ABSENT"
	case ReplicaStateRequested:
		return "REQUESTED"
	case ReplicaStateSwarming:
		return "SWARMING"
	case ReplicaStateBTComplete:
		return "BT_COMPLETE"
	case ReplicaStateHashVerify:
		return "HASH_VERIFY"
	case ReplicaStateVerified:
		return "VERIFIED"
	case ReplicaStateSeeding:
		return "SEEDING"
	case ReplicaStateInvalid:
		return "INVALID"
	case ReplicaStatePurge:
		return "PURGE"
	default:
		return "UNKNOWN"
	}
}

// CountsAsVerified returns true when this replica contributes to the verified-topology count.
// VERIFIED and SEEDING both confirm successful hash check.
func (s NodeReplicaState) CountsAsVerified() bool {
	return s == ReplicaStateVerified || s == ReplicaStateSeeding
}

var nodeReplicaTransitions = map[NodeReplicaState]map[NodeReplicaState]bool{
	ReplicaStateAbsent:     {ReplicaStateRequested: true},
	ReplicaStateRequested:  {ReplicaStateSwarming: true, ReplicaStateAbsent: true}, // ACK or timeout
	ReplicaStateSwarming:   {ReplicaStateBTComplete: true, ReplicaStateAbsent: true},
	ReplicaStateBTComplete: {ReplicaStateHashVerify: true},
	ReplicaStateHashVerify: {ReplicaStateVerified: true, ReplicaStateInvalid: true},
	ReplicaStateVerified:   {ReplicaStateSeeding: true, ReplicaStateInvalid: true},
	ReplicaStateSeeding:    {ReplicaStateInvalid: true, ReplicaStateAbsent: true}, // eviction
	ReplicaStateInvalid:    {ReplicaStatePurge: true},
	ReplicaStatePurge:      {ReplicaStateAbsent: true}, // cleared; eligible for rejoin
}

// ErrReplicaInvalidTransition is returned when a NodeReplica transition is not allowed.
var ErrReplicaInvalidTransition = fmt.Errorf("invalid node replica state transition")

// NodeReplica tracks the lifecycle of a single artifact copy on a managed node.
type NodeReplica struct {
	mu sync.Mutex

	NodeID     uint64
	InfoHash   string
	Class      ReplicaClass
	State      NodeReplicaState
	RetryCount int

	LastTransition time.Time
	RequestedAt    time.Time // when JOIN_SWARM was sent
	VerifiedAt     time.Time // when HASH_VERIFY succeeded
}

func NewNodeReplica(nodeID uint64, infoHash string, class ReplicaClass) *NodeReplica {
	return &NodeReplica{
		NodeID:         nodeID,
		InfoHash:       infoHash,
		Class:          class,
		State:          ReplicaStateAbsent,
		LastTransition: time.Now(),
	}
}

// Transition advances the state machine. Returns the new state or an error.
// Idempotent: transitioning to the same state from itself returns without error
// (handles double JOIN_SWARM gracefully — "already participating").
func (r *NodeReplica) Transition(next NodeReplicaState) (NodeReplicaState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.State == next {
		return r.State, nil // idempotent
	}
	if !nodeReplicaTransitions[r.State][next] {
		return r.State, fmt.Errorf("%w: %s → %s", ErrReplicaInvalidTransition, r.State, next)
	}
	r.State = next
	r.LastTransition = time.Now()
	if next == ReplicaStateRequested {
		r.RequestedAt = time.Now()
	}
	if next == ReplicaStateVerified {
		r.VerifiedAt = time.Now()
	}
	return r.State, nil
}

// GetState returns the current state without locking the caller.
func (r *NodeReplica) GetState() NodeReplicaState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.State
}

// ForceEvict immediately retires a replica on an UNREACHABLE node by bypassing
// the normal FSM, setting State directly to ABSENT. Only acts on SEEDING and
// VERIFIED states (the two counted by VerifiedCount). Idempotent on others.
// Called by the GraceTTL retirement pass in retireGraceExpired.
func (r *NodeReplica) ForceEvict() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.State == ReplicaStateSeeding || r.State == ReplicaStateVerified {
		r.State = ReplicaStateAbsent
		r.LastTransition = time.Now()
	}
}

// UpgradeClass promotes the replica's class when to is higher priority than current.
// Returns true if a promotion occurred. Used by the auto-classification engine.
func (r *NodeReplica) UpgradeClass(to ReplicaClass) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if to > r.Class {
		r.Class = to
		return true
	}
	return false
}

// ── NodeReplicaMap ────────────────────────────────────────────────────────────
// Concurrent-safe registry of NodeReplica indexed by (nodeID, infoHash).

type replicaKey struct {
	nodeID   uint64
	infoHash string
}

type NodeReplicaMap struct {
	mu      sync.RWMutex
	entries map[replicaKey]*NodeReplica
}

func NewNodeReplicaMap() *NodeReplicaMap {
	return &NodeReplicaMap{entries: make(map[replicaKey]*NodeReplica)}
}

func (m *NodeReplicaMap) Get(nodeID uint64, infoHash string) (*NodeReplica, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.entries[replicaKey{nodeID, infoHash}]
	return r, ok
}

func (m *NodeReplicaMap) GetOrCreate(nodeID uint64, infoHash string, class ReplicaClass) *NodeReplica {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := replicaKey{nodeID, infoHash}
	if r, ok := m.entries[k]; ok {
		return r
	}
	r := NewNodeReplica(nodeID, infoHash, class)
	m.entries[k] = r
	return r
}

func (m *NodeReplicaMap) Delete(nodeID uint64, infoHash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, replicaKey{nodeID, infoHash})
}

// ForHash iterates all replicas for a given info_hash.
func (m *NodeReplicaMap) ForHash(infoHash string, fn func(r *NodeReplica) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k, r := range m.entries {
		if k.infoHash == infoHash {
			if !fn(r) {
				break
			}
		}
	}
}

// VerifiedCount returns the number of managed nodes in VERIFIED or SEEDING state for a hash.
func (m *NodeReplicaMap) VerifiedCount(infoHash string) int {
	count := 0
	m.ForHash(infoHash, func(r *NodeReplica) bool {
		if r.GetState().CountsAsVerified() && r.Class.CountsTowardSLA() {
			count++
		}
		return true
	})
	return count
}
