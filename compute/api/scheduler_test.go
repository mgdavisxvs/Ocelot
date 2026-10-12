package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mgdavisxvs/Ocelot/compute/node"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func insertTestNode(t *testing.T, db *sql.DB, id, hostname string, cpuCores, ramMb int, status string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO nodes (id, hostname, arch, cpu_cores, ram_mb, status, node_secret_hash, created_at)
		VALUES (?, ?, 'amd64', ?, ?, ?, 'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef', ?)`,
		id, hostname, cpuCores, ramMb, status, nowMs())
	if err != nil {
		t.Fatalf("insertTestNode %s: %v", id, err)
	}
}

func insertTestGPU(t *testing.T, db *sql.DB, id, nodeID string, deviceIndex, vramMb int) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO node_gpus (id, node_id, device_index, model, vram_mb, health)
		VALUES (?, ?, ?, 'RTX 4090', ?, 'ready')`,
		id, nodeID, deviceIndex, vramMb)
	if err != nil {
		t.Fatalf("insertTestGPU %s: %v", id, err)
	}
}

func insertTestWorkload(t *testing.T, db *sql.DB, orgID string, manifest node.WorkloadManifest, priority int) string {
	t.Helper()
	id := uuid.New().String()
	raw, _ := json.Marshal(manifest)
	_, err := db.Exec(`
		INSERT INTO workloads (id, org_id, name, status, priority, manifest, submitted_at)
		VALUES (?, ?, ?, 'submitted', ?, ?, ?)`,
		id, orgID, manifest.Name, priority, string(raw), nowMs())
	if err != nil {
		t.Fatalf("insertTestWorkload: %v", err)
	}
	return id
}

func workloadStatus(t *testing.T, db *sql.DB, id string) (status, nodeID string) {
	t.Helper()
	var nid sql.NullString
	if err := db.QueryRow("SELECT status, node_id FROM workloads WHERE id = ?", id).Scan(&status, &nid); err != nil {
		t.Fatalf("workloadStatus %s: %v", id, err)
	}
	return status, nid.String
}

func simpleManifest(name string, cpuMc, ramMb int) node.WorkloadManifest {
	return node.WorkloadManifest{
		Name: name,
		Type: "binary",
		Resources: node.ResourceSpec{
			CPUMillicores: cpuMc,
			RAMMb:         ramMb,
		},
	}
}

func gpuManifest(name string, cpuMc, ramMb, gpuVRAMMb int) node.WorkloadManifest {
	return node.WorkloadManifest{
		Name: name,
		Type: "binary",
		Resources: node.ResourceSpec{
			CPUMillicores: cpuMc,
			RAMMb:         ramMb,
			GPUCount:      1,
			GPUVRAMMb:     gpuVRAMMb,
		},
	}
}

