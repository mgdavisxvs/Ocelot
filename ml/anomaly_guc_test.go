package ml

// anomaly_guc_test.go — GUC (Gödel Unified Council) coverage for anomaly_detector.go
// and welford.go.
//
// Lenses:
//   Knuth  — algorithmic correctness, data-structure invariants
//   Turing — termination, halting, edge-case decidability
//   Church — functional purity, side-effect isolation, immutability
//   Gödel  — formal consistency, impossible-state detection, contradiction checks

import (
	"math"
	"sync"
	"testing"
	"time"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func gucCleanBehavior() *PeerBehavior {
	return &PeerBehavior{
		Uploaded:    500_000_000,
		Downloaded:  400_000_000,
		UploadSpeed: 100_000,
		TorrentSize: 1_000_000_000,
		AnnounceCount: 3,
		PortHistory:   []uint16{6881},
		ConnectionTimes: []time.Time{
			time.Now().Add(-5 * time.Minute),
			time.Now().Add(-3 * time.Minute),
		},
		FirstSeen: time.Now().Add(-1 * time.Hour),
	}
}

// ─── Knuth — algorithmic correctness ─────────────────────────────────────────

// TestGUC_Anomaly_NormalTrafficBenign verifies that a clean peer session with
// plausible statistics is never flagged as anomalous.
func TestGUC_Anomaly_NormalTrafficBenign(t *testing.T) {
	ad := NewAnomalyDetector()
	b := gucCleanBehavior()
	flagged, reason := ad.DetectAnomaly(b)
	if flagged {
		t.Errorf("normal traffic must not be flagged: reason=%q", reason)
	}
}

// TestGUC_Anomaly_UploadSpikeFlagged verifies that an upload speed exceeding
// the 1 Gbps cap triggers the impossible_upload_speed anomaly.
func TestGUC_Anomaly_UploadSpikeFlagged(t *testing.T) {
	cases := []struct {
		name  string
		speed float64
		want  bool
	}{
		{"at threshold − 1", 1e9 - 1, false},
		{"at threshold", 1e9, false}, // boundary: > not >=
		{"above threshold", 1e9 + 1, true},
		{"extreme spike", 100e9, true},
	}
	ad := NewAnomalyDetector()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			b := gucCleanBehavior()
			b.UploadSpeed = tc.speed
			flagged, reason := ad.DetectAnomaly(b)
			if flagged != tc.want {
				t.Errorf("speed=%.0f: flagged=%v want=%v reason=%q", tc.speed, flagged, tc.want, reason)
			}
		})
	}
}

// TestGUC_Welford_ZScoreThresholdRespected verifies that ZScore returns a value
// whose magnitude exceeds a threshold when an outlier observation is fed after
// a stable distribution has been established.
func TestGUC_Welford_ZScoreThresholdRespected(t *testing.T) {
	var w WelfordAccum
	// Build a stable distribution around 100.
	for i := 0; i < 50; i++ {
		w.Update(100.0)
	}
	w.Update(98.0)
	w.Update(102.0)
	// Outlier: 3× stddev away should produce |z| > 2.
	outlier := w.Mean() + 10*w.StdDev()
	z := w.ZScore(outlier)
	if math.Abs(z) <= 2 {
		t.Errorf("expected |z| > 2 for 10-sigma outlier, got z=%.4f", z)
	}
}

// TestGUC_Anomaly_BorderlineExactThreshold checks the exact boundary
// for port-scanning detection (maxPortChanges = 10).
func TestGUC_Anomaly_BorderlineExactThreshold(t *testing.T) {
	ad := NewAnomalyDetector()

	// Exactly 10 ports — should NOT trigger (condition is > not >=).
	b10 := gucCleanBehavior()
	b10.PortHistory = make([]uint16, 10)
	for i := range b10.PortHistory {
		b10.PortHistory[i] = uint16(6881 + i)
	}
	flagged, reason := ad.DetectAnomaly(b10)
	if flagged && reason == "port_scanning" {
		t.Errorf("exactly 10 ports must not trigger port_scanning (threshold is >10)")
	}

	// Exactly 11 ports — must trigger.
	b11 := gucCleanBehavior()
	b11.PortHistory = make([]uint16, 11)
	for i := range b11.PortHistory {
		b11.PortHistory[i] = uint16(6881 + i)
	}
	flagged11, reason11 := ad.DetectAnomaly(b11)
	if !flagged11 || reason11 != "port_scanning" {
		t.Errorf("11 ports must trigger port_scanning, got flagged=%v reason=%q", flagged11, reason11)
	}
}

