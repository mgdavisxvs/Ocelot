package tracker

import ()

// Optimization functions implementing Terence Tao and Paul Erdős approaches

// AdaptiveInterval calculates optimal announce interval per torrent
// Terence Tao optimization: Balance tracker load vs swarm freshness
//
// Formula: I_opt = k * sqrt(N / C)
// where N = peer count, C = churn rate, k = calibration constant
//
// Simplified heuristic:
// - Small swarms (N < 10): 600s (10 min) - high churn, need frequent updates
// - Medium swarms (10 ≤ N < 100): 1200s (20 min) - balanced
// - Large swarms (N ≥ 100): 2400s (40 min) - low churn, can wait longer
func AdaptiveInterval(seederCount, leecherCount int, baseInterval int) int32 {
	totalPeers := seederCount + leecherCount

	if totalPeers < 10 {
		return 600 // 10 minutes
	} else if totalPeers < 100 {
		return 1200 // 20 minutes
	} else {
		return 2400 // 40 minutes
	}
}
