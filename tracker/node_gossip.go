package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// nodeRecord is the wire format exchanged during gossip rounds.
// Seq is a CRDT sequence number: the merge rule is last-writer-wins per NodeID,
// keeping the record with the higher Seq.
type nodeRecord struct {
	NodeID     uint64    `json:"node_id"`
	Hostname   string    `json:"hostname"`
	LastIP     string    `json:"last_ip"`
	Tier       NodeTier  `json:"tier"`
	ASN        uint32    `json:"asn"`
	ReachState uint8     `json:"reach_state"`
	LastSeen   time.Time `json:"last_seen"`
	Seq        uint64    `json:"seq"`
}

// nodeGossipSeq is a process-wide sequence counter incremented on every local
// node mutation to guarantee causal ordering across gossip rounds.
var nodeGossipSeq atomic.Uint64

// NodeGossip implements push-pull gossip anti-entropy for the NodeRegistry.
// On each tick it pushes its local snapshot to each configured peer address and
// merges any records the peers push back, keeping the higher-Seq copy per node.
//
// This provides eventual consistency for the NodeRegistry across multiple tracker
// instances without requiring a shared database.
type NodeGossip struct {
	registry    *NodeRegistry
	peerAddrs   []string // URLs of sibling tracker control-plane endpoints
	intervalSec int
	client      *http.Client

	mu      sync.Mutex
	seqByID map[uint64]uint64 // last seen Seq per NodeID (prevents stale overwrites)
}

// NewNodeGossip creates a gossip engine.  peerAddrs should be the
// /internal/gossip/nodes HTTP endpoint of each sibling tracker instance.
func NewNodeGossip(registry *NodeRegistry, peerAddrs []string, intervalSec int) *NodeGossip {
	if intervalSec <= 0 {
		intervalSec = 30
	}
	return &NodeGossip{
		registry:    registry,
		peerAddrs:   peerAddrs,
		intervalSec: intervalSec,
		client:      &http.Client{Timeout: 5 * time.Second},
		seqByID:     make(map[uint64]uint64),
	}
}

// Start launches the background gossip goroutine.  Cancel ctx to stop it.
func (g *NodeGossip) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Duration(g.intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.round(ctx)
			}
		}
	}()
}

// round executes one push-pull gossip round against all configured peers.
func (g *NodeGossip) round(ctx context.Context) {
	local := g.snapshot()
	for _, addr := range g.peerAddrs {
		remote, err := g.pushPull(ctx, addr, local)
		if err != nil {
			GetDefaultLogger().Warn("gossip push-pull failed", "peer", addr, "err", err)
			continue
		}
		g.merge(remote)
	}
}

// snapshot serialises the current registry state to gossip wire format.
func (g *NodeGossip) snapshot() []nodeRecord {
	var records []nodeRecord
	g.registry.ForEach(func(n *NodeIdentity) bool {
		n.mu.RLock()
		r := nodeRecord{
			NodeID:     n.NodeID,
			Hostname:   n.Hostname,
			LastIP:     n.LastIP,
			Tier:       n.Tier,
			ASN:        n.ASN,
			ReachState: uint8(n.ReachState),
			LastSeen:   n.LastSeen,
		}
		n.mu.RUnlock()

		g.mu.Lock()
		r.Seq = g.seqByID[n.NodeID]
		g.mu.Unlock()

		records = append(records, r)
		return true
	})
	return records
}

// pushPull sends local records to addr and returns the records the peer sends back.
func (g *NodeGossip) pushPull(ctx context.Context, addr string, local []nodeRecord) ([]nodeRecord, error) {
	body, err := json.Marshal(local)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gossip %s: HTTP %d", addr, resp.StatusCode)
	}
	var remote []nodeRecord
	return remote, json.NewDecoder(resp.Body).Decode(&remote)
}

// merge applies incoming records, keeping higher-Seq wins per NodeID.
func (g *NodeGossip) merge(records []nodeRecord) {
	for _, r := range records {
		g.mu.Lock()
		known := g.seqByID[r.NodeID]
		if r.Seq <= known {
			g.mu.Unlock()
			continue
		}
		g.seqByID[r.NodeID] = r.Seq
		g.mu.Unlock()

		n, exists := g.registry.Get(r.NodeID)
		if !exists {
			n = NewNodeIdentity(r.NodeID, r.Hostname, "", FailureDomainLabels{})
		}
		n.mu.Lock()
		n.LastIP = r.LastIP
		n.Tier = r.Tier
		n.ASN = r.ASN
		n.ReachState = ReachState(r.ReachState)
		n.LastSeen = r.LastSeen
		n.mu.Unlock()

		if !exists {
			g.registry.Register(n)
		}
	}
}

// HandleGossipPush handles an inbound push from a sibling tracker.
// It merges the received records, then responds with the local snapshot.
// Register this at /internal/gossip/nodes on the control server.
func (g *NodeGossip) HandleGossipPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var incoming []nodeRecord
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	g.merge(incoming)

	local := g.snapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(local) //nolint:errcheck
}

// BumpSeq increments the gossip sequence for a node ID (call after any local
// mutation to ensure the change propagates in the next gossip round).
func (g *NodeGossip) BumpSeq(nodeID uint64) {
	g.mu.Lock()
	g.seqByID[nodeID] = nodeGossipSeq.Add(1)
	g.mu.Unlock()
}