// TestGUC_Welford_NegativeDeltaHandled verifies that negative observations
// update the accumulator correctly without producing negative variance.
func TestGUC_Welford_NegativeDeltaHandled(t *testing.T) {
	var w WelfordAccum
	samples := []float64{-10, -5, -15, -8, -12}
	for _, s := range samples {
		w.Update(s)
	}
	if w.Variance() < 0 {
		t.Errorf("variance must be non-negative, got %f", w.Variance())
	}
	wantMean := (-10 - 5 - 15 - 8 - 12) / 5.0
	if math.Abs(w.Mean()-wantMean) > 1e-9 {
		t.Errorf("mean: got %f, want %f", w.Mean(), wantMean)
	}
}

// ─── Turing — termination and halting behaviour ───────────────────────────────

// TestGUC_Welford_WarmupLessThan10_NotAnomaly verifies that with fewer than
// 2 observations the accumulator produces zero variance and zero Z-score,
// so a caller using it as a warm-up guard will not flag anything.
func TestGUC_Welford_WarmupLessThan10_NotAnomaly(t *testing.T) {
	var w WelfordAccum
	for i := 0; i < 9; i++ {
		w.Update(float64(i * 100))
		if w.Count() < 2 {
			if w.Variance() != 0 {
				t.Errorf("n=%d: variance must be 0 before 2nd sample, got %f", w.Count(), w.Variance())
			}
			if w.ZScore(999) != 0 {
				t.Errorf("n=%d: z-score must be 0 before stable distribution, got %f", w.Count(), w.ZScore(999))
			}
		}
	}
	// After exactly 9 samples, count must be 9.
	if w.Count() != 9 {
		t.Errorf("count = %d, want 9", w.Count())
	}
}

// TestGUC_Anomaly_DetectTerminatesWithEmptySlices checks that DetectAnomaly
// returns deterministically when PortHistory and ConnectionTimes are empty.
func TestGUC_Anomaly_DetectTerminatesWithEmptySlices(t *testing.T) {
	ad := NewAnomalyDetector()
	b := &PeerBehavior{
		Uploaded:        0,
		Downloaded:      0,
		UploadSpeed:     0,
		TorrentSize:     1_000_000_000,
		AnnounceCount:   0,
		PortHistory:     []uint16{},
		ConnectionTimes: []time.Time{},
		FirstSeen:       time.Now(),
	}
	// Must not panic or hang.
	flagged, _ := ad.DetectAnomaly(b)
	_ = flagged // result not the focus here
}

// TestGUC_Anomaly_DetectTerminatesWithSingleConnectionTime ensures the two-
// element connection-time check does not panic with exactly one entry.
func TestGUC_Anomaly_DetectTerminatesWithSingleConnectionTime(t *testing.T) {
	ad := NewAnomalyDetector()
	b := gucCleanBehavior()
	b.ConnectionTimes = []time.Time{time.Now()}
	flagged, _ := ad.DetectAnomaly(b)
	_ = flagged
}

// TestGUC_Welford_NaNInputHandledGracefully verifies that feeding NaN does not
// panic and that Count is still incremented.
func TestGUC_Welford_NaNInputHandledGracefully(t *testing.T) {
	var w WelfordAccum
	w.Update(1.0)
	w.Update(2.0)
	prevCount := w.Count()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Update(NaN) panicked: %v", r)
		}
	}()
	w.Update(math.NaN())
	if w.Count() != prevCount+1 {
		t.Errorf("count after NaN update: got %d, want %d", w.Count(), prevCount+1)
	}
}

