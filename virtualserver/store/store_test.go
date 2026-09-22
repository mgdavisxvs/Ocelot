package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/store"
)

func openStore(t *testing.T) *store.VSStore {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "vs_test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func makeNode(name string) domain.Node {
	return domain.Node{
		Name:        name,
		Arch:        "x86_64",
		State:       domain.NodeReady,
		TotalRAMMiB: 16384,
		AvailRAMMiB: 16384,
		CPUThreads:  8,
	}
}

func makeManifest(ns, name string) domain.ServiceManifest {
	return domain.ServiceManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Service",
		Metadata:   domain.ServiceMetadata{Namespace: ns, Name: name},
		Spec: domain.ServiceSpec{
			Artifact:  domain.ArtifactReference{Type: domain.ArtifactTypeOcelot, InfoHash: "0123456789abcdef0123456789abcdef01234567"},
			Runtime:   "mock",
			Instances: 1,
			Resources: domain.ResourceRequest{RAMMiB: 1024, CPUThreads: 2},
			Restart:   domain.RestartPolicy{Policy: "on-failure", MaximumAttempts: 3},
		},
	}
}

// ── migrations ────────────────────────────────────────────────────────────────

func TestStore_OpenAppliesMigrations(t *testing.T) {
	s := openStore(t)
	// DB is queryable after open — schema must be present.
	nodes, err := s.ListNodes(context.Background(), "")
	if err != nil {
		t.Fatalf("ListNodes after open: %v", err)
	}
	if nodes == nil {
		nodes = []domain.Node{}
	}
	// Empty but not an error — migrations ran successfully.
	_ = nodes
}

func TestStore_IdempotentOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vs.db")
	s1, err := store.Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	s1.Close()

	s2, err := store.Open(path)
	if err != nil {
		t.Fatalf("second open (migration re-run): %v", err)
	}
	s2.Close()
}

// ── namespace ─────────────────────────────────────────────────────────────────

func TestStore_Namespace_CreateList(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	id, err := s.CreateNamespace(ctx, "prod", "production")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive id, got %d", id)
	}

	ns, err := s.ListNamespaces(ctx)
	if err != nil {
		t.Fatalf("ListNamespaces: %v", err)
	}
	found := false
	for _, n := range ns {
		if n == "prod" {
			found = true
		}
	}
	if !found {
		t.Errorf("namespace 'prod' not in list: %v", ns)
	}
}

func TestStore_Namespace_DuplicateReturnsConflict(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if _, err := s.CreateNamespace(ctx, "ns-dup", ""); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := s.CreateNamespace(ctx, "ns-dup", "")
	if err == nil {
		t.Fatal("expected ErrConflict, got nil")
	}
}

// ── node ──────────────────────────────────────────────────────────────────────

func TestStore_Node_CreateGet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	id, err := s.CreateNode(ctx, makeNode("alpha"))
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	n, err := s.GetNode(ctx, id)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if n.Name != "alpha" {
		t.Errorf("name: want alpha, got %q", n.Name)
	}
	if n.State != domain.NodeReady {
		t.Errorf("initial state: want ready (as passed to CreateNode), got %q", n.State)
	}
}

func TestStore_Node_List(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	for _, name := range []string{"n1", "n2", "n3"} {
		if _, err := s.CreateNode(ctx, makeNode(name)); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	nodes, err := s.ListNodes(ctx, "")
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(nodes))
	}
}

