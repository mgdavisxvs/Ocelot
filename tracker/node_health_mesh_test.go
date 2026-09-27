package tracker

import (
	"testing"

	"github.com/mgdavisxvs/Ocelot/compute/agent"
)

// newTestMesh creates a NodeHealthMesh with a fresh registry and no cache
// (disables freshness gating so tests can call EvaluateDemotion directly).
func newTestMesh() (*NodeHealthMesh, *NodeRegistry) {
	reg := NewNodeRegistry()
	return NewNodeHealthMesh(reg, nil), reg
}

// registerCoreNode adds a NodeTierCore node to the registry and returns it.
func registerCoreNode(reg *NodeRegistry, id uint64) *NodeIdentity {
	n := NewNodeIdentity(id, "host"+itoa(int(id)), "", FailureDomainLabels{})
	n.Tier = NodeTierCore
	reg.Register(n)
	return n
}

// TestNodeHealthMesh_RecordAndSnapshot verifies that recorded entries appear
// in Snapshot().
func TestNodeHealthMesh_RecordAndSnapshot(t *testing.T) {
	m, _ := newTestMesh()
	m.Record(1, "hash1", agent.VerifyPassed)
	m.Record(2, "hash2", agent.VerifyFailed)

	snap := m.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("want 2 snapshot entries, got %d", len(snap))
	}
}

// TestNodeHealthMesh_MergeIdempotent confirms that merging the same snapshot
// twice does not create duplicate records.
func TestNodeHealthMesh_MergeIdempotent(t *testing.T) {
	m, _ := newTestMesh()
	m.Record(1, "hash1", agent.VerifyPassed)
	snap := m.Snapshot()

	m.Merge(snap)
	m.Merge(snap)

	if got := len(m.Snapshot()); got != 1 {
		t.Fatalf("after 2×Merge of same snap, want 1 entry, got %d", got)
	}
}

// TestNodeHealthMesh_MergeLastWriterWins confirms that a higher-Seq remote
// record replaces a lower-Seq local record.
func TestNodeHealthMesh_MergeLastWriterWins(t *testing.T) {
	m, _ := newTestMesh()
	m.Record(1, "hashX", agent.VerifyPassed)
	snap := m.Snapshot()

	// Build a remote record with the same key but higher Seq and Failed status.
	higherSeq := snap[0].Seq + 100
	remote := []NodeVerifyRecord{{
		NodeID:   1,
		InfoHash: "hashX",
		Status:   agent.VerifyFailed,
		Seq:      higherSeq,
	}}
	m.Merge(remote)

	snap2 := m.Snapshot()
	if len(snap2) != 1 {
		t.Fatalf("want 1 entry, got %d", len(snap2))
	}
	if snap2[0].Status != agent.VerifyFailed {
		t.Errorf("status = %v, want VerifyFailed after higher-Seq merge", snap2[0].Status)
	}
	if snap2[0].Seq != higherSeq {
		t.Errorf("Seq = %d, want %d", snap2[0].Seq, higherSeq)
	}
}

// TestNodeHealthMesh_StaleSeqIgnored verifies that a lower-Seq remote record
// does not overwrite a higher-Seq local record.
func TestNodeHealthMesh_StaleSeqIgnored(t *testing.T) {
	m, _ := newTestMesh()
	m.Record(1, "hashY", agent.VerifyPassed)
	snap := m.Snapshot()
	localSeq := snap[0].Seq

	// Remote record has a lower Seq.
	stale := []NodeVerifyRecord{{
		NodeID:   1,
		InfoHash: "hashY",
		Status:   agent.VerifyFailed,
		Seq:      localSeq - 1,
	}}
	m.Merge(stale)

	snap2 := m.Snapshot()
	if snap2[0].Status != agent.VerifyPassed {
		t.Errorf("stale merge overwrote local record: status = %v", snap2[0].Status)
	}
}

// TestNodeHealthMesh_EvaluateDemotionCoreToRegional verifies that a Core node
// with ≥ failThreshold consecutive VerifyFailed entries is demoted.
func TestNodeHealthMesh_EvaluateDemotionCoreToRegional(t *testing.T) {
	m, reg := newTestMesh()
	n := registerCoreNode(reg, 10)

	for i := 0; i < failThreshold; i++ {
		m.Record(10, "hash10", agent.VerifyFailed)
	}
	m.EvaluateDemotion()

	n.mu.RLock()
	tier := n.Tier
	n.mu.RUnlock()
	if tier != NodeTierRegional {
		t.Errorf("expected Core→Regional demotion, got tier=%s", tier)
	}
}

