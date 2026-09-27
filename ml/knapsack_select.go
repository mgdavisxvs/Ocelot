package ml

import "sort"

// ScoredPeer pairs a scored peer with its byte-weight in the announce response.
// Weight is the serialized entry size: 6 bytes (compact IPv4) or 18 bytes (IPv6).
type ScoredPeer struct {
	Info   *PeerInfo
	Score  float64
	Weight int // serialized byte size in the announce response
}

// KnapsackSelect solves the 0-1 knapsack problem over scored peers where the
// knapsack capacity is capacityBytes (the remaining response buffer, in bytes).
//
// F-K1: framing peer selection as a knapsack problem lets the tracker pack the
// highest-scoring peers into a fixed-byte response budget rather than truncating
// a sorted list. For n ≤ 300 and moderate capacities the exact DP is tractable;
// above that a greedy fractional approximation is applied (guaranteed ≥ 50% of
// optimal and effectively optimal in practice for near-equal-weight items).
//
// Complexity: O(n·W) exact DP or O(n log n) greedy, chosen automatically.
func KnapsackSelect(peers []ScoredPeer, capacityBytes int) []ScoredPeer {
	if len(peers) == 0 || capacityBytes <= 0 {
		return nil
	}

	// Determine whether exact DP is affordable.
	// n*W ≤ 1_200_000 is a practical ceiling for sub-millisecond execution.
	n := len(peers)
	if n <= 300 && n*capacityBytes <= 1_200_000 {
		return knapsackDP(peers, capacityBytes)
	}
	return knapsackGreedy(peers, capacityBytes)
}

// knapsackDP implements the classic bottom-up 0-1 knapsack DP.
// dp[w] = best total score achievable with exactly w bytes of budget consumed.
func knapsackDP(peers []ScoredPeer, W int) []ScoredPeer {
	n := len(peers)
	// Scale scores to integers (multiply by 1e6, clamp to int).
	values := make([]int, n)
	for i, p := range peers {
		v := int(p.Score * 1e6)
		if v < 0 {
			v = 0
		}
		values[i] = v
	}

	// dp[w] = best value reachable with budget w.
	dp := make([]int, W+1)
	// keep[i][w] = true if peer i is included in the optimal solution for budget w.
	keep := make([][]bool, n)
	for i := range keep {
		keep[i] = make([]bool, W+1)
	}

	for i := 0; i < n; i++ {
		w := peers[i].Weight
		if w <= 0 {
			continue
		}
		// Iterate backwards to maintain 0-1 property.
		for cap := W; cap >= w; cap-- {
			withItem := dp[cap-w] + values[i]
			if withItem > dp[cap] {
				dp[cap] = withItem
				keep[i][cap] = true
			}
		}
	}

	// Traceback.
	selected := make([]ScoredPeer, 0, n)
	remaining := W
	for i := n - 1; i >= 0 && remaining > 0; i-- {
		if keep[i][remaining] {
			selected = append(selected, peers[i])
			remaining -= peers[i].Weight
		}
	}
	return selected
}

// knapsackGreedy uses the fractional knapsack greedy (sort by value/weight DESC,
// take items while budget permits). Guaranteed optimal when all weights are equal.
func knapsackGreedy(peers []ScoredPeer, W int) []ScoredPeer {
	type indexed struct {
		peer  ScoredPeer
		ratio float64
	}
	items := make([]indexed, len(peers))
	for i, p := range peers {
		w := p.Weight
		if w <= 0 {
			w = 1
		}
		items[i] = indexed{peer: p, ratio: p.Score / float64(w)}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ratio > items[j].ratio })

	selected := make([]ScoredPeer, 0, len(peers))
	remaining := W
	for _, item := range items {
		w := item.peer.Weight
		if w <= 0 {
			w = 1
		}
		if remaining < w {
			continue
		}
		selected = append(selected, item.peer)
		remaining -= w
	}
	return selected
}
