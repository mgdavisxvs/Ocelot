package tracker

import (
	"math"
	"sync"
	"time"
)

// PeerKind distinguishes managed nodes (running ocelot-agent) from ordinary
// BitTorrent clients that happened to join the swarm.
//
// Only MANAGED peers count toward replica SLA targets. UNMANAGED peers provide
// distribution bandwidth but are not directable by the controller.
type PeerKind uint8

const (
	PeerUnmanaged PeerKind = iota // ordinary BT client; not controllable
	PeerManaged                   // running ocelot-agent; receives directives
)

func (pk PeerKind) String() string {
	if pk == PeerManaged {
		return "MANAGED"
	}
	return "UNMANAGED"
}

// ── Failure domains ───────────────────────────────────────────────────────────

type FailureDomain uint8

const (
	DomainHost    FailureDomain = iota
	DomainRack
	DomainSite
	DomainNetwork
	DomainPower
)

func (fd FailureDomain) String() string {
	switch fd {
	case DomainHost:
		return "host"
	case DomainRack:
		return "rack"
	case DomainSite:
		return "site"
	case DomainNetwork:
		return "network"
	case DomainPower:
		return "power"
	default:
		return "unknown"
	}
}

// FailureDomainLabels holds a node's placement in each failure domain tier.
// Empty string means "unknown" — the placement engine treats unknown as a
// distinct singleton (unknown ≠ unknown, conservative/safe).
type FailureDomainLabels struct {
	Host    string
	Rack    string
	Site    string
	Network string
	Power   string
}

// SharesDomainWith returns true if n and other share the same non-empty label
// at the given domain tier. Two nodes with an empty label are NOT sharing.
func (f *FailureDomainLabels) SharesDomainWith(other *FailureDomainLabels, domain FailureDomain) bool {
	var a, b string
	switch domain {
	case DomainHost:
		a, b = f.Host, other.Host
	case DomainRack:
		a, b = f.Rack, other.Rack
	case DomainSite:
		a, b = f.Site, other.Site
	case DomainNetwork:
		a, b = f.Network, other.Network
	case DomainPower:
		a, b = f.Power, other.Power
	}
	return a != "" && a == b
}

// ── S-E1: Node tier ───────────────────────────────────────────────────────────

// NodeTier classifies a managed node by its network position relative to users.
// Tier influences default replica class and enforces Core-before-Edge placement.
type NodeTier uint8

const (
	NodeTierEdge     NodeTier = iota // last-mile; close to end users
	NodeTierRegional                  // regional aggregate / POP
	NodeTierCore                      // backbone datacenter; authoritative source
)

func (t NodeTier) String() string {
	switch t {
	case NodeTierEdge:
		return "EDGE"
	case NodeTierRegional:
		return "REGIONAL"
	case NodeTierCore:
		return "CORE"
	default:
		return "UNKNOWN"
	}
}

// DefaultClass returns the preferred replica class for this tier.
func (t NodeTier) DefaultClass() ReplicaClass {
	switch t {
	case NodeTierEdge:
		return ReplicaEphemeral
	case NodeTierRegional:
		return ReplicaCache
	default:
		return ReplicaStandard
	}
}

// tierPlacementBonus is the static placement score bonus by tier.
// Core nodes are preferred for initial placements; edge nodes fill last.
func (t NodeTier) placementBonus() float64 {
	switch t {
	case NodeTierCore:
		return 0.15
	case NodeTierRegional:
		return 0.05
	default:
		return 0
	}
}

// ── S-E5: Reach state ─────────────────────────────────────────────────────────

// ReachState models the controller's confidence in reaching a node.
// FLAPPING: first missed HB — directives suppressed, replicas still counted.
// UNREACHABLE: ≥ FlapThreshold missed HBs — excluded from placement; replicas
// retire after GraceTTL elapses.
type ReachState uint8

const (
	NodeReachable   ReachState = iota // within heartbeat interval; fully operational
	NodeFlapping                       // 1+ missed HBs; directives suppressed
	NodeUnreachable                    // ≥ FlapThreshold; removed from placement
)

// FlapThreshold is the number of consecutive missed heartbeats that promotes
// a FLAPPING node to UNREACHABLE.
const FlapThreshold = 3

// GraceTTL is the time to wait before retiring replicas on an UNREACHABLE node.
const GraceTTL = 10 * time.Minute

func (rs ReachState) String() string {
	switch rs {
	case NodeReachable:
		return "REACHABLE"
	case NodeFlapping:
		return "FLAPPING"
	default:
		return "UNREACHABLE"
	}
}

// ── S-E3: WAN budget ──────────────────────────────────────────────────────────