func TestStore_Node_ListFiltered(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	id, _ := s.CreateNode(ctx, makeNode("ready-node"))
	// Transition discovered → ready via explicit state update.
	// NodeDiscovered → NodeReady requires going through NodeOnline first
	// per domain.ValidateNodeTransition. Skip the validation path by using
	// internal transition via Heartbeat which sets online, then UpdateNodeState.
	s.Heartbeat(ctx, id, 16384, 8)

	nodes, err := s.ListNodes(ctx, string(domain.NodeReady))
	if err != nil {
		t.Fatalf("ListNodes filtered: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("expected 1 ready node, got %d", len(nodes))
	}
}

func TestStore_Node_NotFound(t *testing.T) {
	s := openStore(t)
	_, err := s.GetNode(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
}

func TestStore_Node_DuplicateNameConflict(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if _, err := s.CreateNode(ctx, makeNode("dup")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := s.CreateNode(ctx, makeNode("dup"))
	if err == nil {
		t.Fatal("expected ErrConflict on duplicate name")
	}
}

// ── service ───────────────────────────────────────────────────────────────────

func TestStore_Service_CreateGet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if _, err := s.CreateNamespace(ctx, "ns1", ""); err != nil {
		t.Fatalf("ns: %v", err)
	}
	id, err := s.CreateService(ctx, makeManifest("ns1", "svc-a"))
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	svc, err := s.GetService(ctx, id)
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}
	if svc.Manifest.Metadata.Name != "svc-a" {
		t.Errorf("name: want svc-a, got %q", svc.Manifest.Metadata.Name)
	}
	if svc.DesiredCount != 1 {
		t.Errorf("desired count: want 1, got %d", svc.DesiredCount)
	}
}

func TestStore_Service_ListByNamespace(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	s.CreateNamespace(ctx, "nsA", "")
	s.CreateNamespace(ctx, "nsB", "")
	s.CreateService(ctx, makeManifest("nsA", "svc1"))
	s.CreateService(ctx, makeManifest("nsA", "svc2"))
	s.CreateService(ctx, makeManifest("nsB", "svc3"))

	svcsA, err := s.ListServices(ctx, "nsA")
	if err != nil {
		t.Fatalf("ListServices nsA: %v", err)
	}
	if len(svcsA) != 2 {
		t.Errorf("expected 2 services in nsA, got %d", len(svcsA))
	}

	all, _ := s.ListServices(ctx, "")
	if len(all) != 3 {
		t.Errorf("expected 3 total, got %d", len(all))
	}
}

func TestStore_Service_Delete(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	s.CreateNamespace(ctx, "ns", "")
	id, _ := s.CreateService(ctx, makeManifest("ns", "del-svc"))

	if err := s.DeleteService(ctx, id); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	if _, err := s.GetService(ctx, id); err == nil {
		t.Error("expected ErrNotFound after delete")
	}
}

func TestStore_Service_NotFound(t *testing.T) {
	s := openStore(t)
	_, err := s.GetService(context.Background(), 99999)
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
}

// ── instance ──────────────────────────────────────────────────────────────────

func TestStore_Instance_CreateList(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	s.CreateNamespace(ctx, "ns", "")
	svcID, _ := s.CreateService(ctx, makeManifest("ns", "inst-svc"))

	vsPath := domain.VSPath{Namespace: "ns", Service: "inst-svc", Instance: "0"}
	instID, err := s.CreateInstance(ctx, svcID, vsPath)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	inst, err := s.GetInstance(ctx, instID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.State != domain.InstanceDeclared {
		t.Errorf("initial state: want declared, got %q", inst.State)
	}
	if inst.ServiceID != svcID {
		t.Errorf("service ID: want %d, got %d", svcID, inst.ServiceID)
	}
}

func TestStore_Instance_StateTransitions(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	s.CreateNamespace(ctx, "ns", "")
	svcID, _ := s.CreateService(ctx, makeManifest("ns", "st-svc"))
	instID, _ := s.CreateInstance(ctx, svcID, domain.VSPath{Namespace: "ns", Service: "st-svc", Instance: "0"})

	transitions := []domain.InstanceState{
		domain.InstanceScheduled,
		domain.InstanceProvisioning,
		domain.InstanceStarting,
		domain.InstanceRunning,
	}
	for _, to := range transitions {
		if err := s.UpdateInstanceState(ctx, instID, to); err != nil {
			t.Fatalf("UpdateInstanceState → %s: %v", to, err)
		}
		inst, _ := s.GetInstance(ctx, instID)
		if inst.State != to {
			t.Errorf("expected state %q, got %q", to, inst.State)
		}
	}
}

func TestStore_Instance_AssignNode(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	nodeID, _ := s.CreateNode(ctx, makeNode("assign-node"))
	s.CreateNamespace(ctx, "ns", "")
	svcID, _ := s.CreateService(ctx, makeManifest("ns", "assign-svc"))
	instID, _ := s.CreateInstance(ctx, svcID, domain.VSPath{Namespace: "ns", Service: "assign-svc", Instance: "0"})

	if err := s.AssignNode(ctx, instID, nodeID); err != nil {
		t.Fatalf("AssignNode: %v", err)
	}
	inst, _ := s.GetInstance(ctx, instID)
	if inst.NodeID != nodeID {
		t.Errorf("node ID: want %s, got %s", nodeID, inst.NodeID)
	}
	if inst.State != domain.InstanceScheduled {
		t.Errorf("state after assign: want scheduled, got %q", inst.State)
	}
}

func TestStore_Instance_RetryCount(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	s.CreateNamespace(ctx, "ns", "")
	svcID, _ := s.CreateService(ctx, makeManifest("ns", "retry-svc"))
	instID, _ := s.CreateInstance(ctx, svcID, domain.VSPath{Namespace: "ns", Service: "retry-svc", Instance: "0"})

	for i := 1; i <= 3; i++ {
		if err := s.IncrementRetryCount(ctx, instID); err != nil {
			t.Fatalf("IncrementRetryCount pass %d: %v", i, err)
		}
		inst, _ := s.GetInstance(ctx, instID)
		if inst.RetryCount != i {
			t.Errorf("retry count: want %d, got %d", i, inst.RetryCount)
		}
	}
}

// ── allocation ────────────────────────────────────────────────────────────────

func TestStore_Allocation_AllocateRelease(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	nodeID, _ := s.CreateNode(ctx, makeNode("alloc-node"))
	s.CreateNamespace(ctx, "ns", "")
	svcID, _ := s.CreateService(ctx, makeManifest("ns", "alloc-svc"))
	instID, _ := s.CreateInstance(ctx, svcID, domain.VSPath{Namespace: "ns", Service: "alloc-svc", Instance: "0"})

	if err := s.AllocateResources(ctx, instID, nodeID, 2, 1024, nil); err != nil {
		t.Fatalf("AllocateResources: %v", err)
	}

	alloc, err := s.GetActiveAllocation(ctx, instID)
	if err != nil {
		t.Fatalf("GetActiveAllocation: %v", err)
	}
	if alloc.RAMMiB != 1024 {
		t.Errorf("RAM: want 1024, got %d", alloc.RAMMiB)
	}
	if alloc.CPUThreads != 2 {
		t.Errorf("CPU threads: want 2, got %d", alloc.CPUThreads)
	}
	if alloc.NodeID != nodeID {
		t.Errorf("node: want %s, got %s", nodeID, alloc.NodeID)
	}

	if err := s.ReleaseAllocation(ctx, instID); err != nil {
		t.Fatalf("ReleaseAllocation: %v", err)
	}
	if _, err := s.GetActiveAllocation(ctx, instID); err == nil {
		t.Error("expected ErrNotFound after release")
	}
}

func TestStore_Allocation_InsufficientRAM(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	nodeID, _ := s.CreateNode(ctx, makeNode("small-node")) // 16384 MiB
	s.CreateNamespace(ctx, "ns", "")
	svcID, _ := s.CreateService(ctx, makeManifest("ns", "big-svc"))
	instID, _ := s.CreateInstance(ctx, svcID, domain.VSPath{Namespace: "ns", Service: "big-svc", Instance: "0"})

	err := s.AllocateResources(ctx, instID, nodeID, 2, 999999, nil) // far exceeds node RAM
	if err == nil {
		t.Fatal("expected allocation error for oversized RAM request")
	}
}

// ── audit log ─────────────────────────────────────────────────────────────────

func TestStore_AuditLog_WriteList(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.WriteAuditLog(ctx, "scheduled", "instance", "inst-99", "127.0.0.1", true, ""); err != nil {
		t.Fatalf("WriteAuditLog: %v", err)
	}
	if err := s.WriteAuditLog(ctx, "started", "instance", "inst-99", "", true, ""); err != nil {
		t.Fatalf("WriteAuditLog 2: %v", err)
	}

	events, err := s.ListAuditEvents(ctx, "instance", "inst-99")
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 audit events, got %d", len(events))
	}
}

// ── runtime handle ────────────────────────────────────────────────────────────

func TestStore_UpdateRuntimeHandle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	s.CreateNamespace(ctx, "ns", "")
	svcID, _ := s.CreateService(ctx, makeManifest("ns", "handle-svc"))
	instID, _ := s.CreateInstance(ctx, svcID, domain.VSPath{Namespace: "ns", Service: "handle-svc", Instance: "0"})

	handle := map[string]string{"container_id": "abc123", "pid": "42"}
	if err := s.UpdateRuntimeHandle(ctx, instID, handle); err != nil {
		t.Fatalf("UpdateRuntimeHandle: %v", err)
	}

	inst, _ := s.GetInstance(ctx, instID)
	if inst.RuntimeHandle["container_id"] != "abc123" {
		t.Errorf("handle[container_id]: want abc123, got %q", inst.RuntimeHandle["container_id"])
	}
}

