package ml

import (
	"math"
	"net"
	"sort"
	"time"
)

// PeerWeights holds the five scoring dimensions for peer selection (D-C3).
// Named fields prevent accidental weight assignment to the wrong dimension.
type PeerWeights struct {
	UploadSpeed  float64 // fraction of score from upload speed
	Availability float64 // fraction from uptime / days active
	Proximity    float64 // fraction from network closeness
	Reputation   float64 // fraction from session completion ratio
	Freshness    float64 // fraction from recency of last announce
}

// DefaultPeerWeights returns the canonical weight set.
func DefaultPeerWeights() PeerWeights {
	return PeerWeights{
		UploadSpeed:  0.35,
		Availability: 0.25,
		Proximity:    0.20,
		Reputation:   0.15,
		Freshness:    0.05,
	}
}

// PeerScorer provides ML-based peer recommendation
type PeerScorer struct {
	weights PeerWeights
	// p95Speed is a running P95 upload-speed estimator used as the
	// normalization ceiling instead of the fixed 1e6 magic constant (D-K3).
	p95Speed *p95Estimator
}

// NewPeerScorer creates a new peer scorer
func NewPeerScorer() *PeerScorer {
	return &PeerScorer{
		weights:  DefaultPeerWeights(),
		p95Speed: newP95Estimator(1024),
	}
}

// Score calculates a score for a peer based on multiple factors
func (ps *PeerScorer) Score(peer *PeerInfo, requesterIP net.IP) float64 {
	score := 0.0

	// Upload speed: normalize against live P95 ceiling (D-K3).
	uploadSpeed := float64(peer.Uploaded) / max(float64(time.Since(peer.FirstSeen).Seconds()), 1.0)
	ps.p95Speed.observe(uploadSpeed)
	ceiling := ps.p95Speed.p95()
	if ceiling < 1 {
		ceiling = 1e6 // fallback before enough observations
	}
	normalizedSpeed := normalize(uploadSpeed, 0, ceiling)
	score += ps.weights.UploadSpeed * normalizedSpeed

	// Availability (uptime percentage)
	uptime := time.Since(peer.FirstSeen).Hours()
	availability := min(uptime/24.0, 1.0) // Days active, capped at 1.0
	score += ps.weights.Availability * availability

	// Proximity (network distance)
	proximity := 1.0 - (float64(ipDistance(peer.IP, requesterIP)) / 255.0)
	score += ps.weights.Proximity * proximity

	// Reputation (completed vs dropped sessions)
	reputation := float64(peer.CompletedSessions) / max(float64(peer.TotalSessions), 1.0)
	score += ps.weights.Reputation * reputation

	// Freshness (recent activity)
	secondsSinceAnnounce := time.Since(peer.LastAnnounce).Seconds()
	freshness := 1.0 / (1.0 + secondsSinceAnnounce/1800.0) // Half-life 30 minutes
	score += ps.weights.Freshness * freshness

	return score
}

// p95Estimator tracks a sliding window of float64 samples and returns
// the approximate P95 value (D-K3 running-estimate ceiling).
type p95Estimator struct {
	buf  []float64
	pos  int
	full bool
}

func newP95Estimator(capacity int) *p95Estimator {
	return &p95Estimator{buf: make([]float64, capacity)}
}

func (e *p95Estimator) observe(v float64) {
	e.buf[e.pos] = v
	e.pos = (e.pos + 1) % len(e.buf)
	if e.pos == 0 {
		e.full = true
	}
}

func (e *p95Estimator) p95() float64 {
	size := len(e.buf)
	if !e.full {
		size = e.pos
	}
	if size == 0 {
		return 0
	}
	tmp := make([]float64, size)
	copy(tmp, e.buf[:size])
	sort.Float64s(tmp)
	idx := int(math.Ceil(float64(size)*0.95)) - 1
	if idx < 0 {
		idx = 0
	}
	return tmp[idx]
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

// PeerInfo holds extended peer information for ML scoring.
// IPPort is a pre-computed compact BEP 23 / BEP 7 byte slice (6 or 18 bytes)
// copied from the Peer under the torrent lock so that scoring can proceed
// without holding the lock.
type PeerInfo struct {
	IP                net.IP
	Port              uint16
	Uploaded          int64
	Downloaded        int64
	FirstSeen         time.Time
	LastAnnounce      time.Time
	CompletedSessions int
	TotalSessions     int
	IPPort            []byte
}

// Helper functions

func normalize(value, minVal, maxVal float64) float64 {
	if maxVal-minVal == 0 {
		return 0
	}
	return (value - minVal) / (maxVal - minVal)
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
