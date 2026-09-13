package tracker

import (
	"testing"
	"time"
)

// ── ReplicaClass tests ────────────────────────────────────────────────────────

func TestReplicaClassSLACountingRules(t *testing.T) {
	cases := []struct {
		class    ReplicaClass
		countsOK bool
	}{
		{ReplicaEphemeral, false},
		{ReplicaCache, false},
		{ReplicaStandard, true},
		{ReplicaPinned, true},
		{ReplicaArchival, false},
	}
	for _, tc := range cases {
		got := tc.class.CountsTowardSLA()
		if got != tc.countsOK {
			t.Errorf("class %s: CountsTowardSLA() = %v, want %v", tc.class, got, tc.countsOK)
		}
	}
}

// ── NodeReplica FSM tests ─────────────────────────────────────────────────────

func TestNodeReplicaHappyPath(t *testing.T) {
	r := NewNodeReplica(1, "aabbcc", ReplicaStandard)

	steps := []NodeReplicaState{
		ReplicaStateRequested,
		ReplicaStateSwarming,
		ReplicaStateBTComplete,
		ReplicaStateHashVerify,
		ReplicaStateVerified,
		ReplicaStateSeeding,
	}
	for _, next := range steps {
		if _, err := r.Transition(next); err != nil {
			t.Fatalf("unexpected error transitioning to %s: %v", next, err)
		}
	}
	if !r.GetState().CountsAsVerified() {
		t.Errorf("SEEDING should count as verified")
	}
}

func TestNodeReplicaFailurePath(t *testing.T) {
	r := NewNodeReplica(1, "aabbcc", ReplicaStandard)
	for _, s := range []NodeReplicaState{
		ReplicaStateRequested, ReplicaStateSwarming,
		ReplicaStateBTComplete, ReplicaStateHashVerify,
	} {
		r.Transition(s) //nolint:errcheck
	}
	// Hash verification fails.
	if _, err := r.Transition(ReplicaStateInvalid); err != nil {
		t.Fatalf("HASH_VERIFY → INVALID should be valid: %v", err)
	}
	if _, err := r.Transition(ReplicaStatePurge); err != nil {
		t.Fatalf("INVALID → PURGE should be valid: %v", err)
	}
	if _, err := r.Transition(ReplicaStateAbsent); err != nil {
		t.Fatalf("PURGE → ABSENT should be valid: %v", err)
	}
	// Now eligible for re-join.
	if _, err := r.Transition(ReplicaStateRequested); err != nil {
		t.Fatalf("ABSENT → REQUESTED should be valid after recovery: %v", err)
	}
}

func TestNodeReplicaIdempotentTransition(t *testing.T) {
	r := NewNodeReplica(1, "aabbcc", ReplicaStandard)
	r.Transition(ReplicaStateRequested) //nolint:errcheck
	// Idempotent: same state again must not error.
	if _, err := r.Transition(ReplicaStateRequested); err != nil {
		t.Errorf("idempotent transition should not error: %v", err)
	}
}

func TestNodeReplicaInvalidTransition(t *testing.T) {
	r := NewNodeReplica(1, "aabbcc", ReplicaStandard)
	// ABSENT → SEEDING is not a valid transition.
	if _, err := r.Transition(ReplicaStateSeeding); err == nil {
		t.Error("invalid transition ABSENT → SEEDING should return error")
	}
}

// ── NodeReplicaMap tests ──────────────────────────────────────────────────────