// ── idempotency ───────────────────────────────────────────────────────────────

func TestStore_Idempotency_CheckStore(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	key := "req-key-001"
	_, found, err := s.CheckIdempotency(ctx, key)
	if err != nil {
		t.Fatalf("CheckIdempotency (miss): %v", err)
	}
	if found {
		t.Fatal("expected not found on first check")
	}

	if err := s.StoreIdempotency(ctx, key, `{"id":1}`, 201); err != nil {
		t.Fatalf("StoreIdempotency: %v", err)
	}

	rec, found, err := s.CheckIdempotency(ctx, key)
	if err != nil {
		t.Fatalf("CheckIdempotency (hit): %v", err)
	}
	if !found {
		t.Fatal("expected hit on second check")
	}
	if rec.StatusCode != 201 {
		t.Errorf("status code: want 201, got %d", rec.StatusCode)
	}
}

// ── WAL checkpoint ────────────────────────────────────────────────────────────

func TestStore_CheckpointWAL(t *testing.T) {
	s := openStore(t)
	// Write some data to ensure the WAL has content.
	s.CreateNode(context.Background(), makeNode("wal-node"))

	if err := s.CheckpointWAL(); err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}
}

// ── concurrent writes ─────────────────────────────────────────────────────────

func TestStore_ConcurrentNodeCreation(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	const n = 20

	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("concurrent-node-%02d", i)
		go func(name string) {
			_, err := s.CreateNode(ctx, makeNode(name))
			errs <- err
		}(name)
	}

	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent create: %v", err)
		}
	}

	nodes, _ := s.ListNodes(ctx, "")
	if len(nodes) != n {
		t.Errorf("expected %d nodes, got %d", n, len(nodes))
	}
}

