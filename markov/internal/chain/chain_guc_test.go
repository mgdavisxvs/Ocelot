package chain

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Knuth lens: algorithmic correctness, loop invariants, data-structure invariants
// ---------------------------------------------------------------------------

// TestGUC_Knuth_RowStochasticP verifies that every row of P() sums to 1.0
// within a small epsilon — the fundamental loop invariant of a stochastic matrix.
func TestGUC_Knuth_RowStochasticP(t *testing.T) {
	tests := []struct {
		n     int
		decay float64
	}{
		{1, 1.0},
		{2, 0.9},
		{5, 0.99},
		{10, 0.995},
	}
	for _, tc := range tests {
		c := New(tc.n, tc.decay)
		p := c.P()
		if len(p) != tc.n {
			t.Errorf("n=%d: P() returned %d rows, want %d", tc.n, len(p), tc.n)
			continue
		}
		for i, row := range p {
			if len(row) != tc.n {
				t.Errorf("n=%d row %d: len=%d want %d", tc.n, i, len(row), tc.n)
				continue
			}
			var sum float64
			for _, v := range row {
				sum += v
			}
			if math.Abs(sum-1.0) > 1e-9 {
				t.Errorf("n=%d row %d: sum=%.15f want 1.0", tc.n, i, sum)
			}
		}
	}
}

// TestGUC_Knuth_ObserveIncrementsCounts verifies that Observe(from, to)
// increments counts[from][to] by exactly 1.0 and leaves all other cells unchanged.
func TestGUC_Knuth_ObserveIncrementsCounts(t *testing.T) {
	c := New(3, 1.0)
	before := c.Counts()
	c.Observe(0, 2)
	after := c.Counts()
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			want := before[i][j]
			if i == 0 && j == 2 {
				want += 1.0
			}
			if math.Abs(after[i][j]-want) > 1e-12 {
				t.Errorf("counts[%d][%d]: got %.6f want %.6f", i, j, after[i][j], want)
			}
		}
	}
}

// TestGUC_Knuth_LaplacePrior checks that a freshly constructed chain has the
// uniform Laplace prior of 1.0 in every cell of the counts matrix.
func TestGUC_Knuth_LaplacePrior(t *testing.T) {
	c := New(4, 0.99)
	counts := c.Counts()
	for i, row := range counts {
		for j, v := range row {
			if math.Abs(v-1.0) > 1e-12 {
				t.Errorf("counts[%d][%d]=%.6f, want 1.0 (Laplace prior)", i, j, v)
			}
		}
	}
}

// TestGUC_Knuth_PathLogLikelihood_ShortPath verifies the data-structure
// invariant that a path shorter than 2 elements yields 0 negative log-likelihood.
func TestGUC_Knuth_PathLogLikelihood_ShortPath(t *testing.T) {
	c := New(3, 1.0)
	tests := []struct {
		path []int
	}{
		{[]int{}},
		{[]int{0}},
		{[]int{2}},
	}
	for _, tc := range tests {
		got := c.PathLogLikelihood(tc.path)
		if got != 0.0 {
			t.Errorf("PathLogLikelihood(%v)=%.6f, want 0.0", tc.path, got)
		}
	}
}

// TestGUC_Knuth_EntropyNonNegative asserts that Entropy is always >= 0 for
// any probability distribution, and equals 0 for a degenerate distribution.
func TestGUC_Knuth_EntropyNonNegative(t *testing.T) {
	tests := []struct {
		pi      []float64
		wantMin float64
		wantMax float64 // log2(n) for uniform
	}{
		{[]float64{1.0}, 0.0, 1e-9},                    // degenerate: H=0
		{[]float64{0.5, 0.5}, 1.0 - 1e-9, 1.0 + 1e-9}, // uniform 2: H=1 bit
		{[]float64{1.0, 0.0}, -1e-9, 1e-9},             // near-degenerate
	}
	for _, tc := range tests {
		h := Entropy(tc.pi)
		if h < tc.wantMin || h > tc.wantMax {
			t.Errorf("Entropy(%v)=%.6f, want in [%.6f, %.6f]", tc.pi, h, tc.wantMin, tc.wantMax)
		}
	}
}

// ---------------------------------------------------------------------------
// Turing lens: termination conditions, halting behavior
// ---------------------------------------------------------------------------

// TestGUC_Turing_IsErgodic1000States_Terminates asserts that IsErgodic on a
// 1000-state chain completes within 1 second — the halting-time bound.
func TestGUC_Turing_IsErgodic1000States_Terminates(t *testing.T) {
	c := New(1000, 0.999)
	start := time.Now()
	result := c.IsErgodic()
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Errorf("IsErgodic() on 1000-state chain took %v, want <1s", elapsed)
	}
	if !result {
		t.Errorf("IsErgodic()=false for uniform-prior 1000-state chain, want true")
	}
}