func TestNodeReplicaMapVerifiedCount(t *testing.T) {
	m := NewNodeReplicaMap()
	infoHash := "deadbeef01234567"

	// Node 1: SEEDING (verified, SLA)
	r1 := m.GetOrCreate(1, infoHash, ReplicaStandard)
	for _, s := range []NodeReplicaState{
		ReplicaStateRequested, ReplicaStateSwarming,
		ReplicaStateBTComplete, ReplicaStateHashVerify,
		ReplicaStateVerified, ReplicaStateSeeding,
	} {
		r1.Transition(s) //nolint:errcheck
	}

	// Node 2: SWARMING (in progress; not verified)
	r2 := m.GetOrCreate(2, infoHash, ReplicaStandard)
	r2.Transition(ReplicaStateRequested)  //nolint:errcheck
	r2.Transition(ReplicaStateSwarming)   //nolint:errcheck

	// Node 3: EPHEMERAL SEEDING (verified but not SLA-counted)
	r3 := m.GetOrCreate(3, infoHash, ReplicaEphemeral)
	for _, s := range []NodeReplicaState{
		ReplicaStateRequested, ReplicaStateSwarming,
		ReplicaStateBTComplete, ReplicaStateHashVerify,
		ReplicaStateVerified, ReplicaStateSeeding,
	} {
		r3.Transition(s) //nolint:errcheck
	}

	count := m.VerifiedCount(infoHash)
	if count != 1 {
		t.Errorf("VerifiedCount = %d, want 1 (only STANDARD SEEDING counts)", count)
	}
}

// ── SwarmAdmissionPolicy tests ────────────────────────────────────────────────

func TestAdmissionPolicyOpenByDefault(t *testing.T) {
	p := NewSwarmAdmissionPolicy()
	if !p.IsAdmitted("any_hash", "any_passkey") {
		t.Error("policy should admit all passkeys by default")
	}
}

func TestAdmissionPolicyACL(t *testing.T) {
	p := NewSwarmAdmissionPolicy()
	infoHash := "aabbccddeeff0011"
	p.SetACL(infoHash, []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})

	if p.IsAdmitted(infoHash, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		// ok
	} else {
		t.Error("allowed passkey should be admitted")
	}
	if p.IsAdmitted(infoHash, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Error("unlisted passkey should be rejected when ACL is set")
	}
}

func TestAdmissionPolicyRemoveRestoresOpen(t *testing.T) {
	p := NewSwarmAdmissionPolicy()
	infoHash := "aabbccddeeff0011"
	p.SetACL(infoHash, []string{"aaa"})
	p.SetOpen(infoHash)
	if !p.IsAdmitted(infoHash, "any") {
		t.Error("SetOpen should restore open-access behavior")
	}
}

// ── NodeRegistry tests ────────────────────────────────────────────────────────

func TestNodeRegistryClassifyPeer(t *testing.T) {
	r := NewNodeRegistry()
	n := NewNodeIdentity(99, "node99.prod", "passkey32charslongpadpadpadpadpa", FailureDomainLabels{})
	n.LastIP = "10.0.0.99"
	r.Register(n)

	if r.ClassifyPeer("10.0.0.99") != PeerManaged {
		t.Error("registered IP should be classified as MANAGED")
	}
	if r.ClassifyPeer("192.168.1.1") != PeerUnmanaged {
		t.Error("unknown IP should be classified as UNMANAGED")
	}
}

func TestNodeRegistryMarkStale(t *testing.T) {
	r := NewNodeRegistry()
	n := NewNodeIdentity(1, "stale.host", "passkey32charslongpadpadpadpadpa", FailureDomainLabels{})
	n.LastSeen = time.Now().Add(-5 * time.Minute)
	r.Register(n)

	// First MarkStale call: REACHABLE → FLAPPING (still IsReachable, not IsDirectable).
	r.MarkStale(2 * time.Minute)
	if n.GetReachState() != NodeFlapping {
		t.Errorf("first missed HB should set FLAPPING, got %s", n.GetReachState())
	}
	if !n.IsReachable() {
		t.Error("FLAPPING node should still be IsReachable (replicas counted)")
	}
	if n.IsDirectable() {
		t.Error("FLAPPING node should not be IsDirectable (directives suppressed)")
	}

	// Subsequent misses advance FlapCount until UNREACHABLE.
	for i := 0; i < FlapThreshold; i++ {
		r.MarkStale(2 * time.Minute)
	}
	if n.IsReachable() {
		t.Error("node should be UNREACHABLE after FlapThreshold missed HBs")
	}
}