// ── Node store extras ─────────────────────────────────────────────────────────

func TestStore_Node_GetByName(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	id, err := s.CreateNode(ctx, makeNode("byname-node"))
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	n, err := s.GetNodeByName(ctx, "byname-node")
	if err != nil {
		t.Fatalf("GetNodeByName: %v", err)
	}
	if n.ID != id {
		t.Errorf("id mismatch: got %q want %q", n.ID, id)
	}
}

func TestStore_Node_GetByName_NotFound(t *testing.T) {
	s := openStore(t)
	if _, err := s.GetNodeByName(context.Background(), "missing"); err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestStore_Node_UpdateState(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	id, _ := s.CreateNode(ctx, makeNode("state-node"))
	if err := s.UpdateNodeState(ctx, id, domain.NodeDraining); err != nil {
		t.Fatalf("UpdateNodeState ready→draining: %v", err)
	}
	n, _ := s.GetNode(ctx, id)
	if n.State != domain.NodeDraining {
		t.Errorf("expected draining, got %q", n.State)
	}
}

func TestStore_Node_UpdateState_IllegalTransition(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	id, _ := s.CreateNode(ctx, makeNode("bad-trans-node"))
	// Retired is a terminal state; moving ready → retired is illegal.
	if err := s.UpdateNodeState(ctx, id, domain.NodeRetired); err == nil {
		t.Error("expected error for illegal transition")
	}
}

// ── Volume store ──────────────────────────────────────────────────────────────

func makeVolumeManifest(ns, name string) domain.VolumeManifest {
	return domain.VolumeManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Volume",
		Metadata:   domain.VolumeMetadata{Namespace: ns, Name: name},
		Spec:       domain.VolumeSpec{Class: "local", CapacityMiB: 1024, AccessMode: domain.VolumeAccessRWO},
	}
}

func makeVolume(id, ns, name string) domain.Volume {
	return domain.Volume{
		ID:       id,
		Manifest: makeVolumeManifest(ns, name),
		State:    domain.VolumeDeclared,
	}
}

func TestStore_Volume_CreateGet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "vns", "") //nolint:errcheck
	v := makeVolume("vol-001", "vns", "data")
	id, err := s.CreateVolume(ctx, v)
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if id != "vol-001" {
		t.Errorf("unexpected id: %q", id)
	}
	got, err := s.GetVolume(ctx, id)
	if err != nil {
		t.Fatalf("GetVolume: %v", err)
	}
	if got.State != domain.VolumeDeclared {
		t.Errorf("expected declared, got %q", got.State)
	}
	if got.Manifest.Metadata.Name != "data" {
		t.Errorf("unexpected name: %q", got.Manifest.Metadata.Name)
	}
}

func TestStore_Volume_GetByName(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "vns2", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-002", "vns2", "disk"))
	v, err := s.GetVolumeByName(ctx, "vns2", "disk")
	if err != nil {
		t.Fatalf("GetVolumeByName: %v", err)
	}
	if v.ID != "vol-002" {
		t.Errorf("unexpected id: %q", v.ID)
	}
}

