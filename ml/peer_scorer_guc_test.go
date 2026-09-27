package ml

import (
	"math"
	"net"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// GUC TEST COVERAGE — peer_scorer.go + welford.go
//
// Lens distribution:
//   Knuth  (1–5):  algorithmic correctness, loop invariants, data-structure invariants
//   Turing (6–10): termination, halting, decidability
//   Church (11–15): functional purity, side-effect isolation, referential transparency
//   Gödel  (16–20): formal consistency, invariant preservation, impossible-state detection
// =============================================================================

// ----------------------------------------------------------------------------
// Knuth 1: score is non-decreasing in upload amount (all other fields equal)
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_ScoreMonotoneInUploadRate(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()

	base := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Port:              6881,
		FirstSeen:         now.Add(-24 * time.Hour),
		LastAnnounce:      now,
		CompletedSessions: 5,
		TotalSessions:     5,
	}

	uploads := []struct {
		name     string
		uploaded int64
	}{
		{"zero", 0},
		{"low", 100_000},
		{"medium", 500_000},
		{"high", 900_000},
	}

	prev := -1.0
	for _, tc := range uploads {
		peer := *base
		peer.Uploaded = tc.uploaded
		s := ps.Score(&peer, requester)
		if s < prev-1e-9 {
			t.Errorf("monotonicity violated at %s (uploaded=%d): score %f < prev %f",
				tc.name, tc.uploaded, s, prev)
		}
		prev = s
	}
}

// ----------------------------------------------------------------------------
// Knuth 2: score is non-increasing as time since last announce grows (freshness penalty)
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_ScoreMonotoneInverseLatency(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()

	base := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          500_000,
		FirstSeen:         now.Add(-48 * time.Hour),
		CompletedSessions: 5,
		TotalSessions:     5,
	}

	delays := []time.Duration{0, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	prevScore := math.MaxFloat64
	for _, delay := range delays {
		peer := *base
		peer.LastAnnounce = now.Add(-delay)
		s := ps.Score(&peer, requester)
		if s > prevScore+1e-9 {
			t.Errorf("score should be non-increasing with delay %v: got %f > prev %f",
				delay, s, prevScore)
		}
		prevScore = s
	}
}

// ----------------------------------------------------------------------------
// Knuth 3: all weights in a new PeerScorer must sum to 1.0 (weight invariant)
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_WeightSumInvariant(t *testing.T) {
	ps := NewPeerScorer()
	sum := 0.0
	for _, w := range ps.weights {
		sum += w
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("weight sum = %.15f, want 1.0", sum)
	}
}

// ----------------------------------------------------------------------------
// Knuth 4: WelfordAccum running mean matches direct average for known values
// ----------------------------------------------------------------------------
func TestGUC_WelfordAccum_MeanCorrectness(t *testing.T) {
	var w WelfordAccum
	values := []float64{1, 2, 3, 4, 5}
	for _, v := range values {
		w.Update(v)
	}
	want := 3.0 // (1+2+3+4+5)/5
	if math.Abs(w.Mean()-want) > 1e-9 {
		t.Errorf("Mean() = %f, want %f", w.Mean(), want)
	}
	if w.Count() != int64(len(values)) {
		t.Errorf("Count() = %d, want %d", w.Count(), len(values))
	}
}

// ----------------------------------------------------------------------------
// Knuth 5: WelfordAccum Bessel-corrected sample variance matches known result
// ----------------------------------------------------------------------------
func TestGUC_WelfordAccum_VarianceCorrectness(t *testing.T) {
	var w WelfordAccum
	// Dataset with known sample variance = 4.571428...
	values := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	for _, v := range values {
		w.Update(v)
	}
	wantVar := 4.571428571428571
	wantStd := math.Sqrt(wantVar)
	if math.Abs(w.Variance()-wantVar) > 1e-6 {
		t.Errorf("Variance() = %f, want ~%f", w.Variance(), wantVar)
	}
	if math.Abs(w.StdDev()-wantStd) > 1e-6 {
		t.Errorf("StdDev() = %f, want ~%f", w.StdDev(), wantStd)
	}
}

