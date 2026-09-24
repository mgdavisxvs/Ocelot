package virtualserver

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
)

// ── test fixtures ─────────────────────────────────────────────────────────────

func reconDB(t *testing.T) *db {
	t.Helper()
	d, err := openDB(filepath.Join(t.TempDir(), "vs.db"))
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	d.createNamespace(&Namespace{Name: "default", CreatedAt: time.Now().UTC()})
	t.Cleanup(func() { d.close() })
	return d
}

func newTestReconciler(d *db, stor *storage.Registry) *Reconciler {
	adapters := NewAdapterRegistry()
	adapters.Register(NoOpAdapter{})
	metrics := NewVSMetrics(newTestRegistry())
	return newReconciler(d, adapters, stor, nil, metrics, time.Second, slog.Default())
}

// ── pickGPUs ──────────────────────────────────────────────────────────────────

func TestReconciler_PickGPUs_Empty(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())
	n := &Node{ID: "n1"}
	ids, err := r.pickGPUs(n, nil)
	if err != nil || ids != nil {
		t.Fatalf("no requirements: %v %v", err, ids)
	}
}

func TestReconciler_PickGPUs_Satisfied(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())
	n := &Node{
		ID: "n1",
		GPUs: []GPU{
			{DeviceID: "g0", Model: "A100", VRAMBytes: 80 << 30},
			{DeviceID: "g1", Model: "A100", VRAMBytes: 80 << 30},
		},
	}
	ids, err := r.pickGPUs(n, []GPURequirement{{Count: 2, ModelFilter: "A100"}})
	if err != nil {
		t.Fatalf("pickGPUs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %v", ids)
	}
}

func TestReconciler_PickGPUs_Unsatisfied(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())
	n := &Node{ID: "n1", GPUs: []GPU{{DeviceID: "g0", Model: "T4", VRAMBytes: 16 << 30}}}
	_, err := r.pickGPUs(n, []GPURequirement{{Count: 2, ModelFilter: "A100"}})
	if err == nil {
		t.Fatal("expected error: not enough matching GPUs")
	}
}

func TestReconciler_PickGPUs_NoDuplicateAllocation(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	// g0 already allocated.
	d.upsertNode(&Node{ID: "n1", Name: "n1", Arch: "amd64", State: NodeStateReady,
		CPUMillicores: 4000, RAMBytes: 8 << 30,
		GPUs:          []GPU{{DeviceID: "g0", Model: "A100", VRAMBytes: 80 << 30}},
		LastHeartbeat: time.Now(), CreatedAt: time.Now()})
	d.createNamespace(&Namespace{Name: "ns", CreatedAt: time.Now()})
	d.upsertService(sampleService("s1", "default"))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "s1", Namespace: "default", NodeID: "n1", State: InstanceStateRunning, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)
	d.createAllocation(&Allocation{ID: "a1", InstanceID: "i1", NodeID: "n1", GPUDeviceIDs: []string{"g0"}, CreatedAt: now})

	n := &Node{ID: "n1", GPUs: []GPU{{DeviceID: "g0", Model: "A100", VRAMBytes: 80 << 30}}}
	_, err := r.pickGPUs(n, []GPURequirement{{Count: 1, ModelFilter: "A100"}})
	if err == nil {
		t.Fatal("expected error: g0 already allocated")
	}
}

// ── markFailed ────────────────────────────────────────────────────────────────

func TestReconciler_MarkFailed(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	d.upsertService(sampleService("s1", "default"))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "s1", Namespace: "default", State: InstanceStateProvisioning, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	r.markFailed(inst, "injected failure")

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateFailed {
		t.Fatalf("expected failed, got %s", fetched.State)
	}
	if fetched.Message != "injected failure" {
		t.Fatalf("expected message, got %q", fetched.Message)
	}
}

// ── reconcileScheduled: full placement path ───────────────────────────────────

func TestReconciler_ScheduledToProvisioning(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	d.upsertNode(readyNode("n1", 4000, 8<<30))
	d.upsertService(basicSvc(100, 128<<20))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", State: InstanceStateScheduled, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	r.reconcileScheduled(context.Background())

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateProvisioning {
		t.Fatalf("expected provisioning, got %s", fetched.State)
	}
	if fetched.NodeID != "n1" {
		t.Fatalf("expected n1, got %s", fetched.NodeID)
	}
}