func TestStore_Volume_NotFound(t *testing.T) {
	s := openStore(t)
	if _, err := s.GetVolume(context.Background(), "nonexistent"); err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestStore_Volume_Duplicate(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "vns3", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-dup", "vns3", "dup"))
	if _, err := s.CreateVolume(ctx, makeVolume("vol-dup", "vns3", "dup")); err != store.ErrConflict {
		t.Errorf("expected ErrConflict, got: %v", err)
	}
}

func TestStore_Volume_ListAll(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "list-ns", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-l1", "list-ns", "a"))
	s.CreateVolume(ctx, makeVolume("vol-l2", "list-ns", "b"))
	vols, err := s.ListVolumes(ctx, "")
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(vols) < 2 {
		t.Errorf("expected >=2 volumes, got %d", len(vols))
	}
}

func TestStore_Volume_ListByNamespace(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "nsA", "") //nolint:errcheck
	s.CreateNamespace(ctx, "nsB", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-a", "nsA", "a"))
	s.CreateVolume(ctx, makeVolume("vol-b", "nsB", "b"))
	vols, err := s.ListVolumes(ctx, "nsA")
	if err != nil {
		t.Fatalf("ListVolumes(nsA): %v", err)
	}
	if len(vols) != 1 || vols[0].ID != "vol-a" {
		t.Errorf("expected [vol-a], got %v", vols)
	}
}

func TestStore_Volume_ListByState(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "stateNS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-s1", "stateNS", "s1"))
	s.CreateVolume(ctx, makeVolume("vol-s2", "stateNS", "s2"))

	declared, err := s.ListVolumesByState(ctx, domain.VolumeDeclared)
	if err != nil {
		t.Fatalf("ListVolumesByState: %v", err)
	}
	if len(declared) < 2 {
		t.Errorf("expected >=2 declared volumes, got %d", len(declared))
	}
}

func TestStore_Volume_UpdateState(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "updNS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-upd", "updNS", "upd"))
	if err := s.UpdateVolumeState(ctx, "vol-upd", domain.VolumeProvisioning); err != nil {
		t.Fatalf("UpdateVolumeState declared→provisioning: %v", err)
	}
	v, _ := s.GetVolume(ctx, "vol-upd")
	if v.State != domain.VolumeProvisioning {
		t.Errorf("expected provisioning, got %q", v.State)
	}
}

func TestStore_Volume_UpdateState_IllegalTransition(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "illNS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-ill", "illNS", "ill"))
	if err := s.UpdateVolumeState(ctx, "vol-ill", domain.VolumeReady); err == nil {
		t.Error("expected error for declared→ready (illegal)")
	}
}

func TestStore_Volume_UpdateState_NotFound(t *testing.T) {
	s := openStore(t)
	if err := s.UpdateVolumeState(context.Background(), "missing", domain.VolumeProvisioning); err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestStore_Volume_UpdateHandle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "hNS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-h", "hNS", "h"))
	handle := map[string]string{"path": "/data/vol", "nodeID": "n1"}
	if err := s.UpdateVolumeHandle(ctx, "vol-h", handle); err != nil {
		t.Fatalf("UpdateVolumeHandle: %v", err)
	}
	v, _ := s.GetVolume(ctx, "vol-h")
	if v.DriverHandle["path"] != "/data/vol" {
		t.Errorf("unexpected path: %q", v.DriverHandle["path"])
	}
}

func TestStore_Volume_UpdateBoundNode(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "bnNS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-bn", "bnNS", "bn"))
	if err := s.UpdateVolumeBoundNode(ctx, "vol-bn", "node-42"); err != nil {
		t.Fatalf("UpdateVolumeBoundNode: %v", err)
	}
	v, _ := s.GetVolume(ctx, "vol-bn")
	if v.BoundNodeID != "node-42" {
		t.Errorf("expected boundNodeID=node-42, got %q", v.BoundNodeID)
	}
}

func TestStore_Volume_UpdateFailure(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "failNS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-fail", "failNS", "fail"))
	if err := s.UpdateVolumeFailure(ctx, "vol-fail", "driver error"); err != nil {
		t.Fatalf("UpdateVolumeFailure: %v", err)
	}
	v, _ := s.GetVolume(ctx, "vol-fail")
	if v.State != domain.VolumeFailed {
		t.Errorf("expected failed, got %q", v.State)
	}
	if v.FailureReason != "driver error" {
		t.Errorf("unexpected failure reason: %q", v.FailureReason)
	}
}

func TestStore_Volume_DeleteReleased(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "delNS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-del", "delNS", "del"))
	// Force state to released via intermediate transitions.
	s.UpdateVolumeState(ctx, "vol-del", domain.VolumeProvisioning)
	s.UpdateVolumeState(ctx, "vol-del", domain.VolumeReady)
	s.UpdateVolumeState(ctx, "vol-del", domain.VolumeReleasing)
	s.UpdateVolumeState(ctx, "vol-del", domain.VolumeReleased)

	if err := s.DeleteVolume(ctx, "vol-del"); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	if _, err := s.GetVolume(ctx, "vol-del"); err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestStore_Volume_DeleteNotReleased(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "nd2NS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-nd2", "nd2NS", "nd2"))
	if err := s.DeleteVolume(ctx, "vol-nd2"); err != store.ErrConflict {
		t.Errorf("expected ErrConflict for non-released, got: %v", err)
	}
}

