package scheduler

// GUC Test Coverage — scorer.go
// Knuth  (~5): algorithmic correctness, loop invariants, complexity
// Turing (~5): termination, halting, decidability
// Church (~5): functional purity, side-effect isolation, referential transparency
// Gödel  (~5): formal consistency, impossible-state detection, invariant preservation

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func readyNode(cpuThreads int, availCPU int, totalRAM, availRAM int64) domain.Node {
	return domain.Node{
		ID:              "n1",
		Name:            "node-1",
		State:           domain.NodeReady,
		CPUThreads:      cpuThreads,
		AvailCPUThreads: availCPU,
		TotalRAMMiB:     totalRAM,
		AvailRAMMiB:     availRAM,
	}
}

func simpleSpec() domain.ServiceSpec {
	return domain.ServiceSpec{}
}

// stubCatalog implements ArtifactCatalog for test purposes.
type stubCatalog struct {
	status domain.ArtifactStatus
	err    error
}

func (s *stubCatalog) Lookup(_ context.Context, _ string) (domain.ArtifactStatus, error) {
	return s.status, s.err
}

// ---------------------------------------------------------------------------
// Knuth: algorithmic correctness
// ---------------------------------------------------------------------------

// TestGUC_Scorer_ScoreInUnitInterval verifies computeScore returns a value in [0,1].
func TestGUC_Scorer_ScoreInUnitInterval(t *testing.T) {
	tests := []struct {
		name string
		node domain.Node
		spec domain.ServiceSpec
	}{
		{
			name: "ready_full_resources",
			node: readyNode(8, 8, 8192, 8192),
			spec: simpleSpec(),
		},
		{
			name: "degraded_half_resources",
			node: func() domain.Node {
				n := readyNode(4, 2, 4096, 2048)
				n.State = domain.NodeDegraded
				return n
			}(),
			spec: simpleSpec(),
		},
		{
			name: "retired_node",
			node: func() domain.Node {
				n := readyNode(4, 4, 4096, 4096)
				n.State = domain.NodeRetired
				return n
			}(),
			spec: simpleSpec(),
		},
		{
			name: "zero_ram",
			node: readyNode(4, 4, 0, 0),
			spec: simpleSpec(),
		},
	}
	w := DefaultWeights()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			score := computeScore(tc.node, tc.spec, nil, w)
			if score < 0 || score > 1 {
				t.Errorf("computeScore = %v; want value in [0,1]", score)
			}
		})
	}
}

// TestGUC_Scorer_HighCPULoadRaisesLoadPenalty checks that a heavily loaded node
// receives a higher existingLoadPenaltyScore than a lightly loaded node, which
// reduces its final computeScore.
func TestGUC_Scorer_HighCPULoadRaisesLoadPenalty(t *testing.T) {
	w := DefaultWeights()
	light := readyNode(8, 8, 8192, 8192)
	light.ActiveInstances = 1

	heavy := readyNode(8, 8, 8192, 8192)
	heavy.ActiveInstances = 8

	lightScore := computeScore(light, simpleSpec(), nil, w)
	heavyScore := computeScore(heavy, simpleSpec(), nil, w)

	if heavyScore >= lightScore {
		t.Errorf("heavy load score (%v) should be < light load score (%v)", heavyScore, lightScore)
	}
}

// TestGUC_Scorer_CapacityFitScoreRange verifies capacityFitScore stays in [0,1].
func TestGUC_Scorer_CapacityFitScoreRange(t *testing.T) {
	tests := []struct {
		name     string
		node     domain.Node
		wantMin  float64
		wantMax  float64
	}{
		{"full_avail", readyNode(4, 4, 4096, 4096), 0, 1},
		{"no_avail", readyNode(4, 0, 4096, 0), 0, 1},
		{"zero_total_ram", readyNode(4, 4, 0, 0), 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := capacityFitScore(tc.node, simpleSpec())
			if s < tc.wantMin || s > tc.wantMax {
				t.Errorf("capacityFitScore = %v; want [%v,%v]", s, tc.wantMin, tc.wantMax)
			}
		})
	}
}

