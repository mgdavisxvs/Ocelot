package markov

// GUC test suite for peer engine semantics, exercised through the chain package
// that forms the engine's core. The peer engine source lives at
// internal/engine/peer.go; its constructor (newPeerEngine) is unexported, so
// these tests drive the chain.Chain layer directly with NumPeerStates states and
// peer-lifecycle state constants.
//
// Lenses distributed across ~5 each:
//   Knuth  — algorithmic correctness, loop invariants, data-structure invariants
//   Turing — termination conditions, halting behavior, decidability
//   Church — functional purity, side-effect isolation, referential transparency
//   Gödel  — formal consistency, invariant preservation, impossible-state detection

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mgdavisxvs/ocelot/markov/internal/chain"
)

// ---------------------------------------------------------------------------
// Knuth lens
// ---------------------------------------------------------------------------

// TestGUC_Knuth_PeerStateConstantsValid verifies that every peer-state constant
// is unique and lies within [0, NumPeerStates) — the loop-invariant that all
// dispatch tables and count arrays depend on.
func TestGUC_Knuth_PeerStateConstantsValid(t *testing.T) {
	tests := []struct {
		name  string
		state int
	}{
		{"PeerLeeching", chain.PeerLeeching},
		{"PeerSeeding", chain.PeerSeeding},
		{"PeerDormant", chain.PeerDormant},
		{"PeerSnatched", chain.PeerSnatched},
		{"PeerDead", chain.PeerDead},
	}
	seen := make(map[int]string, len(tests))
	for _, tc := range tests {
		if tc.state < 0 || tc.state >= chain.NumPeerStates {
			t.Errorf("%s=%d is outside [0,%d)", tc.name, tc.state, chain.NumPeerStates)
		}
		if prev, dup := seen[tc.state]; dup {
			t.Errorf("duplicate state value %d: %s and %s", tc.state, prev, tc.name)
		}
		seen[tc.state] = tc.name
	}
	if len(seen) != chain.NumPeerStates {
		t.Errorf("defined %d constants but NumPeerStates=%d", len(seen), chain.NumPeerStates)
	}
}

// TestGUC_Knuth_PeerTransitionProbsSumOne checks the row-stochastic invariant
// of the peer-lifecycle transition matrix after a set of realistic observations.
func TestGUC_Knuth_PeerTransitionProbsSumOne(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 0.99)
	c.Observe(chain.PeerLeeching, chain.PeerSeeding)
	c.Observe(chain.PeerSeeding, chain.PeerDormant)
	c.Observe(chain.PeerDormant, chain.PeerDead)
	c.Observe(chain.PeerSnatched, chain.PeerDead)
	p := c.P()
	for i, row := range p {
		var sum float64
		for _, v := range row {
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("peer chain P()[%d] sum=%.15f, want 1.0", i, sum)
		}
	}
}

// TestGUC_Knuth_PeerStateHistogramMatchesDistribution runs 1000 Steps from a
// point mass and verifies that the resulting histogram is a valid probability
// distribution that has converged (stable under further steps).
func TestGUC_Knuth_PeerStateHistogramMatchesDistribution(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 1.0)
	for i := 0; i < 50; i++ {
		c.Observe(chain.PeerLeeching, chain.PeerSeeding)
		c.Observe(chain.PeerSeeding, chain.PeerLeeching)
	}
	pi := make([]float64, chain.NumPeerStates)
	pi[chain.PeerLeeching] = 1.0

	got := c.Step(pi, 1000)
	// Verify sum and non-negativity.
	var sum float64
	for i, v := range got {
		if v < 0 {
			t.Errorf("got[%d]=%.6f < 0 after 1000 steps", i, v)
		}
		sum += v
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("distribution sum=%.15f after 1000 steps, want 1.0", sum)
	}

	// Verify convergence: further stepping should not change values much.
	got2 := c.Step(got, 100)
	for i := range got {
		if math.Abs(got[i]-got2[i]) > 1e-4 {
			t.Errorf("not converged at state %d: %.6f vs %.6f", i, got[i], got2[i])
		}
	}
}

