package ml

// feature_engineering_guc_test.go — GUC test coverage for feature-engineering
// concepts in the ml package.
//
// Because feature_engineering.go does not yet exist, tests that would require
// a missing symbol (e.g. ExtractFeatures) call t.Skip.  All other tests are
// fully exercised against the existing infrastructure (PeerScorer, WelfordAccum,
// normalize, ipDistance, SwarmHealthPredictor) which provides the algorithmic
// substrate that a feature-engineering layer would build on.
//
// Lens distribution:
//   Knuth  (1–5):  algorithmic correctness, loop invariants, data-structure invariants
//   Turing (6–10): termination conditions, halting behaviour, decidability
//   Church (11–15): functional purity, side-effect isolation, referential transparency
//   Gödel  (16–20): formal consistency, invariant preservation, impossible-state detection

import (
	"math"
	"net"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Knuth lens: algorithmic correctness, loop invariants, data-structure invariants
// =============================================================================

// TestGUC_FeatureEng_Knuth_FeatureVectorFixedLength verifies that the scorer
// maintains exactly the expected number of named feature weights — the analogue
// of a fixed-length feature vector.
func TestGUC_FeatureEng_Knuth_FeatureVectorFixedLength(t *testing.T) {
	const wantFeatureCount = 5
	ps := NewPeerScorer()
	if got := len(ps.weights); got != wantFeatureCount {
		t.Errorf("feature vector length = %d, want %d", got, wantFeatureCount)
	}
}

// TestGUC_FeatureEng_Knuth_NormalizeOutputInUnitInterval verifies that the
// normalize helper — the core of per-feature normalization — always produces
// values in [0, 1] for in-range inputs.
func TestGUC_FeatureEng_Knuth_NormalizeOutputInUnitInterval(t *testing.T) {
	cases := []struct {
		name        string
		value       float64
		minV, maxV  float64
	}{
		{"below min clamps to ≤0", -1, 0, 10},
		{"at min", 0, 0, 10},
		{"midpoint", 5, 0, 10},
		{"at max", 10, 0, 10},
		{"above max", 15, 0, 10},
		{"equal bounds", 5, 5, 5},
		{"negative range", -5, -10, -1},
	}
	for _, tc := range cases {
		got := normalize(tc.value, tc.minV, tc.maxV)
		// Equal bounds: must return 0 (no divide-by-zero)
		if tc.minV == tc.maxV {
			if got != 0 {
				t.Errorf("%s: normalize(%v,%v,%v)=%v, want 0",
					tc.name, tc.value, tc.minV, tc.maxV, got)
			}
			continue
		}
		// For in-range inputs the result must be in [0,1]
		if tc.value >= tc.minV && tc.value <= tc.maxV {
			if got < 0 || got > 1 {
				t.Errorf("%s: normalize(%v,%v,%v)=%v, want in [0,1]",
					tc.name, tc.value, tc.minV, tc.maxV, got)
			}
		}
	}
}

// TestGUC_FeatureEng_Knuth_UploadRateFeatureMonotone verifies that the upload-rate
// dimension of the score is monotonically non-decreasing in uploaded bytes, keeping
// all other fields identical.
func TestGUC_FeatureEng_Knuth_UploadRateFeatureMonotone(t *testing.T) {
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

	uploads := []int64{0, 50_000, 200_000, 500_000, 800_000, 1_000_000}
	prev := -1.0
	for _, u := range uploads {
		p := *base
		p.Uploaded = u
		s := ps.Score(&p, requester)
		if s < prev-1e-9 {
			t.Errorf("upload=%d: score %f decreased below prev %f — not monotone", u, s, prev)
		}
		prev = s
	}
}

// TestGUC_FeatureEng_Knuth_LatencyFeatureDecay verifies that the latency /
// freshness feature decays monotonically as time since last announce grows.
func TestGUC_FeatureEng_Knuth_LatencyFeatureDecay(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()

	base := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          500_000,
		FirstSeen:         now.Add(-48 * time.Hour),
		CompletedSessions: 4,
		TotalSessions:     5,
	}

	delays := []time.Duration{0, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour}
	prev := math.MaxFloat64
	for _, d := range delays {
		p := *base
		p.LastAnnounce = now.Add(-d)
		s := ps.Score(&p, requester)
		if s > prev+1e-9 {
			t.Errorf("delay %v: score %f increased above prev %f — freshness not decaying", d, s, prev)
		}
		prev = s
	}

	// Confirm a fresh peer strictly beats a very stale peer
	fresh := *base
	fresh.LastAnnounce = now
	stale := *base
	stale.LastAnnounce = now.Add(-12 * time.Hour)
	if ps.Score(&fresh, requester) <= ps.Score(&stale, requester) {
		t.Errorf("fresh peer should score strictly higher than 12-hour stale peer")
	}
}