func TestStore_Volume_DeleteNotFound(t *testing.T) {
	s := openStore(t)
	if err := s.DeleteVolume(context.Background(), "missing"); err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// ── Mount store ───────────────────────────────────────────────────────────────

func TestStore_Mount_BindGetState(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "mntNS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-mnt", "mntNS", "m1"))
	svcID, _ := s.CreateService(ctx, makeManifest("mntNS", "mnt-svc1"))
	instID, _ := s.CreateInstance(ctx, svcID, domain.VSPath{Namespace: "mntNS", Service: "mnt-svc1", Instance: "0"})

	m := domain.VolumeMount{
		VolumeID:   "vol-mnt",
		InstanceID: instID,
		TargetPath: "/data",
		ReadOnly:   false,
	}
	id, err := s.BindMount(ctx, m)
	if err != nil {
		t.Fatalf("BindMount: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive id, got %d", id)
	}
	got, err := s.GetMount(ctx, id)
	if err != nil {
		t.Fatalf("GetMount: %v", err)
	}
	if got.State != domain.MountPending {
		t.Errorf("expected pending, got %q", got.State)
	}
	if got.InstanceID != instID {
		t.Errorf("unexpected instanceID: %q", got.InstanceID)
	}
}

func TestStore_Mount_UpdateStateToActive(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "mnt2NS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-m2", "mnt2NS", "m2"))
	svcID2, _ := s.CreateService(ctx, makeManifest("mnt2NS", "mnt-svc2"))
	instID2, _ := s.CreateInstance(ctx, svcID2, domain.VSPath{Namespace: "mnt2NS", Service: "mnt-svc2", Instance: "0"})
	id, _ := s.BindMount(ctx, domain.VolumeMount{VolumeID: "vol-m2", InstanceID: instID2, TargetPath: "/x"})

	if err := s.UpdateMountState(ctx, id, domain.MountActive); err != nil {
		t.Fatalf("UpdateMountState active: %v", err)
	}
	m, _ := s.GetMount(ctx, id)
	if m.State != domain.MountActive {
		t.Errorf("expected active, got %q", m.State)
	}
	if m.MountedAt == nil {
		t.Error("expected MountedAt to be set")
	}
}

func TestStore_Mount_UpdateStateToReleased(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "mnt3NS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-m3", "mnt3NS", "m3"))
	svcID3, _ := s.CreateService(ctx, makeManifest("mnt3NS", "mnt-svc3"))
	instID3, _ := s.CreateInstance(ctx, svcID3, domain.VSPath{Namespace: "mnt3NS", Service: "mnt-svc3", Instance: "0"})
	id, _ := s.BindMount(ctx, domain.VolumeMount{VolumeID: "vol-m3", InstanceID: instID3, TargetPath: "/y"})
	s.UpdateMountState(ctx, id, domain.MountActive)

	if err := s.UpdateMountState(ctx, id, domain.MountReleased); err != nil {
		t.Fatalf("UpdateMountState released: %v", err)
	}
	m, _ := s.GetMount(ctx, id)
	if m.State != domain.MountReleased {
		t.Errorf("expected released, got %q", m.State)
	}
	if m.UnmountedAt == nil {
		t.Error("expected UnmountedAt to be set")
	}
}

func TestStore_Mount_ListByInstance(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "mntNS4", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-m4a", "mntNS4", "m4a"))
	s.CreateVolume(ctx, makeVolume("vol-m4b", "mntNS4", "m4b"))
	svcID4, _ := s.CreateService(ctx, makeManifest("mntNS4", "mnt-svc4"))
	inst4a, _ := s.CreateInstance(ctx, svcID4, domain.VSPath{Namespace: "mntNS4", Service: "mnt-svc4", Instance: "0"})
	inst4b, _ := s.CreateInstance(ctx, svcID4, domain.VSPath{Namespace: "mntNS4", Service: "mnt-svc4", Instance: "1"})
	s.BindMount(ctx, domain.VolumeMount{VolumeID: "vol-m4a", InstanceID: inst4a, TargetPath: "/a"})
	s.BindMount(ctx, domain.VolumeMount{VolumeID: "vol-m4b", InstanceID: inst4a, TargetPath: "/b"})
	s.BindMount(ctx, domain.VolumeMount{VolumeID: "vol-m4a", InstanceID: inst4b, TargetPath: "/c"})

	mounts, err := s.ListMountsByInstance(ctx, inst4a)
	if err != nil {
		t.Fatalf("ListMountsByInstance: %v", err)
	}
	if len(mounts) != 2 {
		t.Errorf("expected 2 mounts for inst4a, got %d", len(mounts))
	}
}