// TestGUC_Knuth_ValidTransitionSequenceNoPanic asserts that PathLogLikelihood
// never panics on well-formed peer-state paths and returns a finite or +∞ value.
func TestGUC_Knuth_ValidTransitionSequenceNoPanic(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 1.0)
	for i := 0; i < chain.NumPeerStates; i++ {
		for j := 0; j < chain.NumPeerStates; j++ {
			c.Observe(i, j)
		}
	}
	tests := []struct {
		name string
		path []int
	}{
		{"empty", []int{}},
		{"single", []int{chain.PeerLeeching}},
		{"leeching_to_seeding", []int{chain.PeerLeeching, chain.PeerSeeding}},
		{"full_lifecycle", []int{chain.PeerLeeching, chain.PeerSeeding, chain.PeerDormant, chain.PeerDead}},
		{"snatched_then_dead", []int{chain.PeerLeeching, chain.PeerSnatched, chain.PeerDead}},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			nll := c.PathLogLikelihood(tc.path)
			if math.IsNaN(nll) {
				t.Errorf("PathLogLikelihood(%v) returned NaN", tc.path)
			}
			if nll < 0 {
				t.Errorf("PathLogLikelihood(%v)=%.6f < 0 (NLL must be ≥ 0)", tc.path, nll)
			}
		})
	}
}

// TestGUC_Knuth_PeerActivityStateClassification checks PeerActivityState's
// algorithmic correctness via boundary-value table tests.
func TestGUC_Knuth_PeerActivityStateClassification(t *testing.T) {
	const timeout int64 = 7200
	const now int64 = 1_000_000
	tests := []struct {
		name      string
		active    bool
		remaining int64
		mtime     int64
		want      int
	}{
		{"active_seeding", true, 0, now - 100, chain.PeerSeeding},
		{"active_leeching", true, 1024, now - 100, chain.PeerLeeching},
		{"inactive_within_timeout", false, 0, now - 100, chain.PeerDormant},
		{"inactive_exactly_expired", false, 0, now - timeout - 1, chain.PeerDead},
		{"inactive_at_boundary_inside", false, 0, now - timeout + 1, chain.PeerDormant},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := chain.PeerActivityState(tc.active, tc.remaining, tc.mtime, now, timeout)
			if got != tc.want {
				t.Errorf("PeerActivityState=%d (%s), want %d (%s)",
					got, chain.PeerStateNames[got],
					tc.want, chain.PeerStateNames[tc.want])
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Turing lens
// ---------------------------------------------------------------------------

// TestGUC_Turing_StepAdvancesToReachableState checks that Step always returns
// a valid probability distribution for any step count, including zero and large k.
func TestGUC_Turing_StepAdvancesToReachableState(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 1.0)
	c.Observe(chain.PeerLeeching, chain.PeerSeeding)
	c.Observe(chain.PeerSeeding, chain.PeerDead)

	pi := make([]float64, chain.NumPeerStates)
	pi[chain.PeerLeeching] = 1.0

	for _, k := range []int{0, 1, 5, 50, 200} {
		got := c.Step(pi, k)
		if len(got) != chain.NumPeerStates {
			t.Errorf("Step(pi,%d) len=%d, want %d", k, len(got), chain.NumPeerStates)
			continue
		}
		var sum float64
		for i, v := range got {
			if v < -1e-12 {
				t.Errorf("Step(pi,%d)[%d]=%.15f < 0", k, i, v)
			}
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("Step(pi,%d) sum=%.15f, want 1.0", k, sum)
		}
	}
}

// TestGUC_Turing_AbsorbingStateHandled verifies that a near-absorbing PeerDead
// state (high self-loop weight) causes the distribution to concentrate there
// after many steps — testing the absorption-time halting property.
func TestGUC_Turing_AbsorbingStateHandled(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 1.0)
	for i := 0; i < 500; i++ {
		c.Observe(chain.PeerDead, chain.PeerDead)
	}
	for i := 0; i < 50; i++ {
		c.Observe(chain.PeerLeeching, chain.PeerDead)
		c.Observe(chain.PeerSeeding, chain.PeerDead)
		c.Observe(chain.PeerDormant, chain.PeerDead)
	}
	pi := make([]float64, chain.NumPeerStates)
	pi[chain.PeerLeeching] = 1.0

	got := c.Step(pi, 200)
	if got[chain.PeerDead] < 0.5 {
		t.Errorf("PeerDead prob after 200 steps=%.4f, want >0.5 with strong self-loop",
			got[chain.PeerDead])
	}
}

// TestGUC_Turing_ChainWithOnlyPriorIsValid tests the "zero user observations"
// case. The chain package uses a Laplace(1.0) prior so there are always counts
// but no user transitions — the chain must still produce valid output without
// returning an error or panicking.
func TestGUC_Turing_ChainWithOnlyPriorIsValid(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 1.0)
	p := c.P()
	for i, row := range p {
		var sum float64
		for _, v := range row {
			if v <= 0 {
				t.Errorf("P()[%d] has non-positive entry %.15f with only prior", i, v)
			}
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("P()[%d] sum=%.15f with only prior, want 1.0", i, sum)
		}
	}
	// Under a uniform prior the row should be symmetric; verify all entries equal.
	for i, row := range p {
		for j := 1; j < len(row); j++ {
			if math.Abs(row[j]-row[0]) > 1e-9 {
				t.Errorf("P()[%d] not uniform under Laplace prior: [0]=%.15f [%d]=%.15f",
					i, row[0], j, row[j])
			}
		}
	}
}