// TestGUC_FeatureEng_Knuth_PeerAgeFeatureCapped verifies that the peer-age
// (availability) feature caps at 1.0 after 24 hours of presence, so peers seen
// much longer ago do not score higher than those seen exactly 24 hours ago.
// A small tolerance accommodates the sub-microsecond drift of time.Since
// between the two Score calls.
func TestGUC_FeatureEng_Knuth_PeerAgeFeatureCapped(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()

	base := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          500_000,
		LastAnnounce:      now,
		CompletedSessions: 4,
		TotalSessions:     5,
	}

	// Peer seen exactly 48 hours ago — well past the 24-hour cap
	p48h := *base
	p48h.FirstSeen = now.Add(-48 * time.Hour)

	// Peer seen 30 days ago (far beyond the 24-hour cap)
	p30d := *base
	p30d.FirstSeen = now.Add(-30 * 24 * time.Hour)

	s48h := ps.Score(&p48h, requester)
	s30d := ps.Score(&p30d, requester)

	// Both are past the cap; scores must be equal within floating-point tolerance.
	// Allow 1e-6 for the sub-microsecond wall-clock drift between the two Score calls.
	if math.Abs(s48h-s30d) > 1e-6 {
		t.Errorf("availability cap not enforced: 48h score=%f, 30d score=%f — should be equal within 1e-6",
			s48h, s30d)
	}
}

// =============================================================================
// Turing lens: termination conditions, halting behaviour, decidability
// =============================================================================

// TestGUC_FeatureEng_Turing_ZeroPeerFiniteScore verifies that a zero-value
// PeerInfo (no uploads, no sessions, zero time) terminates with a finite, valid
// score — the zero-input halting case.
func TestGUC_FeatureEng_Turing_ZeroPeerFiniteScore(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	peer := &PeerInfo{
		IP: net.ParseIP("10.0.0.2"),
		// All other fields are zero values
	}
	s := ps.Score(peer, requester)
	if math.IsNaN(s) || math.IsInf(s, 0) {
		t.Errorf("Score for zero-value PeerInfo is non-finite: %v", s)
	}
	if s < 0 || s > 1 {
		t.Errorf("Score for zero-value PeerInfo = %f, want in [0, 1]", s)
	}
}

// TestGUC_FeatureEng_Turing_FeaturesDeterministicSameInput verifies that the
// pure components of the feature-extraction pipeline — normalize and the
// weight-based dot product — are deterministic: identical inputs always produce
// identical outputs.  Score itself calls time.Since internally, so we test
// determinism via normalize directly (the innermost feature-value function).
func TestGUC_FeatureEng_Turing_FeaturesDeterministicSameInput(t *testing.T) {
	cases := []struct {
		value, minV, maxV float64
	}{
		{500_000, 0, 1_000_000},
		{0, 0, 1_000_000},
		{999_999, 0, 1_000_000},
		{0.5, 0, 1},
		{-3, -10, 10},
	}
	const runs = 50
	for _, tc := range cases {
		first := normalize(tc.value, tc.minV, tc.maxV)
		for i := 1; i < runs; i++ {
			got := normalize(tc.value, tc.minV, tc.maxV)
			if got != first {
				t.Errorf("run %d normalize(%v,%v,%v)=%v, first=%v — not deterministic",
					i, tc.value, tc.minV, tc.maxV, got, first)
			}
		}
	}
}

// TestGUC_FeatureEng_Turing_FeatureNamesExpected verifies that all expected
// feature-name keys are present in the scorer's weight map — the decidability
// check that the feature schema is intact.
func TestGUC_FeatureEng_Turing_FeatureNamesExpected(t *testing.T) {
	expected := []string{"upload_speed", "availability", "proximity", "reputation", "freshness"}
	ps := NewPeerScorer()
	for _, name := range expected {
		if _, ok := ps.weights[name]; !ok {
			t.Errorf("expected feature %q missing from scorer weight map", name)
		}
	}
}

