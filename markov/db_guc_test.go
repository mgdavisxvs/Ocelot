package markov

// GUC test suite for Markov DB persistence.
//
// The markov package does not yet expose SaveChain/LoadChain wrappers; the
// underlying chain-count persistence lives in internal/db (UpsertChainCounts /
// LoadChainCounts) and is not importable here without a non-stdlib dependency.
// Tests that require those missing symbols call t.Skip. All other tests drive
// the persistence-relevant chain.Chain API — Counts() and LoadCounts() — which
// is exactly what SaveChain/LoadChain would serialise and restore.
//
// Lenses (~5 each):
//   Knuth  — algorithmic correctness, loop invariants, data-structure invariants
//   Turing — termination conditions, halting behavior, decidability
//   Church — functional purity, side-effect isolation, referential transparency
//   Gödel  — formal consistency, invariant preservation, impossible-state detection

import (
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

// TestGUC_Knuth_CountsRoundTrip_PreservesValues verifies the loop invariant
// that Counts() → LoadCounts() → Counts() produces an exact deep-copy match:
// every cell value is preserved across the round-trip for chains of various sizes.
func TestGUC_Knuth_CountsRoundTrip_PreservesValues(t *testing.T) {
	tests := []struct {
		name    string
		n       int
		decay   float64
		observe [][2]int
	}{
		{"2-state mutual", 2, 1.0, [][2]int{{0, 1}, {1, 0}}},
		{"3-state ring", 3, 0.99, [][2]int{{0, 1}, {1, 2}, {2, 0}, {0, 0}}},
		{"5-state mixed", 5, 0.995, [][2]int{{0, 4}, {4, 3}, {3, 2}, {2, 1}, {1, 0}, {0, 2}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := chain.New(tc.n, tc.decay)
			for _, obs := range tc.observe {
				src.Observe(obs[0], obs[1])
			}
			saved := src.Counts()

			dst := chain.New(tc.n, tc.decay)
			dst.LoadCounts(saved)
			loaded := dst.Counts()

			for i := range saved {
				for j := range saved[i] {
					if math.Abs(loaded[i][j]-saved[i][j]) > 1e-12 {
						t.Errorf("counts[%d][%d]=%.15f after round-trip, want %.15f",
							i, j, loaded[i][j], saved[i][j])
					}
				}
			}
		})
	}
}

// TestGUC_Knuth_LargeChain1000StatesRoundTrip verifies the algorithmic
// invariant scales to production-sized matrices: a 1000-state chain survives a
// Counts()/LoadCounts() round-trip with all 1 000 000 cell values preserved.
func TestGUC_Knuth_LargeChain1000StatesRoundTrip(t *testing.T) {
	const n = 1000
	src := chain.New(n, 0.999)
	for i := 0; i < n; i++ {
		src.Observe(i, (i+1)%n)
		src.Observe(i, (i+2)%n)
	}
	saved := src.Counts()

	dst := chain.New(n, 0.999)
	dst.LoadCounts(saved)
	loaded := dst.Counts()

	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if math.Abs(loaded[i][j]-saved[i][j]) > 1e-12 {
				t.Fatalf("1000-state round-trip: counts[%d][%d]=%.15f, want %.15f",
					i, j, loaded[i][j], saved[i][j])
			}
		}
	}
}

// TestGUC_Knuth_EmptyChainSavesAndLoads verifies the data-structure invariant
// that a freshly constructed chain (Laplace prior only, no observations) round-
// trips correctly: all cells remain exactly 1.0 after LoadCounts.
func TestGUC_Knuth_EmptyChainSavesAndLoads(t *testing.T) {
	src := chain.New(4, 1.0)
	saved := src.Counts()

	dst := chain.New(4, 1.0)
	dst.LoadCounts(saved)
	loaded := dst.Counts()

	for i, row := range loaded {
		for j, v := range row {
			if math.Abs(v-1.0) > 1e-12 {
				t.Errorf("empty chain round-trip: counts[%d][%d]=%.15f, want 1.0 (Laplace prior)", i, j, v)
			}
		}
	}
}