// TestGUC_Turing_SerializationRoundTrip verifies that Counts() / LoadCounts()
// is a faithful round-trip: a freshly loaded chain produces the same transition
// matrix as the original.
func TestGUC_Turing_SerializationRoundTrip(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 0.99)
	observations := [][2]int{
		{chain.PeerLeeching, chain.PeerSeeding},
		{chain.PeerSeeding, chain.PeerDormant},
		{chain.PeerDormant, chain.PeerDead},
		{chain.PeerLeeching, chain.PeerSnatched},
		{chain.PeerSnatched, chain.PeerDead},
	}
	for _, o := range observations {
		c.Observe(o[0], o[1])
	}

	snapshot := c.Counts()
	pBefore := c.P()

	c2 := chain.New(chain.NumPeerStates, 0.99)
	c2.LoadCounts(snapshot)
	pAfter := c2.P()

	for i := range pBefore {
		for j := range pBefore[i] {
			if math.Abs(pBefore[i][j]-pAfter[i][j]) > 1e-12 {
				t.Errorf("P[%d][%d]: before=%.15f after=%.15f (round-trip mismatch)",
					i, j, pBefore[i][j], pAfter[i][j])
			}
		}
	}
}

// TestGUC_Turing_ResetToInitialStateViaCounts verifies that a chain can be
// restored to its initial uniform state by loading a Laplace-prior counts
// matrix — the "engine reset" operation used at startup.
func TestGUC_Turing_ResetToInitialStateViaCounts(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 1.0)
	for i := 0; i < 100; i++ {
		c.Observe(chain.PeerLeeching, chain.PeerDead)
	}
	skewedP := c.P()

	uniform := make([][]float64, chain.NumPeerStates)
	for i := range uniform {
		uniform[i] = make([]float64, chain.NumPeerStates)
		for j := range uniform[i] {
			uniform[i][j] = 1.0
		}
	}
	c.LoadCounts(uniform)
	resetP := c.P()

	fresh := chain.New(chain.NumPeerStates, 1.0)
	freshP := fresh.P()
	for i := range resetP {
		for j := range resetP[i] {
			if math.Abs(resetP[i][j]-freshP[i][j]) > 1e-12 {
				t.Errorf("after reset P[%d][%d]=%.15f, fresh=%.15f",
					i, j, resetP[i][j], freshP[i][j])
			}
		}
	}
	// Confirm that the skewed matrix was actually different from fresh.
	diffFound := false
	for i := range skewedP {
		for j := range skewedP[i] {
			if math.Abs(skewedP[i][j]-freshP[i][j]) > 1e-6 {
				diffFound = true
			}
		}
	}
	if !diffFound {
		t.Error("skewed matrix identical to fresh — 100 observations had no effect")
	}
}

// ---------------------------------------------------------------------------
// Church lens
// ---------------------------------------------------------------------------

// TestGUC_Church_StepDoesNotMutateInput verifies functional purity of Step:
// the caller's pi slice must be byte-for-byte unchanged after any Step call.
func TestGUC_Church_StepDoesNotMutateInput(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 1.0)
	pi := []float64{0.4, 0.3, 0.1, 0.1, 0.1}
	original := make([]float64, len(pi))
	copy(original, pi)

	_ = c.Step(pi, 10)

	for i, v := range pi {
		if v != original[i] {
			t.Errorf("Step mutated pi[%d]: %.15f → %.15f", i, original[i], v)
		}
	}
}

