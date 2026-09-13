package tracker

import (
	"math"
	"net"
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

// ── GAP-01: GeoCoord.DistanceKm tests ────────────────────────────────────────

func TestGeoCoordDistanceKm(t *testing.T) {
	cases := []struct {
		name    string
		a, b    GeoCoord
		wantMin float64
		wantMax float64
	}{
		{
			name:    "same point",
			a:       GeoCoord{Lat: 51.5, Lon: -0.1},
			b:       GeoCoord{Lat: 51.5, Lon: -0.1},
			wantMin: 0, wantMax: 0.001,
		},
		{
			name:    "equator 1 degree longitude",
			a:       GeoCoord{Lat: 0, Lon: 0},
			b:       GeoCoord{Lat: 0, Lon: 1},
			wantMin: 111.0, wantMax: 111.7,
		},
		{
			name:    "London to Paris",
			a:       GeoCoord{Lat: 51.5074, Lon: -0.1278},
			b:       GeoCoord{Lat: 48.8566, Lon: 2.3522},
			wantMin: 335.0, wantMax: 345.0,
		},
		{
			name:    "north pole to south pole — no NaN",
			a:       GeoCoord{Lat: 90, Lon: 0},
			b:       GeoCoord{Lat: -90, Lon: 0},
			wantMin: 20000, wantMax: 20050,
		},
		{
			name:    "zero value coords",
			a:       GeoCoord{},
			b:       GeoCoord{},
			wantMin: 0, wantMax: 0.001,
		},
	}
	for _, tc := range cases {
		got := tc.a.DistanceKm(tc.b)
		if math.IsNaN(got) {
			t.Errorf("%s: DistanceKm returned NaN", tc.name)
			continue
		}
		if got < tc.wantMin || got > tc.wantMax {
			t.Errorf("%s: DistanceKm() = %.2f km, want [%.1f, %.1f]",
				tc.name, got, tc.wantMin, tc.wantMax)
		}
		// Symmetry invariant
		rev := tc.b.DistanceKm(tc.a)
		if math.Abs(got-rev) > 0.001 {
			t.Errorf("%s: not symmetric: a→b=%.4f b→a=%.4f", tc.name, got, rev)
		}
	}
}

// ── GAP-05: MarkStale race test ───────────────────────────────────────────────

func TestMarkStaleRaceWithHeartbeat(t *testing.T) {
	// Run with: go test -race -run TestMarkStaleRaceWithHeartbeat ./tracker/
	r := NewNodeRegistry()
	n := NewNodeIdentity(1, "race.test", "pk", FailureDomainLabels{})
	n.LastSeen = time.Now().Add(-5 * time.Minute)
	r.Register(n)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			n.Heartbeat("10.0.0.1", NodeCapabilities{})
		}
		close(done)
	}()
	for i := 0; i < 200; i++ {
		r.MarkStale(2 * time.Minute)
	}
	<-done
	// Primary assertion is that -race detects no data race.
	// Secondary: node must be in a valid state.
	state := n.GetReachState()
	if state != NodeReachable && state != NodeFlapping && state != NodeUnreachable {
		t.Errorf("node in invalid reach state: %v", state)
	}
}

func TestMarkStaleHeartbeatPreemptsFlap(t *testing.T) {
	r := NewNodeRegistry()
	n := NewNodeIdentity(1, "preempt.test", "pk", FailureDomainLabels{})
	n.mu.Lock()
	n.LastSeen = time.Now().Add(-5 * time.Minute)
	n.mu.Unlock()
	r.Register(n)

	// Heartbeat arrives just before MarkStale processes this node.
	// Because MarkStale re-checks LastSeen under write lock, it must not flap.
	n.Heartbeat("10.0.0.1", NodeCapabilities{}) // now LastSeen = time.Now()

	r.MarkStale(2 * time.Minute) // threshold = 2min ago; node is fresh now

	if n.GetReachState() != NodeReachable {
		t.Errorf("heartbeat before MarkStale should keep node REACHABLE, got %s",
			n.GetReachState())
	}
}

// ── GAP-07: WANBudget resetIfNewDay tests ────────────────────────────────────