// TestGUC_Knuth_OverwriteExistingCounts verifies the write-path loop invariant:
// LoadCounts fully replaces all pre-existing counts — no merge, no residue.
// This mirrors the DB UpsertChainCounts semantics (INSERT OR REPLACE).
func TestGUC_Knuth_OverwriteExistingCounts(t *testing.T) {
	dst := chain.New(3, 1.0)
	for k := 0; k < 10; k++ {
		dst.Observe(0, 1)
		dst.Observe(1, 2)
	}
	newCounts := [][]float64{
		{7.0, 8.0, 9.0},
		{1.0, 2.0, 3.0},
		{4.0, 5.0, 6.0},
	}
	dst.LoadCounts(newCounts)
	got := dst.Counts()
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if math.Abs(got[i][j]-newCounts[i][j]) > 1e-12 {
				t.Errorf("counts[%d][%d]=%.6f after LoadCounts overwrite, want %.6f",
					i, j, got[i][j], newCounts[i][j])
			}
		}
	}
}

// TestGUC_Knuth_SaveChainRoundTrip verifies the DB-layer SaveChain/LoadChain
// round-trip (FR-002: chain persistence at the markov package boundary).
func TestGUC_Knuth_SaveChainRoundTrip(t *testing.T) {
	t.Skip("not yet implemented: SaveChain/LoadChain in package markov")
}

// ---------------------------------------------------------------------------
// Turing lens: termination conditions, halting behavior, decidability
// ---------------------------------------------------------------------------

// TestGUC_Turing_LoadCountsTerminates1000States verifies that LoadCounts on a
// 1000×1000 matrix completes within 1 second — the termination time bound for
// the hot persistence path.
func TestGUC_Turing_LoadCountsTerminates1000States(t *testing.T) {
	const n = 1000
	src := chain.New(n, 0.999)
	saved := src.Counts()
	dst := chain.New(n, 0.999)

	start := time.Now()
	dst.LoadCounts(saved)
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Errorf("LoadCounts on %d×%d matrix took %v, want <1s", n, n, elapsed)
	}
	// Termination check: Counts() must still return n rows.
	got := dst.Counts()
	if len(got) != n {
		t.Errorf("after LoadCounts: Counts() returned %d rows, want %d", len(got), n)
	}
}

// TestGUC_Turing_LoadChainUnknownIDReturnsError verifies that loading a chain
// ID that was never saved terminates with an error (decidability of existence).
func TestGUC_Turing_LoadChainUnknownIDReturnsError(t *testing.T) {
	t.Skip("not yet implemented: LoadChain in package markov")
}

// TestGUC_Turing_LoadChainAfterDeleteReturnsError verifies that once a chain
// is deleted from the DB, a subsequent LoadChain call terminates with an error.
func TestGUC_Turing_LoadChainAfterDeleteReturnsError(t *testing.T) {
	t.Skip("not yet implemented: DeleteChain/LoadChain in package markov")
}

// TestGUC_Turing_DBFileCreatedOnSave verifies that SaveChain creates the
// database file on first use — the filesystem-creation termination condition.
func TestGUC_Turing_DBFileCreatedOnSave(t *testing.T) {
	t.Skip("not yet implemented: SaveChain (DB file creation) in package markov")
}

// TestGUC_Turing_ConcurrentSaveLoadSafe verifies that concurrent SaveChain and
// LoadChain calls do not deadlock and all terminate — the concurrency halting
// condition for the persistence layer.
func TestGUC_Turing_ConcurrentSaveLoadSafe(t *testing.T) {
	t.Skip("not yet implemented: SaveChain/LoadChain in package markov")
}

// ---------------------------------------------------------------------------
// Church lens: functional purity, side-effect isolation, referential transparency
// ---------------------------------------------------------------------------

// TestGUC_Church_LoadedChainMatchesSaved verifies referential transparency:
// the loaded chain produces a P() matrix identical to the source, confirming the
// counts snapshot fully describes the chain's probabilistic behavior.
func TestGUC_Church_LoadedChainMatchesSaved(t *testing.T) {
	src := chain.New(4, 0.995)
	src.Observe(0, 1)
	src.Observe(1, 2)
	src.Observe(2, 3)
	src.Observe(3, 0)
	saved := src.Counts()

	dst := chain.New(4, 0.995)
	dst.LoadCounts(saved)

	pSrc := src.P()
	pDst := dst.P()
	for i := range pSrc {
		for j := range pSrc[i] {
			if math.Abs(pDst[i][j]-pSrc[i][j]) > 1e-12 {
				t.Errorf("P()[%d][%d]: loaded=%.15f, source=%.15f (mismatch after counts round-trip)",
					i, j, pDst[i][j], pSrc[i][j])
			}
		}
	}
}

