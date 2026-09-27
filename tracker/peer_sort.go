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
// candidates by a blended score of geographic proximity (60%) and Markov
// PageRank (40%).  rankByIP maps a peer's IP string to its [0,1] PageRank
// score; nil or an empty map disables the PageRank contribution (pure geo).
//
// Priority order:
//  1. MANAGED EDGE nodes in the client's /16 network (geo bucket 0)
//  2. MANAGED REGIONAL / EDGE nodes in a different network (geo bucket 1)
//  3. All remaining peers (geo bucket 2)
//
// Within each geo bucket, higher PageRank peers sort first.
//
// Time complexity: O(k log k) where k = len(peers) ≤ numwant (≤ 50).
func SortPeersByProximity(nodes *NodeRegistry, rankByIP map[string]float64) PeerSorterFn {
	return func(clientIP net.IP, peers []PeerEntry) {
		if len(peers) <= 1 || nodes == nil {
			return
		}

		clientV4 := clientIP.To4()
		var clientNet uint16
		if clientV4 != nil {
			clientNet = uint16(clientV4[0])<<8 | uint16(clientV4[1])
		}

		// geoBucket returns 0 (best), 1, or 2 (worst) for a peer.
		geoBucket := func(pe PeerEntry) int {
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

		// blendedScore: lower is better.
		// geo contributes 0.6×bucket (range [0,1.2]); pageRank subtracts up to 0.4.
		blendedScore := func(pe PeerEntry) float64 {
			geo := float64(geoBucket(pe)) * 0.6
			rank := 0.0
			if rankByIP != nil {
				if r, ok := rankByIP[pe.IP.String()]; ok {
					rank = r
				}
			}
			return geo - rank*0.4
		}

		sort.SliceStable(peers, func(i, j int) bool {
			return blendedScore(peers[i]) < blendedScore(peers[j])
		})
	}
}
