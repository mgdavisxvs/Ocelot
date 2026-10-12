package markov

// user_engine_guc_test.go — GUC test suite for user ratio-state engine semantics.
//
// The UserEngine source lives at internal/engine/user.go; its constructor
// (newUserEngine) is unexported, so these tests drive the chain.Chain layer
// directly with NumUserStates states and user ratio-state constants — the same
// approach used by peer_engine_guc_test.go for the peer engine.
//
// Lenses distributed across ~5 each:
//
//	Knuth  — algorithmic correctness, loop invariants, data-structure invariants
//	Turing — termination conditions, halting behavior, decidability
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

// TestGUC_Knuth_UserStateConstantsValid verifies that every user ratio-state
// constant is unique and lies within [0, NumUserStates) — the data-structure
// invariant that dispatch tables and count arrays depend on.
func TestGUC_Knuth_UserStateConstantsValid(t *testing.T) {
	states := []struct {
		name  string
		value int
	}{
		{"UserSurplus", chain.UserSurplus},
		{"UserHealthy", chain.UserHealthy},
		{"UserMarginal", chain.UserMarginal},
		{"UserDeficit", chain.UserDeficit},
		{"UserSevereDeficit", chain.UserSevereDeficit},
	}

	if chain.NumUserStates != 5 {
		t.Errorf("NumUserStates=%d, want 5", chain.NumUserStates)
	}

	seen := make(map[int]string)
	for _, s := range states {
		if s.value < 0 || s.value >= chain.NumUserStates {
			t.Errorf("%s=%d out of range [0, %d)", s.name, s.value, chain.NumUserStates)
		}
		if prev, ok := seen[s.value]; ok {
			t.Errorf("duplicate state value %d: %s and %s", s.value, prev, s.name)
		}
		seen[s.value] = s.name
	}
	if len(seen) != chain.NumUserStates {
		t.Errorf("defined %d constants but NumUserStates=%d", len(seen), chain.NumUserStates)
	}
}

// TestGUC_Knuth_UserTransitionProbsSumOne checks the row-stochastic invariant
// of the user ratio-state transition matrix after a set of realistic ratio
// band observations.
func TestGUC_Knuth_UserTransitionProbsSumOne(t *testing.T) {
	c := chain.New(chain.NumUserStates, 0.99)
	c.Observe(chain.UserSurplus, chain.UserHealthy)
	c.Observe(chain.UserHealthy, chain.UserMarginal)
	c.Observe(chain.UserMarginal, chain.UserDeficit)
	c.Observe(chain.UserDeficit, chain.UserSevereDeficit)
	c.Observe(chain.UserSevereDeficit, chain.UserDeficit)

	p := c.P()
	for i, row := range p {
		var sum float64
		for _, v := range row {
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("user chain P()[%d] sum=%.15f, want 1.0", i, sum)
		}
	}
}

// TestGUC_Knuth_UserRatioStateClassification verifies the algorithmic
// correctness of UserRatioState against the ratio-band specification in
// states.go.
func TestGUC_Knuth_UserRatioStateClassification(t *testing.T) {
	tests := []struct {
		uploaded   int64
		downloaded int64
		want       int
		desc       string
	}{
		{0, 0, chain.UserSevereDeficit, "new user: no activity"},
		{1024, 0, chain.UserSurplus, "uploader with no download: surplus"},
		{2048, 1024, chain.UserSurplus, "ratio=2.0 exactly: surplus boundary (>=2.0)"},
		{1200, 1000, chain.UserHealthy, "ratio=1.2: healthy"},
		{600, 1000, chain.UserHealthy, "ratio=0.6 exactly: healthy boundary (>=0.6)"},
		{400, 1000, chain.UserMarginal, "ratio=0.4: marginal"},
		{300, 1000, chain.UserMarginal, "ratio=0.3 exactly: marginal boundary (>=0.3)"},
		{200, 1000, chain.UserDeficit, "ratio=0.2: deficit"},
		{100, 1000, chain.UserDeficit, "ratio=0.1 exactly: deficit boundary (>=0.1)"},
		{50, 1000, chain.UserSevereDeficit, "ratio=0.05: severe deficit (<0.1)"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			got := chain.UserRatioState(tc.uploaded, tc.downloaded)
			if got != tc.want {
				t.Errorf("UserRatioState(%d, %d)=%d (%s), want %d (%s)",
					tc.uploaded, tc.downloaded,
					got, chain.UserStateNames[got],
					tc.want, chain.UserStateNames[tc.want])
			}
		})
	}
}