func TestStore_Mount_ListActiveByVolume(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "mntNS5", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-m5", "mntNS5", "m5"))
	svcID5, _ := s.CreateService(ctx, makeManifest("mntNS5", "mnt-svc5"))
	instA, _ := s.CreateInstance(ctx, svcID5, domain.VSPath{Namespace: "mntNS5", Service: "mnt-svc5", Instance: "0"})
	instB, _ := s.CreateInstance(ctx, svcID5, domain.VSPath{Namespace: "mntNS5", Service: "mnt-svc5", Instance: "1"})
	id1, _ := s.BindMount(ctx, domain.VolumeMount{VolumeID: "vol-m5", InstanceID: instA, TargetPath: "/p1"})
	id2, _ := s.BindMount(ctx, domain.VolumeMount{VolumeID: "vol-m5", InstanceID: instB, TargetPath: "/p2"})
	s.UpdateMountState(ctx, id1, domain.MountActive)
	s.UpdateMountState(ctx, id2, domain.MountActive)
	s.UpdateMountState(ctx, id2, domain.MountReleased) // released → excluded

	active, err := s.ListActiveMountsByVolume(ctx, "vol-m5")
	if err != nil {
		t.Fatalf("ListActiveMountsByVolume: %v", err)
	}
	if len(active) != 1 {
		t.Errorf("expected 1 active mount, got %d", len(active))
	}
}

// ── Snapshot store ─────────────────────────────────────────────────────────────

func TestStore_Snapshot_CreateGet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "snapNS", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-snap", "snapNS", "sv"))

	snap := domain.VolumeSnapshot{
		ID:       "snap-001",
		VolumeID: "vol-snap",
		Label:    "initial",
	}
	if err := s.CreateSnapshot(ctx, snap); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	got, err := s.GetSnapshot(ctx, "snap-001")
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if got.State != domain.SnapshotPending {
		t.Errorf("expected pending, got %q", got.State)
	}
	if got.Label != "initial" {
		t.Errorf("unexpected label: %q", got.Label)
	}
}

func TestStore_Snapshot_NotFound(t *testing.T) {
	s := openStore(t)
	if _, err := s.GetSnapshot(context.Background(), "missing"); err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestStore_Snapshot_DuplicateID(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "snapNS2", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-sd", "snapNS2", "sd"))
	snap := domain.VolumeSnapshot{ID: "snap-dup", VolumeID: "vol-sd", Label: "x"}
	s.CreateSnapshot(ctx, snap) //nolint:errcheck
	if err := s.CreateSnapshot(ctx, snap); err != store.ErrConflict {
		t.Errorf("expected ErrConflict, got: %v", err)
	}
}

func TestStore_Snapshot_UpdateStateReady(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "snapNS3", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-su", "snapNS3", "su"))
	s.CreateSnapshot(ctx, domain.VolumeSnapshot{ID: "snap-upd", VolumeID: "vol-su", Label: "v1"})

	if err := s.UpdateSnapshotState(ctx, "snap-upd", domain.SnapshotReady, "/snapshots/v1", 512); err != nil {
		t.Fatalf("UpdateSnapshotState ready: %v", err)
	}
	got, _ := s.GetSnapshot(ctx, "snap-upd")
	if got.State != domain.SnapshotReady {
		t.Errorf("expected ready, got %q", got.State)
	}
	if got.DriverRef != "/snapshots/v1" {
		t.Errorf("unexpected driverRef: %q", got.DriverRef)
	}
	if got.SizeMiB != 512 {
		t.Errorf("unexpected sizeMiB: %d", got.SizeMiB)
	}
	if got.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}
}

func TestStore_Snapshot_UpdateStateFailed(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "snapNS4", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-sf", "snapNS4", "sf"))
	s.CreateSnapshot(ctx, domain.VolumeSnapshot{ID: "snap-fail", VolumeID: "vol-sf", Label: "v2"})

	if err := s.UpdateSnapshotState(ctx, "snap-fail", domain.SnapshotFailed, "", 0); err != nil {
		t.Fatalf("UpdateSnapshotState failed: %v", err)
	}
	got, _ := s.GetSnapshot(ctx, "snap-fail")
	if got.State != domain.SnapshotFailed {
		t.Errorf("expected failed, got %q", got.State)
	}
}

