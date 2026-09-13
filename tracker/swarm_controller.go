package tracker

import (
	"context"
	"fmt"
	"log"
	"time"
)

// SwarmDirective is the set of controller actions the policy engine can emit.
// Directives are transmitted to managed nodes via the agent protocol.
//
// JOIN_SWARM and RETIRE_REPLICA are the only verbs the controller issues.
// Everything else (hash verification, seeding, eviction) is handled by the agent.
type SwarmDirective uint8

const (
	DirectiveJoinSwarm    SwarmDirective = iota // add node to swarm for infoHash
	DirectiveRetireReplica                       // stop seeding and delete local copy
)

func (d SwarmDirective) String() string {
	switch d {
	case DirectiveJoinSwarm:
		return "JOIN_SWARM"
	case DirectiveRetireReplica:
		return "RETIRE_REPLICA"
	default:
		return "UNKNOWN"
	}
}

// ControllerAction pairs a directive with its target node and artifact.
type ControllerAction struct {
	Directive SwarmDirective
	NodeID    uint64
	InfoHash  string
	Class     ReplicaClass
}

// PlacementWeights extends LocalityWeights with anti-affinity and proximity terms.
// P(n,a) = w_s·S + w_r·R + w_b·B + w_l·L + w_c·C + w_g·G + w_t·T − w_p·P
//
//	S = storage headroom score ∈ [0,1]
//	R = reliability/uptime ratio ∈ [0,1]
//	B = bandwidth availability ∈ [0,1]
//	L = local artifact presence ∈ {0,1}
//	C = compute availability ∈ [0,1]
//	G = proximity/ASN affinity ∈ [0,1]    (S-E2)
//	T = tier placement bonus ∈ [0,0.15]   (S-E1)
//	P = anti-affinity penalty ∈ [0,1]     (subtracted)
type PlacementWeights struct {
	Storage       float64 `yaml:"storage"`
	Reliability   float64 `yaml:"reliability"`
	Bandwidth     float64 `yaml:"bandwidth"`
	LocalArtifact float64 `yaml:"local_artifact"`
	Compute       float64 `yaml:"compute"`
	Proximity     float64 `yaml:"proximity"`   // S-E2: geo/ASN affinity
	AntiAffinity  float64 `yaml:"anti_affinity"` // subtracted
}

// DefaultPlacementWeights are the baseline weights for artifact placement.
// Weights sum to 1.0 (excluding AntiAffinity which is subtracted, not added).
var DefaultPlacementWeights = PlacementWeights{
	Storage:       0.22,
	Reliability:   0.18,
	Bandwidth:     0.18,
	LocalArtifact: 0.12,
	Compute:       0.08,
	Proximity:     0.15,
	AntiAffinity:  0.07,
}

// PlacementInput holds per-node inputs for the placement scoring function.
type PlacementInput struct {
	StorageScore    float64 // free_bytes / total_bytes
	Reliability     float64 // historical uptime ratio
	BandwidthScore  float64 // normalized upload capacity
	HasArtifact     float64 // 1.0 if artifact already on node, else 0
	ComputeScore    float64 // 1.0 = fully available
	ProximityScore  float64 // S-E2: ASN/geo affinity to demand modal network ∈ [0,1]
	TierBonus       float64 // S-E1: tier placement bonus (Core > Regional > Edge)
	AffinityPenalty float64 // 1.0 = shares domain with an existing replica
}

// Score computes P(n,a) = w_s·S + w_r·R + w_b·B + w_l·L + w_c·C + w_g·G + T − w_p·P
func (w PlacementWeights) Score(in PlacementInput) float64 {
	return w.Storage*in.StorageScore +
		w.Reliability*in.Reliability +
		w.Bandwidth*in.BandwidthScore +
		w.LocalArtifact*in.HasArtifact +
		w.Compute*in.ComputeScore +
		w.Proximity*in.ProximityScore +
		in.TierBonus -
		w.AntiAffinity*in.AffinityPenalty
}

