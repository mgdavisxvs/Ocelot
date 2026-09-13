package tracker

import (
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
//
// Anti-affinity is enforced across these domains. A placement that puts two
// replicas in the same domain is penalized in the scoring function unless
// forced by exhaustion of alternatives.

type FailureDomain uint8

const (
	DomainHost    FailureDomain = iota // Same physical host
	DomainRack                         // Same rack in a datacenter
	DomainSite                         // Same datacenter / building
	DomainNetwork                      // Same network segment / uplink
	DomainPower                        // Same power circuit / UPS
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
// Empty string means "unknown" for that level — the placement engine treats
// unknown as a distinct singleton domain, which is conservative/safe.
type FailureDomainLabels struct {
	Host    string // e.g. "node-42.prod"
	Rack    string // e.g. "rack-B-07"
	Site    string // e.g. "us-east-1a"
	Network string // e.g. "net-spine-3"
	Power   string // e.g. "pdu-row-B"
}

// SharesDomainWith returns true if n and other share the same label at the
// given domain tier. Two nodes with an empty label at a tier are NOT considered
// sharing (unknown ≠ unknown — we don't conflate distinct unknowns).
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

// ── Node identity ─────────────────────────────────────────────────────────────

// NodeCapabilities reports what a node can do, as declared by its agent.
type NodeCapabilities struct {
	StorageFreeBytes int64   // available disk at last heartbeat
	StorageTotalBytes int64  // total disk capacity
	CPUCount         int     // logical CPUs
	MemoryBytes      int64   // total RAM
	MaxTorrents      int     // agent-advertised concurrent torrent limit
	BTClientVersion  string  // e.g. "qbittorrent/4.6.2" or "embedded/0.1.0"
}

// NodeIdentity is the authoritative record for a managed node.
// Keyed by NodeID (assigned at registration); the IP may change on reconnect.
type NodeIdentity struct {
	mu sync.RWMutex

	NodeID      uint64
	Kind        PeerKind
	Hostname    string
	LastIP      string // most recent agent source IP
	Passkey     string // scoped BT passkey issued to this node
	Domains     FailureDomainLabels
	Caps        NodeCapabilities
	LastSeen    time.Time // last heartbeat
	Registered  time.Time
	Unreachable bool // true after 2× HeartbeatInterval silence
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
	}
}

func (n *NodeIdentity) Heartbeat(ip string, caps NodeCapabilities) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.LastIP = ip
	n.Caps = caps
	n.LastSeen = time.Now()
	n.Unreachable = false
}

func (n *NodeIdentity) MarkUnreachable() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Unreachable = true
}

func (n *NodeIdentity) IsReachable() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return !n.Unreachable
}

func (n *NodeIdentity) GetDomains() FailureDomainLabels {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Domains
}

// ── NodeRegistry ──────────────────────────────────────────────────────────────

// NodeRegistry is the concurrent-safe collection of all managed nodes.
type NodeRegistry struct {
	mu    sync.RWMutex
	nodes map[uint64]*NodeIdentity
	byIP  map[string]uint64 // IP → NodeID fast lookup
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

// ReachableNodes returns all nodes not currently marked unreachable.
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

// MarkStale marks nodes silent for > cutoff as unreachable.
// Called by the controller heartbeat monitor.
func (r *NodeRegistry) MarkStale(cutoff time.Duration) {
	threshold := time.Now().Add(-cutoff)
	r.mu.RLock()
	ids := make([]uint64, 0)
	for id, n := range r.nodes {
		n.mu.RLock()
		seen := n.LastSeen
		n.mu.RUnlock()
		if seen.Before(threshold) {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	for _, id := range ids {
		if n, ok := r.Get(id); ok {
			n.MarkUnreachable()
		}
	}
}

// ClassifyPeer returns PeerManaged if the connecting IP belongs to a registered
// managed node, otherwise PeerUnmanaged.
func (r *NodeRegistry) ClassifyPeer(ip string) PeerKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.byIP[ip]; ok {
		return PeerManaged
	}
	return PeerUnmanaged
}