// WANBudget tracks per-day WAN egress budget for a node.
// Nodes near or at cap are excluded from new replica placements.
// All mutations must be called while holding the owning NodeIdentity's mutex.
type WANBudget struct {
	LimitBytesPerDay int64
	UsedBytesThisDay int64
	LastReset        time.Time
}

func (b *WANBudget) resetIfNewDay() {
	if b.LimitBytesPerDay == 0 {
		return
	}
	now := time.Now().UTC()
	last := b.LastReset.UTC()
	if now.Year() != last.Year() || now.YearDay() != last.YearDay() {
		b.UsedBytesThisDay = 0
		b.LastReset = now
	}
}

// RecordUpload adds bytes to today's WAN usage.
func (b *WANBudget) RecordUpload(bytes int64) {
	if bytes <= 0 {
		return
	}
	b.resetIfNewDay()
	b.UsedBytesThisDay += bytes
}

// IsNearCap returns true when ≥ 90% of the daily WAN budget is consumed.
func (b *WANBudget) IsNearCap() bool {
	if b.LimitBytesPerDay <= 0 {
		return false
	}
	b.resetIfNewDay()
	return float64(b.UsedBytesThisDay)/float64(b.LimitBytesPerDay) >= 0.9
}

// Available returns the fraction of WAN budget remaining [0,1]. Returns 1.0 when unlimited.
func (b *WANBudget) Available() float64 {
	if b.LimitBytesPerDay <= 0 {
		return 1.0
	}
	b.resetIfNewDay()
	used := float64(b.UsedBytesThisDay) / float64(b.LimitBytesPerDay)
	return clampFloat(1.0-used, 0, 1)
}

// ── S-E2: Geographic coordinates ─────────────────────────────────────────────

// GeoCoord holds a WGS-84 geographic position (decimal degrees).
type GeoCoord struct {
	Lat float64
	Lon float64
}

// IsZero returns true when the coordinate is unset.
func (g GeoCoord) IsZero() bool { return g.Lat == 0 && g.Lon == 0 }

// DistanceKm returns the great-circle distance in km (haversine formula).
func (g GeoCoord) DistanceKm(other GeoCoord) float64 {
	const R = 6371.0
	dlat := (other.Lat - g.Lat) * math.Pi / 180
	dlon := (other.Lon - g.Lon) * math.Pi / 180
	lat1 := g.Lat * math.Pi / 180
	lat2 := other.Lat * math.Pi / 180
	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dlon/2)*math.Sin(dlon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

// ── Node identity ─────────────────────────────────────────────────────────────

// NodeCapabilities reports what a node can do, as declared by its agent.
type NodeCapabilities struct {
	StorageFreeBytes  int64
	StorageTotalBytes int64
	CPUCount          int
	MemoryBytes       int64
	MaxTorrents       int
	BTClientVersion   string
}

// NodeIdentity is the authoritative record for a managed node.
// Keyed by NodeID (assigned at registration); the IP may change on reconnect.
type NodeIdentity struct {
	mu sync.RWMutex

	NodeID   uint64
	Kind     PeerKind
	Hostname string
	LastIP   string
	Passkey  string
	Domains  FailureDomainLabels

	// S-E1
	Tier NodeTier

	// S-E2
	Geo GeoCoord
	ASN uint32 // autonomous system number (or synthetic /16 proxy when real ASN unavailable)

	// S-E3
	WAN WANBudget

	Caps     NodeCapabilities
	LastSeen time.Time
	Registered time.Time

	// S-E5: replaces Unreachable bool
	ReachState ReachState
	FlapCount  int
	LastFlap   time.Time
}

func NewNodeIdentity(id uint64, hostname, passkey string, domains FailureDomainLabels) *NodeIdentity {
	return &NodeIdentity{
		NodeID:     id,
		Kind:       PeerManaged,
		Hostname:   hostname,
		Passkey:    passkey,
		Domains:    domains,
		LastSeen:   time.Now(),
		Registered: time.Now(),
		ReachState: NodeReachable,
	}
}

// Heartbeat updates live metrics and resets reach state to REACHABLE.
func (n *NodeIdentity) Heartbeat(ip string, caps NodeCapabilities) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.LastIP = ip
	n.Caps = caps
	n.LastSeen = time.Now()
	n.ReachState = NodeReachable
	n.FlapCount = 0
}

// HeartbeatWithGeo is Heartbeat extended with geo and ASN metadata.
func (n *NodeIdentity) HeartbeatWithGeo(ip string, caps NodeCapabilities, geo GeoCoord, asn uint32) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.LastIP = ip
	n.Caps = caps
	n.LastSeen = time.Now()
	n.ReachState = NodeReachable
	n.FlapCount = 0
	if !geo.IsZero() {
		n.Geo = geo
	}
	if asn != 0 {
		n.ASN = asn
	}
}

