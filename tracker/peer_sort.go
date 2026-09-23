package tracker

import (
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