// TestGUC_Scorer_ExistingLoadPenaltyCapped asserts existingLoadPenaltyScore <= 1
// even when ActiveInstances greatly exceeds CPUThreads.
func TestGUC_Scorer_ExistingLoadPenaltyCapped(t *testing.T) {
	n := readyNode(2, 2, 4096, 4096)
	n.ActiveInstances = 1000 // far exceeds CPUThreads
	penalty := existingLoadPenaltyScore(n)
	if penalty > 1.0 {
		t.Errorf("existingLoadPenaltyScore = %v; want <= 1.0", penalty)
	}
}

// TestGUC_Scorer_HealthScoreValues validates healthScore returns correct constants.
func TestGUC_Scorer_HealthScoreValues(t *testing.T) {
	tests := []struct {
		state domain.NodeState
		want  float64
	}{
		{domain.NodeReady, 1.0},
		{domain.NodeDegraded, 0.5},
		{domain.NodeDraining, 0.0},
		{domain.NodeRetired, 0.0},
		{domain.NodeQuarantined, 0.0},
		{domain.NodeMaintenance, 0.0},
	}
	for _, tc := range tests {
		t.Run(string(tc.state), func(t *testing.T) {
			n := readyNode(4, 4, 4096, 4096)
			n.State = tc.state
			got := healthScore(n)
			if got != tc.want {
				t.Errorf("healthScore for state %s = %v; want %v", tc.state, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Turing: termination / halting
// ---------------------------------------------------------------------------

// TestGUC_Scorer_ComputeScoreTerminatesOnZeroResources checks computeScore
// terminates and returns a finite value when the node has no resources.
func TestGUC_Scorer_ComputeScoreTerminatesOnZeroResources(t *testing.T) {
	n := domain.Node{
		ID:    "empty",
		Name:  "empty-node",
		State: domain.NodeReady,
	}
	score := computeScore(n, simpleSpec(), nil, DefaultWeights())
	if score < 0 || score > 1 {
		t.Errorf("zero-resource node score %v is outside [0,1]", score)
	}
}

// TestGUC_Scorer_AntiAffinityTerminatesOnEmptyRequiredMap confirms the function
// returns immediately (0.0) without iterating when AntiAffinityLabels is empty.
func TestGUC_Scorer_AntiAffinityTerminatesOnEmptyRequiredMap(t *testing.T) {
	n := readyNode(4, 4, 4096, 4096)
	n.Labels = map[string]string{"app": "api"}
	spec := domain.ServiceSpec{}
	spec.Placement.AntiAffinityLabels = map[string]string{} // empty map
	penalty := antiAffinityPenaltyScore(n, spec)
	if penalty != 0.0 {
		t.Errorf("antiAffinityPenaltyScore with empty labels = %v; want 0.0", penalty)
	}
}

// TestGUC_Scorer_AntiAffinityTerminatesOnNilLabels confirms nil AntiAffinityLabels
// produces 0.0 (no penalty) without panicking.
func TestGUC_Scorer_AntiAffinityTerminatesOnNilLabels(t *testing.T) {
	n := readyNode(4, 4, 4096, 4096)
	n.Labels = map[string]string{"app": "api"}
	spec := domain.ServiceSpec{}
	// spec.Placement.AntiAffinityLabels is nil by default
	penalty := antiAffinityPenaltyScore(n, spec)
	if penalty != 0.0 {
		t.Errorf("antiAffinityPenaltyScore with nil labels = %v; want 0.0", penalty)
	}
}

// TestGUC_Scorer_ArtifactTransferScoreTerminatesOnNilCatalog verifies the function
// halts gracefully and returns 0 when catalog is nil.
func TestGUC_Scorer_ArtifactTransferScoreTerminatesOnNilCatalog(t *testing.T) {
	n := readyNode(4, 4, 4096, 4096)
	spec := domain.ServiceSpec{}
	spec.Artifact.Type = domain.ArtifactTypeOcelot
	spec.Artifact.InfoHash = "aabbccddeeff00112233445566778899aabbccdd"
	score := artifactTransferScore(n, spec, nil)
	if score != 0.0 {
		t.Errorf("artifactTransferScore with nil catalog = %v; want 0.0", score)
	}
}

// TestGUC_Scorer_LoadPenaltyZeroOnZeroCPUThreads ensures no division-by-zero
// and the function halts returning 0.0 when CPUThreads == 0.
func TestGUC_Scorer_LoadPenaltyZeroOnZeroCPUThreads(t *testing.T) {
	n := domain.Node{
		State:           domain.NodeReady,
		CPUThreads:      0,
		ActiveInstances: 100,
	}
	penalty := existingLoadPenaltyScore(n)
	if penalty != 0.0 {
		t.Errorf("existingLoadPenaltyScore with CPUThreads=0 = %v; want 0.0", penalty)
	}
}

// ---------------------------------------------------------------------------
// Church: functional purity / side-effect isolation
// ---------------------------------------------------------------------------

// TestGUC_Scorer_ComputeScoreIsPure confirms repeated calls with the same
// arguments produce the same result (referential transparency).
func TestGUC_Scorer_ComputeScoreIsPure(t *testing.T) {
	n := readyNode(8, 6, 8192, 6000)
	spec := simpleSpec()
	w := DefaultWeights()
	first := computeScore(n, spec, nil, w)
	for i := 0; i < 10; i++ {
		got := computeScore(n, spec, nil, w)
		if got != first {
			t.Errorf("computeScore not pure: call %d returned %v; first call %v", i+1, got, first)
		}
	}
}

// TestGUC_Scorer_ComputeScoreDoesNotMutateNode verifies computeScore does not
// modify any field of the Node it receives (no side effects).
func TestGUC_Scorer_ComputeScoreDoesNotMutateNode(t *testing.T) {
	original := readyNode(8, 6, 8192, 6000)
	original.Labels = map[string]string{"zone": "us-east"}
	original.ActiveInstances = 3

	snapshot := original // value copy
	_ = computeScore(original, simpleSpec(), nil, DefaultWeights())

	if original.CPUThreads != snapshot.CPUThreads ||
		original.AvailCPUThreads != snapshot.AvailCPUThreads ||
		original.TotalRAMMiB != snapshot.TotalRAMMiB ||
		original.AvailRAMMiB != snapshot.AvailRAMMiB ||
		original.ActiveInstances != snapshot.ActiveInstances ||
		original.State != snapshot.State {
		t.Errorf("computeScore mutated the input node")
	}
}

// TestGUC_Scorer_ComputeScoreDoesNotMutateSpec verifies computeScore does not
// modify the ServiceSpec it receives.
func TestGUC_Scorer_ComputeScoreDoesNotMutateSpec(t *testing.T) {
	spec := domain.ServiceSpec{}
	spec.Placement.AntiAffinityLabels = map[string]string{"app": "worker"}
	spec.Placement.PreferredNodes = []string{"node-a", "node-b"}
	before := fmt.Sprintf("%+v", spec)

	n := readyNode(4, 4, 4096, 4096)
	n.Labels = map[string]string{"app": "worker"}
	_ = computeScore(n, spec, nil, DefaultWeights())

	after := fmt.Sprintf("%+v", spec)
	if before != after {
		t.Errorf("computeScore mutated spec:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestGUC_Scorer_ConcurrentComputeScoreSafe runs computeScore from many goroutines
// simultaneously and checks all returned scores are in [0,1] (no data race).
func TestGUC_Scorer_ConcurrentComputeScoreSafe(t *testing.T) {
	n := readyNode(16, 12, 16384, 10000)
	spec := simpleSpec()
	w := DefaultWeights()
	const goroutines = 64

	var wg sync.WaitGroup
	errs := make(chan string, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			score := computeScore(n, spec, nil, w)
			if score < 0 || score > 1 {
				errs <- fmt.Sprintf("score %v out of range", score)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

// TestGUC_Scorer_WeightsNotMutatedByComputeScore confirms the Weights struct
// passed to computeScore is unchanged after the call.
func TestGUC_Scorer_WeightsNotMutatedByComputeScore(t *testing.T) {
	w := DefaultWeights()
	wBefore := w
	n := readyNode(4, 4, 4096, 4096)
	_ = computeScore(n, simpleSpec(), nil, w)
	if w != wBefore {
		t.Errorf("computeScore mutated weights")
	}
}

// ---------------------------------------------------------------------------
// Gödel: formal consistency / invariant preservation / impossible-state detection
// ---------------------------------------------------------------------------

// TestGUC_Scorer_AntiAffinityPenaltyReducesScore verifies that a matching
// anti-affinity label set lowers the score compared to no anti-affinity.
func TestGUC_Scorer_AntiAffinityPenaltyReducesScore(t *testing.T) {
	n := readyNode(8, 8, 8192, 8192)
	n.Labels = map[string]string{"app": "api"}
	w := DefaultWeights()

	specNoAnti := simpleSpec()

	specWithAnti := simpleSpec()
	specWithAnti.Placement.AntiAffinityLabels = map[string]string{"app": "api"}

	noAntiScore := computeScore(n, specNoAnti, nil, w)
	withAntiScore := computeScore(n, specWithAnti, nil, w)

	if withAntiScore >= noAntiScore {
		t.Errorf("anti-affinity should reduce score: without=%v, with=%v", noAntiScore, withAntiScore)
	}
}

// TestGUC_Scorer_LabelMismatchNoAntiAffinityPenalty asserts that a node whose
// labels do NOT match anti-affinity requirements receives zero penalty.
func TestGUC_Scorer_LabelMismatchNoAntiAffinityPenalty(t *testing.T) {
	n := readyNode(4, 4, 4096, 4096)
	n.Labels = map[string]string{"app": "frontend"}

	spec := simpleSpec()
	spec.Placement.AntiAffinityLabels = map[string]string{"app": "backend"}

	penalty := antiAffinityPenaltyScore(n, spec)
	if penalty != 0.0 {
		t.Errorf("label mismatch should produce 0 penalty; got %v", penalty)
	}
}

// TestGUC_Scorer_PartialLabelMatchNoAntiAffinityPenalty verifies that a node
// matching only SOME of the required anti-affinity labels gets 0 penalty (all
// must match for a penalty to apply).
func TestGUC_Scorer_PartialLabelMatchNoAntiAffinityPenalty(t *testing.T) {
	n := readyNode(4, 4, 4096, 4096)
	n.Labels = map[string]string{"app": "api", "env": "staging"}

	spec := simpleSpec()
	spec.Placement.AntiAffinityLabels = map[string]string{
		"app": "api",
		"env": "production", // mismatch on second label
	}

	penalty := antiAffinityPenaltyScore(n, spec)
	if penalty != 0.0 {
		t.Errorf("partial label match should produce 0 penalty; got %v", penalty)
	}
}

// TestGUC_Scorer_AllLabelsMatchFullAntiAffinityPenalty verifies that when every
// anti-affinity label matches the node, penalty equals 1.0 (maximum).
func TestGUC_Scorer_AllLabelsMatchFullAntiAffinityPenalty(t *testing.T) {
	n := readyNode(4, 4, 4096, 4096)
	n.Labels = map[string]string{"app": "api", "env": "production"}

	spec := simpleSpec()
	spec.Placement.AntiAffinityLabels = map[string]string{
		"app": "api",
		"env": "production",
	}

	penalty := antiAffinityPenaltyScore(n, spec)
	if penalty != 1.0 {
		t.Errorf("all labels matched: expected penalty 1.0, got %v", penalty)
	}
}

// TestGUC_Scorer_PenaltyCappedScoreNonNegative asserts that even with maximum
// anti-affinity weight the final score from computeScore never goes below 0.
func TestGUC_Scorer_PenaltyCappedScoreNonNegative(t *testing.T) {
	n := readyNode(4, 4, 4096, 4096)
	n.Labels = map[string]string{"app": "api"}
	n.State = domain.NodeReady

	spec := simpleSpec()
	spec.Placement.AntiAffinityLabels = map[string]string{"app": "api"}

	// Extremely heavy anti-affinity weight
	w := DefaultWeights()
	w.AntiAffinity = 999.0

	score := computeScore(n, spec, nil, w)
	if score < 0 {
		t.Errorf("score should be clamped to >= 0; got %v", score)
	}
}
