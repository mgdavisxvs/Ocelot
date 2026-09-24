package virtualserver

import (
	"path/filepath"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func readyNode(id string, cpu int, ram int64, gpus ...GPU) *Node {
	return &Node{
		ID: id, Name: "node-" + id, Arch: "amd64",
		State:         NodeStateReady,
		CPUMillicores: cpu, RAMBytes: ram,
		Labels:        map[string]string{"region": "us"},
		GPUs:          gpus,
		LastHeartbeat: time.Now().UTC(),
		CreatedAt:     time.Now().UTC(),
	}
}

func basicSvc(cpu int, ram int64) *Service {
	now := time.Now().UTC()
	return &Service{
		ID: "svc1", Namespace: "default", Name: "test-svc",
		Image: "img:latest", CPUMillicores: cpu, RAMBytes: ram,
		RestartPolicy: RestartOnFailure,
		NodeSelector:  map[string]string{},
		CreatedAt: now, UpdatedAt: now,
	}
}

func schedDB(t *testing.T) *db {
	t.Helper()
	d, err := openDB(filepath.Join(t.TempDir(), "vs.db"))
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	d.createNamespace(&Namespace{Name: "default", CreatedAt: time.Now().UTC()})
	t.Cleanup(func() { d.close() })
	return d
}

// ── constraint 1: node state ──────────────────────────────────────────────────

func TestScheduler_RejectsNotReadyNode(t *testing.T) {
	d := schedDB(t)
	n := readyNode("n1", 4000, 8<<30)
	n.State = NodeStateDraining
	d.upsertNode(n)
	d.upsertService(basicSvc(100, 128<<20))

	svc, _ := d.getService("svc1")
	sched := newScheduler(d)
	if _, err := sched.Schedule(svc); err == nil {
		t.Fatal("expected error: node is draining")
	}
}

// ── constraint 2: arch ───────────────────────────────────────────────────────

func TestScheduler_RejectsArchMismatch(t *testing.T) {
	d := schedDB(t)
	n := readyNode("n1", 4000, 8<<30)
	n.Arch = "arm64"
	d.upsertNode(n)
	svc := basicSvc(100, 128<<20)
	svc.NodeSelector = map[string]string{"arch": "amd64"}
	d.upsertService(svc)

	svc2, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc2); err == nil {
		t.Fatal("expected arch mismatch error")
	}
}

func TestScheduler_AcceptsArchMatch(t *testing.T) {
	d := schedDB(t)
	n := readyNode("n1", 4000, 8<<30)
	n.Arch = "arm64"
	d.upsertNode(n)
	svc := basicSvc(100, 128<<20)
	svc.NodeSelector = map[string]string{"arch": "arm64"}
	d.upsertService(svc)

	svc2, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc2); err != nil {
		t.Fatalf("expected placement: %v", err)
	}
}

// ── constraint 3: label selectors ────────────────────────────────────────────

func TestScheduler_RejectsLabelMismatch(t *testing.T) {
	d := schedDB(t)
	n := readyNode("n1", 4000, 8<<30)
	n.Labels = map[string]string{"region": "eu"}
	d.upsertNode(n)
	svc := basicSvc(100, 128<<20)
	svc.NodeSelector = map[string]string{"region": "us"}
	d.upsertService(svc)

	svc2, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc2); err == nil {
		t.Fatal("expected label mismatch error")
	}
}

// ── constraint 4: node exclusions ────────────────────────────────────────────

func TestScheduler_RejectsExcludedNode(t *testing.T) {
	d := schedDB(t)
	d.upsertNode(readyNode("n1", 4000, 8<<30))
	svc := basicSvc(100, 128<<20)
	svc.NodeExclusions = []string{"n1"}
	d.upsertService(svc)

	svc2, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc2); err == nil {
		t.Fatal("expected exclusion error")
	}
}

func TestScheduler_ExclusionByName(t *testing.T) {
	d := schedDB(t)
	d.upsertNode(readyNode("n1", 4000, 8<<30)) // Name = "node-n1"
	svc := basicSvc(100, 128<<20)
	svc.NodeExclusions = []string{"node-n1"}
	d.upsertService(svc)

	svc2, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc2); err == nil {
		t.Fatal("expected exclusion-by-name error")
	}
}

// ── constraint 5: CPU headroom ────────────────────────────────────────────────

func TestScheduler_RejectsInsufficientCPU(t *testing.T) {
	d := schedDB(t)
	d.upsertNode(readyNode("n1", 500, 8<<30))
	d.upsertService(basicSvc(1000, 128<<20))

	svc, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc); err == nil {
		t.Fatal("expected CPU headroom error")
	}
}

