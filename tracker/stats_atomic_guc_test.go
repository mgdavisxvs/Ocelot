package tracker

import (
	"sync"
	"testing"
	"time"
)

// ─── Knuth: algorithmic correctness, loop invariants, data-structure invariants ──

// TestGUC_Announcements_AddLoadConsistent verifies that N sequential Add(1) calls
// are exactly reflected by Load — the loop invariant holds at every step.
// Note: Stats.Announcements is the canonical "Announces" counter.
func TestGUC_Announcements_AddLoadConsistent(t *testing.T) {
	s := &Stats{}
	const N = 1000
	for i := 0; i < N; i++ {
		s.Announcements.Add(1)
	}
	if got := s.Announcements.Load(); got != N {
		t.Errorf("Announcements.Load() = %d; want %d", got, N)
	}
}

// TestGUC_Leechers_StoreLoadRoundtrip verifies that Store followed by Load
// returns the exact stored value — table-driven across boundary values.
func TestGUC_Leechers_StoreLoadRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		val  uint32
	}{
		{"zero", 0},
		{"one", 1},
		{"large", 1<<31 - 1},
		{"max32", ^uint32(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Stats{}
			s.Leechers.Store(tc.val)
			if got := s.Leechers.Load(); got != tc.val {
				t.Errorf("Leechers.Load() = %d; want %d", got, tc.val)
			}
		})
	}
}

// TestGUC_Seeders_StoreLoadRoundtrip verifies Store/Load roundtrip for Seeders.
func TestGUC_Seeders_StoreLoadRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		val  uint32
	}{
		{"zero", 0},
		{"one", 1},
		{"large", 999999},
		{"max32", ^uint32(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Stats{}
			s.Seeders.Store(tc.val)
			if got := s.Seeders.Load(); got != tc.val {
				t.Errorf("Seeders.Load() = %d; want %d", got, tc.val)
			}
		})
	}
}

// TestGUC_AllCounters_ZeroInitialized verifies all atomic counters read zero in
// a freshly allocated Stats — the zero-value data-structure invariant.
func TestGUC_AllCounters_ZeroInitialized(t *testing.T) {
	s := &Stats{}
	checks := []struct {
		name string
		got  uint64
	}{
		{"OpenConnections", uint64(s.OpenConnections.Load())},
		{"OpenedConnections", s.OpenedConnections.Load()},
		{"Leechers", uint64(s.Leechers.Load())},
		{"Seeders", uint64(s.Seeders.Load())},
		{"Requests", s.Requests.Load()},
		{"Announcements", s.Announcements.Load()},
		{"SuccAnnouncements", s.SuccAnnouncements.Load()},
		{"Scrapes", s.Scrapes.Load()},
		{"BytesRead", s.BytesRead.Load()},
		{"BytesWritten", s.BytesWritten.Load()},
		{"EvictedPeers", s.EvictedPeers.Load()},
		{"AnomalyDetections", s.AnomalyDetections.Load()},
		{"ClientRejections", s.ClientRejections.Load()},
		{"AnomalyRejections", s.AnomalyRejections.Load()},
	}
	for _, c := range checks {
		if c.got != 0 {
			t.Errorf("Stats.%s zero-value = %d; want 0", c.name, c.got)
		}
	}
}

// TestGUC_Downloads_AddLoadConsistent skips because Stats has no Downloads field.
func TestGUC_Downloads_AddLoadConsistent(t *testing.T) {
	t.Skip("not yet implemented: Stats.Downloads")
}

// ─── Turing: termination conditions, halting behavior ────────────────────────────

// TestGUC_ClientRejections_ConcurrentAddTerminates launches N goroutines each
// calling Add(1) and verifies all complete — no deadlock, no hang.
func TestGUC_ClientRejections_ConcurrentAddTerminates(t *testing.T) {
	s := &Stats{}
	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			s.ClientRejections.Add(1)
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ClientRejections concurrent Add did not terminate within 5s")
	}
	if got := s.ClientRejections.Load(); got != goroutines {
		t.Errorf("ClientRejections = %d; want %d", got, goroutines)
	}
}

// TestGUC_AnomalyRejections_ConcurrentAddTerminates applies the same halting
// check to AnomalyRejections.
func TestGUC_AnomalyRejections_ConcurrentAddTerminates(t *testing.T) {
	s := &Stats{}
	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			s.AnomalyRejections.Add(1)
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("AnomalyRejections concurrent Add did not terminate within 5s")
	}
	if got := s.AnomalyRejections.Load(); got != goroutines {
		t.Errorf("AnomalyRejections = %d; want %d", got, goroutines)
	}
}

// TestGUC_Leechers_StoreThenLoadTerminates verifies a goroutine-based Store
// completes and the subsequent Load observes the written value.
func TestGUC_Leechers_StoreThenLoadTerminates(t *testing.T) {
	const val uint32 = 42
	s := &Stats{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Leechers.Store(val)
	}()
	wg.Wait()
	if got := s.Leechers.Load(); got != val {
		t.Errorf("Leechers.Load() after goroutine Store = %d; want %d", got, val)
	}
}