// ── Swarm Policy Controller ────────────────────────────────────────────────────
//
// The controller implements a declarative reconciliation loop:
//
//	observe → compare → decide → signal → observe
//
// Routine monitoring uses scrape() against the tracker's in-memory peer lists
// (cheap — no I/O, no agent contact). Full node inspection happens only on
// remediation paths (replica deficit detected, node unreachable).
//
// The controller never mutates tracker peer lists directly. It emits directives
// (JOIN_SWARM, RETIRE_REPLICA) to agents via the directive channel, which the
// agent transport layer delivers over HTTP.

type SwarmPolicyController struct {
	artifacts *ArtifactList
	torrents  *TorrentList
	nodes     *NodeRegistry
	replicas  *NodeReplicaMap
	admission *SwarmAdmissionPolicy
	policy    *PopularityReplicaCounter
	weights   PlacementWeights

	// directives is the outbound queue of controller actions.
	// The agent transport reads from this channel and delivers to nodes.
	directives chan ControllerAction

	interval time.Duration // reconcile tick rate
}

// NewSwarmPolicyController creates a controller. Callers must call Run(ctx).
func NewSwarmPolicyController(
	artifacts *ArtifactList,
	torrents *TorrentList,
	nodes *NodeRegistry,
	replicas *NodeReplicaMap,
	admission *SwarmAdmissionPolicy,
	interval time.Duration,
) *SwarmPolicyController {
	return &SwarmPolicyController{
		artifacts:  artifacts,
		torrents:   torrents,
		nodes:      nodes,
		replicas:   replicas,
		admission:  admission,
		policy:     NewPopularityReplicaCounter(DefaultReplicationConfig),
		weights:    DefaultPlacementWeights,
		directives: make(chan ControllerAction, 256),
		interval:   interval,
	}
}

// Directives returns the read-only channel of outbound controller actions.
// The caller (agent transport) drains this channel and delivers directives to agents.
func (c *SwarmPolicyController) Directives() <-chan ControllerAction {
	return c.directives
}

// Run starts the reconciliation loop. Blocks until ctx is cancelled.
func (c *SwarmPolicyController) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Heartbeat staleness monitor: 2× reconcile interval before marking unreachable.
	hbCutoff := c.interval * 2

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.nodes.MarkStale(hbCutoff)
			c.reconcile()
		}
	}
}

// reconcile performs one full observe → compare → decide → signal pass.
// It uses scrape-first monitoring: pull replica counts from in-memory peer maps
// (zero I/O), then inspect individual nodes only when a deficit is detected.
func (c *SwarmPolicyController) reconcile() {
	c.artifacts.ForEach(func(infoHash string, artifact *Artifact) bool {
		c.reconcileArtifact(infoHash, artifact)
		return true
	})
}

func (c *SwarmPolicyController) reconcileArtifact(infoHash string, artifact *Artifact) {
	artifact.mu.RLock()
	minR := artifact.MinReplicas
	maxR := artifact.MaxReplicas
	artifact.mu.RUnlock()

	// ── Observe (scrape-first) ────────────────────────────────────────────────
	// Read seeder count directly from in-memory torrent peer map (no I/O).
	rawSeeders := 0
	if t, ok := c.torrents.Get(infoHash); ok {
		rawSeeders = t.Seeders.Size()
	}

	// Verified count: only SLA-counted managed nodes that passed hash verification.
	verifiedManaged := c.replicas.VerifiedCount(infoHash)

	// Compute desired replica count from popularity policy.
	desired := c.policy.Evaluate(float64(rawSeeders), minR, maxR)

	// ── Compare ───────────────────────────────────────────────────────────────
	deficit := desired - verifiedManaged
	if deficit <= 0 {
		// Surplus: check if we need to retire replicas (scale-down path).
		surplus := verifiedManaged - desired
		if surplus > 0 {
			c.selectRetirements(infoHash, surplus)
		}
		return
	}

	// ── Decide + Signal ───────────────────────────────────────────────────────
	// Only REACHABLE (not FLAPPING) nodes receive directives.
	reachable := c.nodes.DirectableNodes()
	existing := c.existingNodeSet(infoHash)
	candidates := c.scoreCandidates(infoHash, artifact, reachable, existing, desired)

	emitted := 0
	for _, cand := range candidates {
		if emitted >= deficit {
			break
		}
		// Idempotency: GetOrCreate returns existing replica; Transition is idempotent.
		replica := c.replicas.GetOrCreate(cand.nodeID, infoHash, ReplicaStandard)
		state := replica.GetState()
		if state != ReplicaStateAbsent {
			continue // already in progress
		}
		if _, err := replica.Transition(ReplicaStateRequested); err != nil {
			continue
		}
		select {
		case c.directives <- ControllerAction{
			Directive: DirectiveJoinSwarm,
			NodeID:    cand.nodeID,
			InfoHash:  infoHash,
			Class:     ReplicaStandard,
		}:
			emitted++
		default:
			// Channel full; back-pressure. Revert transition and try next tick.
			replica.Transition(ReplicaStateAbsent) //nolint:errcheck
		}
	}

	if deficit > 0 && emitted == 0 {
		log.Printf("controller: artifact %s deficit=%d but no eligible nodes available", infoHash, deficit)
	}
}