// TestGUC_Welford_InfInputHandledGracefully verifies that feeding +Inf does
// not panic.
func TestGUC_Welford_InfInputHandledGracefully(t *testing.T) {
	var w WelfordAccum
	w.Update(1.0)
	w.Update(2.0)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Update(+Inf) panicked: %v", r)
		}
	}()
	w.Update(math.Inf(1))
}

// ─── Church — functional purity and side-effect isolation ────────────────────

// TestGUC_Anomaly_DetectDoesNotMutateBehavior verifies that DetectAnomaly is a
// read-only operation on the PeerBehavior it receives.
func TestGUC_Anomaly_DetectDoesNotMutateBehavior(t *testing.T) {
	ad := NewAnomalyDetector()
	b := gucCleanBehavior()

	// Snapshot fields.
	origUploaded := b.Uploaded
	origDownloaded := b.Downloaded
	origPortLen := len(b.PortHistory)
	origConnLen := len(b.ConnectionTimes)

	ad.DetectAnomaly(b)

	if b.Uploaded != origUploaded {
		t.Errorf("Uploaded mutated: %d → %d", origUploaded, b.Uploaded)
	}
	if b.Downloaded != origDownloaded {
		t.Errorf("Downloaded mutated: %d → %d", origDownloaded, b.Downloaded)
	}
	if len(b.PortHistory) != origPortLen {
		t.Errorf("PortHistory length mutated: %d → %d", origPortLen, len(b.PortHistory))
	}
	if len(b.ConnectionTimes) != origConnLen {
		t.Errorf("ConnectionTimes length mutated: %d → %d", origConnLen, len(b.ConnectionTimes))
	}
}

// TestGUC_Welford_ResetClearsState verifies that Reset returns the accumulator
// to exactly its zero-value state.
func TestGUC_Welford_ResetClearsState(t *testing.T) {
	var w WelfordAccum
	for i := 0; i < 100; i++ {
		w.Update(float64(i))
	}
	w.Reset()
	if w.Count() != 0 {
		t.Errorf("count after Reset: got %d, want 0", w.Count())
	}
	if w.Mean() != 0 {
		t.Errorf("mean after Reset: got %f, want 0", w.Mean())
	}
	if w.Variance() != 0 {
		t.Errorf("variance after Reset: got %f, want 0", w.Variance())
	}
	if w.StdDev() != 0 {
		t.Errorf("stddev after Reset: got %f, want 0", w.StdDev())
	}
}

// TestGUC_Anomaly_ThresholdConfigurable verifies that SetThresholds changes
// detection behaviour and Thresholds reflects the new value.
func TestGUC_Anomaly_ThresholdConfigurable(t *testing.T) {
	ad := NewAnomalyDetector()

	// Lower the announce-rate threshold to 5/hour.
	ad.SetThresholds(ThresholdConfig{MaxAnnounceRate: 5})
	got := ad.Thresholds()
	if got.MaxAnnounceRate != 5 {
		t.Fatalf("MaxAnnounceRate: got %d, want 5", got.MaxAnnounceRate)
	}

	// A peer with 10 announces in 1 hour should now be flagged.
	b := gucCleanBehavior()
	b.AnnounceCount = 10
	b.FirstSeen = time.Now().Add(-1 * time.Hour)
	flagged, reason := ad.DetectAnomaly(b)
	if !flagged || reason != "ddos_pattern" {
		t.Errorf("expected ddos_pattern with lowered threshold, got flagged=%v reason=%q", flagged, reason)
	}
}

// TestGUC_Anomaly_IndependentDetectorInstances verifies that two AnomalyDetector
// instances share no mutable state — reconfiguring one must not affect the other.
func TestGUC_Anomaly_IndependentDetectorInstances(t *testing.T) {
	ad1 := NewAnomalyDetector()
	ad2 := NewAnomalyDetector()

	ad1.SetThresholds(ThresholdConfig{MaxAnnounceRate: 1})

	// Behaviour that barely triggers ad1 (2 announces in 30 min).
	// duration < 1h so max(duration,1.0) == 1.0 → rate = int(2/1) = 2 > threshold 1.
	b := gucCleanBehavior()
	b.AnnounceCount = 2
	b.FirstSeen = time.Now().Add(-30 * time.Minute)

	flagged1, _ := ad1.DetectAnomaly(b)
	flagged2, _ := ad2.DetectAnomaly(b)

	if !flagged1 {
		t.Error("ad1 with threshold=1 should flag 2 announces/hr")
	}
	if flagged2 {
		t.Error("ad2 with default threshold=100 must not flag 2 announces/hr")
	}
}

