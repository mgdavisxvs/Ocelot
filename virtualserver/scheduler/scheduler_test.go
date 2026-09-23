package scheduler

import (
	"context"
	"fmt"
	"testing"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// mockCatalog returns a fixed ArtifactStatus for all lookups.
type mockCatalog struct {
	available bool
	exists    bool
}

func (m *mockCatalog) Lookup(_ context.Context, _ string) (domain.ArtifactStatus, error) {
	avail := domain.ArtifactUnavailable
	if m.available {
		avail = domain.ArtifactAvailable
	}
	return domain.ArtifactStatus{Exists: m.exists, Available: m.available, Availability: avail}, nil
}

func newTestManifest(arch string, requiredLabels map[string]string, ramMiB int64, gpuRequired bool, minVRAM int64) domain.ServiceManifest {
	return domain.ServiceManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Service",
		Metadata:   domain.ServiceMetadata{Namespace: "test", Name: "svc"},
		Spec: domain.ServiceSpec{
			Artifact:  domain.ArtifactReference{Type: domain.ArtifactTypeOcelot, InfoHash: "0123456789abcdef0123456789abcdef01234567"},
			Runtime:   "mock",
			Instances: 1,
			Resources: domain.ResourceRequest{
				RAMMiB: ramMiB,
				GPU:    domain.GPURequest{Required: gpuRequired, Count: 1, MinVRAMMiB: minVRAM},
			},
			Placement: domain.PlacementPolicy{
				RequiredArch:   arch,
				RequiredLabels: requiredLabels,
			},
		},
	}
}

// ── Acceptance scenario nodes ─────────────────────────────────────────────────

func makeAcceptanceNodes() []domain.Node {
	return []domain.Node{
		{
			ID: "n1", Name: "r640-core-01", Arch: "x86_64",
			TotalRAMMiB: 768 * 1024, AvailRAMMiB: 768 * 1024, CPUThreads: 80,
			GPUDevices: []domain.GPUDevice{
				{Index: 0, Vendor: "nvidia", Model: "p4", VRAMMiB: 8192},
				{Index: 1, Vendor: "nvidia", Model: "p4", VRAMMiB: 8192},
			},
			Labels: map[string]string{"role": "control", "location": "lab-01"},
			State:  domain.NodeReady,
		},
		{
			ID: "n2", Name: "r730-v100-01", Arch: "x86_64",
			TotalRAMMiB: 768 * 1024, AvailRAMMiB: 768 * 1024, CPUThreads: 56,
			GPUDevices: []domain.GPUDevice{
				{Index: 0, Vendor: "nvidia", Model: "v100", VRAMMiB: 16384},
				{Index: 1, Vendor: "nvidia", Model: "v100", VRAMMiB: 16384},
			},
			Labels: map[string]string{"role": "inference", "location": "lab-01"},
			State:  domain.NodeReady,
		},
		{
			ID: "n3", Name: "ironbox-01", Arch: "arm64",
			TotalRAMMiB: 32768, AvailRAMMiB: 32768, CPUThreads: 8,
			GPUDevices: []domain.GPUDevice{
				{Index: 0, Vendor: "edge", Model: "edge-gpu", VRAMMiB: 4096},
			},
			Labels: map[string]string{"role": "edge", "sovereign": "true"},
			State:  domain.NodeReady,
		},
	}
}

func TestScheduler_AcceptanceScenario(t *testing.T) {
	sched := New(DefaultWeights())
	nodes := makeAcceptanceNodes()
	manifest := newTestManifest("x86_64", map[string]string{"role": "inference"}, 32768, true, 16384)

	decision, err := sched.Schedule(context.Background(), manifest, nodes, &mockCatalog{available: true, exists: true})
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}

	// r730-v100-01 must be selected
	if decision.SelectedNodeID != "n2" {
		t.Errorf("expected n2 (r730-v100-01), got %q", decision.SelectedNodeID)
	}

	// r640-core-01 must be rejected with correct reasons
	r1 := decision.RejectionReasons["r640-core-01"]
	if r1 == nil {
		t.Error("r640-core-01 should have rejection reasons")
	} else {
		assertContains(t, r1, "required label role=inference not present", "r640-core-01 label rejection")
		assertContains(t, r1, "no individual GPU satisfies 16384 MiB VRAM requirement", "r640-core-01 GPU rejection")
	}

	// ironbox-01 must be rejected with arch + label mismatch
	r3 := decision.RejectionReasons["ironbox-01"]
	if r3 == nil {
		t.Error("ironbox-01 should have rejection reasons")
	} else {
		assertContains(t, r3, "architecture mismatch", "ironbox-01 arch rejection")
		assertContains(t, r3, "required label role=inference not present", "ironbox-01 label rejection")
	}
}

func assertContains(t *testing.T, reasons []string, substr, context string) {
	t.Helper()
	for _, r := range reasons {
		if len(r) >= len(substr) {
			// substring check
			for i := 0; i <= len(r)-len(substr); i++ {
				if r[i:i+len(substr)] == substr {
					return
				}
			}
		}
	}
	t.Errorf("%s: expected a reason containing %q in %v", context, substr, reasons)
}

func TestScheduler_NoReadyNodes(t *testing.T) {
	sched := New(DefaultWeights())
	nodes := []domain.Node{
		{ID: "n1", Name: "node1", Arch: "x86_64", State: domain.NodeDraining,
			TotalRAMMiB: 1024, AvailRAMMiB: 1024},
	}
	manifest := newTestManifest("x86_64", nil, 512, false, 0)
	_, err := sched.Schedule(context.Background(), manifest, nodes, nil)
	if err == nil {
		t.Error("expected error when no ready nodes")
	}
}