// ----------------------------------------------------------------------------
// Turing 6: Score always stays in the decidable [0, 1] range for valid inputs
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_ScoreAlwaysInUnitInterval(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("192.168.1.1")
	now := time.Now()

	cases := []struct {
		name string
		peer *PeerInfo
	}{
		{
			"zero-upload",
			&PeerInfo{
				IP:           net.ParseIP("10.0.0.1"),
				FirstSeen:    now.Add(-time.Minute),
				LastAnnounce: now,
			},
		},
		{
			"max-int32-upload-24h",
			&PeerInfo{
				IP:                net.ParseIP("10.0.0.1"),
				Uploaded:          math.MaxInt32,
				FirstSeen:         now.Add(-24 * time.Hour),
				LastAnnounce:      now,
				CompletedSessions: 100,
				TotalSessions:     100,
			},
		},
		{
			"old-announce",
			&PeerInfo{
				IP:                net.ParseIP("10.0.0.1"),
				Uploaded:          500_000,
				FirstSeen:         now.Add(-720 * time.Hour),
				LastAnnounce:      now.Add(-24 * time.Hour),
				CompletedSessions: 1,
				TotalSessions:     2,
			},
		},
		{
			"same-subnet-recent",
			&PeerInfo{
				IP:                net.ParseIP("192.168.1.5"),
				Uploaded:          250_000,
				FirstSeen:         now.Add(-2 * time.Hour),
				LastAnnounce:      now.Add(-time.Minute),
				CompletedSessions: 3,
				TotalSessions:     3,
			},
		},
		{
			"no-sessions",
			&PeerInfo{
				IP:                net.ParseIP("10.0.0.2"),
				Uploaded:          200_000,
				FirstSeen:         now.Add(-2 * time.Hour),
				LastAnnounce:      now,
				CompletedSessions: 0,
				TotalSessions:     0,
			},
		},
	}

	for _, tc := range cases {
		s := ps.Score(tc.peer, requester)
		if s < 0 || s > 1 {
			t.Errorf("%s: Score() = %f, want in [0, 1]", tc.name, s)
		}
	}
}

// ----------------------------------------------------------------------------
// Turing 7: Score terminates correctly under concurrent load (no data race)
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_ConcurrentScoreSafe(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()
	peer := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          500_000,
		FirstSeen:         now.Add(-time.Hour),
		LastAnnounce:      now,
		CompletedSessions: 3,
		TotalSessions:     5,
	}

	const goroutines = 50
	scores := make([]float64, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			scores[idx] = ps.Score(peer, requester)
		}(i)
	}
	wg.Wait()

	s0 := scores[0]
	for i, s := range scores {
		if math.Abs(s-s0) > 1e-4 {
			t.Errorf("goroutine %d produced divergent score: %f vs %f", i, s, s0)
		}
	}
}

// ----------------------------------------------------------------------------
// Turing 8: SelectBest never returns more than numWant peers
// ----------------------------------------------------------------------------
func TestGUC_SelectBest_NumWantLimitRespected(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()

	makePeer := func(i int) *PeerInfo {
		return &PeerInfo{
			IP:           net.ParseIP("10.0.0.2"),
			Uploaded:     int64(i * 50_000),
			FirstSeen:    now.Add(-time.Hour),
			LastAnnounce: now,
		}
	}
	peers := make([]*PeerInfo, 20)
	for i := range peers {
		peers[i] = makePeer(i)
	}

	cases := []struct{ numWant, wantLen int }{
		{5, 5},
		{0, 0},
		{25, 20}, // requests more than available
		{20, 20}, // exact match
	}
	for _, tc := range cases {
		got := ps.SelectBest(peers, requester, tc.numWant)
		if len(got) != tc.wantLen {
			t.Errorf("numWant=%d: got %d peers, want %d", tc.numWant, len(got), tc.wantLen)
		}
	}
}

