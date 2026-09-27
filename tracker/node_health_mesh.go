package tracker

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mgdavisxvs/Ocelot/compute/agent"
)

// NodeVerifyRecord carries gossip-replicated artifact verification state for one
// (node, info_hash) pair.  Seq is a CRDT monotonic counter: higher wins on merge.
type NodeVerifyRecord struct {
	NodeID   uint64            `json:"node_id"`
	InfoHash string            `json:"info_hash"`
	Status   agent.VerifyStatus `json:"status"`
	Seq      uint64            `json:"seq"`
	At       time.Time         `json:"at"`
}

// meshGossipSeq is the process-wide Lamport counter for NodeVerifyRecord mutations.
var meshGossipSeq atomic.Uint64

// failThreshold is the number of consecutive VerifyFailed records that triggers
// an automatic tier demotion for a node.
const failThreshold = 3

// NodeHealthMesh (C-05) maintains a gossip-replicated map of artifact
// verification results keyed by (NodeID, InfoHash).  It:
//
//  1. Merges incoming gossip records via last-writer-wins on Seq.
//  2. Gates any record's node on MarkovShadowCache freshness — stale cache
//     entries are treated as unknown rather than failing, preventing false demotions.
//  3. Calls EvaluateDemotion periodically: a node with ≥ failThreshold
//     consecutive VerifyFailed entries is demoted from NodeTierCore → NodeTierRegional
//     or NodeTierRegional → NodeTierEdge.
//
// Safe for concurrent use.
type NodeHealthMesh struct {
	registry *NodeRegistry
	cache    *MarkovShadowCache

	mu      sync.RWMutex
	records map[uint64]map[string]*NodeVerifyRecord // nodeID → infoHash → record

	// failCounts tracks consecutive VerifyFailed per nodeID.
	failCounts map[uint64]int
}

// NewNodeHealthMesh creates the mesh.  cache may be nil (disables freshness gating).
func NewNodeHealthMesh(registry *NodeRegistry, cache *MarkovShadowCache) *NodeHealthMesh {
	return &NodeHealthMesh{
		registry:   registry,
		cache:      cache,
		records:    make(map[uint64]map[string]*NodeVerifyRecord),
		failCounts: make(map[uint64]int),
	}
}

// Record stores or updates a verification result for (nodeID, infoHash).
// It bumps the global Seq counter to mark this as a local mutation for gossip.
func (m *NodeHealthMesh) Record(nodeID uint64, infoHash string, status agent.VerifyStatus) {
	seq := meshGossipSeq.Add(1)
	rec := &NodeVerifyRecord{
		NodeID:   nodeID,
		InfoHash: infoHash,
		Status:   status,
		Seq:      seq,
		At:       time.Now(),
	}
	m.mu.Lock()
	m.applyLocked(rec)
	m.mu.Unlock()
}

// applyLocked writes rec into the map if its Seq beats the stored copy.
// Caller must hold m.mu write lock.
func (m *NodeHealthMesh) applyLocked(rec *NodeVerifyRecord) {
	byHash, ok := m.records[rec.NodeID]
	if !ok {
		byHash = make(map[string]*NodeVerifyRecord)
		m.records[rec.NodeID] = byHash
	}
	existing, exists := byHash[rec.InfoHash]
	if exists && existing.Seq >= rec.Seq {
		return
	}
	byHash[rec.InfoHash] = rec

	// Update consecutive fail counter.
	if rec.Status == agent.VerifyFailed {
		m.failCounts[rec.NodeID]++
	} else if rec.Status == agent.VerifyPassed {
		m.failCounts[rec.NodeID] = 0
	}
}

// Snapshot returns all records as a flat slice for gossip dissemination.
func (m *NodeHealthMesh) Snapshot() []NodeVerifyRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []NodeVerifyRecord
	for _, byHash := range m.records {
		for _, r := range byHash {
			out = append(out, *r)
		}
	}
	return out
}

// Merge applies an inbound batch from a gossip peer.
func (m *NodeHealthMesh) Merge(records []NodeVerifyRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range records {
		m.applyLocked(&records[i])
	}
}

// EvaluateDemotion scans nodes with ≥ failThreshold consecutive failures.
// If the Markov cache is stale it skips evaluation to prevent false demotions
// caused by data drift rather than real node degradation.
// Demoted nodes log their new tier.
func (m *NodeHealthMesh) EvaluateDemotion() {
	// Gate on cache freshness.
	if m.cache != nil && !m.cache.IsFresh() {
		return
	}

	m.mu.RLock()
	candidates := make(map[uint64]int, len(m.failCounts))
	for id, count := range m.failCounts {
		if count >= failThreshold {
			candidates[id] = count
		}
	}
	m.mu.RUnlock()

	for nodeID := range candidates {
		n, ok := m.registry.Get(nodeID)
		if !ok {
			continue
		}
		n.mu.Lock()
		switch n.Tier {
		case NodeTierCore:
			n.Tier = NodeTierRegional
		case NodeTierRegional:
			n.Tier = NodeTierEdge
		}
		n.mu.Unlock()

		// Reset counter after demotion.
		m.mu.Lock()
		m.failCounts[nodeID] = 0
		m.mu.Unlock()

		GetDefaultLogger().Warn("node demoted due to verify failures",
			"node_id", nodeID,
			"new_tier", n.Tier.String(),
		)
	}
}

// HandleGossipPush is the HTTP handler for the /internal/gossip/health endpoint.
// It accepts a JSON array of NodeVerifyRecords, merges them, then responds with
// the local snapshot.
func (m *NodeHealthMesh) HandleGossipPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var incoming []NodeVerifyRecord
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	m.Merge(incoming)

	snapshot := m.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshot) //nolint:errcheck
}