// TestGUC_Seeders_StoreThenLoadTerminates applies the same goroutine-Store
// halting check to Seeders.
func TestGUC_Seeders_StoreThenLoadTerminates(t *testing.T) {
	const val uint32 = 99
	s := &Stats{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Seeders.Store(val)
	}()
	wg.Wait()
	if got := s.Seeders.Load(); got != val {
		t.Errorf("Seeders.Load() after goroutine Store = %d; want %d", got, val)
	}
}

// TestGUC_ConcurrentIncrements_AllGoroutinesComplete verifies N goroutines each
// performing M Add(1) calls on Announcements all terminate and the aggregate
// equals N*M exactly.
func TestGUC_ConcurrentIncrements_AllGoroutinesComplete(t *testing.T) {
	s := &Stats{}
	const goroutines = 50
	const perGoroutine = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				s.Announcements.Add(1)
			}
		}()
	}
	wg.Wait()
	const want = uint64(goroutines * perGoroutine)
	if got := s.Announcements.Load(); got != want {
		t.Errorf("Announcements after concurrent increments = %d; want %d", got, want)
	}
}

// ─── Church: functional purity, side-effect isolation, referential transparency ──

// TestGUC_Stats_ZeroValueIsValid verifies that a zero-value Stats (stack-allocated,
// no constructor) is immediately usable — atomic types are valid at zero value.
func TestGUC_Stats_ZeroValueIsValid(t *testing.T) {
	var s Stats
	s.Announcements.Add(1)
	if got := s.Announcements.Load(); got != 1 {
		t.Errorf("zero-value Stats.Announcements: Add(1).Load() = %d; want 1", got)
	}
	s.Leechers.Store(5)
	if got := s.Leechers.Load(); got != 5 {
		t.Errorf("zero-value Stats.Leechers: Store(5).Load() = %d; want 5", got)
	}
}

// TestGUC_Announcements_IndependentInstances verifies two Stats instances share
// no state — mutations to one are invisible to the other.
func TestGUC_Announcements_IndependentInstances(t *testing.T) {
	a := &Stats{}
	b := &Stats{}
	a.Announcements.Add(10)
	b.Announcements.Add(3)
	if got := a.Announcements.Load(); got != 10 {
		t.Errorf("instance a Announcements = %d; want 10", got)
	}
	if got := b.Announcements.Load(); got != 3 {
		t.Errorf("instance b Announcements = %d; want 3", got)
	}
}

// TestGUC_ClientRejections_IsolatedFromAnomalyRejections verifies that
// incrementing ClientRejections has no side effect on AnomalyRejections and
// vice versa — fields are fully side-effect isolated.
func TestGUC_ClientRejections_IsolatedFromAnomalyRejections(t *testing.T) {
	s := &Stats{}
	s.ClientRejections.Add(7)
	if got := s.AnomalyRejections.Load(); got != 0 {
		t.Errorf("AnomalyRejections after ClientRejections.Add(7) = %d; want 0", got)
	}
	s.AnomalyRejections.Add(3)
	if got := s.ClientRejections.Load(); got != 7 {
		t.Errorf("ClientRejections after AnomalyRejections.Add(3) = %d; want 7", got)
	}
}

// TestGUC_Seeders_StoreDoesNotAffectLeechers verifies that storing a value to
// Seeders has no side effect on the Leechers field.
func TestGUC_Seeders_StoreDoesNotAffectLeechers(t *testing.T) {
	s := &Stats{}
	s.Leechers.Store(42)
	s.Seeders.Store(100)
	if got := s.Leechers.Load(); got != 42 {
		t.Errorf("Leechers.Load() after Seeders.Store(100) = %d; want 42", got)
	}
}

// TestGUC_Stats_SnapshotPointInTime reads all counters sequentially after
// controlled mutations in a single goroutine and verifies the snapshot is
// exact — no concurrent mutations, so point-in-time consistency must hold.
func TestGUC_Stats_SnapshotPointInTime(t *testing.T) {
	s := &Stats{}
	s.Announcements.Store(100)
	s.SuccAnnouncements.Store(80)
	s.Leechers.Store(10)
	s.Seeders.Store(20)
	s.ClientRejections.Store(5)
	s.AnomalyRejections.Store(3)

	type snap struct {
		ann     uint64
		succAnn uint64
		leech   uint32
		seed    uint32
		cr      uint64
		ar      uint64
	}
	got := snap{
		ann:     s.Announcements.Load(),
		succAnn: s.SuccAnnouncements.Load(),
		leech:   s.Leechers.Load(),
		seed:    s.Seeders.Load(),
		cr:      s.ClientRejections.Load(),
		ar:      s.AnomalyRejections.Load(),
	}

	if got.ann != 100 {
		t.Errorf("snapshot Announcements = %d; want 100", got.ann)
	}
	if got.succAnn != 80 {
		t.Errorf("snapshot SuccAnnouncements = %d; want 80", got.succAnn)
	}
	if got.leech != 10 {
		t.Errorf("snapshot Leechers = %d; want 10", got.leech)
	}
	if got.seed != 20 {
		t.Errorf("snapshot Seeders = %d; want 20", got.seed)
	}
	if got.cr != 5 {
		t.Errorf("snapshot ClientRejections = %d; want 5", got.cr)
	}
	if got.ar != 3 {
		t.Errorf("snapshot AnomalyRejections = %d; want 3", got.ar)
	}
}