func newTestScheduler(db *sql.DB) *Scheduler {
	return NewScheduler(db, 5*time.Second)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestScheduleBasic(t *testing.T) {
	_, db := newTestEnv(t)
	insertTestNode(t, db, "node-1", "host-1", 8, 16384, "ready")
	wid := insertTestWorkload(t, db, "org-test", simpleManifest("job-basic", 2000, 4096), 50)

	newTestScheduler(db).scheduleBatch(context.Background())

	status, nid := workloadStatus(t, db, wid)
	if status != "dispatched" {
		t.Errorf("expected dispatched, got %q", status)
	}
	if nid != "node-1" {
		t.Errorf("expected node-1, got %q", nid)
	}

	var resCount int
	db.QueryRow("SELECT COUNT(*) FROM reservations WHERE workload_id = ?", wid).Scan(&resCount)
	if resCount != 1 {
		t.Errorf("expected 1 reservation, got %d", resCount)
	}
}

func TestScheduleNoReadyNode(t *testing.T) {
	_, db := newTestEnv(t)
	insertTestNode(t, db, "node-lost", "host-lost", 8, 16384, "lost")
	wid := insertTestWorkload(t, db, "org-test", simpleManifest("job-nonode", 2000, 4096), 50)

	newTestScheduler(db).scheduleBatch(context.Background())

	status, _ := workloadStatus(t, db, wid)
	if status != "submitted" {
		t.Errorf("expected submitted (unchanged), got %q", status)
	}
}

func TestScheduleInsufficientCPU(t *testing.T) {
	_, db := newTestEnv(t)
	// Node has only 2 cores = 2000 mcores; workload needs 4000.
	insertTestNode(t, db, "node-small-cpu", "host-small-cpu", 2, 16384, "ready")
	wid := insertTestWorkload(t, db, "org-test", simpleManifest("job-cpu", 4000, 4096), 50)

	newTestScheduler(db).scheduleBatch(context.Background())

	status, _ := workloadStatus(t, db, wid)
	if status != "submitted" {
		t.Errorf("expected submitted (unchanged), got %q", status)
	}
}

func TestScheduleInsufficientRAM(t *testing.T) {
	_, db := newTestEnv(t)
	// Node has 4 GiB RAM; workload needs 8 GiB.
	insertTestNode(t, db, "node-small-ram", "host-small-ram", 8, 4096, "ready")
	wid := insertTestWorkload(t, db, "org-test", simpleManifest("job-ram", 500, 8192), 50)

	newTestScheduler(db).scheduleBatch(context.Background())

	status, _ := workloadStatus(t, db, wid)
	if status != "submitted" {
		t.Errorf("expected submitted (unchanged), got %q", status)
	}
}

func TestScheduleGPURequired(t *testing.T) {
	_, db := newTestEnv(t)
	insertTestNode(t, db, "gpu-node", "host-gpu", 8, 16384, "ready")
	insertTestGPU(t, db, "gpu-1", "gpu-node", 0, 24576) // 24 GiB

	wid := insertTestWorkload(t, db, "org-test", gpuManifest("job-gpu", 1000, 4096, 16384), 50)

	newTestScheduler(db).scheduleBatch(context.Background())

	status, nid := workloadStatus(t, db, wid)
	if status != "dispatched" {
		t.Errorf("expected dispatched, got %q", status)
	}
	if nid != "gpu-node" {
		t.Errorf("expected gpu-node, got %q", nid)
	}

	// GPU should be claimed.
	var gpuWorkload sql.NullString
	db.QueryRow("SELECT workload_id FROM node_gpus WHERE id = 'gpu-1'").Scan(&gpuWorkload)
	if !gpuWorkload.Valid || gpuWorkload.String != wid {
		t.Errorf("expected gpu-1.workload_id=%q, got %q", wid, gpuWorkload.String)
	}
}

func TestScheduleGPUInsufficientVRAM(t *testing.T) {
	_, db := newTestEnv(t)
	insertTestNode(t, db, "gpu-node-small", "host-gpu-small", 8, 16384, "ready")
	insertTestGPU(t, db, "gpu-tiny", "gpu-node-small", 0, 8192) // 8 GiB VRAM

	// Workload needs 16 GiB VRAM.
	wid := insertTestWorkload(t, db, "org-test", gpuManifest("job-gpu-big", 1000, 4096, 16384), 50)

	newTestScheduler(db).scheduleBatch(context.Background())

	status, _ := workloadStatus(t, db, wid)
	if status != "submitted" {
		t.Errorf("expected submitted (unchanged), got %q", status)
	}
}

func TestScheduleGPUAlreadyClaimed(t *testing.T) {
	_, db := newTestEnv(t)
	insertTestNode(t, db, "gpu-node-busy", "host-gpu-busy", 8, 16384, "ready")
	insertTestGPU(t, db, "gpu-claimed", "gpu-node-busy", 0, 24576)
	// Mark GPU as claimed by another workload.
	db.Exec("UPDATE node_gpus SET workload_id = 'some-other-wl' WHERE id = 'gpu-claimed'")

	wid := insertTestWorkload(t, db, "org-test", gpuManifest("job-need-gpu", 1000, 4096, 16384), 50)

	newTestScheduler(db).scheduleBatch(context.Background())

	status, _ := workloadStatus(t, db, wid)
	if status != "submitted" {
		t.Errorf("expected submitted (unchanged), got %q", status)
	}
}

func TestSchedulePriorityOrdering(t *testing.T) {
	_, db := newTestEnv(t)
	// Node can only run one workload at a time (1 core = 1000 mcores).
	insertTestNode(t, db, "node-1core", "host-1core", 1, 8192, "ready")

	// Submit low-priority first, then high-priority.
	wLow := insertTestWorkload(t, db, "org-test", simpleManifest("low", 1000, 512), 10)
	time.Sleep(2 * time.Millisecond) // ensure different submitted_at
	wHigh := insertTestWorkload(t, db, "org-test", simpleManifest("high", 1000, 512), 90)

	newTestScheduler(db).scheduleBatch(context.Background())

	statusLow, _ := workloadStatus(t, db, wLow)
	statusHigh, _ := workloadStatus(t, db, wHigh)

	if statusHigh != "dispatched" {
		t.Errorf("high-priority workload: expected dispatched, got %q", statusHigh)
	}
	if statusLow != "submitted" {
		t.Errorf("low-priority workload: expected submitted (waiting), got %q", statusLow)
	}
}

func TestScheduleReservationPreventsDoubleBook(t *testing.T) {
	_, db := newTestEnv(t)
	// Node has exactly 4 GiB RAM — enough for one 4 GiB workload.
	insertTestNode(t, db, "node-tight", "host-tight", 8, 4096, "ready")

	w1 := insertTestWorkload(t, db, "org-test", simpleManifest("job-a", 500, 4096), 50)
	w2 := insertTestWorkload(t, db, "org-test", simpleManifest("job-b", 500, 4096), 50)

	newTestScheduler(db).scheduleBatch(context.Background())

	s1, _ := workloadStatus(t, db, w1)
	s2, _ := workloadStatus(t, db, w2)

	dispatched := 0
	for _, s := range []string{s1, s2} {
		if s == "dispatched" {
			dispatched++
		}
	}
	if dispatched != 1 {
		t.Errorf("expected exactly 1 dispatched workload, got %d (s1=%s, s2=%s)", dispatched, s1, s2)
	}
}

func TestReleaseWorkloadResources(t *testing.T) {
	_, db := newTestEnv(t)
	insertTestNode(t, db, "node-rel", "host-rel", 8, 16384, "ready")
	insertTestGPU(t, db, "gpu-rel", "node-rel", 0, 24576)

	wid := insertTestWorkload(t, db, "org-test", gpuManifest("job-release", 1000, 4096, 16384), 50)
	newTestScheduler(db).scheduleBatch(context.Background())

	status, _ := workloadStatus(t, db, wid)
	if status != "dispatched" {
		t.Fatalf("prerequisite: expected dispatched, got %q", status)
	}

	releaseWorkloadResources(context.Background(), db, wid, nowMs())

	var resState string
	db.QueryRow("SELECT state FROM reservations WHERE workload_id = ?", wid).Scan(&resState)
	if resState != "released" {
		t.Errorf("expected reservation state=released, got %q", resState)
	}

	var gpuWorkload sql.NullString
	db.QueryRow("SELECT workload_id FROM node_gpus WHERE id = 'gpu-rel'").Scan(&gpuWorkload)
	if gpuWorkload.Valid {
		t.Errorf("expected gpu-rel.workload_id=NULL after release, got %q", gpuWorkload.String)
	}
}

func TestScheduleRunCancels(t *testing.T) {
	_, db := newTestEnv(t)
	s := NewScheduler(db, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after context cancellation")
	}
}
