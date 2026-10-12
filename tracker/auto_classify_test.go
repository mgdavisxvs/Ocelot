package tracker

import (
	"testing"
	"time"
)

// ── OPP-A: Heatmap-Driven Tier Auto-Classification tests ─────────────────────

func buildAutoClassifyController(t *testing.T) *SwarmPolicyController {
	t.Helper()
	return NewSwarmPolicyController(
		NewArtifactList(),
		NewTorrentList(),
		NewNodeRegistry(),
		NewNodeReplicaMap(),
		NewSwarmAdmissionPolicy(),
		60*time.Second,
	)
}

// seedEdgeNode registers a EDGE node with the given ASN in both the registry
// and replica map, advancing the replica to SEEDING state.
func seedEdgeNode(t *testing.T, c *SwarmPolicyController, nodeID uint64, asn uint32, infoHash string) *NodeReplica {
	t.Helper()
	n := NewNodeIdentity(nodeID, "edge.node", "pk", FailureDomainLabels{})
	n.Tier = NodeTierEdge
	n.ASN = asn
	c.nodes.Register(n)

	replica := c.replicas.GetOrCreate(nodeID, infoHash, ReplicaEphemeral)
	// Walk the FSM to SEEDING.
	for _, next := range []NodeReplicaState{
		ReplicaStateRequested,
		ReplicaStateSwarming,
		ReplicaStateBTComplete,
		ReplicaStateHashVerify,
		ReplicaStateVerified,
		ReplicaStateSeeding,
	} {
		if _, err := replica.Transition(next); err != nil {
			t.Fatalf("FSM transition to %s failed: %v", next, err)
		}
	}
	return replica
}

func TestAutoClassifyEdgePromotedAfter3Cycles(t *testing.T) {
	c := buildAutoClassifyController(t)
	const infoHash = "testhash"
	const asn = 0xAB_0001 // /16 = 0x0001

	artifact := NewArtifact(infoHash)
	c.artifacts.Set(infoHash, artifact)

	// Record demand from the same /16 network (ASN 0x0001) — majority demand.
	// NetFraction(0x0001) will be 1.0 > 0.30, satisfying the threshold.
	for i := 0; i < 10; i++ {
		artifact.Heatmap.Record([]byte{0x00, 0x01, byte(i), 0x01})
	}

	replica := seedEdgeNode(t, c, 1, asn, infoHash)

	// Cycle 1 and 2: counter increments but no promotion yet.
	c.classifyEdgeReplicas(infoHash, artifact)
	if replica.Class != ReplicaEphemeral {
		t.Errorf("after 1 cycle: want EPHEMERAL, got %s", replica.Class)
	}
	c.classifyEdgeReplicas(infoHash, artifact)
	if replica.Class != ReplicaEphemeral {
		t.Errorf("after 2 cycles: want EPHEMERAL, got %s", replica.Class)
	}

	// Cycle 3: promotion threshold reached.
	c.classifyEdgeReplicas(infoHash, artifact)
	if replica.Class != ReplicaCache {
		t.Errorf("after 3 cycles: want CACHE, got %s", replica.Class)
	}
}

func TestAutoClassifyCounterResetOnLowFraction(t *testing.T) {
	c := buildAutoClassifyController(t)
	const infoHash = "testhash2"
	const asn = uint32(0x0001)

	artifact := NewArtifact(infoHash)
	c.artifacts.Set(infoHash, artifact)

	// Majority demand from /16 = 0x0001.
	for i := 0; i < 10; i++ {
		artifact.Heatmap.Record([]byte{0x00, 0x01, byte(i), 0x01})
	}

	replica := seedEdgeNode(t, c, 2, asn, infoHash)

	// Two qualifying cycles.
	c.classifyEdgeReplicas(infoHash, artifact)
	c.classifyEdgeReplicas(infoHash, artifact)
	key := heatCycleKey{infoHash: infoHash, nodeID: 2}
	if c.heatCycles[key] != 2 {
		t.Fatalf("want heatCycles=2 after 2 cycles, got %d", c.heatCycles[key])
	}

	// Flood heatmap with demand from a DIFFERENT /16 so node's fraction drops.
	for i := 0; i < 200; i++ {
		artifact.Heatmap.Record([]byte{0xC0, 0xA8, byte(i), 0x01}) // 192.168.x.1
	}
	// Now NetFraction(0x0001) is well below 0.30.

	c.classifyEdgeReplicas(infoHash, artifact)
	if c.heatCycles[key] != 0 {
		t.Errorf("counter should reset to 0 when fraction drops below threshold, got %d", c.heatCycles[key])
	}
	if replica.Class != ReplicaEphemeral {
		t.Errorf("replica should remain EPHEMERAL after counter reset, got %s", replica.Class)
	}
}

func TestAutoClassifyNonEdgeNotPromoted(t *testing.T) {
	c := buildAutoClassifyController(t)
	const infoHash = "testhash3"
	const asn = uint32(0x0001)

	artifact := NewArtifact(infoHash)
	c.artifacts.Set(infoHash, artifact)
	for i := 0; i < 20; i++ {
		artifact.Heatmap.Record([]byte{0x00, 0x01, byte(i), 0x01})
	}

	// CORE node — should never be promoted by the EDGE classifier.
	n := NewNodeIdentity(3, "core.node", "pk", FailureDomainLabels{})
	n.Tier = NodeTierCore
	n.ASN = asn
	c.nodes.Register(n)

	replica := c.replicas.GetOrCreate(3, infoHash, ReplicaEphemeral)
	for _, next := range []NodeReplicaState{
		ReplicaStateRequested, ReplicaStateSwarming, ReplicaStateBTComplete,
		ReplicaStateHashVerify, ReplicaStateVerified, ReplicaStateSeeding,
	} {
		replica.Transition(next) //nolint:errcheck
	}

	for i := 0; i < 5; i++ {
		c.classifyEdgeReplicas(infoHash, artifact)
	}

	if replica.Class != ReplicaEphemeral {
		t.Errorf("CORE node replica must not be auto-promoted, got %s", replica.Class)
	}
}

func TestAutoClassifySkipsAbsentReplicas(t *testing.T) {
	c := buildAutoClassifyController(t)
	const infoHash = "testhash4"
	const asn = uint32(0x0001)

	artifact := NewArtifact(infoHash)
	c.artifacts.Set(infoHash, artifact)
	for i := 0; i < 20; i++ {
		artifact.Heatmap.Record([]byte{0x00, 0x01, byte(i), 0x01})
	}

	// EDGE node exists but replica is ABSENT (not yet seeding).
	n := NewNodeIdentity(4, "edge2.node", "pk", FailureDomainLabels{})
	n.Tier = NodeTierEdge
	n.ASN = asn
	c.nodes.Register(n)
	replica := c.replicas.GetOrCreate(4, infoHash, ReplicaEphemeral) // ABSENT

	for i := 0; i < 5; i++ {
		c.classifyEdgeReplicas(infoHash, artifact)
	}

	if replica.Class != ReplicaEphemeral {
		t.Errorf("ABSENT replica must not be promoted, got %s", replica.Class)
	}
}
