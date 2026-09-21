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
