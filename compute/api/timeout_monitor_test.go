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

func insertRunningWorkloadWithDeadline(t *testing.T, db *sql.DB, orgID, nodeID string, deadlineMs int64) string {
	t.Helper()
	id := uuid.New().String()
	manifest := node.WorkloadManifest{Name: "timeout-job", Type: "binary",
		Resources: node.ResourceSpec{CPUMillicores: 100, RAMMb: 128}}
	raw, _ := json.Marshal(manifest)
	_, err := db.Exec(`
		INSERT INTO workloads
			(id, org_id, name, status, priority, manifest, submitted_at, started_at, deadline, node_id)
		VALUES (?, ?, 'timeout-job', 'running', 50, ?, ?, ?, ?, ?)`,
		id, orgID, string(raw), nowMs(), nowMs(), deadlineMs, nodeID)
	if err != nil {
		t.Fatalf("insertRunningWorkloadWithDeadline: %v", err)
	}
	return id
}

func newTestTimeoutMonitor(db *sql.DB) *WorkloadTimeoutMonitor {
	return NewWorkloadTimeoutMonitor(db, 50*time.Millisecond)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestTimeoutMonitor_MarksExpiredWorkload(t *testing.T) {
	_, db := newTestEnv(t) // seeds org-test

	// Insert a node for the FK.
	insertTestNode(t, db, "node-to", "host-to", 4, 8192, "ready")

	// Past deadline: 1 second ago.
	pastDeadline := nowMs() - 1000
	wid := insertRunningWorkloadWithDeadline(t, db, "org-test", "node-to", pastDeadline)

	m := newTestTimeoutMonitor(db)
	m.check(context.Background())

	var status string
	db.QueryRow("SELECT status FROM workloads WHERE id = ?", wid).Scan(&status)
	if status != "timed_out" {
		t.Errorf("want timed_out, got %q", status)
	}
	var finishedAt sql.NullInt64
	db.QueryRow("SELECT finished_at FROM workloads WHERE id = ?", wid).Scan(&finishedAt)
	if !finishedAt.Valid {
		t.Error("want finished_at to be set after timeout")
	}
}

func TestTimeoutMonitor_IgnoresFreshWorkload(t *testing.T) {
	_, db := newTestEnv(t)

	insertTestNode(t, db, "node-fresh", "host-fresh", 4, 8192, "ready")

	// Future deadline: 1 hour from now.
	futureDeadline := nowMs() + 3_600_000
	wid := insertRunningWorkloadWithDeadline(t, db, "org-test", "node-fresh", futureDeadline)

	m := newTestTimeoutMonitor(db)
	m.check(context.Background())

	var status string
	db.QueryRow("SELECT status FROM workloads WHERE id = ?", wid).Scan(&status)
	if status != "running" {
		t.Errorf("want running (untouched), got %q", status)
	}
}

func TestTimeoutMonitor_IgnoresNullDeadline(t *testing.T) {
	_, db := newTestEnv(t)

	insertTestNode(t, db, "node-nod", "host-nod", 4, 8192, "ready")

	// Workload without a deadline.
	id := uuid.New().String()
	manifest := node.WorkloadManifest{Name: "no-deadline", Type: "binary",
		Resources: node.ResourceSpec{CPUMillicores: 100, RAMMb: 128}}
	raw, _ := json.Marshal(manifest)
	db.Exec(`
		INSERT INTO workloads
			(id, org_id, name, status, priority, manifest, submitted_at, started_at, node_id)
		VALUES (?, 'org-test', 'no-deadline', 'running', 50, ?, ?, ?, 'node-nod')`,
		id, string(raw), nowMs(), nowMs())

	m := newTestTimeoutMonitor(db)
	m.check(context.Background())

	var status string
	db.QueryRow("SELECT status FROM workloads WHERE id = ?", id).Scan(&status)
	if status != "running" {
		t.Errorf("want running (no deadline = no timeout), got %q", status)
	}
}

func TestTimeoutMonitor_IgnoresTerminalWorkload(t *testing.T) {
	_, db := newTestEnv(t)

	insertTestNode(t, db, "node-term", "host-term", 4, 8192, "ready")

	pastDeadline := nowMs() - 1000
	wid := insertRunningWorkloadWithDeadline(t, db, "org-test", "node-term", pastDeadline)
	db.Exec("UPDATE workloads SET status='completed' WHERE id=?", wid)

	m := newTestTimeoutMonitor(db)
	m.check(context.Background())

	var status string
	db.QueryRow("SELECT status FROM workloads WHERE id = ?", wid).Scan(&status)
	if status != "completed" {
		t.Errorf("want completed (terminal not re-timed-out), got %q", status)
	}
}

func TestTimeoutMonitor_ReleasesResources(t *testing.T) {
	_, db := newTestEnv(t)

	insertTestNode(t, db, "node-res", "host-res", 8, 16384, "ready")
	insertTestGPU(t, db, "gpu-res", "node-res", 0, 8192)

	wid := uuid.New().String()
	manifest := node.WorkloadManifest{Name: "res-job", Type: "binary",
		Resources: node.ResourceSpec{CPUMillicores: 1000, RAMMb: 4096, GPUCount: 1, GPUVRAMMb: 4096}}
	raw, _ := json.Marshal(manifest)

	// Manually wire reservation and GPU claim to test resource release.
	db.Exec(`INSERT INTO workloads
		(id, org_id, name, status, priority, manifest, submitted_at, started_at, deadline, node_id)
		VALUES (?, 'org-test', 'res-job', 'running', 50, ?, ?, ?, ?, 'node-res')`,
		wid, string(raw), nowMs(), nowMs(), nowMs()-500)
	db.Exec(`INSERT INTO reservations
		(id, node_id, workload_id, cpu_mcores, ram_mb, gpu_id, gpu_vram_mb, state, created_at)
		VALUES (?, 'node-res', ?, 1000, 4096, 'gpu-res', 4096, 'running', ?)`,
		uuid.New().String(), wid, nowMs())
	db.Exec("UPDATE node_gpus SET workload_id=?, vram_reserved=4096 WHERE id='gpu-res'", wid)

	m := newTestTimeoutMonitor(db)
	m.check(context.Background())

	var resState string
	db.QueryRow("SELECT state FROM reservations WHERE workload_id=?", wid).Scan(&resState)
	if resState != "released" {
		t.Errorf("want reservation released, got %q", resState)
	}
	var gpuWL sql.NullString
	db.QueryRow("SELECT workload_id FROM node_gpus WHERE id='gpu-res'").Scan(&gpuWL)
	if gpuWL.Valid {
		t.Errorf("want gpu-res.workload_id=NULL after timeout, got %q", gpuWL.String)
	}
}

func TestTimeoutMonitor_RunCancels(t *testing.T) {
	_, db := newTestEnv(t)
	m := NewWorkloadTimeoutMonitor(db, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after context cancellation")
	}
}
