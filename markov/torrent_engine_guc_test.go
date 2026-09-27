package markov

// torrent_engine_guc_test.go — GUC coverage for the torrent state engine.
//
// Tests exercise the chain package's torrent-health machinery through the four
// GUC analytical lenses:
//
//	Knuth  — algorithmic correctness, loop invariants, data-structure invariants
//	Turing — termination conditions, halting behaviour, decidability
//	Church — functional purity, side-effect isolation, referential transparency
//	Gödel  — formal consistency, invariant preservation, impossible-state detection

import (
	"encoding/json"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgdavisxvs/ocelot/markov/internal/chain"
)

// ---------------------------------------------------------------------------
// Knuth lens: algorithmic correctness, loop invariants, data-structure invariants
// ---------------------------------------------------------------------------

// TestGUC_Knuth_TorrentStatesDefined verifies that all five torrent health
// state constants are defined in [0, NumTorrentStates) and that no two share
// the same integer value — the data-structure invariant of a disjoint state space.
func TestGUC_Knuth_TorrentStatesDefined(t *testing.T) {
	states := []struct {
		name  string
		value int
	}{
		{"TorrentThriving", chain.TorrentThriving},
		{"TorrentHealthy", chain.TorrentHealthy},
		{"TorrentAtRisk", chain.TorrentAtRisk},
		{"TorrentDying", chain.TorrentDying},
		{"TorrentUnavailable", chain.TorrentUnavailable},
	}

	if chain.NumTorrentStates != 5 {
		t.Errorf("NumTorrentStates=%d, want 5", chain.NumTorrentStates)
	}

	seen := make(map[int]string)
	for _, s := range states {
		if s.value < 0 || s.value >= chain.NumTorrentStates {
			t.Errorf("%s=%d out of range [0, %d)", s.name, s.value, chain.NumTorrentStates)
		}
		if prev, ok := seen[s.value]; ok {
			t.Errorf("duplicate state value %d: %s and %s", s.value, prev, s.name)
		}
		seen[s.value] = s.name
	}
}

// TestGUC_Knuth_TorrentHealthStateClassification verifies the algorithmic
// correctness of TorrentHealthState against the spec in states.go.
func TestGUC_Knuth_TorrentHealthStateClassification(t *testing.T) {
	tests := []struct {
		seeders  int64
		leechers int64
		want     int
		desc     string
	}{
		{0, 0, chain.TorrentUnavailable, "no peers"},
		{0, 5, chain.TorrentDying, "no seeders, leechers present"},
		{1, 0, chain.TorrentAtRisk, "1 seeder, no leechers"},
		{2, 10, chain.TorrentAtRisk, "2 seeders"},
		{3, 0, chain.TorrentHealthy, "3 seeders, no leechers"},
		{5, 5, chain.TorrentHealthy, "5 seeders, 5 leechers, ratio=1.0 (>=0.5)"},
		{10, 0, chain.TorrentThriving, "10 seeders, no leechers"},
		{10, 4, chain.TorrentThriving, "10 seeders, 4 leechers, ratio=2.5 (>=2.0)"},
		{10, 5, chain.TorrentThriving, "10 seeders, 5 leechers, ratio=2.0 exactly (boundary: S/L>=2.0 → Thriving)"},
		{3, 10, chain.TorrentHealthy, "3 seeders, 10 leechers, ratio=0.3 (<0.5 but seeders>=3)"},
	}

	for _, tc := range tests {
		got := chain.TorrentHealthState(tc.seeders, tc.leechers)
		if got != tc.want {
			t.Errorf("[%s] TorrentHealthState(%d, %d)=%d, want %d",
				tc.desc, tc.seeders, tc.leechers, got, tc.want)
		}
	}
}

// TestGUC_Knuth_StateProbabilitiesSum1 verifies the loop invariant that after
// any number of Step calls the resulting distribution sums to 1.0.
func TestGUC_Knuth_StateProbabilitiesSum1(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	// Observe some torrent health transitions.
	for i := 0; i < 50; i++ {
		c.Observe(chain.TorrentHealthy, chain.TorrentAtRisk)
		c.Observe(chain.TorrentAtRisk, chain.TorrentDying)
		c.Observe(chain.TorrentDying, chain.TorrentUnavailable)
	}

	steps := []int{1, 5, 10, 50, 100}
	for _, s := range steps {
		pi := make([]float64, chain.NumTorrentStates)
		pi[chain.TorrentHealthy] = 1.0

		out := c.Step(pi, s)
		var sum float64
		for _, v := range out {
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("Step(pi, %d): sum=%.15f, want 1.0", s, sum)
		}
	}
}