// existingNodeSet returns the set of nodeIDs that already hold this artifact
// in a non-absent state.
func (c *SwarmPolicyController) existingNodeSet(infoHash string) map[uint64]struct{} {
	existing := make(map[uint64]struct{})
	c.replicas.ForHash(infoHash, func(r *NodeReplica) bool {
		if r.GetState() != ReplicaStateAbsent {
			existing[r.NodeID] = struct{}{}
		}
		return true
	})
	return existing
}

type scoredCandidate struct {
	nodeID uint64
	score  float64
}

// scoreCandidates computes P(n,a) for each eligible node, applies WAN cap filter,
// anti-affinity, tier bonus, and proximity scoring; returns candidates sorted descending.
func (c *SwarmPolicyController) scoreCandidates(
	infoHash string,
	artifact *Artifact,
	reachable []*NodeIdentity,
	existing map[uint64]struct{},
	desired int,
) []scoredCandidate {
	// S-E1: Enforce Core-before-Edge — if no Core replica verified yet, skip Edge candidates.
	hasCore := c.nodes.HasVerifiedCoreReplica(infoHash, c.replicas)

	// S-E6: Get modal demand network for proximity scoring.
	var modalNet uint16
	var haveModalNet bool
	if artifact != nil && artifact.Heatmap != nil {
		modalNet, haveModalNet = artifact.Heatmap.ModalNet()
	}

	// Build the failure-domain set of nodes already holding the artifact.
	existingDomains := make([]*FailureDomainLabels, 0, len(existing))
	for id := range existing {
		if n, ok := c.nodes.Get(id); ok {
			d := n.GetDomains()
			existingDomains = append(existingDomains, &d)
		}
	}

	candidates := make([]scoredCandidate, 0, len(reachable))
	for _, n := range reachable {
		if _, alreadyHas := existing[n.NodeID]; alreadyHas {
			continue
		}

		n.mu.RLock()
		caps := n.Caps
		domains := n.Domains
		tier := n.Tier
		nodeASN := n.ASN
		wan := n.WAN
		n.mu.RUnlock()

		// S-E3: Skip nodes at or near WAN cap.
		if wan.IsNearCap() {
			continue
		}

		// S-E1: Skip Edge nodes until at least one Core replica is verified.
		if tier == NodeTierEdge && !hasCore {
			continue
		}

		var storageScore float64
		if caps.StorageTotalBytes > 0 {
			storageScore = float64(caps.StorageFreeBytes) / float64(caps.StorageTotalBytes)
		}

		// S-E2: Proximity score — ASN/network affinity to demand modal network.
		var proxScore float64
		if haveModalNet && nodeASN != 0 {
			nodeNet := uint16(nodeASN & 0xFFFF)
			proxScore = NetAffinity(nodeNet, modalNet)
		}

		// Anti-affinity penalty.
		penalty := c.antiAffinityPenalty(domains, existingDomains)

		in := PlacementInput{
			StorageScore:    clamp01(storageScore),
			Reliability:     0.9,
			BandwidthScore:  0.5,
			HasArtifact:     0,
			ComputeScore:    0.5,
			ProximityScore:  proxScore,
			TierBonus:       tier.placementBonus(),
			AffinityPenalty: penalty,
		}
		candidates = append(candidates, scoredCandidate{
			nodeID: n.NodeID,
			score:  c.weights.Score(in),
		})
	}

	// Insertion sort descending (candidate sets are small, ≤ 100 nodes typical).
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].score > candidates[j-1].score; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}
	return candidates
}