// ----------------------------------------------------------------------------
// Turing 9: PredictCompletionTime halts with sentinel when seeders=0
// ----------------------------------------------------------------------------
func TestGUC_SwarmHealthPredictor_ZeroSeedersHalts(t *testing.T) {
	shp := NewSwarmHealthPredictor()
	d := shp.PredictCompletionTime(0, 100, 1e6, 1e9)
	if d != time.Duration(math.MaxInt64) {
		t.Errorf("expected MaxInt64 duration for 0 seeders, got %v", d)
	}
}

// ----------------------------------------------------------------------------
// Turing 10: high latency (stale announce) is decidably penalized vs fresh
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_HighLatencyPenalizes(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()

	base := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          500_000,
		FirstSeen:         now.Add(-24 * time.Hour),
		CompletedSessions: 3,
		TotalSessions:     5,
	}

	recentPeer := *base
	recentPeer.LastAnnounce = now

	stalePeer := *base
	stalePeer.LastAnnounce = now.Add(-6 * time.Hour)

	recent := ps.Score(&recentPeer, requester)
	stale := ps.Score(&stalePeer, requester)

	if recent <= stale {
		t.Errorf("recent announce (%f) should score higher than stale (%f)", recent, stale)
	}
}

// ----------------------------------------------------------------------------
// Church 11: Score has no side effects on the PeerScorer's weight map
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_ScoreIsPure(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()
	peer := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          500_000,
		FirstSeen:         now.Add(-time.Hour),
		LastAnnounce:      now.Add(-5 * time.Minute),
		CompletedSessions: 4,
		TotalSessions:     5,
	}

	wCountBefore := len(ps.weights)
	wValuesBefore := make(map[string]float64, len(ps.weights))
	for k, v := range ps.weights {
		wValuesBefore[k] = v
	}

	// Call Score multiple times
	for i := 0; i < 10; i++ {
		ps.Score(peer, requester)
	}

	if len(ps.weights) != wCountBefore {
		t.Errorf("Score mutated weight map size: before=%d after=%d", wCountBefore, len(ps.weights))
	}
	for k, want := range wValuesBefore {
		got, ok := ps.weights[k]
		if !ok {
			t.Errorf("weight key %q disappeared after scoring", k)
		} else if got != want {
			t.Errorf("weight[%q] changed from %f to %f", k, want, got)
		}
	}
}

// ----------------------------------------------------------------------------
// Church 12: WelfordAccum Reset restores zero state (side-effect isolation)
// ----------------------------------------------------------------------------
func TestGUC_WelfordAccum_ResetRestoresZeroState(t *testing.T) {
	var w WelfordAccum
	for _, v := range []float64{10, 20, 30, 40, 50} {
		w.Update(v)
	}
	w.Reset()

	if w.Count() != 0 {
		t.Errorf("Count after Reset = %d, want 0", w.Count())
	}
	if w.Mean() != 0 {
		t.Errorf("Mean after Reset = %f, want 0", w.Mean())
	}
	if w.Variance() != 0 {
		t.Errorf("Variance after Reset = %f, want 0", w.Variance())
	}
	if w.StdDev() != 0 {
		t.Errorf("StdDev after Reset = %f, want 0", w.StdDev())
	}
	// ZScore with no observations must return 0
	if w.ZScore(99) != 0 {
		t.Errorf("ZScore after Reset = %f, want 0", w.ZScore(99))
	}
}

