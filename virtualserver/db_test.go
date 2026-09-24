package virtualserver

import (
	"path/filepath"
	"testing"
	"time"
)

// openTestDB opens a fresh SQLite DB in a temp directory.
func openTestDB(t *testing.T) *db {
	t.Helper()
	d, err := openDB(filepath.Join(t.TempDir(), "vs.db"))
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { d.close() })
	return d
}

// ── namespace ─────────────────────────────────────────────────────────────────

func TestDB_Namespace(t *testing.T) {
	d := openTestDB(t)

	ns := &Namespace{Name: "default", CreatedAt: time.Now().UTC()}
	if err := d.createNamespace(ns); err != nil {
		t.Fatalf("createNamespace: %v", err)
	}

	list, err := d.listNamespaces()
	if err != nil {
		t.Fatalf("listNamespaces: %v", err)
	}
	if len(list) != 1 || list[0].Name != "default" {
		t.Fatalf("unexpected namespaces: %v", list)
	}

	// Idempotent insert.
	if err := d.createNamespace(ns); err != nil {
		t.Fatalf("duplicate createNamespace: %v", err)
	}

	if err := d.deleteNamespace("default"); err != nil {
		t.Fatalf("deleteNamespace: %v", err)
	}
	list, _ = d.listNamespaces()
	if len(list) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(list))
	}
}

// ── node ──────────────────────────────────────────────────────────────────────

func sampleNode(id string) *Node {
	return &Node{
		ID:            id,
		Name:          "node-" + id,
		Arch:          "amd64",
		State:         NodeStateReady,
		CPUMillicores: 4000,
		RAMBytes:      8 * 1024 * 1024 * 1024,
		Labels:        map[string]string{"region": "us-west", "zone": "a"},
		GPUs: []GPU{
			{DeviceID: "gpu0", Model: "A100", VRAMBytes: 80 * 1024 * 1024 * 1024},
		},
		BearerToken:   "tok-" + id,
		LastHeartbeat: time.Now().UTC(),
		CreatedAt:     time.Now().UTC(),
	}
}

