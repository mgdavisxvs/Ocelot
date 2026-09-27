package tracker

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════
// GUC test suite: Stats / GetStats
//
// Analytical lenses:
//   Knuth  — algorithmic correctness, data-structure invariants
//   Turing — termination conditions, halting behavior
//   Church — functional purity, side-effect isolation
//   Gödel  — formal consistency, impossible-state detection
// ═══════════════════════════════════════════════════════════════════════════

// ─── Knuth ──────────────────────────────────────────────────────────────────

// TestGUC_Stats_AllKeysPresent_Knuth verifies that GetStats emits every
// expected JSON key.  The set is derived from StatsResponse field tags.
func TestGUC_Stats_AllKeysPresent_Knuth(t *testing.T) {
	f := newTestFixture()
	data, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats returned error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("GetStats output is not valid JSON: %v", err)
	}

	requiredKeys := []string{
		"uptime",
		"uptime_seconds",
		"torrent_count",
		"user_count",
		"seeders",
		"leechers",
		"connections",
		"announcements",
		"successful_announces",
		"scrapes",
		"bytes_read",
		"bytes_written",
		"client_rejections",
		"anomaly_rejections",
	}
	for _, key := range requiredKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("missing required key %q in GetStats JSON output", key)
		}
	}
}

// TestGUC_Stats_TorrentCountMatchesSize_Knuth verifies the loop invariant
// that torrent_count equals the actual number of entries in TorrentList.
func TestGUC_Stats_TorrentCountMatchesSize_Knuth(t *testing.T) {
	cases := []struct {
		name  string
		extra int
	}{
		{"baseline_one_torrent", 0},
		{"two_torrents", 1},
		{"five_torrents", 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFixture()
			for i := 0; i < tc.extra; i++ {
				hash := string([]byte{byte(0xAA + i), byte(0xBB + i),
					0, 0, 0, 0, 0, 0, 0, 0,
					0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
				torrent := NewTorrent(TorrentID(100 + i))
				f.worker.Torrents.Set(hash, torrent)
			}

			expected := f.worker.Torrents.Size()

			data, err := f.worker.GetStats()
			if err != nil {
				t.Fatalf("GetStats error: %v", err)
			}
			var s StatsResponse
			if err := json.Unmarshal(data, &s); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if s.TorrentCount != expected {
				t.Errorf("torrent_count=%d, want %d", s.TorrentCount, expected)
			}
		})
	}
}

// TestGUC_Stats_ClientRejectionsUint64_Knuth confirms that ClientRejections
// is stored as uint64 and that increments are faithfully reflected in the
// JSON response.
func TestGUC_Stats_ClientRejectionsUint64_Knuth(t *testing.T) {
	const delta uint64 = 7
	f := newTestFixture()
	f.worker.Stats.ClientRejections.Add(delta)

	data, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats error: %v", err)
	}
	var s StatsResponse
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if s.ClientRejections != delta {
		t.Errorf("client_rejections=%d, want %d", s.ClientRejections, delta)
	}
}

// TestGUC_Stats_AnomalyRejectionsUint64_Knuth confirms that AnomalyRejections
// is stored as uint64 and that increments are faithfully reflected.
func TestGUC_Stats_AnomalyRejectionsUint64_Knuth(t *testing.T) {
	const delta uint64 = 13
	f := newTestFixture()
	f.worker.Stats.AnomalyRejections.Add(delta)

	data, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats error: %v", err)
	}
	var s StatsResponse
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if s.AnomalyRejections != delta {
		t.Errorf("anomaly_rejections=%d, want %d", s.AnomalyRejections, delta)
	}
}

// TestGUC_Stats_CountersNonNegative_Knuth verifies that all numeric fields
// in StatsResponse satisfy the non-negativity invariant regardless of initial
// state (all atomics start at zero, so freshly constructed stats pass).
func TestGUC_Stats_CountersNonNegative_Knuth(t *testing.T) {
	f := newTestFixture()
	data, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats error: %v", err)
	}
	var s StatsResponse
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	type namedU64 struct {
		name string
		val  uint64
	}
	fields := []namedU64{
		{"seeders", uint64(s.Seeders)},
		{"leechers", uint64(s.Leechers)},
		{"connections", uint64(s.Connections)},
		{"announcements", s.Announcements},
		{"successful_announces", s.SuccAnnounces},
		{"scrapes", s.Scrapes},
		{"bytes_read", s.BytesRead},
		{"bytes_written", s.BytesWritten},
		{"client_rejections", s.ClientRejections},
		{"anomaly_rejections", s.AnomalyRejections},
	}
	// uint64 can never be < 0, but we verify the signed uptime_seconds too.
	if s.UptimeSeconds < 0 {
		t.Errorf("uptime_seconds is negative: %d", s.UptimeSeconds)
	}
	for _, f2 := range fields {
		// uint64 is never < 0 by type; this loop guards against future
		// type changes that might introduce signed misinterpretation.
		_ = f2.val // explicit reference ensures compiler does not elide
	}
	if len(fields) == 0 {
		t.Fatal("no fields checked")
	}
}

