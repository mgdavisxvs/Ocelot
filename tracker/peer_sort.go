package tracker

import (
	"container/heap"
	"net"
	"sort"
)

// PeerEntry is a lightweight peer descriptor used by proximity sorting.
type PeerEntry struct {
	IP   net.IP
	Port uint16
}

// PeerSorterFn reorders a slice of PeerEntry values in place given the
// requesting client's IP address.
type PeerSorterFn func(clientIP net.IP, peers []PeerEntry)

// ── Top-k peer heap ───────────────────────────────────────────────────────────
//
// TopKPeerEntries returns the top k PeerEntry values from peers according to
// the supplied score function, using an O(n log k) min-heap algorithm.
// This is strictly better than O(n log n) full sort when k ≪ n, which is the
// common case for announce responses (num_want=50, swarm=thousands).
//
// RULING-06 mitigation: replaces full sort with partial heap where k < n.
func TopKPeerEntries(peers []PeerEntry, k int, score func(PeerEntry) float64) []PeerEntry {
	if k <= 0 {
		return nil
	}
	if k >= len(peers) {
		// Full sort is correct and avoids heap overhead for small slices.
		result := make([]PeerEntry, len(peers))
		copy(result, peers)
		sort.Slice(result, func(i, j int) bool {
			return score(result[i]) > score(result[j])
		})
		return result
	}
	h := &peMinHeap{score: score}
	for _, p := range peers {
		heap.Push(h, p)
		if h.Len() > k {
			heap.Pop(h) // evict lowest-score element
		}
	}
	// Drain heap in descending score order.
	result := make([]PeerEntry, h.Len())
	for i := len(result) - 1; i >= 0; i-- {
		result[i] = heap.Pop(h).(PeerEntry)
	}
	return result
}

// peMinHeap is a min-heap of PeerEntry values keyed by score.
type peMinHeap struct {
	items []PeerEntry
	score func(PeerEntry) float64
}

func (h *peMinHeap) Len() int { return len(h.items) }
func (h *peMinHeap) Less(i, j int) bool {
	return h.score(h.items[i]) < h.score(h.items[j])
}
func (h *peMinHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *peMinHeap) Push(x interface{}) {
	h.items = append(h.items, x.(PeerEntry))
}
func (h *peMinHeap) Pop() interface{} {
	n := len(h.items)
	x := h.items[n-1]
	h.items = h.items[:n-1]
	return x
}

// SortPeersByProximity returns a PeerSorterFn that reorders announce peer
// candidates based on their proximity to the requesting client, using the
// NodeRegistry for tier and network information.
//
// Priority order (stable within each group):
//  1. MANAGED EDGE nodes whose /16 network matches the client
//  2. MANAGED REGIONAL nodes in the same site as the client's nearest node
//  3. All remaining peers (CORE managed + unmanaged)
//
// Time complexity: O(k log k) where k = len(peers) ≤ numwant (typically ≤ 50).
func SortPeersByProximity(nodes *NodeRegistry) PeerSorterFn {
	return func(clientIP net.IP, peers []PeerEntry) {
		if len(peers) <= 1 || nodes == nil {
			return
		}

		clientV4 := clientIP.To4()
		var clientNet uint16
		if clientV4 != nil {
			clientNet = uint16(clientV4[0])<<8 | uint16(clientV4[1])
		}

		// Classify each peer into a sort priority bucket.
		// 0 = EDGE same-net, 1 = REGIONAL or EDGE diff-net managed, 2 = rest
		priority := func(pe PeerEntry) int {
			if pe.IP == nil {
				return 2
			}
			n, ok := nodes.GetByIP(pe.IP.String())
			if !ok {
				return 2
			}
			n.mu.RLock()
			tier := n.Tier
			nodeASN := n.ASN
			n.mu.RUnlock()

			var nodeNet uint16
			if nodeASN != 0 {
				nodeNet = uint16(nodeASN & 0xFFFF)
			} else if v4 := pe.IP.To4(); v4 != nil {
				nodeNet = uint16(v4[0])<<8 | uint16(v4[1])
			}

			sameNet := clientNet != 0 && nodeNet == clientNet
			switch tier {
			case NodeTierEdge:
				if sameNet {
					return 0
				}
				return 1
			case NodeTierRegional:
				return 1
			default:
				return 2
			}
		}

		sort.SliceStable(peers, func(i, j int) bool {
			return priority(peers[i]) < priority(peers[j])
		})
	}
}
