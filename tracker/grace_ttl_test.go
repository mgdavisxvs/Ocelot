package tracker

import (
	"testing"
	"time"
)

// ── Feature 1: GraceTTL Replica Retirement tests ─────────────────────────────

// buildGraceController builds a SwarmPolicyController with a pre-seeded
// CORE node holding a SEEDING replica, whose node is then set UNREACHABLE.
func buildGraceController(t *testing.T) (*SwarmPolicyController, *NodeReplica, *NodeIdentity) {
	t.Helper()
	c := NewSwarmPolicyController(
		NewArtifactList(),
		NewTorrentList(),
		NewNodeRegistry(),
		NewNodeReplicaMap(),
		NewSwarmAdmissionPolicy(),
		60*time.Second,
	)
	const infoHash = "gracehash"
	artifact := NewArtifact(infoHash)
	c.artifacts.Set(infoHash, artifact)

	n := NewNodeIdentity(10, "grace.node", "pk", FailureDomainLabels{})
	n.Tier = NodeTierCore
	c.nodes.Register(n)

	replica := c.replicas.GetOrCreate(10, infoHash, ReplicaStandard)
	for _, s := range []NodeReplicaState{
		ReplicaStateRequested, ReplicaStateSwarming, ReplicaStateBTComplete,
		ReplicaStateHashVerify, ReplicaStateVerified, ReplicaStateSeeding,
	} {
		if _, err := replica.Transition(s); err != nil {
			t.Fatalf("FSM transition to %s: %v", s, err)
		}
	}
	return c, replica, n
}

func TestGraceTTLRetiresUnreachableReplica(t *testing.T) {
	c, replica, n := buildGraceController(t)

	n.mu.Lock()
	n.ReachState = NodeUnreachable
	n.LastFlap = time.Now().Add(-(GraceTTL + time.Second)) // expired
	n.mu.Unlock()

	// VerifiedCount should be 1 before retirement (replica is SEEDING, STANDARD class).
	if v := c.replicas.VerifiedCount("gracehash"); v != 1 {
		t.Fatalf("pre-eviction: want VerifiedCount=1, got %d", v)
	}

	c.retireGraceExpired("gracehash")

	if replica.GetState() != ReplicaStateAbsent {
		t.Errorf("want ABSENT after grace expiry, got %s", replica.GetState())
	}
	if v := c.replicas.VerifiedCount("gracehash"); v != 0 {
		t.Errorf("want VerifiedCount=0 after eviction, got %d", v)
	}
}

func TestGraceTTLSkipsNodeWithinGrace(t *testing.T) {
	c, replica, n := buildGraceController(t)

	n.mu.Lock()
	n.ReachState = NodeUnreachable
	n.LastFlap = time.Now().Add(-30 * time.Second) // 30s — well within 10-min GraceTTL
	n.mu.Unlock()

	c.retireGraceExpired("gracehash")

	if replica.GetState() != ReplicaStateSeeding {
		t.Errorf("replica within GraceTTL must stay SEEDING, got %s", replica.GetState())
	}
}

func TestGraceTTLSkipsReachableNode(t *testing.T) {
	c, replica, _ := buildGraceController(t)
	// Node stays REACHABLE; no eviction should occur.

	c.retireGraceExpired("gracehash")

	if replica.GetState() != ReplicaStateSeeding {
		t.Errorf("replica on REACHABLE node must not be evicted, got %s", replica.GetState())
	}
}

func TestGraceTTLSkipsFlappingNode(t *testing.T) {
	c, replica, n := buildGraceController(t)

	n.mu.Lock()
	n.ReachState = NodeFlapping
	n.LastFlap = time.Now().Add(-(GraceTTL + time.Second)) // past grace, but still FLAPPING
	n.mu.Unlock()

	c.retireGraceExpired("gracehash")

	if replica.GetState() != ReplicaStateSeeding {
		t.Errorf("FLAPPING node must not trigger grace retirement, got %s", replica.GetState())
	}
}

func TestForceEvictIdempotentOnAbsent(t *testing.T) {
	r := NewNodeReplica(1, "idem", ReplicaStandard)
	// r.State == ABSENT; ForceEvict is a no-op.
	r.ForceEvict()
	if r.GetState() != ReplicaStateAbsent {
		t.Errorf("ForceEvict on ABSENT must be no-op, got %s", r.GetState())
	}
}

func TestForceEvictFromVerified(t *testing.T) {
	r := NewNodeReplica(2, "fev", ReplicaStandard)
	for _, s := range []NodeReplicaState{
		ReplicaStateRequested, ReplicaStateSwarming, ReplicaStateBTComplete,
		ReplicaStateHashVerify, ReplicaStateVerified,
	} {
		r.Transition(s) //nolint:errcheck
	}
	r.ForceEvict()
	if r.GetState() != ReplicaStateAbsent {
		t.Errorf("ForceEvict from VERIFIED: want ABSENT, got %s", r.GetState())
	}
}