// TestGUC_Knuth_UserStateHistogramPlausible runs 1000 Steps from a point mass
// on UserSurplus and verifies the resulting histogram is a valid probability
// distribution with non-negative entries and unit sum.
func TestGUC_Knuth_UserStateHistogramPlausible(t *testing.T) {
	c := chain.New(chain.NumUserStates, 1.0)
	// Inject a symmetric set of ratio-band transitions to seed the prior.
	for i := 0; i < 20; i++ {
		c.Observe(chain.UserSurplus, chain.UserHealthy)
		c.Observe(chain.UserHealthy, chain.UserSurplus)
		c.Observe(chain.UserHealthy, chain.UserMarginal)
		c.Observe(chain.UserMarginal, chain.UserHealthy)
	}

	pi := make([]float64, chain.NumUserStates)
	pi[chain.UserSurplus] = 1.0

	got := c.Step(pi, 1000)
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

	// The distribution must have converged: another 100 steps changes nothing.
	got2 := c.Step(got, 100)
	for i := range got {
		if math.Abs(got[i]-got2[i]) > 1e-4 {
			t.Errorf("not converged at state %d: %.6f vs %.6f after extra 100 steps",
				i, got[i], got2[i])
		}
	}
}

// TestGUC_Knuth_1000StepsNoPanic asserts that Step never panics over a sequence
// of 1000 steps from each possible starting state — exercising the main loop
// invariant that intermediate values stay finite.
func TestGUC_Knuth_1000StepsNoPanic(t *testing.T) {
	c := chain.New(chain.NumUserStates, 0.995)
	for i := 0; i < chain.NumUserStates; i++ {
		for j := 0; j < chain.NumUserStates; j++ {
			c.Observe(i, j)
		}
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Step panicked during 1000-step run: %v", r)
		}
	}()

	for s := 0; s < chain.NumUserStates; s++ {
		pi := make([]float64, chain.NumUserStates)
		pi[s] = 1.0
		out := c.Step(pi, 1000)
		var sum float64
		for _, v := range out {
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("Step(pi[%d], 1000) sum=%.15f, want 1.0", s, sum)
		}
	}
}

// ---------------------------------------------------------------------------
// Turing lens: termination conditions, halting behavior, decidability
// ---------------------------------------------------------------------------

// TestGUC_Turing_SevereDeficitNearAbsorbing verifies the "banned state
// absorbing" analog: when UserSevereDeficit is given a high self-loop weight the
// distribution concentrates there after many steps, exercising the absorption-
// time halting property.
func TestGUC_Turing_SevereDeficitNearAbsorbing(t *testing.T) {
	c := chain.New(chain.NumUserStates, 1.0)
	// Make SevereDeficit strongly self-reinforcing.
	for i := 0; i < 500; i++ {
		c.Observe(chain.UserSevereDeficit, chain.UserSevereDeficit)
	}
	// A few transitions into SevereDeficit from other states.
	for i := 0; i < 50; i++ {
		c.Observe(chain.UserSurplus, chain.UserSevereDeficit)
		c.Observe(chain.UserDeficit, chain.UserSevereDeficit)
	}

	pi := make([]float64, chain.NumUserStates)
	pi[chain.UserSurplus] = 1.0

	got := c.Step(pi, 200)
	if got[chain.UserSevereDeficit] < 0.5 {
		t.Errorf("UserSevereDeficit prob after 200 steps=%.4f, want >0.5 with strong self-loop",
			got[chain.UserSevereDeficit])
	}
}

// TestGUC_Turing_StepWithOnlyPriorIsValid tests the "zero user observations"
// case. The chain uses a Laplace(1.0) prior so there are always counts but no
// user transitions — Step must produce a valid distribution without panicking.
func TestGUC_Turing_StepWithOnlyPriorIsValid(t *testing.T) {
	c := chain.New(chain.NumUserStates, 1.0)
	pi := make([]float64, chain.NumUserStates)
	pi[chain.UserHealthy] = 1.0

	for _, k := range []int{0, 1, 10, 100, 1000} {
		got := c.Step(pi, k)
		if len(got) != chain.NumUserStates {
			t.Errorf("Step(pi, %d) len=%d, want %d", k, len(got), chain.NumUserStates)
			continue
		}
		var sum float64
		for i, v := range got {
			if v < -1e-12 {
				t.Errorf("Step(pi,%d)[%d]=%.15f < 0 with only prior", k, i, v)
			}
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("Step(pi,%d) sum=%.15f with only prior, want 1.0", k, sum)
		}
	}
}