// TestGUC_Knuth_LargeNStepsBounded verifies the algorithmic invariant that
// 10 000 steps on the torrent chain keeps every probability in [0, 1] and the
// row sum equal to 1 — the bounding loop invariant for the power iteration.
func TestGUC_Knuth_LargeNStepsBounded(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.999)
	pi := make([]float64, chain.NumTorrentStates)
	pi[chain.TorrentThriving] = 1.0

	out := c.Step(pi, 10000)

	var sum float64
	for i, v := range out {
		if v < -1e-12 {
			t.Errorf("out[%d]=%v < 0 after 10000 steps (probability underflow)", i, v)
		}
		if v > 1.0+1e-9 {
			t.Errorf("out[%d]=%v > 1 after 10000 steps (probability overflow)", i, v)
		}
		sum += v
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("sum=%.15f after 10000 steps, want 1.0", sum)
	}
}

// TestGUC_Knuth_TransitionMatrixDimensions verifies that P() always returns an
// exactly NumTorrentStates × NumTorrentStates row-stochastic matrix regardless
// of how many observations have been made — the shape loop invariant.
func TestGUC_Knuth_TransitionMatrixDimensions(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	for k := 0; k < 30; k++ {
		c.Observe(k%chain.NumTorrentStates, (k+1)%chain.NumTorrentStates)
		p := c.P()
		if len(p) != chain.NumTorrentStates {
			t.Fatalf("P() returned %d rows after %d obs, want %d", len(p), k+1, chain.NumTorrentStates)
		}
		for i, row := range p {
			if len(row) != chain.NumTorrentStates {
				t.Fatalf("P()[%d] has %d cols after %d obs, want %d", i, len(row), k+1, chain.NumTorrentStates)
			}
			var sum float64
			for _, v := range row {
				sum += v
			}
			if math.Abs(sum-1.0) > 1e-9 {
				t.Errorf("P()[%d] sum=%.15f after %d obs, want 1.0", i, sum, k+1)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Turing lens: termination conditions, halting behaviour, decidability
// ---------------------------------------------------------------------------

// TestGUC_Turing_LargeNStepsTerminates asserts that Step(pi, 10000) on the
// torrent chain completes within 5 seconds — the Turing halting-time bound.
func TestGUC_Turing_LargeNStepsTerminates(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	pi := make([]float64, chain.NumTorrentStates)
	pi[chain.TorrentAtRisk] = 1.0

	done := make(chan struct{}, 1)
	go func() {
		c.Step(pi, 10000)
		close(done)
	}()

	select {
	case <-done:
		// halted correctly
	case <-time.After(5 * time.Second):
		t.Fatal("Step(pi, 10000) did not terminate within 5s (halting violation)")
	}
}

// TestGUC_Turing_StepNonPanicking verifies that Step does not panic for
// various initial distributions including degenerate and edge cases.
func TestGUC_Turing_StepNonPanicking(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Step panicked: %v", r)
		}
	}()

	inputs := [][]float64{
		{1.0, 0.0, 0.0, 0.0, 0.0},           // point mass on TorrentThriving
		{0.0, 0.0, 0.0, 0.0, 1.0},           // point mass on TorrentUnavailable
		{0.2, 0.2, 0.2, 0.2, 0.2},           // uniform
		{0.0, 0.0, 0.0, 0.0, 0.0},           // all-zero (edge case)
		{0.5, 0.3, 0.1, 0.05, 0.05},         // arbitrary distribution
	}
	for _, pi := range inputs {
		for _, k := range []int{0, 1, 10, 100} {
			c.Step(pi, k)
		}
	}
}

// TestGUC_Turing_TorrentUnavailableRecoverable verifies that TorrentUnavailable
// is NOT an absorbing state: the chain can transition out of it (probability of
// leaving TorrentUnavailable is > 0), matching the UMM-03 specification.
func TestGUC_Turing_TorrentUnavailableRecoverable(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	// Observe some revivals from unavailable.
	for i := 0; i < 20; i++ {
		c.Observe(chain.TorrentUnavailable, chain.TorrentDying)
		c.Observe(chain.TorrentUnavailable, chain.TorrentAtRisk)
	}

	p := c.P()
	unavailRow := p[chain.TorrentUnavailable]

	// Sum of transitions leaving TorrentUnavailable must be > 0.
	var leavingProb float64
	for s := 0; s < chain.NumTorrentStates; s++ {
		if s != chain.TorrentUnavailable {
			leavingProb += unavailRow[s]
		}
	}
	if leavingProb <= 0 {
		t.Error("TorrentUnavailable is absorbing (no escape probability > 0): violates UMM-03 recoverable spec")
	}

	// Point-mass on TorrentUnavailable after many steps should spread (not stay 1.0).
	pi := make([]float64, chain.NumTorrentStates)
	pi[chain.TorrentUnavailable] = 1.0
	out := c.Step(pi, 100)
	if out[chain.TorrentUnavailable] > 1.0-1e-9 {
		t.Errorf("after 100 steps from TorrentUnavailable, P(Unavailable)=%.6f (absorbing state behaviour)", out[chain.TorrentUnavailable])
	}
}

// TestGUC_Turing_TorrentHealthStateExtremeInputs verifies that TorrentHealthState
// does not panic or produce out-of-range values for extreme seeder/leecher inputs.
func TestGUC_Turing_TorrentHealthStateExtremeInputs(t *testing.T) {
	extremes := []struct {
		seeders  int64
		leechers int64
	}{
		{0, 0},
		{1 << 30, 0},
		{0, 1 << 30},
		{1 << 30, 1 << 30},
		{math.MaxInt32, math.MaxInt32},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("TorrentHealthState panicked on extreme input: %v", r)
		}
	}()

	for _, tc := range extremes {
		got := chain.TorrentHealthState(tc.seeders, tc.leechers)
		if got < 0 || got >= chain.NumTorrentStates {
			t.Errorf("TorrentHealthState(%d, %d)=%d out of [0, %d)",
				tc.seeders, tc.leechers, got, chain.NumTorrentStates)
		}
	}
}

// TestGUC_Turing_ErgodicAfterObservations verifies that IsErgodic terminates
// and returns true on the torrent chain after realistic health observations.
func TestGUC_Turing_ErgodicAfterObservations(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	// Simulate a realistic sequence of torrent health transitions.
	transitions := [][2]int{
		{chain.TorrentThriving, chain.TorrentHealthy},
		{chain.TorrentHealthy, chain.TorrentAtRisk},
		{chain.TorrentAtRisk, chain.TorrentDying},
		{chain.TorrentDying, chain.TorrentUnavailable},
		{chain.TorrentUnavailable, chain.TorrentDying},
		{chain.TorrentDying, chain.TorrentAtRisk},
		{chain.TorrentAtRisk, chain.TorrentHealthy},
		{chain.TorrentHealthy, chain.TorrentThriving},
	}
	for i := 0; i < 10; i++ {
		for _, tr := range transitions {
			c.Observe(tr[0], tr[1])
		}
	}

	done := make(chan bool, 1)
	go func() { done <- c.IsErgodic() }()

	select {
	case result := <-done:
		if !result {
			t.Error("IsErgodic()=false after realistic torrent observations; epsilon floor must guarantee true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("IsErgodic() did not terminate within 2s")
	}
}

// ---------------------------------------------------------------------------
// Church lens: functional purity, side-effect isolation, referential transparency
// ---------------------------------------------------------------------------

// TestGUC_Church_TorrentStepDoesNotMutateInput verifies that Step is a pure
// function with respect to its input: the pi slice is not mutated by the call.
func TestGUC_Church_TorrentStepDoesNotMutateInput(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	pi := []float64{0.4, 0.3, 0.15, 0.1, 0.05}

	// Take a deep copy before calling Step.
	before := make([]float64, len(pi))
	copy(before, pi)

	_ = c.Step(pi, 50)

	for i := range pi {
		if pi[i] != before[i] {
			t.Errorf("pi[%d] mutated by Step: was %.8f, now %.8f", i, before[i], pi[i])
		}
	}
}

// TestGUC_Church_TorrentHealthStatePure verifies referential transparency:
// TorrentHealthState is a pure function — identical inputs always produce the
// same output with no visible side effects.
func TestGUC_Church_TorrentHealthStatePure(t *testing.T) {
	cases := []struct {
		s, l int64
	}{
		{0, 0}, {0, 5}, {1, 0}, {3, 6}, {10, 4},
	}
	for _, tc := range cases {
		r1 := chain.TorrentHealthState(tc.s, tc.l)
		r2 := chain.TorrentHealthState(tc.s, tc.l)
		if r1 != r2 {
			t.Errorf("TorrentHealthState(%d,%d) not pure: %d != %d", tc.s, tc.l, r1, r2)
		}
	}
}

// TestGUC_Church_ChainConstructionNoPanic verifies that constructing a torrent
// chain with the standard NumTorrentStates and realistic decay values never panics.
func TestGUC_Church_ChainConstructionNoPanic(t *testing.T) {
	decays := []float64{0.5, 0.9, 0.99, 0.999, 1.0}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("chain.New panicked: %v", r)
		}
	}()

	for _, d := range decays {
		c := chain.New(chain.NumTorrentStates, d)
		if c == nil {
			t.Errorf("chain.New(NumTorrentStates, %.3f) returned nil", d)
		}
		if c.N() != chain.NumTorrentStates {
			t.Errorf("c.N()=%d, want %d", c.N(), chain.NumTorrentStates)
		}
	}
}

// TestGUC_Church_InitialStateInRange verifies side-effect isolation: for any
// seeder/leecher pair drawn from a representative domain, TorrentHealthState
// always returns a value in [0, NumTorrentStates), ruling out phantom states.
func TestGUC_Church_InitialStateInRange(t *testing.T) {
	seederCounts := []int64{0, 1, 2, 3, 9, 10, 11, 100}
	leecherCounts := []int64{0, 1, 5, 10, 100}

	for _, s := range seederCounts {
		for _, l := range leecherCounts {
			got := chain.TorrentHealthState(s, l)
			if got < 0 || got >= chain.NumTorrentStates {
				t.Errorf("TorrentHealthState(%d, %d)=%d outside [0, %d)",
					s, l, got, chain.NumTorrentStates)
			}
		}
	}
}

// TestGUC_Church_StepOutputIsolated verifies referential transparency: mutating
// the slice returned by Step does not affect the next call to Step, proving
// the function returns independent memory.
func TestGUC_Church_StepOutputIsolated(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	pi := make([]float64, chain.NumTorrentStates)
	pi[chain.TorrentHealthy] = 1.0

	out1 := c.Step(pi, 5)
	// Record one value before mutation.
	savedVal := out1[0]
	// Mutate the returned slice aggressively.
	for i := range out1 {
		out1[i] = 9999.0
	}

	// A second call must produce output independent from the mutated slice.
	out2 := c.Step(pi, 5)
	if out2[0] == 9999.0 {
		t.Error("out2[0]==9999: Step output is not isolated (shares memory with previous return)")
	}
	// Sanity: out2[0] should match the original out1[0] value (same inputs).
	if math.Abs(out2[0]-savedVal) > 1e-12 {
		t.Errorf("identical Step calls returned different [0]: %.15f vs %.15f", savedVal, out2[0])
	}
}

// ---------------------------------------------------------------------------
// Gödel lens: formal consistency, invariant preservation, impossible-state detection
// ---------------------------------------------------------------------------

// TestGUC_Godel_ErgodicFromHealthy verifies the formal invariant that a chain
// trained on transitions observed from TorrentHealthy is ergodic: every state
// remains reachable from every other, ruling out impossible isolated states.
func TestGUC_Godel_ErgodicFromHealthy(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	// Simulate a full lifecycle starting from TorrentHealthy.
	lifecycle := [][2]int{
		{chain.TorrentHealthy, chain.TorrentAtRisk},
		{chain.TorrentAtRisk, chain.TorrentDying},
		{chain.TorrentDying, chain.TorrentUnavailable},
		{chain.TorrentUnavailable, chain.TorrentAtRisk},
		{chain.TorrentAtRisk, chain.TorrentHealthy},
		{chain.TorrentHealthy, chain.TorrentThriving},
		{chain.TorrentThriving, chain.TorrentHealthy},
	}
	for i := 0; i < 20; i++ {
		for _, tr := range lifecycle {
			c.Observe(tr[0], tr[1])
		}
	}

	if !c.IsErgodic() {
		t.Error("chain is not ergodic after lifecycle observations: epsilon floor invariant violated")
	}
}

// TestGUC_Godel_StateNamesCountConsistent verifies the formal consistency
// between the TorrentStateNames array and NumTorrentStates: they must agree, or
// a rename/addition created a contradiction between the two declarations.
func TestGUC_Godel_StateNamesCountConsistent(t *testing.T) {
	gotLen := len(chain.TorrentStateNames)
	if gotLen != chain.NumTorrentStates {
		t.Errorf("len(TorrentStateNames)=%d, NumTorrentStates=%d: inconsistency detected",
			gotLen, chain.NumTorrentStates)
	}
	// Each name must be non-empty.
	for i, name := range chain.TorrentStateNames {
		if name == "" {
			t.Errorf("TorrentStateNames[%d] is empty (impossible state: unnamed health band)", i)
		}
	}
}

// TestGUC_Godel_SerializationRoundTrip verifies formal consistency of the
// counts persistence path: a counts matrix marshalled to JSON and loaded back
// via LoadCounts must exactly restore the original chain transition matrix.
func TestGUC_Godel_SerializationRoundTrip(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	// Populate with asymmetric observations to distinguish cells.
	for i := 0; i < chain.NumTorrentStates; i++ {
		for j := 0; j < chain.NumTorrentStates; j++ {
			for k := 0; k < (i+1)*(j+2); k++ {
				c.Observe(i, j)
			}
		}
	}

	original := c.Counts()

	// Serialise.
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal(counts): %v", err)
	}

	// Deserialise.
	var restored [][]float64
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal(counts): %v", err)
	}

	// Load into a fresh chain.
	c2 := chain.New(chain.NumTorrentStates, 0.99)
	c2.LoadCounts(restored)
	roundTripped := c2.Counts()

	for i := range original {
		for j := range original[i] {
			if math.Abs(roundTripped[i][j]-original[i][j]) > 1e-9 {
				t.Errorf("counts[%d][%d]: original=%.8f roundTripped=%.8f (serialisation round-trip failed)",
					i, j, original[i][j], roundTripped[i][j])
			}
		}
	}
}