// ── FailureDomain anti-affinity tests ─────────────────────────────────────────

func TestFailureDomainSharesDomainWith(t *testing.T) {
	a := &FailureDomainLabels{Host: "h1", Rack: "r1", Site: "s1"}
	b := &FailureDomainLabels{Host: "h2", Rack: "r1", Site: "s1"}
	c := &FailureDomainLabels{Host: "", Rack: "", Site: ""}

	if !a.SharesDomainWith(b, DomainRack) {
		t.Error("same rack label should share DomainRack")
	}
	if a.SharesDomainWith(b, DomainHost) {
		t.Error("different host labels should not share DomainHost")
	}
	// Empty labels are never shared (two unknowns ≠ same domain).
	if a.SharesDomainWith(c, DomainRack) {
		t.Error("empty label should not share with non-empty")
	}
	if c.SharesDomainWith(c, DomainRack) {
		t.Error("two empty labels should not share domain")
	}
}

// ── PlacementWeights scoring tests ───────────────────────────────────────────

func TestPlacementScoreAntiAffinityReducesScore(t *testing.T) {
	w := DefaultPlacementWeights
	good := PlacementInput{
		StorageScore: 0.8, Reliability: 0.9, BandwidthScore: 0.7,
		HasArtifact: 0, ComputeScore: 0.8, AffinityPenalty: 0,
	}
	penalized := good
	penalized.AffinityPenalty = 1.0

	if w.Score(penalized) >= w.Score(good) {
		t.Error("anti-affinity penalty should reduce score")
	}
}

func TestPlacementScoreArtifactPresenceBoostsScore(t *testing.T) {
	w := DefaultPlacementWeights
	without := PlacementInput{
		StorageScore: 0.5, Reliability: 0.9, BandwidthScore: 0.5,
		HasArtifact: 0, ComputeScore: 0.5,
	}
	with := without
	with.HasArtifact = 1.0

	if w.Score(with) <= w.Score(without) {
		t.Error("artifact presence should boost placement score")
	}
}

// ── S-E1: NodeTier tests ──────────────────────────────────────────────────────

func TestNodeTierDefaultClass(t *testing.T) {
	cases := []struct {
		tier  NodeTier
		class ReplicaClass
	}{
		{NodeTierEdge, ReplicaEphemeral},
		{NodeTierRegional, ReplicaCache},
		{NodeTierCore, ReplicaStandard},
	}
	for _, tc := range cases {
		if got := tc.tier.DefaultClass(); got != tc.class {
			t.Errorf("tier %s DefaultClass() = %v, want %v", tc.tier, got, tc.class)
		}
	}
}

func TestNodeTierPlacementBonusOrdering(t *testing.T) {
	w := DefaultPlacementWeights
	base := PlacementInput{StorageScore: 0.5, Reliability: 0.5, BandwidthScore: 0.5, ComputeScore: 0.5}

	core := base
	core.TierBonus = NodeTierCore.placementBonus()
	regional := base
	regional.TierBonus = NodeTierRegional.placementBonus()
	edge := base
	edge.TierBonus = NodeTierEdge.placementBonus()

	if w.Score(core) <= w.Score(regional) {
		t.Error("CORE should score higher than REGIONAL")
	}
	if w.Score(regional) <= w.Score(edge) {
		t.Error("REGIONAL should score higher than EDGE")
	}
}

// ── S-E2: Proximity scoring tests ────────────────────────────────────────────

func TestProximityScoreBoostsScore(t *testing.T) {
	w := DefaultPlacementWeights
	base := PlacementInput{StorageScore: 0.5, Reliability: 0.5, BandwidthScore: 0.5, ComputeScore: 0.5}
	withProx := base
	withProx.ProximityScore = 1.0

	if w.Score(withProx) <= w.Score(base) {
		t.Error("proximity score = 1.0 should boost total score")
	}
}