func TestDB_Node_UpsertListGet(t *testing.T) {
	d := openTestDB(t)

	n := sampleNode("n1")
	if err := d.upsertNode(n); err != nil {
		t.Fatalf("upsertNode: %v", err)
	}

	nodes, err := d.listNodes()
	if err != nil {
		t.Fatalf("listNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	got := nodes[0]
	if got.Arch != "amd64" || got.CPUMillicores != 4000 {
		t.Fatalf("node mismatch: %+v", got)
	}
	if got.Labels["region"] != "us-west" {
		t.Fatalf("labels not persisted: %v", got.Labels)
	}
	if len(got.GPUs) != 1 || got.GPUs[0].Model != "A100" {
		t.Fatalf("GPUs not persisted: %v", got.GPUs)
	}

	// getNode round-trip.
	fetched, err := d.getNode("n1")
	if err != nil {
		t.Fatalf("getNode: %v", err)
	}
	if fetched.ID != "n1" {
		t.Fatalf("getNode returned wrong node: %s", fetched.ID)
	}

	// Upsert updates labels.
	n.Labels["zone"] = "b"
	n.CPUMillicores = 8000
	d.upsertNode(n)
	fetched, _ = d.getNode("n1")
	if fetched.CPUMillicores != 8000 || fetched.Labels["zone"] != "b" {
		t.Fatalf("upsert did not update: %+v", fetched)
	}
}

func TestDB_Node_GetMissing(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.getNode("nonexistent"); err == nil {
		t.Fatal("expected error for missing node")
	}
}

func TestDB_Node_Delete(t *testing.T) {
	d := openTestDB(t)
	d.upsertNode(sampleNode("n2"))
	if err := d.deleteNode("n2"); err != nil {
		t.Fatalf("deleteNode: %v", err)
	}
	nodes, _ := d.listNodes()
	if len(nodes) != 0 {
		t.Fatalf("expected 0 nodes after delete")
	}
}

// ── service ───────────────────────────────────────────────────────────────────

func mustNamespace(t *testing.T, d *db, name string) {
	t.Helper()
	if err := d.createNamespace(&Namespace{Name: name, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("createNamespace %s: %v", name, err)
	}
}

func sampleService(id, ns string) *Service {
	now := time.Now().UTC()
	return &Service{
		ID: id, Namespace: ns, Name: "svc-" + id,
		Image: "registry/img:latest",
		CPUMillicores: 1000, RAMBytes: 512 * 1024 * 1024,
		RestartPolicy: RestartOnFailure,
		NodeSelector:  map[string]string{"arch": "amd64"},
		NodeExclusions: []string{"bad-node"},
		Env:           map[string]string{"FOO": "bar"},
		GPUs: []GPURequirement{{Count: 1, VRAMPerDevice: 40 * 1024 * 1024 * 1024, ModelFilter: "A100"}},
		VolumeMounts: []VolumeMount{{Name: "data", VolumeID: "vol-1", MountPath: "/data"}},
		ArtifactHash: "abc123",
		CreatedAt:    now, UpdatedAt: now,
	}
}

func TestDB_Service_CRUD(t *testing.T) {
	d := openTestDB(t)
	mustNamespace(t, d, "default")

	svc := sampleService("s1", "default")
	if err := d.upsertService(svc); err != nil {
		t.Fatalf("upsertService: %v", err)
	}

	svcs, err := d.listServices("")
	if err != nil || len(svcs) != 1 {
		t.Fatalf("listServices: %v, len=%d", err, len(svcs))
	}
	got := svcs[0]
	if got.Env["FOO"] != "bar" {
		t.Fatalf("env not persisted: %v", got.Env)
	}
	if got.NodeSelector["arch"] != "amd64" {
		t.Fatalf("nodeSelector not persisted: %v", got.NodeSelector)
	}
	if len(got.GPUs) != 1 || got.GPUs[0].Count != 1 {
		t.Fatalf("GPUs not persisted: %v", got.GPUs)
	}
	if len(got.VolumeMounts) != 1 || got.VolumeMounts[0].MountPath != "/data" {
		t.Fatalf("VolumeMounts not persisted: %v", got.VolumeMounts)
	}
	if got.ArtifactHash != "abc123" {
		t.Fatalf("ArtifactHash not persisted: %s", got.ArtifactHash)
	}
	if len(got.NodeExclusions) != 1 || got.NodeExclusions[0] != "bad-node" {
		t.Fatalf("NodeExclusions not persisted: %v", got.NodeExclusions)
	}

	fetched, err := d.getService("s1")
	if err != nil || fetched.ID != "s1" {
		t.Fatalf("getService: %v %+v", err, fetched)
	}

	// Namespace filter.
	mustNamespace(t, d, "other")
	svc2 := sampleService("s2", "other")
	d.upsertService(svc2)

	filtered, _ := d.listServices("default")
	if len(filtered) != 1 || filtered[0].ID != "s1" {
		t.Fatalf("namespace filter failed: %v", filtered)
	}

	if err := d.deleteService("s1"); err != nil {
		t.Fatalf("deleteService: %v", err)
	}
	if _, err := d.getService("s1"); err == nil {
		t.Fatal("expected error after delete")
	}
}

// ── instance ──────────────────────────────────────────────────────────────────

func TestDB_Instance_CRUD(t *testing.T) {
	d := openTestDB(t)
	mustNamespace(t, d, "default")
	d.upsertService(sampleService("s1", "default"))

	now := time.Now().UTC()
	inst := &Instance{
		ID: "i1", ServiceID: "s1", Namespace: "default",
		State: InstanceStateScheduled, CreatedAt: now, UpdatedAt: now,
	}
	if err := d.upsertInstance(inst); err != nil {
		t.Fatalf("upsertInstance: %v", err)
	}

	// listInstances
	list, err := d.listInstances("")
	if err != nil || len(list) != 1 {
		t.Fatalf("listInstances: %v len=%d", err, len(list))
	}

	// getInstancesByState
	scheduled, _ := d.getInstancesByState(InstanceStateScheduled)
	if len(scheduled) != 1 || scheduled[0].ID != "i1" {
		t.Fatalf("getInstancesByState: %v", scheduled)
	}
	running, _ := d.getInstancesByState(InstanceStateRunning)
	if len(running) != 0 {
		t.Fatalf("expected 0 running instances")
	}

	// State transition via upsert.
	inst.State = InstanceStateRunning
	inst.RuntimeID = "runtime-xyz"
	inst.NodeID = "n1"
	d.upsertInstance(inst)

	fetched, err := d.getInstance("i1")
	if err != nil || fetched.State != InstanceStateRunning {
		t.Fatalf("getInstance after update: %v %v", err, fetched)
	}
	if fetched.RuntimeID != "runtime-xyz" {
		t.Fatalf("RuntimeID not updated: %s", fetched.RuntimeID)
	}

	// Namespace filter.
	mustNamespace(t, d, "other")
	d.upsertService(sampleService("s2", "other"))
	inst2 := &Instance{ID: "i2", ServiceID: "s2", Namespace: "other", State: InstanceStateScheduled, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst2)
	filtered, _ := d.listInstances("default")
	if len(filtered) != 1 || filtered[0].ID != "i1" {
		t.Fatalf("namespace filter: %v", filtered)
	}

	if err := d.deleteInstance("i1"); err != nil {
		t.Fatalf("deleteInstance: %v", err)
	}
	if _, err := d.getInstance("i1"); err == nil {
		t.Fatal("expected error for deleted instance")
	}
}

// ── volume ────────────────────────────────────────────────────────────────────

func TestDB_Volume_CRUD(t *testing.T) {
	d := openTestDB(t)
	mustNamespace(t, d, "default")

	vol := &Volume{
		ID: "v1", Namespace: "default", Name: "data",
		DriverName: "local", SizeBytes: 1024, Handle: "/tmp/v1",
		CreatedAt: time.Now().UTC(),
	}
	if err := d.upsertVolume(vol); err != nil {
		t.Fatalf("upsertVolume: %v", err)
	}

	vols, _ := d.listVolumes("")
	if len(vols) != 1 || vols[0].Handle != "/tmp/v1" {
		t.Fatalf("listVolumes: %v", vols)
	}

	fetched, err := d.getVolume("v1")
	if err != nil || fetched.DriverName != "local" {
		t.Fatalf("getVolume: %v %v", err, fetched)
	}

	if err := d.deleteVolume("v1"); err != nil {
		t.Fatalf("deleteVolume: %v", err)
	}
	if _, err := d.getVolume("v1"); err == nil {
		t.Fatal("expected error after delete")
	}
}

// ── snapshot ──────────────────────────────────────────────────────────────────

func TestDB_Snapshot(t *testing.T) {
	d := openTestDB(t)
	mustNamespace(t, d, "default")
	d.upsertVolume(&Volume{ID: "v1", Namespace: "default", Name: "data", DriverName: "local", CreatedAt: time.Now().UTC()})

	snap := &Snapshot{
		ID: "snap1", VolumeID: "v1", SnapshotHandle: "/snaps/snap1",
		SizeBytes: 512, CreatedAt: time.Now().UTC(),
	}
	if err := d.createSnapshot(snap); err != nil {
		t.Fatalf("createSnapshot: %v", err)
	}

	snaps, err := d.listSnapshots("v1")
	if err != nil || len(snaps) != 1 {
		t.Fatalf("listSnapshots: %v len=%d", err, len(snaps))
	}
	if snaps[0].SnapshotHandle != "/snaps/snap1" {
		t.Fatalf("wrong handle: %s", snaps[0].SnapshotHandle)
	}

	// No snapshots for unknown volume.
	snaps, _ = d.listSnapshots("no-such-volume")
	if len(snaps) != 0 {
		t.Fatalf("expected 0 snapshots for unknown volume")
	}
}

// ── allocation and headroom ───────────────────────────────────────────────────

func TestDB_Allocation_Headroom(t *testing.T) {
	d := openTestDB(t)
	mustNamespace(t, d, "default")
	d.upsertNode(sampleNode("n1"))
	d.upsertService(sampleService("s1", "default"))

	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "s1", Namespace: "default", NodeID: "n1", State: InstanceStateRunning, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	alloc := &Allocation{
		ID: "a1", InstanceID: "i1", NodeID: "n1",
		CPUMillicores: 2000, RAMBytes: 2 * 1024 * 1024 * 1024,
		GPUDeviceIDs: []string{"gpu0"},
		CreatedAt:    now,
	}
	if err := d.createAllocation(alloc); err != nil {
		t.Fatalf("createAllocation: %v", err)
	}

	freeCPU, freeRAM, err := d.nodeHeadroom("n1", 4000, 8*1024*1024*1024)
	if err != nil {
		t.Fatalf("nodeHeadroom: %v", err)
	}
	if freeCPU != 2000 {
		t.Fatalf("freeCPU=%d want 2000", freeCPU)
	}
	if freeRAM != 6*1024*1024*1024 {
		t.Fatalf("freeRAM=%d want 6GiB", freeRAM)
	}

	used, err := d.nodeAllocatedGPUs("n1")
	if err != nil {
		t.Fatalf("nodeAllocatedGPUs: %v", err)
	}
	if _, ok := used["gpu0"]; !ok {
		t.Fatal("gpu0 should be allocated")
	}

	// Stopped instances don't count toward headroom.
	inst.State = InstanceStateStopped
	d.upsertInstance(inst)
	freeCPU, _, _ = d.nodeHeadroom("n1", 4000, 8*1024*1024*1024)
	if freeCPU != 4000 {
		t.Fatalf("stopped instance still counted: freeCPU=%d", freeCPU)
	}
}

// ── volume affinity ───────────────────────────────────────────────────────────

func TestDB_VolumeAffinity(t *testing.T) {
	d := openTestDB(t)
	mustNamespace(t, d, "default")
	d.upsertVolume(&Volume{ID: "v1", Namespace: "default", Name: "data", DriverName: "local", CreatedAt: time.Now().UTC()})

	aff, err := d.volumeAffinityNode("v1")
	if err != nil || aff != "" {
		t.Fatalf("expected empty affinity, got %q %v", aff, err)
	}

	if err := d.setVolumeAffinity("v1", "n1"); err != nil {
		t.Fatalf("setVolumeAffinity: %v", err)
	}
	aff, _ = d.volumeAffinityNode("v1")
	if aff != "n1" {
		t.Fatalf("expected n1, got %q", aff)
	}
}

// ── operation ─────────────────────────────────────────────────────────────────

func TestDB_Operation(t *testing.T) {
	d := openTestDB(t)

	now := time.Now().UTC()
	op := &Operation{
		ID: "op1", Type: "snapshot", ResourceID: "v1",
		State: OpStatePending, CreatedAt: now, UpdatedAt: now,
	}
	if err := d.createOperation(op); err != nil {
		t.Fatalf("createOperation: %v", err)
	}

	fetched, err := d.getOperation("op1")
	if err != nil || fetched.State != OpStatePending {
		t.Fatalf("getOperation: %v %v", err, fetched)
	}

	if err := d.updateOperation("op1", OpStateSucceeded, "done"); err != nil {
		t.Fatalf("updateOperation: %v", err)
	}
	fetched, _ = d.getOperation("op1")
	if fetched.State != OpStateSucceeded || fetched.Message != "done" {
		t.Fatalf("update not persisted: %+v", fetched)
	}

	if _, err := d.getOperation("nonexistent"); err == nil {
		t.Fatal("expected error for missing operation")
	}
}

// ── volume bindings ───────────────────────────────────────────────────────────

func TestDB_VolumeBindings(t *testing.T) {
	d := openTestDB(t)
	mustNamespace(t, d, "default")
	d.upsertService(sampleService("s1", "default"))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "s1", Namespace: "default", State: InstanceStateRunning, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)
	d.upsertVolume(&Volume{ID: "v1", Namespace: "default", Name: "data", DriverName: "local", CreatedAt: now})

	b := &VolumeBinding{
		ID: "b1", InstanceID: "i1", VolumeID: "v1",
		MountPath: "/data", HostPath: "/vol/v1", CreatedAt: now,
	}
	if err := d.createBinding(b); err != nil {
		t.Fatalf("createBinding: %v", err)
	}

	bindings, err := d.bindingsForInstance("i1")
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindingsForInstance: %v len=%d", err, len(bindings))
	}
	if bindings[0].HostPath != "/vol/v1" {
		t.Fatalf("wrong HostPath: %s", bindings[0].HostPath)
	}

	if err := d.deleteBindingsForInstance("i1"); err != nil {
		t.Fatalf("deleteBindings: %v", err)
	}
	bindings, _ = d.bindingsForInstance("i1")
	if len(bindings) != 0 {
		t.Fatalf("expected 0 bindings after delete, got %d", len(bindings))
	}
}

// ── idempotency ───────────────────────────────────────────────────────────────

func TestDB_Idempotency(t *testing.T) {
	d := openTestDB(t)

	// First call stores the key and returns "".
	existing, err := d.checkOrSetIdempotency("req-1", "resource-abc")
	if err != nil || existing != "" {
		t.Fatalf("first checkOrSet: %v %q", err, existing)
	}

	// Second call returns the stored resource ID.
	existing, err = d.checkOrSetIdempotency("req-1", "resource-xyz")
	if err != nil || existing != "resource-abc" {
		t.Fatalf("second checkOrSet: %v %q", err, existing)
	}

	// Different key is independent.
	existing, _ = d.checkOrSetIdempotency("req-2", "resource-2")
	if existing != "" {
		t.Fatalf("req-2 should be new, got %q", existing)
	}
}

// ── placement record ──────────────────────────────────────────────────────────

func TestDB_RecordPlacement(t *testing.T) {
	d := openTestDB(t)
	mustNamespace(t, d, "default")
	d.upsertService(sampleService("s1", "default"))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "s1", Namespace: "default", State: InstanceStateScheduled, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	p := &PlacementDecision{
		InstanceID: "i1", NodeID: "n1",
		Score: 0.85, Reason: "best fit", DecidedAt: now,
	}
	if err := d.recordPlacement(p); err != nil {
		t.Fatalf("recordPlacement: %v", err)
	}

	// Duplicate placement key (instance+time) should fail due to UNIQUE constraint.
	// (Testing idempotency at the DB level.)
	err := d.recordPlacement(p)
	if err == nil {
		t.Fatal("expected error on duplicate placement key")
	}
}

// ── recent failures ───────────────────────────────────────────────────────────

func TestDB_NodeRecentFailures(t *testing.T) {
	d := openTestDB(t)
	mustNamespace(t, d, "default")
	d.upsertService(sampleService("s1", "default"))
	d.upsertNode(sampleNode("n1"))
	now := time.Now().UTC()

	// Create two failed instances on n1 within the last hour.
	for i, id := range []string{"i1", "i2"} {
		_ = i
		inst := &Instance{
			ID: id, ServiceID: "s1", Namespace: "default",
			NodeID: "n1", State: InstanceStateFailed,
			CreatedAt: now, UpdatedAt: now,
		}
		d.upsertInstance(inst)
	}

	count, err := d.nodeRecentFailures("n1", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("nodeRecentFailures: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 recent failures, got %d", count)
	}

	// Future window should return 0.
	count, _ = d.nodeRecentFailures("n1", now.Add(1*time.Hour))
	if count != 0 {
		t.Fatalf("expected 0 for future window, got %d", count)
	}
}