// ── constraint 6: RAM headroom ────────────────────────────────────────────────

func TestScheduler_RejectsInsufficientRAM(t *testing.T) {
	d := schedDB(t)
	d.upsertNode(readyNode("n1", 4000, 256<<20))
	d.upsertService(basicSvc(100, 512<<20))

	svc, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc); err == nil {
		t.Fatal("expected RAM headroom error")
	}
}

// ── constraint 7: GPU VRAM ────────────────────────────────────────────────────

func TestScheduler_RejectsInsufficientGPU(t *testing.T) {
	d := schedDB(t)
	gpu := GPU{DeviceID: "g0", Model: "T4", VRAMBytes: 16 << 30}
	d.upsertNode(readyNode("n1", 4000, 8<<30, gpu))
	svc := basicSvc(100, 128<<20)
	svc.GPUs = []GPURequirement{{Count: 1, VRAMPerDevice: 80 << 30, ModelFilter: "A100"}}
	d.upsertService(svc)

	svc2, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc2); err == nil {
		t.Fatal("expected GPU VRAM constraint error")
	}
}

func TestScheduler_AcceptsMatchingGPU(t *testing.T) {
	d := schedDB(t)
	gpu := GPU{DeviceID: "g0", Model: "A100", VRAMBytes: 80 << 30}
	d.upsertNode(readyNode("n1", 4000, 8<<30, gpu))
	svc := basicSvc(100, 128<<20)
	svc.GPUs = []GPURequirement{{Count: 1, VRAMPerDevice: 40 << 30, ModelFilter: "A100"}}
	d.upsertService(svc)

	svc2, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc2); err != nil {
		t.Fatalf("expected placement: %v", err)
	}
}

// ── constraint 8: volume affinity ────────────────────────────────────────────

func TestScheduler_VolumeAffinityConstraint(t *testing.T) {
	d := schedDB(t)
	d.upsertNode(readyNode("n1", 4000, 8<<30))
	d.upsertNode(readyNode("n2", 4000, 8<<30))
	d.upsertVolume(&Volume{ID: "v1", Namespace: "default", Name: "data", DriverName: "local", NodeAffinity: "n1", CreatedAt: time.Now().UTC()})

	svc := basicSvc(100, 128<<20)
	svc.VolumeMounts = []VolumeMount{{Name: "data", VolumeID: "v1", MountPath: "/data"}}
	d.upsertService(svc)

	svc2, _ := d.getService("svc1")
	dec, err := newScheduler(d).Schedule(svc2)
	if err != nil {
		t.Fatalf("expected placement: %v", err)
	}
	if dec.NodeID != "n1" {
		t.Fatalf("expected placement on n1 (affinity), got %s", dec.NodeID)
	}
}

func TestScheduler_ConflictingVolumeAffinity(t *testing.T) {
	d := schedDB(t)
	d.upsertNode(readyNode("n1", 4000, 8<<30))
	d.upsertVolume(&Volume{ID: "v1", Namespace: "default", Name: "d1", DriverName: "local", NodeAffinity: "n1", CreatedAt: time.Now().UTC()})
	d.upsertVolume(&Volume{ID: "v2", Namespace: "default", Name: "d2", DriverName: "local", NodeAffinity: "n2", CreatedAt: time.Now().UTC()})

	svc := basicSvc(100, 128<<20)
	svc.VolumeMounts = []VolumeMount{
		{Name: "a", VolumeID: "v1", MountPath: "/a"},
		{Name: "b", VolumeID: "v2", MountPath: "/b"},
	}
	d.upsertService(svc)

	svc2, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc2); err == nil {
		t.Fatal("expected error for conflicting volume affinity")
	}
}

// ── Schedule: best candidate selection ───────────────────────────────────────

func TestScheduler_PicksHighestScore(t *testing.T) {
	d := schedDB(t)
	// n1: barely enough RAM, n2: plentiful
	d.upsertNode(readyNode("n1", 4000, 512<<20))
	d.upsertNode(readyNode("n2", 4000, 8<<30))
	d.upsertService(basicSvc(100, 256<<20))

	svc, _ := d.getService("svc1")
	dec, err := newScheduler(d).Schedule(svc)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	// n1 has less free RAM → higher utilization → better capacity score
	if dec.NodeID != "n1" {
		t.Fatalf("expected n1 (tighter fit), got %s", dec.NodeID)
	}
}

func TestScheduler_NoNodes(t *testing.T) {
	d := schedDB(t)
	d.upsertService(basicSvc(100, 128<<20))
	svc, _ := d.getService("svc1")
	if _, err := newScheduler(d).Schedule(svc); err == nil {
		t.Fatal("expected error with no nodes")
	}
}