func TestReconciler_ScheduledFailsNoNode(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	d.upsertService(basicSvc(100, 128<<20))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", State: InstanceStateScheduled, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	r.reconcileScheduled(context.Background())

	// No node → instance stays scheduled (not failed; unschedulable is just deferred).
	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateScheduled {
		t.Fatalf("expected still scheduled, got %s", fetched.State)
	}
}

// ── catalog gating ────────────────────────────────────────────────────────────

func TestReconciler_CatalogGating_Defers(t *testing.T) {
	d := reconDB(t)
	cat := &stubCatalog{seeders: 0}

	adapters := NewAdapterRegistry()
	adapters.Register(NoOpAdapter{})
	r := newReconciler(d, adapters, storage.NewRegistry(), cat,
		NewVSMetrics(newTestRegistry()), time.Second, slog.Default())

	d.upsertNode(readyNode("n1", 4000, 8<<30))
	svc := basicSvc(100, 128<<20)
	svc.ArtifactHash = "deadbeef"
	d.upsertService(svc)
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", State: InstanceStateScheduled, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	r.reconcileScheduled(context.Background())

	// 0 seeders → deferred (still scheduled)
	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateScheduled {
		t.Fatalf("expected deferred (scheduled), got %s", fetched.State)
	}
}

func TestReconciler_CatalogGating_Proceeds(t *testing.T) {
	d := reconDB(t)
	cat := &stubCatalog{seeders: 3}

	adapters := NewAdapterRegistry()
	adapters.Register(NoOpAdapter{})
	r := newReconciler(d, adapters, storage.NewRegistry(), cat,
		NewVSMetrics(newTestRegistry()), time.Second, slog.Default())

	d.upsertNode(readyNode("n1", 4000, 8<<30))
	svc := basicSvc(100, 128<<20)
	svc.ArtifactHash = "deadbeef"
	d.upsertService(svc)
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", State: InstanceStateScheduled, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	r.reconcileScheduled(context.Background())

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateProvisioning {
		t.Fatalf("expected provisioning (3 seeders), got %s", fetched.State)
	}
}

// ── reconcileProvisioning → starting ─────────────────────────────────────────

func TestReconciler_ProvisioningToStarting(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	d.upsertNode(readyNode("n1", 4000, 8<<30))
	d.upsertService(basicSvc(100, 128<<20))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", NodeID: "n1", State: InstanceStateProvisioning, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	r.reconcileProvisioning(context.Background())

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateStarting {
		t.Fatalf("expected starting, got %s", fetched.State)
	}
	if fetched.RuntimeID == "" {
		t.Fatal("RuntimeID should be set after provisioning")
	}
}

// ── reconcileStarting → running ───────────────────────────────────────────────

func TestReconciler_StartingToRunning(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	d.upsertService(basicSvc(100, 128<<20))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", NodeID: "n1", State: InstanceStateStarting, RuntimeID: "noop-i1", CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	r.reconcileStarting(context.Background())

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateRunning {
		t.Fatalf("expected running, got %s", fetched.State)
	}
}

// ── reconcileStopping → stopped ───────────────────────────────────────────────

func TestReconciler_StoppingToStopped(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	d.upsertService(basicSvc(100, 128<<20))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", NodeID: "n1", State: InstanceStateStopping, RuntimeID: "noop-i1", CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	r.reconcileStopping(context.Background())

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateStopped {
		t.Fatalf("expected stopped, got %s", fetched.State)
	}
}

// ── handleExit: restart policy ────────────────────────────────────────────────