// TestGUC_Church_PeerActivityStatePure verifies referential transparency of
// PeerActivityState: identical arguments must always return the same state.
func TestGUC_Church_PeerActivityStatePure(t *testing.T) {
	const timeout int64 = 3600
	const now int64 = 5_000_000
	tests := []struct {
		active    bool
		remaining int64
		mtime     int64
	}{
		{true, 0, now - 100},
		{true, 1000, now - 100},
		{false, 0, now - 100},
		{false, 0, now - timeout - 1},
	}
	for _, tc := range tests {
		r1 := chain.PeerActivityState(tc.active, tc.remaining, tc.mtime, now, timeout)
		r2 := chain.PeerActivityState(tc.active, tc.remaining, tc.mtime, now, timeout)
		if r1 != r2 {
			t.Errorf("PeerActivityState not pure: %d vs %d for same inputs", r1, r2)
		}
	}
}

// TestGUC_Church_TwoChainIndependence verifies side-effect isolation:
// observations on one PeerEngine chain must never alter another chain's state.
func TestGUC_Church_TwoChainIndependence(t *testing.T) {
	c1 := chain.New(chain.NumPeerStates, 1.0)
	c2 := chain.New(chain.NumPeerStates, 1.0)
	fresh := chain.New(chain.NumPeerStates, 1.0)

	for i := 0; i < 100; i++ {
		c1.Observe(chain.PeerLeeching, chain.PeerDead)
	}

	p2 := c2.P()
	freshP := fresh.P()
	for i := range p2 {
		for j := range p2[i] {
			if math.Abs(p2[i][j]-freshP[i][j]) > 1e-12 {
				t.Errorf("c2.P()[%d][%d] changed after c1 observations (isolation broken)", i, j)
			}
		}
	}
	// Sanity: c1 must differ from c2.
	found := false
	p1 := c1.P()
	for i := range p1 {
		for j := range p1[i] {
			if math.Abs(p1[i][j]-p2[i][j]) > 1e-6 {
				found = true
			}
		}
	}
	if !found {
		t.Error("c1 and c2 have identical P() after c1 was observed — no effect detected")
	}
}

// TestGUC_Church_PeerStateNamesAlignWithConstants verifies the declarative
// immutability of PeerStateNames: each index maps to the expected string constant.
func TestGUC_Church_PeerStateNamesAlignWithConstants(t *testing.T) {
	if len(chain.PeerStateNames) != chain.NumPeerStates {
		t.Fatalf("len(PeerStateNames)=%d != NumPeerStates=%d",
			len(chain.PeerStateNames), chain.NumPeerStates)
	}
	tests := []struct {
		idx  int
		want string
	}{
		{chain.PeerLeeching, "LEECHING"},
		{chain.PeerSeeding, "SEEDING"},
		{chain.PeerDormant, "DORMANT"},
		{chain.PeerSnatched, "SNATCHED"},
		{chain.PeerDead, "DEAD"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			got := chain.PeerStateNames[tc.idx]
			if got != tc.want {
				t.Errorf("PeerStateNames[%d]=%q, want %q", tc.idx, got, tc.want)
			}
		})
	}
}

// TestGUC_Church_EntropyReferentialTransparencyPeer verifies that chain.Entropy
// is a pure function when applied to peer-state distributions: same input always
// yields same output and the result is non-negative.
func TestGUC_Church_EntropyReferentialTransparencyPeer(t *testing.T) {
	peerDists := [][]float64{
		{1.0, 0.0, 0.0, 0.0, 0.0},
		{0.2, 0.2, 0.2, 0.2, 0.2},
		{0.6, 0.1, 0.1, 0.1, 0.1},
		{0.0, 0.0, 0.0, 0.0, 1.0},
	}
	for _, dist := range peerDists {
		h1 := chain.Entropy(dist)
		h2 := chain.Entropy(dist)
		if h1 != h2 {
			t.Errorf("Entropy not referentially transparent for %v: %.15f != %.15f",
				dist, h1, h2)
		}
		if h1 < 0 {
			t.Errorf("Entropy(%v)=%.6f < 0", dist, h1)
		}
	}
}

// ---------------------------------------------------------------------------
// Gödel lens
// ---------------------------------------------------------------------------

// TestGUC_Godel_AllPeerStatesReachableErgodic verifies the formal ergodicity
// invariant: the epsilon floor in P() must guarantee IsErgodic() == true for
// any peer chain, both freshly constructed and after asymmetric observations.
func TestGUC_Godel_AllPeerStatesReachableErgodic(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 0.99)
	if !c.IsErgodic() {
		t.Error("peer chain IsErgodic()=false with epsilon floor — invariant violated")
	}
	for i := 0; i < 100; i++ {
		c.Observe(chain.PeerLeeching, chain.PeerDead)
	}
	if !c.IsErgodic() {
		t.Error("peer chain IsErgodic()=false after asymmetric observations — epsilon floor must guarantee ergodicity")
	}
}