// ── gpuConstraintSatisfied ────────────────────────────────────────────────────

func TestGPUConstraintSatisfied(t *testing.T) {
	gpus := []GPU{
		{DeviceID: "g0", Model: "A100", VRAMBytes: 80 << 30},
		{DeviceID: "g1", Model: "A100", VRAMBytes: 80 << 30},
		{DeviceID: "g2", Model: "T4", VRAMBytes: 16 << 30},
	}

	// All free, 2 A100s available: satisfied.
	ok, _ := gpuConstraintSatisfied(gpus, map[string]struct{}{}, GPURequirement{Count: 2, ModelFilter: "A100"})
	if !ok {
		t.Fatal("expected satisfied")
	}

	// g0 in use: only 1 A100 free, requesting 2: unsatisfied.
	ok, reason := gpuConstraintSatisfied(gpus, map[string]struct{}{"g0": {}}, GPURequirement{Count: 2, ModelFilter: "A100"})
	if ok {
		t.Fatal("expected unsatisfied when GPU in use")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}

	// VRAM filter: T4 (16GiB) can't satisfy 40GiB requirement.
	ok, _ = gpuConstraintSatisfied(gpus, map[string]struct{}{}, GPURequirement{Count: 1, VRAMPerDevice: 40 << 30, ModelFilter: "T4"})
	if ok {
		t.Fatal("T4 VRAM too small")
	}

	// No model filter, just count.
	ok, _ = gpuConstraintSatisfied(gpus, map[string]struct{}{}, GPURequirement{Count: 3})
	if !ok {
		t.Fatal("3 free GPUs should satisfy count=3")
	}

	// Requiring 4 when only 3 exist.
	ok, _ = gpuConstraintSatisfied(gpus, map[string]struct{}{}, GPURequirement{Count: 4})
	if ok {
		t.Fatal("count=4 should be unsatisfied")
	}
}

// ── scoreHealth ───────────────────────────────────────────────────────────────

func TestScoreHealth(t *testing.T) {
	d := schedDB(t)
	s := newScheduler(d)

	freshNode := readyNode("n1", 1000, 1<<30)
	freshNode.LastHeartbeat = time.Now()
	if score := s.scoreHealth(freshNode); score != 1.0 {
		t.Fatalf("fresh node: score=%f want 1.0", score)
	}

	staleNode := readyNode("n2", 1000, 1<<30)
	staleNode.LastHeartbeat = time.Now().Add(-200 * time.Second)
	if score := s.scoreHealth(staleNode); score != 0.0 {
		t.Fatalf("stale node: score=%f want 0.0", score)
	}

	midNode := readyNode("n3", 1000, 1<<30)
	midNode.LastHeartbeat = time.Now().Add(-75 * time.Second)
	mid := s.scoreHealth(midNode)
	if mid <= 0 || mid >= 1 {
		t.Fatalf("mid-age node: score=%f want (0,1)", mid)
	}
}

// ── scoreReliability ──────────────────────────────────────────────────────────

func TestScoreReliability(t *testing.T) {
	d := schedDB(t)
	d.upsertNode(readyNode("n1", 1000, 1<<30))
	d.createNamespace(&Namespace{Name: "ns", CreatedAt: time.Now().UTC()})

	now := time.Now().UTC()
	// 0 failures → 1.0
	if score := newScheduler(d).scoreReliability(readyNode("n1", 0, 0)); score != 1.0 {
		t.Fatalf("0 failures: score=%f", score)
	}

	// Inject 3 failures.
	d.upsertService(sampleService("s1", "default"))
	for i := 0; i < 3; i++ {
		id := "i" + string(rune('0'+i))
		inst := &Instance{ID: id, ServiceID: "s1", Namespace: "default", NodeID: "n1", State: InstanceStateFailed, CreatedAt: now, UpdatedAt: now}
		d.upsertInstance(inst)
	}
	n := readyNode("n1", 0, 0)
	score := newScheduler(d).scoreReliability(n)
	if score != 0.4 {
		t.Fatalf("3 failures: score=%f want 0.4", score)
	}
}

// ── clamp01 ───────────────────────────────────────────────────────────────────

func TestClamp01(t *testing.T) {
	if clamp01(-0.5) != 0 {
		t.Fatal("below 0")
	}
	if clamp01(1.5) != 1 {
		t.Fatal("above 1")
	}
	if clamp01(0.5) != 0.5 {
		t.Fatal("mid value")
	}
}