func TestReconciler_HandleExit_RestartAlways(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	svc := basicSvc(100, 128<<20)
	svc.RestartPolicy = RestartAlways
	d.upsertService(svc)
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", NodeID: "n1", State: InstanceStateRunning, RuntimeID: "r1", CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	svc2, _ := d.getService("svc1")
	r.handleExit(context.Background(), inst, InstanceStateFailed, svc2)

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateScheduled {
		t.Fatalf("RestartAlways: expected rescheduled, got %s", fetched.State)
	}
	if fetched.Restarts != 1 {
		t.Fatalf("RestartAlways: expected 1 restart, got %d", fetched.Restarts)
	}
	if fetched.NodeID != "" {
		t.Fatalf("RestartAlways: expected empty NodeID after reschedule")
	}
}

func TestReconciler_HandleExit_RestartNever(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	svc := basicSvc(100, 128<<20)
	svc.RestartPolicy = RestartNever
	d.upsertService(svc)
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", State: InstanceStateRunning, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	svc2, _ := d.getService("svc1")
	r.handleExit(context.Background(), inst, InstanceStateFailed, svc2)

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateFailed {
		t.Fatalf("RestartNever: expected failed, got %s", fetched.State)
	}
	if fetched.Restarts != 0 {
		t.Fatalf("RestartNever: no restarts expected")
	}
}

func TestReconciler_HandleExit_RestartOnFailure_Success(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	svc := basicSvc(100, 128<<20)
	svc.RestartPolicy = RestartOnFailure
	d.upsertService(svc)
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", State: InstanceStateRunning, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	svc2, _ := d.getService("svc1")
	// Clean exit (stopped) should NOT restart.
	r.handleExit(context.Background(), inst, InstanceStateStopped, svc2)

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateStopped {
		t.Fatalf("RestartOnFailure+stopped: expected stopped, got %s", fetched.State)
	}
}

func TestReconciler_HandleExit_RestartOnFailure_Failure(t *testing.T) {
	d := reconDB(t)
	r := newTestReconciler(d, storage.NewRegistry())

	svc := basicSvc(100, 128<<20)
	svc.RestartPolicy = RestartOnFailure
	d.upsertService(svc)
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "svc1", Namespace: "default", State: InstanceStateRunning, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	svc2, _ := d.getService("svc1")
	r.handleExit(context.Background(), inst, InstanceStateFailed, svc2)

	fetched, _ := d.getInstance("i1")
	if fetched.State != InstanceStateScheduled {
		t.Fatalf("RestartOnFailure+failure: expected rescheduled, got %s", fetched.State)
	}
}

// ── resolveMounts / teardownMounts ────────────────────────────────────────────

func TestReconciler_ResolveTeardownMounts(t *testing.T) {
	d := reconDB(t)
	tmp := t.TempDir()
	drv, err := storage.NewLocalDriver(tmp, "n1")
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	stor := storage.NewRegistry()
	stor.Register(drv)

	r := newTestReconciler(d, stor)

	// Create a volume on disk and record it in the DB.
	ctx := context.Background()
	handle, _ := drv.Create(ctx, "vol-test", 0)
	d.upsertVolume(&Volume{
		ID: "v1", Namespace: "default", Name: "data",
		DriverName: "local", Handle: string(handle),
		CreatedAt: time.Now().UTC(),
	})

	svc := basicSvc(100, 128<<20)
	svc.VolumeMounts = []VolumeMount{{Name: "data", VolumeID: "v1", MountPath: "/data"}}
	d.upsertService(svc)

	mounts, err := r.resolveMounts(ctx, svc, "i1", "n1")
	if err != nil {
		t.Fatalf("resolveMounts: %v", err)
	}
	if len(mounts) != 1 || mounts[0].MountPath != "/data" {
		t.Fatalf("unexpected mounts: %v", mounts)
	}
	if mounts[0].HostPath == "" {
		t.Fatal("HostPath should be set")
	}

	// Binding should have been persisted.
	bindings, _ := d.bindingsForInstance("i1")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}

	// Node affinity should have been set.
	aff, _ := d.volumeAffinityNode("v1")
	if aff != "n1" {
		t.Fatalf("expected n1 affinity, got %q", aff)
	}

	// Teardown.
	r.teardownMounts(ctx, "i1", svc)
	bindings, _ = d.bindingsForInstance("i1")
	if len(bindings) != 0 {
		t.Fatalf("expected 0 bindings after teardown, got %d", len(bindings))
	}
}