// MarkUnreachable directly sets the node to UNREACHABLE (used in tests and hard-down events).
func (n *NodeIdentity) MarkUnreachable() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ReachState = NodeUnreachable
}

// IsReachable returns true when the node is not UNREACHABLE.
// FLAPPING nodes are still reachable (replicas counted) but not directable.
func (n *NodeIdentity) IsReachable() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.ReachState != NodeUnreachable
}

// IsDirectable returns true only for fully REACHABLE nodes.
// FLAPPING nodes do not receive JOIN_SWARM / RETIRE_REPLICA directives.
func (n *NodeIdentity) IsDirectable() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.ReachState == NodeReachable
}

func (n *NodeIdentity) GetDomains() FailureDomainLabels {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Domains
}

// GetReachState returns the current reach state.
func (n *NodeIdentity) GetReachState() ReachState {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.ReachState
}

// ── NodeRegistry ──────────────────────────────────────────────────────────────

// NodeRegistry is the concurrent-safe collection of all managed nodes.
type NodeRegistry struct {
	mu    sync.RWMutex
	nodes map[uint64]*NodeIdentity
	byIP  map[string]uint64
}

func NewNodeRegistry() *NodeRegistry {
	return &NodeRegistry{
		nodes: make(map[uint64]*NodeIdentity),
		byIP:  make(map[string]uint64),
	}
}

func (r *NodeRegistry) Register(n *NodeIdentity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[n.NodeID] = n
	if n.LastIP != "" {
		r.byIP[n.LastIP] = n.NodeID
	}
}

func (r *NodeRegistry) Get(id uint64) (*NodeIdentity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	return n, ok
}

func (r *NodeRegistry) GetByIP(ip string) (*NodeIdentity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byIP[ip]
	if !ok {
		return nil, false
	}
	n, ok := r.nodes[id]
	return n, ok
}

func (r *NodeRegistry) Unregister(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[id]; ok {
		delete(r.byIP, n.LastIP)
	}
	delete(r.nodes, id)
}

// ForEach iterates all nodes. Return false from fn to stop early.
func (r *NodeRegistry) ForEach(fn func(n *NodeIdentity) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range r.nodes {
		if !fn(n) {
			break
		}
	}
}

// ReachableNodes returns all nodes not marked UNREACHABLE (includes FLAPPING).
func (r *NodeRegistry) ReachableNodes() []*NodeIdentity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*NodeIdentity, 0, len(r.nodes))
	for _, n := range r.nodes {
		if n.IsReachable() {
			out = append(out, n)
		}
	}
	return out
}

// DirectableNodes returns only fully REACHABLE nodes.
// Use this for placement candidate selection (FLAPPING nodes excluded).
func (r *NodeRegistry) DirectableNodes() []*NodeIdentity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*NodeIdentity, 0, len(r.nodes))
	for _, n := range r.nodes {
		if n.IsDirectable() {
			out = append(out, n)
		}
	}
	return out
}

// MarkStale advances the reach state of nodes silent for > cutoff.
// REACHABLE → FLAPPING on first miss; FLAPPING → UNREACHABLE after FlapThreshold misses.
func (r *NodeRegistry) MarkStale(cutoff time.Duration) {
	threshold := time.Now().Add(-cutoff)
	r.mu.RLock()
	stale := make([]uint64, 0)
	for id, n := range r.nodes {
		n.mu.RLock()
		seen := n.LastSeen
		n.mu.RUnlock()
		if seen.Before(threshold) {
			stale = append(stale, id)
		}
	}
	r.mu.RUnlock()

	for _, id := range stale {
		n, ok := r.Get(id)
		if !ok {
			continue
		}
		n.mu.Lock()
		switch n.ReachState {
		case NodeReachable:
			n.ReachState = NodeFlapping
			n.FlapCount = 1
			n.LastFlap = time.Now()
		case NodeFlapping:
			n.FlapCount++
			if n.FlapCount >= FlapThreshold {
				n.ReachState = NodeUnreachable
			}
		}
		n.mu.Unlock()
	}
}

// ClassifyPeer returns PeerManaged if the IP belongs to a registered node.
func (r *NodeRegistry) ClassifyPeer(ip string) PeerKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.byIP[ip]; ok {
		return PeerManaged
	}
	return PeerUnmanaged
}

// HasVerifiedCoreReplica returns true if any CORE node holds a verified replica
// for the given info_hash. Used to enforce Core-before-Edge placement.
func (r *NodeRegistry) HasVerifiedCoreReplica(infoHash string, replicas *NodeReplicaMap) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range r.nodes {
		n.mu.RLock()
		tier := n.Tier
		n.mu.RUnlock()
		if tier != NodeTierCore {
			continue
		}
		rep, ok := replicas.Get(n.NodeID, infoHash)
		if ok && rep.GetState().CountsAsVerified() {
			return true
		}
	}
	return false
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