// TestGUC_Church_LoadCountsPure verifies side-effect isolation: LoadCounts
// stores values without retaining a reference to the input slice — subsequent
// mutation of the caller's buffer does not affect the chain's internal state.
func TestGUC_Church_LoadCountsPure(t *testing.T) {
	c := chain.New(3, 1.0)
	input := [][]float64{
		{5.0, 3.0, 2.0},
		{1.0, 7.0, 4.0},
		{6.0, 2.0, 9.0},
	}
	c.LoadCounts(input)
	want00 := input[0][0]
	input[0][0] = 999.0 // mutate caller's buffer post-load

	got := c.Counts()
	// float64 is copied by value, so want00 (5.0) must be preserved.
	if math.Abs(got[0][0]-want00) > 1e-12 {
		t.Errorf("counts[0][0]=%.6f after caller mutation, want %.6f (LoadCounts copy isolation broken)",
			got[0][0], want00)
	}
}

// TestGUC_Church_CountsDeepCopyIsolation verifies that Counts() is a pure
// read: mutating the returned matrix does not alter the chain's internal state,
// preserving referential transparency of the serialisation snapshot.
func TestGUC_Church_CountsDeepCopyIsolation(t *testing.T) {
	c := chain.New(3, 1.0)
	c.Observe(0, 1)
	snap := c.Counts()
	snap[0][1] = 99999.0 // mutate snapshot

	snap2 := c.Counts()
	if math.Abs(snap2[0][1]-99999.0) < 1.0 {
		t.Errorf("Counts() snapshot mutation leaked into chain internal state: got %.6f", snap2[0][1])
	}
}

// TestGUC_Church_SaveChainTwiceIdempotent verifies referential transparency of
// SaveChain: calling it twice with the same chain produces the same persisted state.
func TestGUC_Church_SaveChainTwiceIdempotent(t *testing.T) {
	t.Skip("not yet implemented: SaveChain in package markov")
}

// TestGUC_Church_ChainMetadataPreserved verifies that chain governance metadata
// (created_at, schema_version) is preserved across a DB save/load cycle.
func TestGUC_Church_ChainMetadataPreserved(t *testing.T) {
	t.Skip("not yet implemented: chain metadata persistence (SaveChain/LoadChain) in package markov")
}

// ---------------------------------------------------------------------------
// Gödel lens: formal consistency, invariant preservation, impossible-state detection
// ---------------------------------------------------------------------------

// TestGUC_Godel_LoadCountsNonNegativeInvariant verifies the formal invariant
// that all counts remain non-negative after LoadCounts — no negative-count
// impossible state can be introduced through the persistence read path.
func TestGUC_Godel_LoadCountsNonNegativeInvariant(t *testing.T) {
	tests := []struct {
		name   string
		n      int
		counts [][]float64
	}{
		{"2-state", 2, [][]float64{{1.0, 2.0}, {3.0, 4.0}}},
		{"3-state sparse", 3, [][]float64{{5.0, 0.0, 1.0}, {0.0, 8.0, 2.0}, {3.0, 4.0, 7.0}}},
		{"4-state uniform", 4, [][]float64{
			{1.0, 1.0, 1.0, 1.0}, {1.0, 1.0, 1.0, 1.0},
			{1.0, 1.0, 1.0, 1.0}, {1.0, 1.0, 1.0, 1.0},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := chain.New(tc.n, 1.0)
			c.LoadCounts(tc.counts)
			got := c.Counts()
			for i, row := range got {
				for j, v := range row {
					if v < 0 {
						t.Errorf("n=%d: counts[%d][%d]=%.6f < 0 after LoadCounts (impossible negative state)",
							tc.n, i, j, v)
					}
				}
			}
		})
	}
}