// ─── Gödel — formal consistency and invariant preservation ───────────────────

// TestGUC_Welford_VarianceAlwaysNonNegative is a property test asserting that
// variance is always >= 0 across a variety of inputs (Gödel invariant check).
func TestGUC_Welford_VarianceAlwaysNonNegative(t *testing.T) {
	suites := [][]float64{
		{1, 2, 3, 4, 5},
		{0, 0, 0},
		{1e15, -1e15, 1e15, -1e15},
		{0.001, 0.002, 0.003},
		{-100, 0, 100},
	}
	for _, samples := range suites {
		var w WelfordAccum
		for _, s := range samples {
			w.Update(s)
		}
		if v := w.Variance(); v < 0 {
			t.Errorf("variance negative (%f) for samples %v", v, samples)
		}
	}
}

// TestGUC_Anomaly_FalsePositiveLow_SyntheticNormal verifies that the anomaly
// detector flags fewer than 5% of synthetic normal-traffic peers.
func TestGUC_Anomaly_FalsePositiveLow_SyntheticNormal(t *testing.T) {
	ad := NewAnomalyDetector()
	const total = 200
	falsePositives := 0

	now := time.Now()
	for i := 0; i < total; i++ {
		b := &PeerBehavior{
			Uploaded:        int64(500_000_000 + i*1000),
			Downloaded:      int64(400_000_000 + i*900),
			UploadSpeed:     50_000 + float64(i)*10,
			TorrentSize:     2_000_000_000,
			AnnounceCount:   3,
			PortHistory:     []uint16{6881},
			ConnectionTimes: []time.Time{now.Add(-10 * time.Minute), now.Add(-5 * time.Minute)},
			FirstSeen:       now.Add(-2 * time.Hour),
		}
		flagged, _ := ad.DetectAnomaly(b)
		if flagged {
			falsePositives++
		}
	}

	fpRate := float64(falsePositives) / float64(total)
	if fpRate >= 0.05 {
		t.Errorf("false-positive rate %.2f%% >= 5%% (%d/%d)", fpRate*100, falsePositives, total)
	}
}

// TestGUC_Anomaly_ConcurrentDetectSafe verifies that concurrent goroutines
// calling DetectAnomaly on a shared AnomalyDetector do not race.
// Run with: go test -race ./ml/...
func TestGUC_Anomaly_ConcurrentDetectSafe(t *testing.T) {
	ad := NewAnomalyDetector()
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(seed int) {
			defer wg.Done()
			b := gucCleanBehavior()
			b.UploadSpeed = float64(seed) * 1000
			_, _ = ad.DetectAnomaly(b)
		}(i)
	}
	wg.Wait()
}

// TestGUC_Anomaly_AnomalyRejections_NotImplemented is a placeholder for a
// counter that tracks how many peers have been rejected due to anomaly
// detection.  It is not yet implemented in the source.
func TestGUC_Anomaly_AnomalyRejections_NotImplemented(t *testing.T) {
	t.Skip("not yet implemented: AnomalyRejections counter on AnomalyDetector")
}

// TestGUC_Anomaly_SwarmWideBaseline_NotImplemented is a placeholder for a
// swarm-wide statistical baseline that would allow the detector to normalise
// per-peer behaviour against the population.  It is not yet implemented in the
// source.
func TestGUC_Anomaly_SwarmWideBaseline_NotImplemented(t *testing.T) {
	t.Skip("not yet implemented: swarm-wide baseline in AnomalyDetector")
}

// TestGUC_Anomaly_PerPeerBaseline_NotImplemented is a placeholder for optional
// per-peer Welford accumulators that would give individualised baselines.
// Not yet implemented.
func TestGUC_Anomaly_PerPeerBaseline_NotImplemented(t *testing.T) {
	t.Skip("not yet implemented: per-peer baseline option on AnomalyDetector")
}
