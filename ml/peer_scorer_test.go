package ml

import (
	"math"
	"net"
	"testing"
	"time"
)

// ── PeerScorer ────────────────────────────────────────────────────────────────

func makePeerInfo(ip string, uploadedMB int, firstSeenHoursAgo float64, lastAnnounceSecsAgo float64) *PeerInfo {
	return &PeerInfo{
		IP:           net.ParseIP(ip),
		Port:         6881,
		Uploaded:     int64(uploadedMB) * 1_000_000,
		Downloaded:   int64(uploadedMB) * 500_000,
		FirstSeen:    time.Now().Add(-time.Duration(firstSeenHoursAgo * float64(time.Hour))),
		LastAnnounce: time.Now().Add(-time.Duration(lastAnnounceSecsAgo * float64(time.Second))),
	}
}

func TestScore_ReturnsValueInRange(t *testing.T) {
	ps := NewPeerScorer()
	peer := makePeerInfo("1.2.3.4", 500, 24, 60)
	requester := net.ParseIP("1.2.3.5")

	score := ps.Score(peer, requester)
	if score < 0 || score > 1.01 { // small epsilon for floating point
		t.Errorf("score %f out of expected [0, 1] range", score)
	}
}

func TestScore_FreshPeerScoresHigherThanStale(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("1.2.3.5")

	fresh := makePeerInfo("1.2.3.4", 500, 1, 60)    // announced 60s ago
	stale := makePeerInfo("1.2.3.6", 500, 1, 7200)  // announced 2 hours ago

	freshScore := ps.Score(fresh, requester)
	staleScore := ps.Score(stale, requester)

	if freshScore <= staleScore {
		t.Errorf("fresh peer score %f should exceed stale peer score %f", freshScore, staleScore)
	}
}

func TestScore_FastUploaderScoresHigher(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("1.2.3.5")

	// Both recently active; fast uploader has much higher uploaded bytes.
	fast := makePeerInfo("1.2.3.4", 10_000, 1, 30) // 10 GB uploaded in 1 hour
	slow := makePeerInfo("1.2.3.6", 10, 1, 30)     // 10 MB uploaded in 1 hour

	fastScore := ps.Score(fast, requester)
	slowScore := ps.Score(slow, requester)

	if fastScore <= slowScore {
		t.Errorf("fast uploader score %f should exceed slow uploader score %f", fastScore, slowScore)
	}
}

func TestScore_NetworkProximity_SameSubnet(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")

	near := makePeerInfo("10.0.0.2", 100, 1, 60)  // same /8 — first-octet XOR = 0
	far := makePeerInfo("200.0.0.2", 100, 1, 60)  // different /8 — first-octet XOR = 210

	nearScore := ps.Score(near, requester)
	farScore := ps.Score(far, requester)

	if nearScore <= farScore {
		t.Errorf("nearby peer score %f should exceed far peer score %f", nearScore, farScore)
	}
}

// ── SelectBest ────────────────────────────────────────────────────────────────

func TestSelectBest_ReturnsExactNumWant(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("1.2.3.5")

	peers := make([]*PeerInfo, 10)
	for i := range peers {
		peers[i] = makePeerInfo("1.2.3.4", (i+1)*100, 1, float64(i*60))
	}

	result := ps.SelectBest(peers, requester, 3)
	if len(result) != 3 {
		t.Errorf("SelectBest returned %d peers, want 3", len(result))
	}
}

func TestSelectBest_NumWantExceedsPeers(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("1.2.3.5")

	peers := []*PeerInfo{
		makePeerInfo("1.2.3.4", 100, 1, 60),
		makePeerInfo("1.2.3.6", 200, 2, 120),
	}

	result := ps.SelectBest(peers, requester, 10)
	if len(result) != 2 {
		t.Errorf("SelectBest returned %d peers, want 2 (capped at input size)", len(result))
	}
}