// TestGUC_Godel_CountsDimensionNxN verifies the formal dimension invariant:
// Counts() always returns an n×n matrix regardless of the number of Observe()
// or LoadCounts() calls — the impossible-state detection for serialisers.
func TestGUC_Godel_CountsDimensionNxN(t *testing.T) {
	tests := []struct{ n int }{{1}, {2}, {5}, {10}, {20}}
	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			c := chain.New(tc.n, 1.0)
			for i := 0; i < tc.n; i++ {
				c.Observe(i, (i+1)%tc.n)
			}
			counts := c.Counts()
			if len(counts) != tc.n {
				t.Fatalf("n=%d: Counts() returned %d rows, want %d", tc.n, len(counts), tc.n)
			}
			for i, row := range counts {
				if len(row) != tc.n {
					t.Errorf("n=%d: Counts()[%d] len=%d, want %d (n×n invariant violated)",
						tc.n, i, len(row), tc.n)
				}
			}
		})
	}
}

// TestGUC_Godel_ConcurrentCountsConsistency verifies formal consistency under
// concurrent reads: parallel Counts() calls must always return n×n matrices
// with no race-induced shape corruption. Run with go test -race.
func TestGUC_Godel_ConcurrentCountsConsistency(t *testing.T) {
	const n = 6
	const goroutines = 8
	const iters = 100
	c := chain.New(n, 1.0)
	for i := 0; i < n; i++ {
		c.Observe(i, (i+1)%n)
	}

	var wg sync.WaitGroup
	var inconsistent atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < iters; k++ {
				counts := c.Counts()
				if len(counts) != n {
					inconsistent.Add(1)
					continue
				}
				for _, row := range counts {
					if len(row) != n {
						inconsistent.Add(1)
					}
				}
			}
		}()
	}
	wg.Wait()
	if inconsistent.Load() > 0 {
		t.Errorf("%d concurrent Counts() calls returned inconsistent matrix shape (formal invariant violated)",
			inconsistent.Load())
	}
}

// TestGUC_Godel_LoadCountsPartialMatrixNoImpossibleState verifies that loading
// a matrix smaller than n×n does not leave the chain in an impossible state:
// unwritten cells retain their previous values and all rows remain length n.
func TestGUC_Godel_LoadCountsPartialMatrixNoImpossibleState(t *testing.T) {
	const n = 4
	c := chain.New(n, 1.0)
	// Load only a 2×2 sub-matrix into a 4-state chain.
	partial := [][]float64{
		{10.0, 20.0},
		{30.0, 40.0},
	}
	c.LoadCounts(partial)
	got := c.Counts()
	if len(got) != n {
		t.Fatalf("Counts() after partial LoadCounts: %d rows, want %d", len(got), n)
	}
	for i, row := range got {
		if len(row) != n {
			t.Errorf("Counts()[%d] len=%d after partial load, want %d (impossible state)",
				i, len(row), n)
		}
	}
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			if math.Abs(got[i][j]-partial[i][j]) > 1e-12 {
				t.Errorf("counts[%d][%d]=%.6f after partial LoadCounts, want %.6f",
					i, j, got[i][j], partial[i][j])
			}
		}
	}
}

// TestGUC_Godel_LoadCountsNilDoesNotCorrupt verifies the impossible-state guard
// for the zero-input edge case: calling LoadCounts with nil or an empty matrix
// must leave the chain's state entirely unchanged.
func TestGUC_Godel_LoadCountsNilDoesNotCorrupt(t *testing.T) {
	c := chain.New(3, 1.0)
	c.Observe(0, 1)
	c.Observe(1, 2)
	before := c.Counts()

	// Nil input: must be a no-op.
	c.LoadCounts(nil)
	after := c.Counts()
	for i := range before {
		for j := range before[i] {
			if math.Abs(after[i][j]-before[i][j]) > 1e-12 {
				t.Errorf("counts[%d][%d] changed after LoadCounts(nil): %.15f → %.15f",
					i, j, before[i][j], after[i][j])
			}
		}
	}

	// Empty-slice input: must also be a no-op.
	c.LoadCounts([][]float64{})
	afterEmpty := c.Counts()
	for i := range before {
		for j := range before[i] {
			if math.Abs(afterEmpty[i][j]-before[i][j]) > 1e-12 {
				t.Errorf("counts[%d][%d] changed after LoadCounts([]): %.15f → %.15f",
					i, j, before[i][j], afterEmpty[i][j])
			}
		}
	}
}