// TestGUC_Godel_NoProbabilityNegative checks the impossible-state invariant:
// P()[i][j] must never be negative regardless of observation weighting.
func TestGUC_Godel_NoProbabilityNegative(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 0.99)
	for i := 0; i < chain.NumPeerStates; i++ {
		for j := 0; j < chain.NumPeerStates; j++ {
			for k := 0; k < (i*chain.NumPeerStates+j)*3+1; k++ {
				c.Observe(i, j)
			}
		}
	}
	p := c.P()
	for i, row := range p {
		for j, v := range row {
			if v < 0 {
				t.Errorf("P()[%d][%d]=%.15f < 0 — impossible negative probability", i, j, v)
			}
		}
	}
}

// TestGUC_Godel_ConcurrentStepSafe verifies that concurrent calls to Step on
// a shared chain produce no data race and always return valid distributions.
// Run with -race to surface any concurrency violation.
func TestGUC_Godel_ConcurrentStepSafe(t *testing.T) {
	const goroutines = 8
	const iters = 50
	c := chain.New(chain.NumPeerStates, 1.0)
	for i := 0; i < chain.NumPeerStates; i++ {
		c.Observe(i, (i+1)%chain.NumPeerStates)
	}

	var wg sync.WaitGroup
	var errCount atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		g := g
		go func() {
			defer wg.Done()
			pi := make([]float64, chain.NumPeerStates)
			pi[g%chain.NumPeerStates] = 1.0
			for k := 0; k < iters; k++ {
				got := c.Step(pi, k%10+1)
				var sum float64
				for _, v := range got {
					if v < -1e-12 {
						errCount.Add(1)
					}
					sum += v
				}
				if math.Abs(sum-1.0) > 1e-9 {
					errCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if n := errCount.Load(); n > 0 {
		t.Errorf("%d invalid distributions observed during concurrent Step", n)
	}
}

// TestGUC_Godel_PeerStatesPartitionComplete verifies formal consistency:
// NumPeerStates must equal the number of distinct state constants, with no
// gaps or duplicates — a contradiction check on the state-space definition.
func TestGUC_Godel_PeerStatesPartitionComplete(t *testing.T) {
	const expectedStates = 5
	if chain.NumPeerStates != expectedStates {
		t.Errorf("NumPeerStates=%d, want %d", chain.NumPeerStates, expectedStates)
	}
	stateSet := map[int]bool{
		chain.PeerLeeching: true,
		chain.PeerSeeding:  true,
		chain.PeerDormant:  true,
		chain.PeerSnatched: true,
		chain.PeerDead:     true,
	}
	for i := 0; i < chain.NumPeerStates; i++ {
		if !stateSet[i] {
			t.Errorf("integer %d in [0,NumPeerStates) has no named constant (gap)", i)
		}
	}
	if len(stateSet) != chain.NumPeerStates {
		t.Errorf("stateSet has %d entries but NumPeerStates=%d (duplicate constants?)",
			len(stateSet), chain.NumPeerStates)
	}
}

// TestGUC_Godel_ExpectedAbsorptionStepsNonNegative verifies that expected
// steps to PeerDead absorption are non-negative for all states, and exactly
// zero for the absorbing state itself — a contradiction check on the fundamental
// matrix computation.
func TestGUC_Godel_ExpectedAbsorptionStepsNonNegative(t *testing.T) {
	c := chain.New(chain.NumPeerStates, 0.99)
	for i := 0; i < 20; i++ {
		c.Observe(chain.PeerLeeching, chain.PeerSeeding)
		c.Observe(chain.PeerSeeding, chain.PeerDormant)
		c.Observe(chain.PeerDormant, chain.PeerDead)
	}
	abs := c.ExpectedAbsorptionSteps(chain.PeerDead)
	if len(abs) != chain.NumPeerStates {
		t.Fatalf("ExpectedAbsorptionSteps len=%d, want %d", len(abs), chain.NumPeerStates)
	}
	for i, v := range abs {
		if v < 0 {
			t.Errorf("ExpectedAbsorptionSteps[%d]=%.6f < 0 — impossible", i, v)
		}
	}
	if math.Abs(abs[chain.PeerDead]) > 1e-9 {
		t.Errorf("ExpectedAbsorptionSteps[PeerDead]=%.6f, want 0.0", abs[chain.PeerDead])
	}
}
