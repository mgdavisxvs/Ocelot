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

func normalize(value, min, max float64) float64 {
	if max-min == 0 {
		return 0
	}
	return (value - min) / (max - min)
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// ipDistance calculates network distance between two IPs
// Simple implementation: XOR of first octet
func ipDistance(ip1, ip2 net.IP) int {
	ip1v4 := ip1.To4()
	ip2v4 := ip2.To4()

	if ip1v4 != nil && ip2v4 != nil {
		// IPv4: XOR distance
		return int(ip1v4[0] ^ ip2v4[0])
	}

	// IPv6 or mixed: Simple distance
	if len(ip1) == 16 && len(ip2) == 16 {
		return int(ip1[0] ^ ip2[0])
	}

	return 128 // Max distance for mixed/unknown
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