// TestGUC_Godel_NoProbabilityUnderflow verifies the formal invariant that no
// cell of the transition matrix ever becomes negative — an impossible state
// that would corrupt every downstream forecast.
func TestGUC_Godel_NoProbabilityUnderflow(t *testing.T) {
	c := chain.New(chain.NumTorrentStates, 0.99)
	// Apply many observation-and-decay cycles as would occur in production.
	for epoch := 0; epoch < 50; epoch++ {
		for from := 0; from < chain.NumTorrentStates; from++ {
			c.Observe(from, (from+1)%chain.NumTorrentStates)
		}
		c.Decay()
	}

	p := c.P()
	for i, row := range p {
		for j, v := range row {
			if v < 0 {
				t.Errorf("P[%d][%d]=%v < 0 after observation-decay cycles (impossible state: negative probability)",
					i, j, v)
			}
		}
	}

	// Also check via Step: 10000 steps from uniform must have no negative component.
	pi := make([]float64, chain.NumTorrentStates)
	for i := range pi {
		pi[i] = 1.0 / float64(chain.NumTorrentStates)
	}
	out := c.Step(pi, 10000)
	for i, v := range out {
		if v < -1e-12 {
			t.Errorf("Step(uniform, 10000)[%d]=%v < 0 (probability underflow impossible state)", i, v)
		}
	}
}