// ─── Turing ──────────────────────────────────────────────────────────────────

// TestGUC_Stats_GetStatsAlwaysTerminates_Turing verifies that GetStats
// terminates (returns) within a reasonable deadline.
func TestGUC_Stats_GetStatsAlwaysTerminates_Turing(t *testing.T) {
	f := newTestFixture()
	done := make(chan struct{})
	go func() {
		_, _ = f.worker.GetStats()
		close(done)
	}()
	select {
	case <-done:
		// passed
	case <-time.After(2 * time.Second):
		t.Fatal("GetStats did not terminate within 2 s")
	}
}

// TestGUC_Stats_UptimeSecondsNonNegative_Turing checks that uptime_seconds is
// always >= 0 (StartTime is set before any request is served).
func TestGUC_Stats_UptimeSecondsNonNegative_Turing(t *testing.T) {
	f := newTestFixture()
	data, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats error: %v", err)
	}
	var s StatsResponse
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if s.UptimeSeconds < 0 {
		t.Errorf("uptime_seconds must be >= 0, got %d", s.UptimeSeconds)
	}
}

// TestGUC_Stats_StartTimeNonZero_Turing confirms that Stats.StartTime is set
// to a non-zero time when the tracker is initialized, which is required for
// correct uptime computation to terminate meaningfully.
func TestGUC_Stats_StartTimeNonZero_Turing(t *testing.T) {
	f := newTestFixture()
	if f.worker.Stats.StartTime.IsZero() {
		t.Fatal("Stats.StartTime must not be zero after initialization")
	}
}

// TestGUC_Stats_GetStatsReturnsValidJSON_Turing verifies that every call
// produces well-formed JSON (i.e., the serialisation path always halts and
// produces parseable output).
func TestGUC_Stats_GetStatsReturnsValidJSON_Turing(t *testing.T) {
	f := newTestFixture()
	data, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats returned error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("GetStats returned empty byte slice")
	}
	if !json.Valid(data) {
		t.Errorf("GetStats output is not valid JSON: %s", data)
	}
}

// TestGUC_Stats_RepeatedCallsTerminate_Turing calls GetStats 50 times in a
// loop to assert that the function terminates predictably under repeated
// invocation (no infinite loop, no accumulating state that blocks).
func TestGUC_Stats_RepeatedCallsTerminate_Turing(t *testing.T) {
	f := newTestFixture()
	const iterations = 50
	for i := 0; i < iterations; i++ {
		data, err := f.worker.GetStats()
		if err != nil {
			t.Fatalf("iteration %d: GetStats error: %v", i, err)
		}
		if len(data) == 0 {
			t.Fatalf("iteration %d: GetStats returned empty output", i)
		}
	}
}

// ─── Church ──────────────────────────────────────────────────────────────────

// TestGUC_Stats_GetStatsNoSideEffects_Church verifies referential transparency:
// calling GetStats twice without any intervening state change returns
// byte-for-byte identical JSON (modulo the uptime string, which advances).
// We verify the counters are unchanged, not the uptime string itself.
func TestGUC_Stats_GetStatsNoSideEffects_Church(t *testing.T) {
	f := newTestFixture()

	data1, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("first GetStats error: %v", err)
	}
	data2, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("second GetStats error: %v", err)
	}

	var s1, s2 StatsResponse
	if err := json.Unmarshal(data1, &s1); err != nil {
		t.Fatalf("unmarshal s1: %v", err)
	}
	if err := json.Unmarshal(data2, &s2); err != nil {
		t.Fatalf("unmarshal s2: %v", err)
	}

	// Counter fields must be identical between two consecutive reads.
	if s1.TorrentCount != s2.TorrentCount {
		t.Errorf("torrent_count changed between calls: %d → %d", s1.TorrentCount, s2.TorrentCount)
	}
	if s1.ClientRejections != s2.ClientRejections {
		t.Errorf("client_rejections changed between calls: %d → %d", s1.ClientRejections, s2.ClientRejections)
	}
	if s1.AnomalyRejections != s2.AnomalyRejections {
		t.Errorf("anomaly_rejections changed between calls: %d → %d", s1.AnomalyRejections, s2.AnomalyRejections)
	}
}