func TestWANBudgetResetZeroLastReset(t *testing.T) {
	b := &WANBudget{LimitBytesPerDay: 1000}
	b.UsedBytesThisDay = 900
	// LastReset is zero value (e.g., after tracker restart).
	b.resetIfNewDay()
	if b.UsedBytesThisDay == 0 {
		t.Error("zero LastReset should not clear UsedBytesThisDay mid-day")
	}
	if b.LastReset.IsZero() {
		t.Error("resetIfNewDay should initialize LastReset from zero")
	}
}

func TestWANBudgetResetNewDay(t *testing.T) {
	yesterday := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	b := &WANBudget{
		LimitBytesPerDay: 1000,
		UsedBytesThisDay: 900,
		LastReset:        yesterday,
	}
	b.IsNearCap() // triggers resetIfNewDay
	if b.UsedBytesThisDay != 0 {
		t.Error("new day should reset UsedBytesThisDay to 0")
	}
	if b.IsNearCap() {
		t.Error("after reset, IsNearCap should be false")
	}
}

func TestWANBudgetNoResetSameDay(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	b := &WANBudget{
		LimitBytesPerDay: 1000,
		UsedBytesThisDay: 500,
		LastReset:        today,
	}
	b.resetIfNewDay()
	if b.UsedBytesThisDay != 500 {
		t.Error("same-day call should not reset usage")
	}
}

// ── GAP-08: SortPeersByProximity order tests ─────────────────────────────────

func TestSortPeersByProximityOrder(t *testing.T) {
	nodes := NewNodeRegistry()

	edgeNode := NewNodeIdentity(1, "edge.node", "pk", FailureDomainLabels{})
	edgeNode.LastIP = "10.0.0.10"
	edgeNode.Tier = NodeTierEdge
	nodes.Register(edgeNode)

	regionalNode := NewNodeIdentity(2, "regional.node", "pk", FailureDomainLabels{})
	regionalNode.LastIP = "10.0.1.20"
	regionalNode.Tier = NodeTierRegional
	nodes.Register(regionalNode)

	coreNode := NewNodeIdentity(3, "core.node", "pk", FailureDomainLabels{})
	coreNode.LastIP = "192.168.1.5"
	coreNode.Tier = NodeTierCore
	nodes.Register(coreNode)

	clientIP := net.ParseIP("10.0.0.1")

	// Worst-case ordering: core first, edge last.
	peers := []PeerEntry{
		{IP: net.ParseIP("192.168.1.5"), Port: 6881}, // CORE       → priority 2
		{IP: net.ParseIP("10.0.1.20"), Port: 6882},   // REGIONAL   → priority 1
		{IP: nil, Port: 0},                            // nil IP     → priority 2
		{IP: net.ParseIP("203.0.113.1"), Port: 6883}, // UNMANAGED  → priority 2
		{IP: net.ParseIP("10.0.0.10"), Port: 6884},   // EDGE same  → priority 0
	}

	sorter := SortPeersByProximity(nodes)
	sorter(clientIP, peers)

	if peers[0].Port != 6884 {
		t.Errorf("slot 0: want EDGE same-net (port 6884), got port %d", peers[0].Port)
	}
	if peers[1].Port != 6882 {
		t.Errorf("slot 1: want REGIONAL (port 6882), got port %d", peers[1].Port)
	}
	// Slots 2-4 are all priority 2; verify they are all in that group.
	prio2Ports := map[uint16]bool{6881: true, 0: true, 6883: true}
	for i, p := range peers[2:] {
		if !prio2Ports[p.Port] {
			t.Errorf("slot %d: unexpected port %d in priority-2 group", i+2, p.Port)
		}
	}
}

func TestSortPeersByProximityNilNodes(t *testing.T) {
	sorter := SortPeersByProximity(nil)
	peers := []PeerEntry{
		{IP: net.ParseIP("10.0.0.1"), Port: 6881},
		{IP: net.ParseIP("10.0.0.2"), Port: 6882},
	}
	sorter(net.ParseIP("10.0.0.99"), peers)
	// nil NodeRegistry → no-op; first element unchanged.
	if peers[0].Port != 6881 {
		t.Error("nil NodeRegistry should leave peer order unchanged")
	}
}

func TestSortPeersByProximitySinglePeer(t *testing.T) {
	sorter := SortPeersByProximity(NewNodeRegistry())
	peers := []PeerEntry{{IP: net.ParseIP("10.0.0.1"), Port: 6881}}
	sorter(net.ParseIP("10.0.0.2"), peers) // must not panic with len=1
}