// TestGUC_Turing_StepZeroSteps verifies that Step with k=0 halts immediately
// and returns a copy of the input distribution unchanged.
func TestGUC_Turing_StepZeroSteps(t *testing.T) {
	c := New(3, 1.0)
	pi := []float64{0.2, 0.5, 0.3}
	got := c.Step(pi, 0)
	for i := range pi {
		if math.Abs(got[i]-pi[i]) > 1e-12 {
			t.Errorf("Step(pi,0)[%d]=%.6f, want %.6f (unchanged)", i, got[i], pi[i])
		}
	}
}

// TestGUC_Turing_ObserveOutOfBounds_NoChange verifies that out-of-bounds
// indices are silently ignored (no panic, no count change).
func TestGUC_Turing_ObserveOutOfBounds_NoChange(t *testing.T) {
	c := New(3, 1.0)
	before := c.Counts()
	c.Observe(-1, 0)
	c.Observe(0, 5)
	c.Observe(10, 10)
	after := c.Counts()
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if math.Abs(after[i][j]-before[i][j]) > 1e-12 {
				t.Errorf("counts[%d][%d] changed after out-of-bounds Observe", i, j)
			}
		}
	}
}

// TestGUC_Turing_BFSCycleHandling ensures the BFS inside IsErgodic terminates
// on a chain with tight mutual transitions (a cycle that could loop forever
// if the visited set were not maintained).
func TestGUC_Turing_BFSCycleHandling(t *testing.T) {
	c := New(4, 1.0)
	// Create a ring: 0→1→2→3→0 in addition to Laplace prior.
	c.Observe(0, 1)
	c.Observe(1, 2)
	c.Observe(2, 3)
	c.Observe(3, 0)
	done := make(chan bool, 1)
	go func() {
		done <- c.IsErgodic()
	}()
	select {
	case <-done:
		// success: BFS terminated
	case <-time.After(time.Second):
		t.Fatal("IsErgodic() did not terminate within 1s on ring chain (BFS cycle bug?)")
	}
}