// TestGUC_Turing_UserSerializationRoundTrip verifies that Counts() / LoadCounts()
// is a faithful round-trip via JSON serialization: a freshly loaded chain
// produces the same transition matrix as the original, as would occur during
// DB-persisted engine state reload.
func TestGUC_Turing_UserSerializationRoundTrip(t *testing.T) {
	c := chain.New(chain.NumUserStates, 0.99)
	observations := [][2]int{
		{chain.UserSurplus, chain.UserHealthy},
		{chain.UserHealthy, chain.UserMarginal},
		{chain.UserMarginal, chain.UserDeficit},
		{chain.UserDeficit, chain.UserSevereDeficit},
		{chain.UserSevereDeficit, chain.UserDeficit},
		{chain.UserDeficit, chain.UserHealthy},
	}
	for _, o := range observations {
		c.Observe(o[0], o[1])
	}

	snapshot := c.Counts()
	pBefore := c.P()

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal(counts): %v", err)
	}
	var restored [][]float64
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal(counts): %v", err)
	}

	c2 := chain.New(chain.NumUserStates, 0.99)
	c2.LoadCounts(restored)
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

// TestGUC_Turing_UserEngineReset verifies that a chain can be restored to its
// initial uniform state by loading a Laplace-prior counts matrix — the "engine
// reset" operation used at startup or when clearing accumulated observations.
func TestGUC_Turing_UserEngineReset(t *testing.T) {
	c := chain.New(chain.NumUserStates, 1.0)
	for i := 0; i < 100; i++ {
		c.Observe(chain.UserHealthy, chain.UserSevereDeficit)
	}
	skewedP := c.P()

	// Build a Laplace-prior uniform counts matrix (the "reset" payload).
	uniform := make([][]float64, chain.NumUserStates)
	for i := range uniform {
		uniform[i] = make([]float64, chain.NumUserStates)
		for j := range uniform[i] {
			uniform[i][j] = 1.0
		}
	}
	c.LoadCounts(uniform)
	resetP := c.P()

	fresh := chain.New(chain.NumUserStates, 1.0)
	freshP := fresh.P()
	for i := range resetP {
		for j := range resetP[i] {
			if math.Abs(resetP[i][j]-freshP[i][j]) > 1e-12 {
				t.Errorf("after reset P[%d][%d]=%.15f, fresh=%.15f",
					i, j, resetP[i][j], freshP[i][j])
			}
		}
	}
	// Confirm the skewed matrix was actually different.
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

// TestGUC_Turing_UserStepAdvancesToReachableState checks that Step always returns a
// valid probability distribution for any step count, including zero and large k.
func TestGUC_Turing_UserStepAdvancesToReachableState(t *testing.T) {
	c := chain.New(chain.NumUserStates, 1.0)
	c.Observe(chain.UserSurplus, chain.UserHealthy)
	c.Observe(chain.UserHealthy, chain.UserMarginal)
	c.Observe(chain.UserMarginal, chain.UserDeficit)

	pi := make([]float64, chain.NumUserStates)
	pi[chain.UserSurplus] = 1.0

	for _, k := range []int{0, 1, 5, 50, 200} {
		got := c.Step(pi, k)
		if len(got) != chain.NumUserStates {
			t.Errorf("Step(pi,%d) len=%d, want %d", k, len(got), chain.NumUserStates)
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

// ---------------------------------------------------------------------------
// Church lens: functional purity, side-effect isolation, referential transparency
// ---------------------------------------------------------------------------

// TestGUC_Church_UserRatioStatePure verifies referential transparency:
// UserRatioState must be a pure function — identical arguments always return
// the same result.
func TestGUC_Church_UserRatioStatePure(t *testing.T) {
	tests := []struct {
		uploaded   int64
		downloaded int64
	}{
		{0, 0},
		{1024, 0},
		{2048, 1024},
		{600, 1000},
		{50, 1000},
	}
	for _, tc := range tests {
		r1 := chain.UserRatioState(tc.uploaded, tc.downloaded)
		r2 := chain.UserRatioState(tc.uploaded, tc.downloaded)
		if r1 != r2 {
			t.Errorf("UserRatioState(%d, %d) not pure: %d != %d on repeated call",
				tc.uploaded, tc.downloaded, r1, r2)
		}
		if r1 < 0 || r1 >= chain.NumUserStates {
			t.Errorf("UserRatioState(%d, %d)=%d outside [0, NumUserStates)",
				tc.uploaded, tc.downloaded, r1)
		}
	}
}

// TestGUC_Church_UserStepDoesNotMutateInput verifies functional purity of Step:
// the caller's pi slice must be unchanged after any Step call.
func TestGUC_Church_UserStepDoesNotMutateInput(t *testing.T) {
	c := chain.New(chain.NumUserStates, 1.0)
	pi := []float64{0.4, 0.3, 0.15, 0.1, 0.05}
	original := make([]float64, len(pi))
	copy(original, pi)

	_ = c.Step(pi, 10)

	for i, v := range pi {
		if v != original[i] {
			t.Errorf("Step mutated pi[%d]: %.15f -> %.15f", i, original[i], v)
		}
	}
}

// TestGUC_Church_UserTwoChainIndependence verifies side-effect isolation:
// observations on one user chain must never alter another chain's state.
func TestGUC_Church_UserTwoChainIndependence(t *testing.T) {
	c1 := chain.New(chain.NumUserStates, 1.0)
	c2 := chain.New(chain.NumUserStates, 1.0)
	fresh := chain.New(chain.NumUserStates, 1.0)

	for i := 0; i < 100; i++ {
		c1.Observe(chain.UserSurplus, chain.UserSevereDeficit)
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

// TestGUC_Church_InitialStateNewUserActive verifies that a new user with
// uploaded > 0 and downloaded = 0 is classified as UserSurplus — the "initial
// state active" invariant for ratio-state semantics.
func TestGUC_Church_InitialStateNewUserActive(t *testing.T) {
	tests := []struct {
		uploaded int64
		desc     string
	}{
		{1, "minimal upload"},
		{1024, "1 KB upload"},
		{1 << 30, "1 GB upload"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			got := chain.UserRatioState(tc.uploaded, 0)
			if got != chain.UserSurplus {
				t.Errorf("new user with upload=%d, download=0: got state %d (%s), want UserSurplus",
					tc.uploaded, got, chain.UserStateNames[got])
			}
		})
	}
	// A truly new user with no activity is SevereDeficit (the "inactive" state).
	got := chain.UserRatioState(0, 0)
	if got != chain.UserSevereDeficit {
		t.Errorf("brand-new user (0,0): got state %d (%s), want UserSevereDeficit",
			got, chain.UserStateNames[got])
	}
}

// TestGUC_Church_UserStateNamesAlignWithConstants verifies the declarative
// immutability of UserStateNames: each index maps to the expected string
// constant, ensuring names and integer constants are always in sync.
func TestGUC_Church_UserStateNamesAlignWithConstants(t *testing.T) {
	if len(chain.UserStateNames) != chain.NumUserStates {
		t.Fatalf("len(UserStateNames)=%d != NumUserStates=%d",
			len(chain.UserStateNames), chain.NumUserStates)
	}
	expected := []struct {
		idx  int
		want string
	}{
		{chain.UserSurplus, "SURPLUS"},
		{chain.UserHealthy, "HEALTHY"},
		{chain.UserMarginal, "MARGINAL"},
		{chain.UserDeficit, "DEFICIT"},
		{chain.UserSevereDeficit, "SEVERE_DEFICIT"},
	}
	for _, tc := range expected {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			got := chain.UserStateNames[tc.idx]
			if got != tc.want {
				t.Errorf("UserStateNames[%d]=%q, want %q", tc.idx, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Gödel lens: formal consistency, invariant preservation, impossible-state detection
// ---------------------------------------------------------------------------

// TestGUC_Godel_UserChainAlwaysErgodic verifies the formal ergodicity invariant:
// the epsilon floor in P() must guarantee IsErgodic() == true for the user chain,
// both freshly constructed and after asymmetric observations.
func TestGUC_Godel_UserChainAlwaysErgodic(t *testing.T) {
	c := chain.New(chain.NumUserStates, 0.99)
	if !c.IsErgodic() {
		t.Error("user chain IsErgodic()=false with only prior — epsilon floor invariant violated")
	}
	// Asymmetric observations that would normally concentrate mass.
	for i := 0; i < 100; i++ {
		c.Observe(chain.UserSurplus, chain.UserSevereDeficit)
	}
	if !c.IsErgodic() {
		t.Error("user chain IsErgodic()=false after asymmetric observations — epsilon floor must guarantee ergodicity")
	}
}

// TestGUC_Godel_UserNoProbabilityNegative checks the impossible-state invariant:
// P()[i][j] must never be negative regardless of observation weighting or decay.
func TestGUC_Godel_UserNoProbabilityNegative(t *testing.T) {
	c := chain.New(chain.NumUserStates, 0.99)
	// Non-uniform observation counts to stress-test normalization.
	for i := 0; i < chain.NumUserStates; i++ {
		for j := 0; j < chain.NumUserStates; j++ {
			for k := 0; k < (i*chain.NumUserStates+j)*3+1; k++ {
				c.Observe(i, j)
			}
		}
	}
	// Several decay cycles as would occur in production.
	for epoch := 0; epoch < 10; epoch++ {
		c.Decay()
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

// TestGUC_Godel_UserConcurrentStepSafe verifies that concurrent calls to Step on a
// shared user chain produce no data race and always return valid distributions.
// Run with -race to surface any concurrency violation.
func TestGUC_Godel_UserConcurrentStepSafe(t *testing.T) {
	const goroutines = 8
	const iters = 50
	c := chain.New(chain.NumUserStates, 1.0)
	for i := 0; i < chain.NumUserStates; i++ {
		c.Observe(i, (i+1)%chain.NumUserStates)
	}

	var wg sync.WaitGroup
	var errCount atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		g := g
		go func() {
			defer wg.Done()
			pi := make([]float64, chain.NumUserStates)
			pi[g%chain.NumUserStates] = 1.0
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

// TestGUC_Godel_EngineConstructedFromDBCounts verifies that loading counts
// from a simulated DB snapshot produces a formally valid chain — the "engine
// constructed from DB" invariant that LoadCounts must preserve all formal
// properties: row-stochastic matrix, non-negative probabilities, and ergodicity.
func TestGUC_Godel_EngineConstructedFromDBCounts(t *testing.T) {
	// Simulate DB-persisted counts from a production engine snapshot.
	dbCounts := make([][]float64, chain.NumUserStates)
	for i := range dbCounts {
		dbCounts[i] = make([]float64, chain.NumUserStates)
		for j := range dbCounts[i] {
			// Asymmetric counts reflecting typical user ratio band dynamics.
			dbCounts[i][j] = float64((i+1)*(j+2)*3) + 1.0
		}
	}

	c := chain.New(chain.NumUserStates, 0.99)
	c.LoadCounts(dbCounts)

	// Formal post-condition: transition matrix must be valid.
	p := c.P()
	if len(p) != chain.NumUserStates {
		t.Fatalf("P() returned %d rows after LoadCounts, want %d", len(p), chain.NumUserStates)
	}
	for i, row := range p {
		var sum float64
		for j, v := range row {
			if v < 0 {
				t.Errorf("P[%d][%d]=%.15f < 0 after LoadCounts from DB snapshot", i, j, v)
			}
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("P[%d] sum=%.15f after LoadCounts from DB snapshot, want 1.0", i, sum)
		}
	}
	if !c.IsErgodic() {
		t.Error("user chain IsErgodic()=false after LoadCounts from DB snapshot — epsilon floor invariant violated")
	}
}

// TestGUC_Godel_UserStatesPartitionComplete verifies formal consistency:
// NumUserStates must equal the number of distinct state constants, with no
// gaps or duplicates — a contradiction check on the state-space definition.
func TestGUC_Godel_UserStatesPartitionComplete(t *testing.T) {
	const expectedStates = 5
	if chain.NumUserStates != expectedStates {
		t.Errorf("NumUserStates=%d, want %d", chain.NumUserStates, expectedStates)
	}
	stateSet := map[int]bool{
		chain.UserSurplus:       true,
		chain.UserHealthy:       true,
		chain.UserMarginal:      true,
		chain.UserDeficit:       true,
		chain.UserSevereDeficit: true,
	}
	for i := 0; i < chain.NumUserStates; i++ {
		if !stateSet[i] {
			t.Errorf("integer %d in [0, NumUserStates) has no named constant (gap)", i)
		}
	}
	if len(stateSet) != chain.NumUserStates {
		t.Errorf("stateSet has %d entries but NumUserStates=%d (duplicate constants?)",
			len(stateSet), chain.NumUserStates)
	}
	// All state names must be non-empty — an empty name is an impossible unnamed state.
	for i, name := range chain.UserStateNames {
		if name == "" {
			t.Errorf("UserStateNames[%d] is empty (impossible state: unnamed ratio band)", i)
		}
	}
	// time import used: verify IsErgodic terminates for a Gödel halting check.
	done := make(chan bool, 1)
	c := chain.New(chain.NumUserStates, 0.99)
	go func() { done <- c.IsErgodic() }()
	select {
	case result := <-done:
		if !result {
			t.Error("IsErgodic()=false on fresh user chain (epsilon floor must guarantee true)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("IsErgodic() did not terminate within 2s on fresh user chain")
	}
}