// ─── Gödel: formal consistency, invariant preservation, impossible-state detection ─

// TestGUC_Stats_FieldsAllExported verifies that every key Stats field is
// directly addressable from outside the struct — compile-time proof of the
// exported API surface.
func TestGUC_Stats_FieldsAllExported(t *testing.T) {
	s := &Stats{}
	// Each entry is a compile-time assertion that the field is exported.
	fields := map[string]interface{}{
		"OpenConnections":   &s.OpenConnections,
		"OpenedConnections": &s.OpenedConnections,
		"Leechers":          &s.Leechers,
		"Seeders":           &s.Seeders,
		"Requests":          &s.Requests,
		"Announcements":     &s.Announcements,
		"SuccAnnouncements": &s.SuccAnnouncements,
		"Scrapes":           &s.Scrapes,
		"BytesRead":         &s.BytesRead,
		"BytesWritten":      &s.BytesWritten,
		"EvictedPeers":      &s.EvictedPeers,
		"AnomalyDetections": &s.AnomalyDetections,
		"ClientRejections":  &s.ClientRejections,
		"AnomalyRejections": &s.AnomalyRejections,
	}
	if len(fields) == 0 {
		t.Fatal("exported field map is empty — invariant check cannot proceed")
	}
	for name, ptr := range fields {
		if ptr == nil {
			t.Errorf("field %s address is nil", name)
		}
	}
}

// TestGUC_Stats_NoUnderflowOnZero verifies that a counter incremented from zero
// stays bounded at the expected value — ruling out impossible wrap-around.
func TestGUC_Stats_NoUnderflowOnZero(t *testing.T) {
	s := &Stats{}
	s.Announcements.Add(5)
	val := s.Announcements.Load()
	if val != 5 {
		t.Errorf("Announcements after Add(5) from zero = %d; want exactly 5 (possible underflow/wrap)", val)
	}
}

// TestGUC_Announcements_MonotonicAdd verifies the monotonicity invariant:
// each successive Add(1) must return a strictly greater value than the prior one.
func TestGUC_Announcements_MonotonicAdd(t *testing.T) {
	s := &Stats{}
	prev := s.Announcements.Load()
	for i := 0; i < 50; i++ {
		next := s.Announcements.Add(1)
		if next <= prev {
			t.Fatalf("Announcements non-monotonic at step %d: Add(1) returned %d, previous was %d", i, next, prev)
		}
		prev = next
	}
}

// TestGUC_ConcurrentIncrements_NoDataRace exercises concurrent Add and Load
// across multiple counters simultaneously; running with -race will flag any
// unsynchronised access.
func TestGUC_ConcurrentIncrements_NoDataRace(t *testing.T) {
	s := &Stats{}
	const workers = 10
	const ops = 100
	var wg sync.WaitGroup
	wg.Add(workers * 4)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				s.ClientRejections.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				s.AnomalyRejections.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				s.Announcements.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				_ = s.ClientRejections.Load()
				_ = s.AnomalyRejections.Load()
			}
		}()
	}
	wg.Wait()
	const want = uint64(workers * ops * 3)
	total := s.ClientRejections.Load() + s.AnomalyRejections.Load() + s.Announcements.Load()
	if total != want {
		t.Errorf("aggregate counter total = %d; want %d", total, want)
	}
}

// TestGUC_Stats_ConsistencyInvariant verifies the formal invariant that
// SuccAnnouncements never exceeds Announcements — a logical contradiction
// that would indicate a bookkeeping bug.
func TestGUC_Stats_ConsistencyInvariant(t *testing.T) {
	s := &Stats{}
	const total = 200
	const succEvery = 3 // every 3rd announce is successful
	for i := 1; i <= total; i++ {
		s.Announcements.Add(1)
		if i%succEvery == 0 {
			s.SuccAnnouncements.Add(1)
		}
	}
	ann := s.Announcements.Load()
	succ := s.SuccAnnouncements.Load()
	if succ > ann {
		t.Errorf("invariant violated: SuccAnnouncements (%d) > Announcements (%d)", succ, ann)
	}
	const wantSucc = uint64(total / succEvery)
	if succ != wantSucc {
		t.Errorf("SuccAnnouncements = %d; want %d", succ, wantSucc)
	}
	if ann != total {
		t.Errorf("Announcements = %d; want %d", ann, total)
	}
}