func TestScheduler_InsufficientRAM(t *testing.T) {
	sched := New(DefaultWeights())
	nodes := []domain.Node{
		{ID: "n1", Name: "node1", Arch: "x86_64", State: domain.NodeReady,
			TotalRAMMiB: 8192, AvailRAMMiB: 8192},
	}
	manifest := newTestManifest("x86_64", nil, 16384, false, 0)
	_, err := sched.Schedule(context.Background(), manifest, nodes, nil)
	if err == nil {
		t.Error("expected error for insufficient RAM")
	}
}

func TestScheduler_GPUVRAMNotSummed(t *testing.T) {
	// Two 8 GB GPUs do NOT satisfy a 16 GB per-device requirement
	sched := New(DefaultWeights())
	nodes := []domain.Node{
		{
			ID: "n1", Name: "node1", Arch: "x86_64", State: domain.NodeReady,
			TotalRAMMiB: 65536, AvailRAMMiB: 65536, CPUThreads: 16,
			GPUDevices: []domain.GPUDevice{
				{Index: 0, VRAMMiB: 8192},
				{Index: 1, VRAMMiB: 8192},
			},
		},
	}
	manifest := newTestManifest("x86_64", nil, 32768, true, 16384)
	_, err := sched.Schedule(context.Background(), manifest, nodes, nil)
	if err == nil {
		t.Error("expected rejection: two 8GB GPUs should not satisfy 16GB per-device requirement")
	}
	// Verify the rejection reason
	d, _ := sched.Schedule(context.Background(), manifest, nodes, nil)
	if d != nil {
		reasons := d.RejectionReasons["node1"]
		assertContains(t, reasons, "no individual GPU satisfies 16384 MiB VRAM requirement", "vram pooling check")
	}
}

func TestScheduler_ExcludedNode(t *testing.T) {
	sched := New(DefaultWeights())
	nodes := []domain.Node{
		{ID: "n1", Name: "node1", Arch: "x86_64", State: domain.NodeReady,
			TotalRAMMiB: 65536, AvailRAMMiB: 65536, CPUThreads: 16},
	}
	m := newTestManifest("x86_64", nil, 1024, false, 0)
	m.Spec.Placement.ExcludedNodes = []string{"node1"}
	_, err := sched.Schedule(context.Background(), m, nodes, nil)
	if err == nil {
		t.Error("expected error when only node is excluded")
	}
}

func TestScheduler_PreferredNode(t *testing.T) {
	sched := New(DefaultWeights())
	nodes := []domain.Node{
		{ID: "n1", Name: "preferred", Arch: "x86_64", State: domain.NodeReady,
			TotalRAMMiB: 65536, AvailRAMMiB: 65536, CPUThreads: 16},
		{ID: "n2", Name: "other", Arch: "x86_64", State: domain.NodeReady,
			TotalRAMMiB: 65536, AvailRAMMiB: 65536, CPUThreads: 16},
	}
	m := newTestManifest("x86_64", nil, 1024, false, 0)
	m.Spec.Placement.PreferredNodes = []string{"preferred"}
	d, err := sched.Schedule(context.Background(), m, nodes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.SelectedNodeID != "n1" {
		t.Errorf("expected preferred node n1, got %q", d.SelectedNodeID)
	}
}

func TestScheduler_Deterministic(t *testing.T) {
	sched := New(DefaultWeights())
	// Two identical nodes — tie broken by name lexicographic order
	nodes := []domain.Node{
		{ID: "n2", Name: "z-node", Arch: "x86_64", State: domain.NodeReady,
			TotalRAMMiB: 65536, AvailRAMMiB: 65536, CPUThreads: 16},
		{ID: "n1", Name: "a-node", Arch: "x86_64", State: domain.NodeReady,
			TotalRAMMiB: 65536, AvailRAMMiB: 65536, CPUThreads: 16},
	}
	m := newTestManifest("x86_64", nil, 1024, false, 0)
	for i := 0; i < 5; i++ {
		d, err := sched.Schedule(context.Background(), m, nodes, nil)
		if err != nil {
			t.Fatal(err)
		}
		if d.SelectedNodeID != "n1" {
			t.Errorf("iter %d: expected n1 (a-node) via tie-break, got %q", i, d.SelectedNodeID)
		}
	}
}

func TestScheduler_EmptyNodeList(t *testing.T) {
	sched := New(DefaultWeights())
	m := newTestManifest("x86_64", nil, 1024, false, 0)
	_, err := sched.Schedule(context.Background(), m, nil, nil)
	if err == nil {
		t.Error("expected error for empty node list")
	}
}

// BenchmarkScheduler_100Nodes measures placement throughput against a 100-node fleet.
func BenchmarkScheduler_100Nodes(b *testing.B) {
	nodes := make([]domain.Node, 100)
	for i := range nodes {
		nodes[i] = domain.Node{
			ID:          fmt.Sprintf("n%03d", i),
			Arch:        "x86_64",
			State:       domain.NodeReady,
			TotalRAMMiB: 65536,
			AvailRAMMiB: 65536,
			CPUThreads:  32,
		}
	}
	manifest := newTestManifest("x86_64", nil, 2048, false, 0)
	cat := &mockCatalog{available: true, exists: true}
	sched := New(DefaultWeights())
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sched.Schedule(ctx, manifest, nodes, cat); err != nil {
			b.Fatal(err)
		}
	}
}