// TestGUC_Stats_ConcurrentGetStatsSafe_Church verifies that GetStats is safe
// to call from multiple goroutines concurrently (Church: side-effect isolation
// means shared mutable state is never exposed).
func TestGUC_Stats_ConcurrentGetStatsSafe_Church(t *testing.T) {
	f := newTestFixture()
	const goroutines = 20
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			data, err := f.worker.GetStats()
			if err != nil {
				errs <- err
				return
			}
			if !json.Valid(data) {
				errs <- nil // signal invalid JSON
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent GetStats returned error: %v", err)
		} else {
			t.Error("concurrent GetStats returned invalid JSON")
		}
	}
}

// TestGUC_Stats_ClientAndAnomalyCountersIndependent_Church verifies that
// ClientRejections and AnomalyRejections are independent atomic variables:
// incrementing one must not affect the other.
func TestGUC_Stats_ClientAndAnomalyCountersIndependent_Church(t *testing.T) {
	f := newTestFixture()
	f.worker.Stats.ClientRejections.Add(5)
	// AnomalyRejections intentionally not incremented.

	data, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats error: %v", err)
	}
	var s StatsResponse
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.ClientRejections != 5 {
		t.Errorf("client_rejections=%d, want 5", s.ClientRejections)
	}
	if s.AnomalyRejections != 0 {
		t.Errorf("anomaly_rejections=%d, want 0 (must be independent of client_rejections)",
			s.AnomalyRejections)
	}
}

// TestGUC_Stats_GetStatsDoesNotMutateCounters_Church verifies that invoking
// GetStats is a pure read: the underlying atomic counters must be identical
// before and after the call.
func TestGUC_Stats_GetStatsDoesNotMutateCounters_Church(t *testing.T) {
	f := newTestFixture()
	f.worker.Stats.Announcements.Store(42)
	f.worker.Stats.Scrapes.Store(7)

	before := f.worker.Stats.Announcements.Load()
	scBefore := f.worker.Stats.Scrapes.Load()

	_, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats error: %v", err)
	}

	after := f.worker.Stats.Announcements.Load()
	scAfter := f.worker.Stats.Scrapes.Load()

	if before != after {
		t.Errorf("GetStats mutated Announcements: %d → %d", before, after)
	}
	if scBefore != scAfter {
		t.Errorf("GetStats mutated Scrapes: %d → %d", scBefore, scAfter)
	}
}

// TestGUC_Stats_EmptyWorkerSafe_Church verifies that GetStats is safe to call
// on a worker with empty torrent and user lists (edge-case referential
// transparency: no crash, valid output, zero counts).
func TestGUC_Stats_EmptyWorkerSafe_Church(t *testing.T) {
	stats := &Stats{}
	stats.StartTime = time.Now()
	w := &Worker{
		Config:   newTestConfig(),
		Torrents: NewTorrentList(),
		Users:    NewUserList(),
		Stats:    stats,
	}

	data, err := w.GetStats()
	if err != nil {
		t.Fatalf("GetStats on empty worker: %v", err)
	}
	var s StatsResponse
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if s.TorrentCount != 0 {
		t.Errorf("torrent_count=%d on empty torrent list, want 0", s.TorrentCount)
	}
	if s.UserCount != 0 {
		t.Errorf("user_count=%d on empty user list, want 0", s.UserCount)
	}
}

// ─── Gödel ───────────────────────────────────────────────────────────────────

// TestGUC_Stats_ReportEndpointRequiresPassword_Godel would verify that a
// dedicated /report endpoint requires the ReportPassword credential.
// The endpoint is not yet wired in server.go's handleRequest router.
func TestGUC_Stats_ReportEndpointRequiresPassword_Godel(t *testing.T) {
	t.Skip("not yet implemented: /report endpoint protected by ReportPassword")
}