// TestNodeHealthMesh_EvaluateDemotionRegionalToEdge verifies the second step.
func TestNodeHealthMesh_EvaluateDemotionRegionalToEdge(t *testing.T) {
	m, reg := newTestMesh()
	n := NewNodeIdentity(11, "host11", "", FailureDomainLabels{})
	n.Tier = NodeTierRegional
	reg.Register(n)

	for i := 0; i < failThreshold; i++ {
		m.Record(11, "hash11", agent.VerifyFailed)
	}
	m.EvaluateDemotion()

	n.mu.RLock()
	tier := n.Tier
	n.mu.RUnlock()
	if tier != NodeTierEdge {
		t.Errorf("expected Regional→Edge demotion, got tier=%s", tier)
	}
}

// TestNodeHealthMesh_EvaluateDemotionResetAfterDemotion confirms that the
// fail counter resets after a demotion so a second evaluation does not
// demote further.
func TestNodeHealthMesh_EvaluateDemotionResetAfterDemotion(t *testing.T) {
	m, reg := newTestMesh()
	n := registerCoreNode(reg, 12)

	for i := 0; i < failThreshold; i++ {
		m.Record(12, "hash12", agent.VerifyFailed)
	}
	m.EvaluateDemotion()

	n.mu.RLock()
	after1 := n.Tier
	n.mu.RUnlock()
	if after1 != NodeTierRegional {
		t.Fatalf("first demotion: expected Regional, got %s", after1)
	}

	// Second evaluation — counter should be 0; no further demotion.
	m.EvaluateDemotion()
	n.mu.RLock()
	after2 := n.Tier
	n.mu.RUnlock()
	if after2 != NodeTierRegional {
		t.Errorf("second evaluation: unexpected demotion to %s", after2)
	}
}

// TestNodeHealthMesh_PassedResetsCounter verifies that a VerifyPassed record
// resets the consecutive-failure counter so EvaluateDemotion does not fire.
func TestNodeHealthMesh_PassedResetsCounter(t *testing.T) {
	m, reg := newTestMesh()
	n := registerCoreNode(reg, 13)

	// Two failures followed by a pass.
	m.Record(13, "hash13", agent.VerifyFailed)
	m.Record(13, "hash13b", agent.VerifyFailed)
	m.Record(13, "hash13c", agent.VerifyPassed)
	m.EvaluateDemotion()

	n.mu.RLock()
	tier := n.Tier
	n.mu.RUnlock()
	if tier != NodeTierCore {
		t.Errorf("expected no demotion after pass reset, got tier=%s", tier)
	}
}

// TestNodeHealthMesh_StaleCacheGateSkips verifies that EvaluateDemotion is
// skipped when the Markov shadow cache is stale.
func TestNodeHealthMesh_StaleCacheGateSkips(t *testing.T) {
	reg := NewNodeRegistry()
	staleCache := NewMarkovShadowCache(1) // 1 ns TTL — always stale
	// Never call StartRefresh, so lastFetch stays at zero.

	m := NewNodeHealthMesh(reg, staleCache)
	n := registerCoreNode(reg, 20)

	for i := 0; i < failThreshold; i++ {
		m.Record(20, "hash20", agent.VerifyFailed)
	}
	m.EvaluateDemotion() // should be skipped due to stale cache

	n.mu.RLock()
	tier := n.Tier
	n.mu.RUnlock()
	if tier != NodeTierCore {
		t.Errorf("stale cache gate failed: node was demoted to %s", tier)
	}
}

// TestNodeHealthMesh_EdgeNodeNotDemotedFurther confirms an Edge node (already
// at the lowest tier) is not demoted further even with many failures.
func TestNodeHealthMesh_EdgeNodeNotDemotedFurther(t *testing.T) {
	m, reg := newTestMesh()
	n := NewNodeIdentity(14, "host14", "", FailureDomainLabels{})
	n.Tier = NodeTierEdge
	reg.Register(n)

	for i := 0; i < failThreshold*3; i++ {
		m.Record(14, "hash14", agent.VerifyFailed)
	}
	m.EvaluateDemotion()

	n.mu.RLock()
	tier := n.Tier
	n.mu.RUnlock()
	if tier != NodeTierEdge {
		t.Errorf("edge node demoted further to %s", tier)
	}
}

// TestNodeHealthMesh_Concurrency stress-tests concurrent Record + Merge calls.
func TestNodeHealthMesh_Concurrency(t *testing.T) {
	m, _ := newTestMesh()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(nodeID uint64) {
			for j := 0; j < 50; j++ {
				status := agent.VerifyPassed
				if j%3 == 0 {
					status = agent.VerifyFailed
				}
				m.Record(nodeID, "hashConc", status)
				snap := m.Snapshot()
				m.Merge(snap)
			}
			done <- struct{}{}
		}(uint64(i + 1))
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