// ----------------------------------------------------------------------------
// Church 13: empty WelfordAccum returns zero defaults (referential transparency)
// ----------------------------------------------------------------------------
func TestGUC_WelfordAccum_EmptyHistoryReturnsDefault(t *testing.T) {
	var w WelfordAccum
	if w.Count() != 0 {
		t.Errorf("Count() = %d, want 0 for empty accum", w.Count())
	}
	if w.Mean() != 0 {
		t.Errorf("Mean() = %f, want 0 for empty accum", w.Mean())
	}
	if w.Variance() != 0 {
		t.Errorf("Variance() = %f, want 0 for empty accum", w.Variance())
	}
	if w.ZScore(0) != 0 {
		t.Errorf("ZScore(0) = %f, want 0 for empty accum", w.ZScore(0))
	}
}

// ----------------------------------------------------------------------------
// Church 14: same peer rescored after field update yields a different result
//            (scorer reads live data, caches nothing)
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_SamePeerRescoredAfterUpdate(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()
	peer := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          100_000,
		FirstSeen:         now.Add(-24 * time.Hour),
		LastAnnounce:      now,
		CompletedSessions: 1,
		TotalSessions:     5,
	}
	s1 := ps.Score(peer, requester)

	// Simulate successful upload and completed session
	peer.Uploaded = 950_000
	peer.CompletedSessions = 5
	s2 := ps.Score(peer, requester)

	if s2 <= s1 {
		t.Errorf("score should increase after upload update: before=%f after=%f", s1, s2)
	}
}

// ----------------------------------------------------------------------------
// Church 15: Score for a zero-value PeerInfo is finite and in [0, 1]
//            (pure function handles edge-case inputs without panic)
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_DefaultScoreForNewPeer(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	peer := &PeerInfo{
		IP: net.ParseIP("10.0.0.2"),
		// All other fields are zero values: zero time, 0 uploaded, etc.
	}
	s := ps.Score(peer, requester)
	if math.IsNaN(s) || math.IsInf(s, 0) {
		t.Errorf("Score for zero-value PeerInfo is non-finite: %v", s)
	}
	if s < 0 || s > 1 {
		t.Errorf("Score for zero-value PeerInfo = %f, want in [0, 1]", s)
	}
}

// ----------------------------------------------------------------------------
// Gödel 16: zero upload contributes nothing to upload_speed component
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_ZeroUploadScoresMinimal(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()

	base := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		FirstSeen:         now.Add(-24 * time.Hour),
		LastAnnounce:      now,
		CompletedSessions: 3,
		TotalSessions:     5,
	}

	peerZero := *base
	peerZero.Uploaded = 0
	s0 := ps.Score(&peerZero, requester)

	peerHigh := *base
	peerHigh.Uploaded = 500_000
	sHigh := ps.Score(&peerHigh, requester)

	if s0 >= sHigh {
		t.Errorf("zero-upload score (%f) should be < non-zero upload score (%f)", s0, sHigh)
	}
}

// ----------------------------------------------------------------------------
// Gödel 17: score is non-decreasing across a table of strictly increasing uploads
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_HighUploadScoresHigher(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()

	base := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		FirstSeen:         now.Add(-24 * time.Hour),
		LastAnnounce:      now,
		CompletedSessions: 3,
		TotalSessions:     5,
	}

	uploads := []int64{0, 50_000, 250_000, 500_000, 750_000}
	prev := -1.0
	for _, u := range uploads {
		peer := *base
		peer.Uploaded = u
		s := ps.Score(&peer, requester)
		if s < prev-1e-9 {
			t.Errorf("upload=%d: score %f decreased from prev %f", u, s, prev)
		}
		prev = s
	}
	// Ensure the highest upload strictly beats zero upload
	peerTop := *base
	peerTop.Uploaded = uploads[len(uploads)-1]
	peerBot := *base
	peerBot.Uploaded = uploads[0]
	if ps.Score(&peerTop, requester) <= ps.Score(&peerBot, requester) {
		t.Errorf("highest upload should strictly outscore zero upload")
	}
}