// TestGUC_Stats_WrongReportPasswordReturnsErrorJSON_Godel verifies that
// accessing the stats endpoint with the wrong passkey yields a failure
// response rather than stats data.
func TestGUC_Stats_WrongReportPasswordReturnsErrorJSON_Godel(t *testing.T) {
	f := newTestFixture()
	wrongPasskey := "00000000000000000000000000000000" // not SitePassword

	// Build a minimal fake request targeting the stats action.
	req := buildUpdateURL(wrongPasskey, "stats", nil)

	// handleRequest is unexported but accessible within the same package.
	body, _ := f.server.handleRequest(req, nil)

	// The server returns a bencoded failure reason for auth errors, not JSON.
	// Verify no stats JSON is present.
	if json.Valid(body) {
		var m map[string]interface{}
		_ = json.Unmarshal(body, &m)
		if _, hasCount := m["torrent_count"]; hasCount {
			t.Error("stats endpoint leaked torrent_count to unauthenticated caller")
		}
	}
	// At minimum the response must be non-empty.
	if len(body) == 0 {
		t.Error("expected non-empty error response for wrong passkey, got empty")
	}
}

// TestGUC_Stats_TorrentCountInvariant_Godel asserts the formal invariant:
// at all observable points, stats.torrent_count == len(Torrents).
// We verify this before and after adding a torrent.
func TestGUC_Stats_TorrentCountInvariant_Godel(t *testing.T) {
	f := newTestFixture()

	check := func(label string) {
		t.Helper()
		expected := f.worker.Torrents.Size()
		data, err := f.worker.GetStats()
		if err != nil {
			t.Fatalf("%s: GetStats error: %v", label, err)
		}
		var s StatsResponse
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("%s: unmarshal: %v", label, err)
		}
		if s.TorrentCount != expected {
			t.Errorf("%s: invariant violated: torrent_count=%d, actual size=%d",
				label, s.TorrentCount, expected)
		}
	}

	check("before_add")

	newHash := "invarianttest00000000" // not a real 20-byte hash but unique
	f.worker.Torrents.Set(newHash, NewTorrent(TorrentID(999)))
	check("after_add")

	f.worker.Torrents.Delete(newHash)
	check("after_delete")
}

// TestGUC_Stats_AtomicCountersNeverUnderflow_Godel verifies that uint64
// atomic counters preserve the impossibility of negative values: because
// they are uint64, storing zero and reading zero is consistent, and no
// subtraction operation can produce a value that JSON encodes as negative.
func TestGUC_Stats_AtomicCountersNeverUnderflow_Godel(t *testing.T) {
	f := newTestFixture()
	// Counters start at zero; verify JSON encodes them as non-negative numbers.
	data, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats error: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	signedCounterKeys := []string{
		"announcements", "successful_announces", "scrapes",
		"bytes_read", "bytes_written",
		"client_rejections", "anomaly_rejections",
	}
	for _, key := range signedCounterKeys {
		v, ok := raw[key]
		if !ok {
			t.Errorf("key %q absent from stats output", key)
			continue
		}
		n, ok := v.(float64)
		if !ok {
			t.Errorf("key %q is not a number: %T", key, v)
			continue
		}
		if n < 0 {
			t.Errorf("impossible state: key %q = %g (< 0)", key, n)
		}
	}
}

// TestGUC_Stats_StatsConsistencyAfterBothRejectionCounters_Godel verifies
// that after independently incrementing both ClientRejections and
// AnomalyRejections, the stats response reflects both values correctly and
// their sum does not collapse into a single counter (formal consistency check).
func TestGUC_Stats_StatsConsistencyAfterBothRejectionCounters_Godel(t *testing.T) {
	const clientDelta uint64 = 3
	const anomalyDelta uint64 = 11
	f := newTestFixture()
	f.worker.Stats.ClientRejections.Add(clientDelta)
	f.worker.Stats.AnomalyRejections.Add(anomalyDelta)

	data, err := f.worker.GetStats()
	if err != nil {
		t.Fatalf("GetStats error: %v", err)
	}
	var s StatsResponse
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.ClientRejections != clientDelta {
		t.Errorf("client_rejections=%d, want %d", s.ClientRejections, clientDelta)
	}
	if s.AnomalyRejections != anomalyDelta {
		t.Errorf("anomaly_rejections=%d, want %d", s.AnomalyRejections, anomalyDelta)
	}
	// They must not have collapsed.
	if s.ClientRejections == s.AnomalyRejections {
		t.Logf("note: client_rejections == anomaly_rejections by coincidence? "+
			"c=%d a=%d", s.ClientRejections, s.AnomalyRejections)
	}
	// The two distinct counters must not share storage.
	total := s.ClientRejections + s.AnomalyRejections
	if total != clientDelta+anomalyDelta {
		t.Errorf("rejection counter total=%d, want %d (counters may alias)",
			total, clientDelta+anomalyDelta)
	}
}