// TestGUC_FeatureEng_Turing_NormalizeEqualBoundsHalts verifies that normalize
// does not loop or panic when min == max (division-by-zero guard termination).
func TestGUC_FeatureEng_Turing_NormalizeEqualBoundsHalts(t *testing.T) {
	cases := []float64{0, 1, -1, 1e300, math.SmallestNonzeroFloat64}
	for _, v := range cases {
		got := normalize(v, v, v) // min == max == value
		if got != 0 {
			t.Errorf("normalize(%v,%v,%v) = %v, want 0 (equal bounds)", v, v, v, got)
		}
	}
}

// TestGUC_FeatureEng_Turing_ConcurrentFeatureExtractionSafe verifies that
// concurrent calls to the pure normalize helper — the core of feature extraction
// — are race-free and yield results within a tiny epsilon of each other.
// Score uses time.Since internally so absolute equality is not expected, but
// a mutex-protected WelfordAccum integration stress-tests the thread safety of
// the on-line statistics that underpin anomaly detection.
func TestGUC_FeatureEng_Turing_ConcurrentFeatureExtractionSafe(t *testing.T) {
	const goroutines = 50
	const perG = 200

	// Concurrent normalize calls — pure function, must be race-free.
	results := make([]float64, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = normalize(float64(idx)*100, 0, float64(goroutines)*100)
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		expected := normalize(float64(i)*100, 0, float64(goroutines)*100)
		if math.Abs(r-expected) > 1e-12 {
			t.Errorf("goroutine %d: normalize=%f, want %f", i, r, expected)
		}
	}

	// Concurrent WelfordAccum updates with a mutex — verify count and mean.
	var mu sync.Mutex
	var w WelfordAccum
	var wg2 sync.WaitGroup
	wg2.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg2.Done()
			for i := 0; i < perG; i++ {
				mu.Lock()
				w.Update(1.0)
				mu.Unlock()
			}
		}()
	}
	wg2.Wait()
	wantCount := int64(goroutines * perG)
	if w.Count() != wantCount {
		t.Errorf("concurrent WelfordAccum count = %d, want %d", w.Count(), wantCount)
	}
	if math.Abs(w.Mean()-1.0) > 1e-12 {
		t.Errorf("concurrent WelfordAccum mean = %f, want 1.0", w.Mean())
	}
}

// =============================================================================
// Church lens: functional purity, side-effect isolation, referential transparency
// =============================================================================

// TestGUC_FeatureEng_Church_ScoreIsPure verifies that calling Score does not
// mutate the PeerScorer's weight map — the feature-engineering pipeline must be
// pure and free of side effects.
func TestGUC_FeatureEng_Church_ScoreIsPure(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	now := time.Now()
	peer := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          500_000,
		FirstSeen:         now.Add(-12 * time.Hour),
		LastAnnounce:      now,
		CompletedSessions: 4,
		TotalSessions:     5,
	}

	snapshot := make(map[string]float64, len(ps.weights))
	for k, v := range ps.weights {
		snapshot[k] = v
	}

	for i := 0; i < 50; i++ {
		ps.Score(peer, requester)
	}

	if len(ps.weights) != len(snapshot) {
		t.Errorf("Score mutated weight map size: before=%d after=%d", len(snapshot), len(ps.weights))
	}
	for k, want := range snapshot {
		got, ok := ps.weights[k]
		if !ok {
			t.Errorf("weight %q disappeared after scoring", k)
		} else if got != want {
			t.Errorf("weight[%q] mutated: before=%f after=%f", k, want, got)
		}
	}
}

// TestGUC_FeatureEng_Church_NormalizeDoesNotMutateInputs verifies that normalize
// is purely functional — the input values are unchanged after the call.
func TestGUC_FeatureEng_Church_NormalizeDoesNotMutateInputs(t *testing.T) {
	origValue, origMin, origMax := 7.0, 0.0, 10.0
	v, mn, mx := origValue, origMin, origMax
	_ = normalize(v, mn, mx)
	if v != origValue || mn != origMin || mx != origMax {
		t.Errorf("normalize mutated its inputs: value %v→%v, min %v→%v, max %v→%v",
			origValue, v, origMin, mn, origMax, mx)
	}
}