// ── S-E5: ReachState / FLAPPING tests ────────────────────────────────────────

func TestNodeReachStateFlapProgression(t *testing.T) {
	n := NewNodeIdentity(1, "node.test", "passkey", FailureDomainLabels{})
	if n.GetReachState() != NodeReachable {
		t.Fatal("new node should start REACHABLE")
	}

	// Manually advance: REACHABLE → FLAPPING
	n.mu.Lock()
	n.ReachState = NodeFlapping
	n.FlapCount = 1
	n.mu.Unlock()

	if !n.IsReachable() {
		t.Error("FLAPPING should still be IsReachable")
	}
	if n.IsDirectable() {
		t.Error("FLAPPING should not be IsDirectable")
	}

	// Heartbeat resets to REACHABLE
	n.Heartbeat("10.0.0.1", NodeCapabilities{})
	if n.GetReachState() != NodeReachable {
		t.Error("Heartbeat should reset FLAPPING → REACHABLE")
	}
	if !n.IsDirectable() {
		t.Error("after Heartbeat, node should be REACHABLE and IsDirectable")
	}
}

// ── S-E3: WANBudget tests ─────────────────────────────────────────────────────

func TestWANBudgetIsNearCap(t *testing.T) {
	b := &WANBudget{LimitBytesPerDay: 1000, LastReset: time.Now()}
	b.RecordUpload(899)
	if b.IsNearCap() {
		t.Error("899/1000 = 89.9%% should not be near cap")
	}
	b.RecordUpload(1) // 900 = exactly 90%
	if !b.IsNearCap() {
		t.Error("900/1000 = 90%% should be near cap")
	}
}

func TestWANBudgetUnlimitedNeverNearCap(t *testing.T) {
	b := &WANBudget{LimitBytesPerDay: 0}
	b.RecordUpload(1 << 40)
	if b.IsNearCap() {
		t.Error("unlimited budget should never be near cap")
	}
	if b.Available() != 1.0 {
		t.Error("unlimited Available() should return 1.0")
	}
}

// ── S-E6: DemandHeatmap tests ─────────────────────────────────────────────────

func TestDemandHeatmapModalNet(t *testing.T) {
	h := NewDemandHeatmap(100)
	// Record 7 announces from 10.0.x.x and 3 from 192.168.x.x
	for i := 0; i < 7; i++ {
		h.Record([]byte{10, 0, byte(i), 1})
	}
	for i := 0; i < 3; i++ {
		h.Record([]byte{192, 168, byte(i), 1})
	}
	modal, ok := h.ModalNet()
	if !ok {
		t.Fatal("ModalNet should return true when records exist")
	}
	want := uint16(10)<<8 | uint16(0)
	if modal != want {
		t.Errorf("ModalNet() = %d, want %d (10.0/16)", modal, want)
	}
}

func TestDemandHeatmapRingEviction(t *testing.T) {
	h := NewDemandHeatmap(5) // tiny ring
	for i := 0; i < 10; i++ {
		h.Record([]byte{byte(i), 0, 0, 0})
	}
	if h.TotalCount() != 5 {
		t.Errorf("TotalCount() = %d after 10 records with cap 5, want 5", h.TotalCount())
	}
}

func TestNetAffinity(t *testing.T) {
	sameNet := uint16(10)<<8 | 0
	diffSameOctet := uint16(10)<<8 | 1
	different := uint16(192)<<8 | 168

	if NetAffinity(sameNet, sameNet) != 1.0 {
		t.Error("same /16 should return 1.0")
	}
	if NetAffinity(sameNet, diffSameOctet) != 0.5 {
		t.Error("same first octet should return 0.5")
	}
	if NetAffinity(sameNet, different) != 0.0 {
		t.Error("different /8 should return 0.0")
	}
}