// antiAffinityPenalty returns a [0,1] penalty based on how many existing replicas
// share failure domains with this node. Penalty = fraction of (host,rack,site) domains shared.
func (c *SwarmPolicyController) antiAffinityPenalty(candidate FailureDomainLabels, existing []*FailureDomainLabels) float64 {
	if len(existing) == 0 {
		return 0
	}
	shared := 0
	for _, e := range existing {
		if candidate.SharesDomainWith(e, DomainHost) {
			shared += 3 // host sharing is worst
		} else if candidate.SharesDomainWith(e, DomainRack) {
			shared += 2
		} else if candidate.SharesDomainWith(e, DomainSite) {
			shared++
		}
	}
	// Normalize: max possible = 3 * len(existing)
	max := float64(3 * len(existing))
	if max == 0 {
		return 0
	}
	return clamp01(float64(shared) / max)
}

// selectRetirements emits RETIRE_REPLICA directives for the highest-penalty
// (worst-placed) replicas when the artifact is over-replicated.
func (c *SwarmPolicyController) selectRetirements(infoHash string, surplus int) {
	type retiree struct {
		nodeID uint64
	}
	candidates := make([]retiree, 0)
	// Prefer retiring EPHEMERAL/CACHE replicas first, then STANDARD.
	c.replicas.ForHash(infoHash, func(r *NodeReplica) bool {
		state := r.GetState()
		if state.CountsAsVerified() && r.Class <= ReplicaCache {
			candidates = append(candidates, retiree{r.NodeID})
		}
		return true
	})
	if len(candidates) < surplus {
		// Not enough low-priority replicas; also consider STANDARD.
		c.replicas.ForHash(infoHash, func(r *NodeReplica) bool {
			state := r.GetState()
			if state.CountsAsVerified() && r.Class == ReplicaStandard {
				candidates = append(candidates, retiree{r.NodeID})
			}
			return true
		})
	}

	emitted := 0
	for _, cand := range candidates {
		if emitted >= surplus {
			break
		}
		select {
		case c.directives <- ControllerAction{
			Directive: DirectiveRetireReplica,
			NodeID:    cand.nodeID,
			InfoHash:  infoHash,
		}:
			emitted++
		default:
			// Channel full; skip this surplus reduction cycle.
			return
		}
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ── Verified topology tracking ────────────────────────────────────────────────
//
// VerifiedTopology maps announced peer IPs to their managed node identity and
// verified replica state, enabling the controller to determine which seeders
// in the tracker's peer list are actually verified managed replicas vs raw BT peers.

type VerifiedTopology struct {
	nodes    *NodeRegistry
	replicas *NodeReplicaMap
}

func NewVerifiedTopology(nodes *NodeRegistry, replicas *NodeReplicaMap) *VerifiedTopology {
	return &VerifiedTopology{nodes: nodes, replicas: replicas}
}

// ReplicaInfo describes what the topology knows about a peer's managed status.
type ReplicaInfo struct {
	IsManaged   bool
	NodeID      uint64
	ReplicaState NodeReplicaState
}

// Lookup maps a peer's source IP to its managed identity and replica state.
// Returns IsManaged=false when the IP belongs to an unmanaged peer.
func (vt *VerifiedTopology) Lookup(ip, infoHash string) ReplicaInfo {
	n, ok := vt.nodes.GetByIP(ip)
	if !ok {
		return ReplicaInfo{IsManaged: false}
	}
	r, ok := vt.replicas.Get(n.NodeID, infoHash)
	if !ok {
		return ReplicaInfo{IsManaged: true, NodeID: n.NodeID, ReplicaState: ReplicaStateAbsent}
	}
	return ReplicaInfo{IsManaged: true, NodeID: n.NodeID, ReplicaState: r.GetState()}
}

// VerifiedSeeders counts peers in the tracker's seeder list that are managed nodes
// in a VERIFIED or SEEDING state for the given info_hash.
// This is the scrape-first monitoring path — reads only in-memory state, no I/O.
func (vt *VerifiedTopology) VerifiedSeeders(torrent *Torrent, infoHash string) int {
	ips := make([]string, 0)
	torrent.Seeders.ForEach(func(_ string, peer *Peer) bool {
		if peer.IP != nil {
			ips = append(ips, peer.IP.String())
		}
		return true
	})

	count := 0
	for _, ip := range ips {
		info := vt.Lookup(ip, infoHash)
		if info.IsManaged && info.ReplicaState.CountsAsVerified() {
			count++
		}
	}
	return count
}

// ── Compute integration ───────────────────────────────────────────────────────

// EnsureRequest is the payload for the compute "ensure artifact on node" API.
// A scheduler sends this to guarantee an artifact is present and verified
// before starting a job on a specific node.
type EnsureRequest struct {
	InfoHash string `json:"info_hash"`
	NodeID   uint64 `json:"node_id"`
	Class    string `json:"class"` // "standard", "pinned", "ephemeral", etc.
}

// EnsureResult is the response from the ensure endpoint.
type EnsureResult struct {
	InfoHash string           `json:"info_hash"`
	NodeID   uint64           `json:"node_id"`
	State    string           `json:"state"`
	Queued   bool             `json:"queued"` // true if JOIN_SWARM was emitted
	Message  string           `json:"message,omitempty"`
}

// EnsureArtifact is the idempotent "ensure artifact X is on node Y" handler.
// Emits JOIN_SWARM if the replica is absent; returns current state if already in progress.
// Called by compute schedulers before starting a job on a node.
func (c *SwarmPolicyController) EnsureArtifact(req EnsureRequest) (EnsureResult, error) {
	if _, ok := c.artifacts.Get(req.InfoHash); !ok {
		return EnsureResult{}, fmt.Errorf("artifact not registered: %s", req.InfoHash)
	}
	if _, ok := c.nodes.Get(req.NodeID); !ok {
		return EnsureResult{}, fmt.Errorf("node not registered: %d", req.NodeID)
	}

	class := parseReplicaClass(req.Class)
	replica := c.replicas.GetOrCreate(req.NodeID, req.InfoHash, class)
	state := replica.GetState()

	if state.CountsAsVerified() {
		return EnsureResult{
			InfoHash: req.InfoHash,
			NodeID:   req.NodeID,
			State:    state.String(),
			Queued:   false,
			Message:  "already verified",
		}, nil
	}

	if state != ReplicaStateAbsent {
		return EnsureResult{
			InfoHash: req.InfoHash,
			NodeID:   req.NodeID,
			State:    state.String(),
			Queued:   false,
			Message:  "in progress",
		}, nil
	}

	// Transition to REQUESTED and emit JOIN_SWARM.
	if _, err := replica.Transition(ReplicaStateRequested); err != nil {
		return EnsureResult{}, err
	}

	select {
	case c.directives <- ControllerAction{
		Directive: DirectiveJoinSwarm,
		NodeID:    req.NodeID,
		InfoHash:  req.InfoHash,
		Class:     class,
	}:
	default:
		// Directive channel full; revert and report.
		replica.Transition(ReplicaStateAbsent) //nolint:errcheck
		return EnsureResult{}, fmt.Errorf("directive channel full; retry")
	}

	return EnsureResult{
		InfoHash: req.InfoHash,
		NodeID:   req.NodeID,
		State:    ReplicaStateRequested.String(),
		Queued:   true,
		Message:  "JOIN_SWARM queued",
	}, nil
}

func parseReplicaClass(s string) ReplicaClass {
	switch s {
	case "ephemeral":
		return ReplicaEphemeral
	case "cache":
		return ReplicaCache
	case "pinned":
		return ReplicaPinned
	case "archival":
		return ReplicaArchival
	default:
		return ReplicaStandard
	}
}