func TestStore_Snapshot_List(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	s.CreateNamespace(ctx, "snapNS5", "") //nolint:errcheck
	s.CreateVolume(ctx, makeVolume("vol-sl", "snapNS5", "sl"))
	s.CreateSnapshot(ctx, domain.VolumeSnapshot{ID: "sl-1", VolumeID: "vol-sl", Label: "a"})
	s.CreateSnapshot(ctx, domain.VolumeSnapshot{ID: "sl-2", VolumeID: "vol-sl", Label: "b"})
	s.CreateSnapshot(ctx, domain.VolumeSnapshot{ID: "sl-3", VolumeID: "other-vol", Label: "c"})

	snaps, err := s.ListSnapshots(ctx, "vol-sl")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Errorf("expected 2 snapshots for vol-sl, got %d", len(snaps))
	}
}

// ── Operation store ────────────────────────────────────────────────────────────

func makeOpInstance(t *testing.T, s *store.VSStore, ns, svcSuffix string) string {
	t.Helper()
	ctx := context.Background()
	s.CreateNamespace(ctx, ns, "") //nolint:errcheck
	svcID, _ := s.CreateService(ctx, makeManifest(ns, "op-svc-"+svcSuffix))
	instID, err := s.CreateInstance(ctx, svcID, domain.VSPath{Namespace: ns, Service: "op-svc-" + svcSuffix, Instance: "0"})
	if err != nil {
		t.Fatalf("makeOpInstance CreateInstance: %v", err)
	}
	return instID
}

func TestStore_Operation_CreateGet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	instID := makeOpInstance(t, s, "opNS1", "1")

	id, err := s.CreateOperation(ctx, instID, domain.OpProvision, "mock")
	if err != nil {
		t.Fatalf("CreateOperation: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	op, err := s.GetOperation(ctx, id)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if op.State != domain.OperationPending {
		t.Errorf("expected pending, got %q", op.State)
	}
	if op.Type != domain.OpProvision {
		t.Errorf("expected provision, got %q", op.Type)
	}
	if op.Adapter != "mock" {
		t.Errorf("expected adapter=mock, got %q", op.Adapter)
	}
}

func TestStore_Operation_NotFound(t *testing.T) {
	s := openStore(t)
	if _, err := s.GetOperation(context.Background(), "missing"); err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestStore_Operation_UpdateState(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	instID2 := makeOpInstance(t, s, "opNS2", "2")
	id, _ := s.CreateOperation(ctx, instID2, domain.OpStart, "mock")

	if err := s.UpdateOperationState(ctx, id, domain.OperationRunning); err != nil {
		t.Fatalf("UpdateOperationState pending→running: %v", err)
	}
	op, _ := s.GetOperation(ctx, id)
	if op.State != domain.OperationRunning {
		t.Errorf("expected running, got %q", op.State)
	}

	if err := s.UpdateOperationState(ctx, id, domain.OperationSucceeded); err != nil {
		t.Fatalf("UpdateOperationState running→succeeded: %v", err)
	}
	op, _ = s.GetOperation(ctx, id)
	if op.State != domain.OperationSucceeded {
		t.Errorf("expected succeeded, got %q", op.State)
	}
	if op.CompletedAt == nil {
		t.Error("expected CompletedAt to be set on success")
	}
}

func TestStore_Operation_UpdateState_IllegalTransition(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	instID3 := makeOpInstance(t, s, "opNS3", "3")
	id, _ := s.CreateOperation(ctx, instID3, domain.OpStop, "mock")
	// pending→succeeded is illegal (must go through running)
	if err := s.UpdateOperationState(ctx, id, domain.OperationSucceeded); err == nil {
		t.Error("expected error for illegal transition pending→succeeded")
	}
}

func TestStore_Operation_AppendEvent(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	instID4 := makeOpInstance(t, s, "opNS4", "4")
	id, _ := s.CreateOperation(ctx, instID4, domain.OpInspect, "mock")
	payload := map[string]interface{}{"key": "value"}

	if err := s.AppendOperationEvent(ctx, id, "info", "starting up", payload); err != nil {
		t.Fatalf("AppendOperationEvent: %v", err)
	}
	if err := s.AppendOperationEvent(ctx, id, "warn", "slow disk", nil); err != nil {
		t.Fatalf("AppendOperationEvent nil payload: %v", err)
	}

	op, err := s.GetOperation(ctx, id)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if len(op.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(op.Events))
	}
	if op.Events[0].Message != "starting up" {
		t.Errorf("unexpected message: %q", op.Events[0].Message)
	}
	if op.Events[0].Payload["key"] != "value" {
		t.Errorf("unexpected payload: %v", op.Events[0].Payload)
	}
}