func TestSelectBest_EmptyInput(t *testing.T) {
	ps := NewPeerScorer()
	result := ps.SelectBest(nil, net.ParseIP("1.2.3.4"), 5)
	if len(result) != 0 {
		t.Errorf("SelectBest on nil input returned %d peers, want 0", len(result))
	}
}

func TestSelectBest_OrderedByScore(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("1.2.3.5")

	// Deliberately construct peers with large upload spread and same staleness.
	best := makePeerInfo("1.2.3.4", 50_000, 1, 10)  // 50 GB upload, very fresh
	mid := makePeerInfo("1.2.3.6", 1_000, 1, 10)
	worst := makePeerInfo("1.2.3.8", 1, 1, 3600)    // 1 MB upload, stale

	result := ps.SelectBest([]*PeerInfo{worst, mid, best}, requester, 2)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	// First result should be `best` — highest score.
	if result[0] != best {
		t.Errorf("SelectBest[0] should be the best peer (highest upload + freshness)")
	}
}

// ── SwarmHealthPredictor ──────────────────────────────────────────────────────

func TestSwarmHealth_ZeroSeeders(t *testing.T) {
	shp := NewSwarmHealthPredictor()
	score := shp.HealthScore(0, 10)
	if score != 0 {
		t.Errorf("HealthScore with 0 seeders = %d, want 0", score)
	}
}

func TestSwarmHealth_ZeroLeechers(t *testing.T) {
	shp := NewSwarmHealthPredictor()
	score := shp.HealthScore(5, 0)
	if score != 100 {
		t.Errorf("HealthScore with 0 leechers = %d, want 100", score)
	}
}

func TestSwarmHealth_GoodRatio_SeedersDominant(t *testing.T) {
	shp := NewSwarmHealthPredictor()
	score := shp.HealthScore(10, 5) // ratio = 2.0 ≥ 1.0 → 100
	if score != 100 {
		t.Errorf("HealthScore(10s, 5l) = %d, want 100", score)
	}
}

func TestSwarmHealth_PoorRatio(t *testing.T) {
	shp := NewSwarmHealthPredictor()
	score := shp.HealthScore(1, 100) // ratio = 0.01 → low score
	if score > 20 {
		t.Errorf("HealthScore(1s, 100l) = %d, expected poor score (≤20)", score)
	}
}

func TestSwarmHealth_ScoreRange(t *testing.T) {
	shp := NewSwarmHealthPredictor()
	cases := [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}, {5, 10}, {10, 5}, {100, 1}}
	for _, c := range cases {
		score := shp.HealthScore(c[0], c[1])
		if score < 0 || score > 100 {
			t.Errorf("HealthScore(%d, %d) = %d, out of [0, 100]", c[0], c[1], score)
		}
	}
}

func TestPredictCompletionTime_NoSeeders(t *testing.T) {
	shp := NewSwarmHealthPredictor()
	d := shp.PredictCompletionTime(0, 10, 1_000_000, 10_000_000_000)
	// math.MaxInt64 nanoseconds ≈ 2.56 million hours — check we got the sentinel value.
	if d != time.Duration(math.MaxInt64) {
		t.Errorf("PredictCompletionTime with 0 seeders = %v, want max duration", d)
	}
}

func TestPredictCompletionTime_SingleSeeder(t *testing.T) {
	shp := NewSwarmHealthPredictor()
	// 1 seeder at 1 MB/s, 1 leecher wants 100 MB → ~100s
	d := shp.PredictCompletionTime(1, 1, 1_000_000, 100_000_000)
	if d.Seconds() < 90 || d.Seconds() > 110 {
		t.Errorf("PredictCompletionTime = %v, want ~100s", d)
	}
}

func TestPredictCompletionTime_MultipleSeeders(t *testing.T) {
	shp := NewSwarmHealthPredictor()
	// 10 seeders at 1 MB/s each, 10 leechers each want 100 MB → still ~100s
	d := shp.PredictCompletionTime(10, 10, 1_000_000, 100_000_000)
	if d.Seconds() < 90 || d.Seconds() > 110 {
		t.Errorf("PredictCompletionTime = %v, want ~100s", d)
	}
}