// TestGUC_Godel_EngineStateCountConsistent verifies the Gödel consistency
// invariant under concurrent load: after many concurrent observations and
// decay calls the transition matrix remains formally valid (row-stochastic,
// no negative entries, ergodic).  Run with -race to surface any data race.
func TestGUC_Godel_EngineStateCountConsistent(t *testing.T) {
	const goroutines = 8
	const iters = 250
	c := chain.New(chain.NumTorrentStates, 0.99)

	var wg sync.WaitGroup
	var observed atomic.Int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		g := g
		go func() {
			defer wg.Done()
			for k := 0; k < iters; k++ {
				from := (g + k) % chain.NumTorrentStates
				to := (g*3 + k*7 + 1) % chain.NumTorrentStates
				c.Observe(from, to)
				observed.Add(1)
				if k%50 == 0 {
					c.Decay()
				}
			}
		}()
	}
	wg.Wait()

	if observed.Load() != int64(goroutines*iters) {
		t.Errorf("only %d/%d Observe calls completed (state count inconsistency)",
			observed.Load(), goroutines*iters)
	}

	// Post-condition: matrix must still satisfy all formal invariants.
	p := c.P()
	if len(p) != chain.NumTorrentStates {
		t.Fatalf("P() returned %d rows after concurrent ops, want %d", len(p), chain.NumTorrentStates)
	}
	for i, row := range p {
		var sum float64
		for j, v := range row {
			if v < 0 {
				t.Errorf("P[%d][%d]=%v < 0 after concurrent ops", i, j, v)
			}
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("P[%d] sum=%.15f after concurrent ops, want 1.0", i, sum)
		}
	}
	if !c.IsErgodic() {
		t.Error("IsErgodic()=false after concurrent ops: epsilon floor invariant violated")
	}
}