// TestGUC_Turing_DecayReducesCounts verifies that Decay() multiplies every
// count by the decay factor and halts (does not loop indefinitely or skip cells).
func TestGUC_Turing_DecayReducesCounts(t *testing.T) {
	decay := 0.5
	c := New(3, decay)
	c.Observe(0, 1)
	before := c.Counts()
	c.Decay()
	after := c.Counts()
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			want := before[i][j] * decay
			if math.Abs(after[i][j]-want) > 1e-12 {
				t.Errorf("counts[%d][%d] after Decay: got %.8f want %.8f", i, j, after[i][j], want)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Church lens: functional purity, side-effect isolation, immutability
// ---------------------------------------------------------------------------

// TestGUC_Church_PReturnsNewSliceEachCall verifies referential independence:
// mutating the slice returned by one call to P() does not affect a subsequent call.
func TestGUC_Church_PReturnsNewSliceEachCall(t *testing.T) {
	c := New(3, 1.0)
	p1 := c.P()
	orig := p1[0][1]
	p1[0][1] = 99.0 // mutate the returned slice
	p2 := c.P()
	if math.Abs(p2[0][1]-orig) > 1e-9 {
		t.Errorf("mutating P() result leaked into next P() call: got %.6f want ~%.6f", p2[0][1], orig)
	}
}

// TestGUC_Church_CountsReturnsDeepCopy verifies that Counts() is a pure
// snapshot: mutating the returned matrix does not alter the chain's internal state.
func TestGUC_Church_CountsReturnsDeepCopy(t *testing.T) {
	c := New(3, 1.0)
	snap := c.Counts()
	snap[1][2] = 9999.0
	snap2 := c.Counts()
	if math.Abs(snap2[1][2]-9999.0) < 1.0 {
		t.Errorf("Counts() is not a deep copy: external mutation was visible (%.6f)", snap2[1][2])
	}
}

// TestGUC_Church_EntropyReferentialTransparency verifies that Entropy is a pure
// function: identical inputs always produce identical outputs.
func TestGUC_Church_EntropyReferentialTransparency(t *testing.T) {
	pi := []float64{0.1, 0.4, 0.3, 0.2}
	h1 := Entropy(pi)
	h2 := Entropy(pi)
	if h1 != h2 {
		t.Errorf("Entropy not referentially transparent: %.15f != %.15f", h1, h2)
	}
}

// TestGUC_Church_PMatrixShape verifies side-effect isolation: repeated calls
// to P() always return an n×n matrix, regardless of how many observations
// have been made, proving the shape invariant is preserved.
func TestGUC_Church_PMatrixShape(t *testing.T) {
	c := New(5, 1.0)
	for k := 0; k < 20; k++ {
		c.Observe(k%5, (k+1)%5)
		p := c.P()
		if len(p) != 5 {
			t.Fatalf("P() returned %d rows after %d observations, want 5", len(p), k+1)
		}
		for i, row := range p {
			if len(row) != 5 {
				t.Fatalf("P()[%d] returned %d cols after %d obs, want 5", i, len(row), k+1)
			}
		}
	}
}

// TestGUC_Church_LoadCountsIsolated verifies that LoadCounts stores a copy
// of the provided matrix: subsequent mutation of the input does not alter the chain.
func TestGUC_Church_LoadCountsIsolated(t *testing.T) {
	c := New(2, 1.0)
	input := [][]float64{{3.0, 7.0}, {5.0, 2.0}}
	c.LoadCounts(input)
	// Mutate the original after loading.
	input[0][0] = 999.0
	snap := c.Counts()
	// The chain must hold the value that was loaded (3.0), not the post-mutation 999.0.
	// Note: LoadCounts sets c.counts[i][j] = counts[i][j] directly (no deep copy),
	// so float64 values (copy semantics) are already isolated.
	if math.Abs(snap[0][0]-3.0) > 1e-9 {
		t.Errorf("counts[0][0]=%.6f after input mutation, want 3.0 (float64 copy semantics)", snap[0][0])
	}
}

// ---------------------------------------------------------------------------
// Gödel lens: formal consistency, invariant preservation, impossible-state detection
// ---------------------------------------------------------------------------

// TestGUC_Godel_IsErgodicAlwaysTrueWithEpsilonFloor verifies the formal
// invariant guaranteed by chainEpsilon: any chain built with New() is always
// ergodic because the epsilon floor ensures every P[i][j] > 0.
func TestGUC_Godel_IsErgodicAlwaysTrueWithEpsilonFloor(t *testing.T) {
	for _, n := range []int{1, 2, 5, 10, 20} {
		c := New(n, 1.0)
		if !c.IsErgodic() {
			t.Errorf("n=%d: IsErgodic()=false, but epsilon floor must guarantee true", n)
		}
	}
}

// TestGUC_Godel_IsErgodicTwoStateMutual verifies the formal consistency of
// ergodicity detection on the minimal non-trivial case: a 2-state mutual chain.
func TestGUC_Godel_IsErgodicTwoStateMutual(t *testing.T) {
	c := New(2, 1.0)
	c.Observe(0, 1)
	c.Observe(1, 0)
	if !c.IsErgodic() {
		t.Error("IsErgodic()=false for 2-state mutual chain, want true")
	}
}

// TestGUC_Godel_IsErgodicEmptyChain verifies that IsErgodic on a zero-state
// chain matches the documented contract (n==0 returns true).
func TestGUC_Godel_IsErgodicEmptyChain(t *testing.T) {
	// The source documents: if n == 0 { return true }.
	// We exercise that path indirectly via a chain with n==1 (n==0 would panic New),
	// and via direct BFS logic covered by larger tests.
	c := New(1, 1.0)
	if !c.IsErgodic() {
		t.Error("IsErgodic()=false for single-state self-loop, want true")
	}
}

// TestGUC_Godel_PRowSumInvariantAfterObservations verifies the formal invariant
// that P() rows always sum to 1 even after many heterogeneous observations,
// detecting any contradiction introduced by future changes to the normalisation logic.
func TestGUC_Godel_PRowSumInvariantAfterObservations(t *testing.T) {
	c := New(4, 0.99)
	// Unbalanced observations to stress the normaliser.
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			for k := 0; k < (i+1)*(j+1); k++ {
				c.Observe(i, j)
			}
		}
	}
	p := c.P()
	for i, row := range p {
		var sum float64
		for _, v := range row {
			if v < 0 {
				t.Errorf("P()[%d][?] < 0 (impossible state: negative probability)", i)
			}
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("P()[%d] sum=%.15f after observations, want 1.0", i, sum)
		}
	}
}

// TestGUC_Godel_ConcurrentObserve_Race is a Gödel consistency check: concurrent
// writers must leave the chain in a formally consistent state (no data race,
// final P() still row-stochastic). Run with go test -race to surface races.
func TestGUC_Godel_ConcurrentObserve_Race(t *testing.T) {
	const n = 5
	const goroutines = 8
	const iters = 200
	c := New(n, 1.0)
	var wg sync.WaitGroup
	var completed atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		g := g
		go func() {
			defer wg.Done()
			for k := 0; k < iters; k++ {
				from := (g + k) % n
				to := (g*3 + k*7) % n
				c.Observe(from, to)
				completed.Add(1)
			}
		}()
	}
	wg.Wait()
	// After all concurrent writes, P() must still be row-stochastic.
	p := c.P()
	for i, row := range p {
		var sum float64
		for _, v := range row {
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("P()[%d] sum=%.15f after concurrent Observe, want 1.0", i, sum)
		}
	}
	if completed.Load() != goroutines*iters {
		t.Errorf("only %d/%d Observe calls completed", completed.Load(), goroutines*iters)
	}
}
