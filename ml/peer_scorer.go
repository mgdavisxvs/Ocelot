package ml

import (
	"math"
	"net"
	"sort"
	"time"
)

// PeerScorer provides ML-based peer recommendation
type PeerScorer struct {
	weights map[string]float64
}

// NewPeerScorer creates a new peer scorer
func NewPeerScorer() *PeerScorer {
	return &PeerScorer{
		weights: map[string]float64{
			"upload_speed": 0.35,
			"availability": 0.25,
			"proximity":    0.20,
			"reputation":   0.15,
			"freshness":    0.05,
		},
	}
}

// Score calculates a score for a peer based on multiple factors
func (ps *PeerScorer) Score(peer *PeerInfo, requesterIP net.IP) float64 {
	score := 0.0

	// Upload speed (normalized to 0-1 range)
	uploadSpeed := float64(peer.Uploaded) / max(float64(time.Since(peer.FirstSeen).Seconds()), 1.0)
	normalizedSpeed := normalize(uploadSpeed, 0, 1e6) // Normalize to 1MB/s max
	score += ps.weights["upload_speed"] * normalizedSpeed

	// Availability (uptime percentage)
	uptime := time.Since(peer.FirstSeen).Hours()
	availability := min(uptime/24.0, 1.0) // Days active, capped at 1.0
	score += ps.weights["availability"] * availability

	// Proximity (network distance)
	proximity := 1.0 - (float64(ipDistance(peer.IP, requesterIP)) / 255.0)
	score += ps.weights["proximity"] * proximity

	// Reputation (completed vs dropped sessions)
	reputation := float64(peer.CompletedSessions) / max(float64(peer.TotalSessions), 1.0)
	score += ps.weights["reputation"] * reputation

	// Freshness (recent activity)
	secondsSinceAnnounce := time.Since(peer.LastAnnounce).Seconds()
	freshness := 1.0 / (1.0 + secondsSinceAnnounce/1800.0) // Half-life 30 minutes
	score += ps.weights["freshness"] * freshness

	return score
}

// SelectBest selects the best peers based on ML scoring
func (ps *PeerScorer) SelectBest(peers []*PeerInfo, requesterIP net.IP, numWant int) []*PeerInfo {
	type scoredPeer struct {
		peer  *PeerInfo
		score float64
	}

	scored := make([]scoredPeer, len(peers))
	for i, p := range peers {
		scored[i] = scoredPeer{
			peer:  p,
			score: ps.Score(p, requesterIP),
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Return top numWant peers
	result := make([]*PeerInfo, min(numWant, len(scored)))
	for i := range result {
		result[i] = scored[i].peer
	}

	return result
}

// PeerInfo holds extended peer information for ML scoring
type PeerInfo struct {
	IP                net.IP
	Port              uint16
	Uploaded          int64
	Downloaded        int64
	FirstSeen         time.Time
	LastAnnounce      time.Time
	CompletedSessions int
	TotalSessions     int
}

// Helper functions

func normalize(value, minVal, maxVal float64) float64 {
	if maxVal-minVal == 0 {
		return 0
	}
	v := (value - minVal) / (maxVal - minVal)
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ipDistance returns a network-topology distance in [0, 255] between two IPs.
//
// IPv4: four levels based on /8, /16, /24 prefix matches.
//   - same /24 (first 3 octets match) → 0
//   - same /16, different /24         → 85
//   - same /8,  different /16         → 170
//   - different /8                    → 255
//
// IPv6: four levels based on /16, /32, /48 prefix matches.
//   - same /48 (first 6 bytes match)  → 0
//   - same /32, different /48         → 85
//   - same /16, different /32         → 170
//   - different /16                   → 255
//
// Mixed address families → 255 (maximum distance).
func ipDistance(ip1, ip2 net.IP) int {
	ip1v4 := ip1.To4()
	ip2v4 := ip2.To4()

	if ip1v4 != nil && ip2v4 != nil {
		switch {
		case ip1v4[0] != ip2v4[0]:
			return 255
		case ip1v4[1] != ip2v4[1]:
			return 170
		case ip1v4[2] != ip2v4[2]:
			return 85
		default:
			return 0
		}
	}

	ip1v6 := ip1.To16()
	ip2v6 := ip2.To16()
	if ip1v6 != nil && ip2v6 != nil && ip1v4 == nil && ip2v4 == nil {
		switch {
		case ip1v6[0] != ip2v6[0] || ip1v6[1] != ip2v6[1]:
			return 255
		case ip1v6[2] != ip2v6[2] || ip1v6[3] != ip2v6[3]:
			return 170
		case ip1v6[4] != ip2v6[4] || ip1v6[5] != ip2v6[5]:
			return 85
		default:
			return 0
		}
	}

	return 255 // mixed or unknown address families
}

// SwarmHealthPredictor predicts swarm health metrics
type SwarmHealthPredictor struct {
	// Simple heuristic-based predictor
}

// NewSwarmHealthPredictor creates a new swarm health predictor
func NewSwarmHealthPredictor() *SwarmHealthPredictor {
	return &SwarmHealthPredictor{}
}

// PredictCompletionTime predicts time to complete download
func (shp *SwarmHealthPredictor) PredictCompletionTime(seeders, leechers int, avgUploadSpeed, torrentSize float64) time.Duration {
	if seeders == 0 {
		return time.Duration(math.MaxInt64) // Never completes without seeders
	}

	// Total upload bandwidth available
	totalUpload := float64(seeders) * avgUploadSpeed

	// Download demand
	totalDemand := float64(leechers) * torrentSize

	// Simple calculation: time = data / bandwidth
	if totalUpload == 0 {
		return time.Duration(math.MaxInt64)
	}

	secondsToComplete := totalDemand / totalUpload
	return time.Duration(secondsToComplete) * time.Second
}

// HealthScore calculates overall swarm health (0-100)
func (shp *SwarmHealthPredictor) HealthScore(seeders, leechers int) int {
	if seeders == 0 {
		return 0 // Dead swarm
	}

	if leechers == 0 {
		return 100 // Perfect health (all seeds)
	}

	// Seed-to-leech ratio
	ratio := float64(seeders) / float64(leechers)

	// Score based on ratio
	// ratio >= 1.0: 100 (excellent)
	// ratio = 0.5: 75 (good)
	// ratio = 0.1: 50 (fair)
	// ratio < 0.1: < 50 (poor)

	if ratio >= 1.0 {
		return 100
	} else if ratio >= 0.5 {
		return 75 + int((ratio-0.5)*50)
	} else if ratio >= 0.1 {
		return 50 + int((ratio-0.1)*62.5)
	} else {
		return int(ratio * 500)
	}
}