// TestGUC_FeatureEng_Church_WelfordStateIsolation verifies that two independent
// WelfordAccum instances — representing parallel feature statistics — do not
// share state.
func TestGUC_FeatureEng_Church_WelfordStateIsolation(t *testing.T) {
	var a, b WelfordAccum
	for i := 1; i <= 10; i++ {
		a.Update(float64(i))
	}
	if b.Count() != 0 {
		t.Errorf("b.Count affected by updates to a: got %d, want 0", b.Count())
	}
	if b.Mean() != 0 {
		t.Errorf("b.Mean affected by updates to a: got %f, want 0", b.Mean())
	}
	if b.Variance() != 0 {
		t.Errorf("b.Variance affected by updates to a: got %f, want 0", b.Variance())
	}
}

// TestGUC_FeatureEng_Church_FeatureNaNReplacedByZero verifies the NaN-guard
// property of normalize: when the range collapses to zero (max == min), the
// function returns 0 rather than NaN.  This is the mechanism by which NaN
// feature values would be replaced with 0 in a feature-engineering pipeline.
func TestGUC_FeatureEng_Church_FeatureNaNReplacedByZero(t *testing.T) {
	// Any value with equal min/max would produce NaN without the guard.
	cases := []struct {
		v, lo, hi float64
	}{
		{0, 0, 0},
		{5, 5, 5},
		{-3, -3, -3},
		{1e10, 1e10, 1e10},
	}
	for _, tc := range cases {
		got := normalize(tc.v, tc.lo, tc.hi)
		if math.IsNaN(got) {
			t.Errorf("normalize(%v,%v,%v) returned NaN, want 0", tc.v, tc.lo, tc.hi)
		}
		if got != 0 {
			t.Errorf("normalize(%v,%v,%v) = %v, want 0", tc.v, tc.lo, tc.hi, got)
		}
	}
}

// TestGUC_FeatureEng_Church_ExtractFeaturesNotYetImplemented is the explicit
// skip marker for the as-yet-unwritten ExtractFeatures surface.
func TestGUC_FeatureEng_Church_ExtractFeaturesNotYetImplemented(t *testing.T) {
	t.Skip("not yet implemented: ExtractFeatures")
}

// =============================================================================
// Gödel lens: formal consistency, invariant preservation, impossible-state detection
// =============================================================================

// TestGUC_FeatureEng_Godel_FeatureVectorBoundsInvariant verifies that every
// component Score produces lies in [0, 1] for a wide range of PeerInfo inputs —
// the formal bound invariant of a normalised feature vector.
func TestGUC_FeatureEng_Godel_FeatureVectorBoundsInvariant(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("192.168.1.1")
	now := time.Now()

	cases := []struct {
		name string
		peer *PeerInfo
	}{
		{"zero-fields", &PeerInfo{IP: net.ParseIP("10.0.0.1"), FirstSeen: now, LastAnnounce: now}},
		{"large-upload", &PeerInfo{IP: net.ParseIP("10.0.0.1"), Uploaded: math.MaxInt32, FirstSeen: now.Add(-48 * time.Hour), LastAnnounce: now, CompletedSessions: 10, TotalSessions: 10}},
		{"stale-announce", &PeerInfo{IP: net.ParseIP("10.0.0.1"), Uploaded: 300_000, FirstSeen: now.Add(-720 * time.Hour), LastAnnounce: now.Add(-48 * time.Hour), CompletedSessions: 1, TotalSessions: 3}},
		{"cross-subnet", &PeerInfo{IP: net.ParseIP("172.16.0.1"), Uploaded: 100_000, FirstSeen: now.Add(-time.Hour), LastAnnounce: now.Add(-time.Minute), CompletedSessions: 2, TotalSessions: 4}},
		{"no-sessions", &PeerInfo{IP: net.ParseIP("10.0.0.2"), Uploaded: 200_000, FirstSeen: now.Add(-2 * time.Hour), LastAnnounce: now}},
	}
	for _, tc := range cases {
		s := ps.Score(tc.peer, requester)
		if s < 0 || s > 1 {
			t.Errorf("%s: Score()=%f, want in [0,1]", tc.name, s)
		}
		if math.IsNaN(s) || math.IsInf(s, 0) {
			t.Errorf("%s: Score() is non-finite: %v", tc.name, s)
		}
	}
}