// ----------------------------------------------------------------------------
// Gödel 18: WelfordAccum detects outliers via Z-score (consistency invariant)
// ----------------------------------------------------------------------------
func TestGUC_WelfordAccum_OutlierZScoreDetected(t *testing.T) {
	var w WelfordAccum
	// Populate with integers 1–20 (mean=10.5, stddev≈5.92)
	for i := 1; i <= 20; i++ {
		w.Update(float64(i))
	}
	outlier := 100.0
	z := w.ZScore(outlier)
	if math.Abs(z) < 2.0 {
		t.Errorf("outlier z-score = %f, want |z| > 2 for value %f", z, outlier)
	}
	// Normal value should have low z-score
	normal := w.Mean()
	zNormal := w.ZScore(normal)
	if math.Abs(zNormal) > 1e-9 {
		t.Errorf("ZScore(mean) = %f, want 0", zNormal)
	}
}

// ----------------------------------------------------------------------------
// Gödel 19: Score with multiple features matches the structural formula
//            (no component is silently dropped)
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_MultipleFeaturesCorrect(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()

	peer := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          500_000,
		FirstSeen:         now.Add(-24 * time.Hour),
		LastAnnounce:      now,
		CompletedSessions: 4,
		TotalSessions:     5,
	}

	got := ps.Score(peer, requester)

	// Score must be positive (all components contribute)
	if got <= 0 {
		t.Errorf("multi-feature score should be > 0, got %f", got)
	}
	if got > 1 {
		t.Errorf("multi-feature score should be <= 1, got %f", got)
	}

	// Removing upload component should lower the score
	peerNoUpload := *peer
	peerNoUpload.Uploaded = 0
	withoutUpload := ps.Score(&peerNoUpload, requester)
	if got <= withoutUpload {
		t.Errorf("500k upload (%f) should score higher than 0 upload (%f)", got, withoutUpload)
	}

	// Removing reputation component (0/5 sessions) should lower the score
	peerNoRep := *peer
	peerNoRep.CompletedSessions = 0
	withoutRep := ps.Score(&peerNoRep, requester)
	if got <= withoutRep {
		t.Errorf("4/5 reputation (%f) should score higher than 0/5 (%f)", got, withoutRep)
	}
}

// ----------------------------------------------------------------------------
// Gödel 20: Score is never negative for any valid (non-negative upload) PeerInfo
// ----------------------------------------------------------------------------
func TestGUC_PeerScorer_ScoreNotNegative(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("192.168.1.1")
	now := time.Now()

	cases := []struct {
		name string
		peer *PeerInfo
	}{
		{
			"all-zero-upload",
			&PeerInfo{
				IP:           net.ParseIP("10.0.0.1"),
				FirstSeen:    now,
				LastAnnounce: now,
			},
		},
		{
			"no-sessions",
			&PeerInfo{
				IP:                net.ParseIP("10.0.0.1"),
				Uploaded:          200_000,
				FirstSeen:         now.Add(-2 * time.Hour),
				LastAnnounce:      now,
				CompletedSessions: 0,
				TotalSessions:     0,
			},
		},
		{
			"perfect-reputation",
			&PeerInfo{
				IP:                net.ParseIP("10.0.0.1"),
				Uploaded:          800_000,
				FirstSeen:         now.Add(-48 * time.Hour),
				LastAnnounce:      now,
				CompletedSessions: 10,
				TotalSessions:     10,
			},
		},
		{
			"cross-subnet",
			&PeerInfo{
				IP:                net.ParseIP("172.16.0.1"),
				Uploaded:          100_000,
				FirstSeen:         now.Add(-time.Hour),
				LastAnnounce:      now.Add(-time.Minute),
				CompletedSessions: 2,
				TotalSessions:     4,
			},
		},
	}

	for _, tc := range cases {
		s := ps.Score(tc.peer, requester)
		if s < 0 {
			t.Errorf("%s: Score() = %f, must be >= 0", tc.name, s)
		}
	}
}