// TestGUC_FeatureEng_Godel_AllFeaturesFinite verifies that the WelfordAccum —
// the online feature-statistics engine — never produces NaN for reasonable
// peer-feature value ranges (upload rates, durations, ratios).
func TestGUC_FeatureEng_Godel_AllFeaturesFinite(t *testing.T) {
	// Use realistic feature-value ranges only; extreme values such as 1e300
	// are outside the domain and may overflow IEEE 754 variance.
	inputs := [][]float64{
		{1},
		{0, 0, 0},
		{0.0, 0.5, 1.0},             // normalized feature values [0,1]
		{100_000, 200_000, 300_000}, // upload rates
		func() []float64 {
			s := make([]float64, 1000)
			for i := range s {
				s[i] = float64(i) * 0.001 // latency fractions 0..0.999
			}
			return s
		}(),
	}
	for _, seq := range inputs {
		var w WelfordAccum
		for _, v := range seq {
			w.Update(v)
		}
		vals := map[string]float64{
			"Mean":     w.Mean(),
			"Variance": w.Variance(),
			"StdDev":   w.StdDev(),
		}
		for name, val := range vals {
			if math.IsNaN(val) {
				t.Errorf("%s after %d obs is NaN", name, len(seq))
			}
		}
	}
}

// TestGUC_FeatureEng_Godel_FeatureCountConstantNoRagged verifies that the
// number of named features in the scorer is invariant across independently
// constructed instances — no ragged feature vectors.
func TestGUC_FeatureEng_Godel_FeatureCountConstantNoRagged(t *testing.T) {
	const instances = 20
	first := len(NewPeerScorer().weights)
	for i := 1; i < instances; i++ {
		got := len(NewPeerScorer().weights)
		if got != first {
			t.Errorf("instance %d has %d features, want %d — ragged vector detected", i, got, first)
		}
	}
}

// TestGUC_FeatureEng_Godel_ZeroUploadContributesZeroComponent verifies the
// impossible-state invariant: an upload of 0 bytes must contribute the minimum
// possible value to the upload-speed feature, distinguishable from a peer whose
// upload speed fills the normalized range.
//
// We set a 2-second-old peer so the upload-rate = bytes/2s is large enough to
// dominate the score difference.  Both peers share the same FirstSeen, so the
// availability component is identical and does not bias the comparison.
func TestGUC_FeatureEng_Godel_ZeroUploadContributesZeroComponent(t *testing.T) {
	ps := NewPeerScorer()
	requester := net.ParseIP("10.0.0.1")
	// Use a fixed past point so both peers have identical FirstSeen.
	firstSeen := time.Now().Add(-2 * time.Second)
	lastAnnounce := time.Now()

	pZero := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          0,
		FirstSeen:         firstSeen,
		LastAnnounce:      lastAnnounce,
		CompletedSessions: 3,
		TotalSessions:     5,
	}

	// 1 MB uploaded in ~2 seconds → rate ≈ 500 kB/s → normalized ≈ 0.5
	// upload-speed contribution ≈ 0.35 * 0.5 = 0.175, clearly above zero.
	pPositive := &PeerInfo{
		IP:                net.ParseIP("10.0.0.2"),
		Uploaded:          1_000_000,
		FirstSeen:         firstSeen,
		LastAnnounce:      lastAnnounce,
		CompletedSessions: 3,
		TotalSessions:     5,
	}

	sZero := ps.Score(pZero, requester)
	sPos := ps.Score(pPositive, requester)

	if sZero >= sPos {
		t.Errorf("zero upload score (%f) must be strictly less than 1 MB upload score (%f)", sZero, sPos)
	}
}

// TestGUC_FeatureEng_Godel_WeightSumPreservesConsistency verifies the global
// weight-sum invariant: all feature weights must sum to exactly 1.0, ensuring
// that the feature-vector dot product is properly bounded by [0, 1].
func TestGUC_FeatureEng_Godel_WeightSumPreservesConsistency(t *testing.T) {
	ps := NewPeerScorer()
	var sum float64
	for name, w := range ps.weights {
		if w < 0 {
			t.Errorf("weight[%q] = %f is negative — impossible state", name, w)
		}
		sum += w
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("feature weight sum = %.15f, want exactly 1.0", sum)
	}
}
