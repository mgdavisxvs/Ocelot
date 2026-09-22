package api

import (
	"context"
	"testing"
	"time"
)

func TestMonitorMarksLostNode(t *testing.T) {
	h, db := newTestEnv(t)
	_ = h

	// Insert a node with a heartbeat older than the loss threshold.
	staleMs := time.Now().Add(-10 * time.Minute).UnixMilli()
	_, err := db.Exec(`
		INSERT INTO nodes (id, hostname, arch, cpu_cores, ram_mb, status, last_heartbeat,
		                   node_secret_hash, created_at)
		VALUES ('node-stale','stale-host','amd64',4,8192,'ready',?,
		        'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef',?)`,
		staleMs, nowMs())
	if err != nil {
		t.Fatalf("insert stale node: %v", err)
	}

	m := NewNodeLossMonitor(db, 30*time.Second)
	m.check(context.Background())

	var status string
	err = db.QueryRow("SELECT status FROM nodes WHERE id = 'node-stale'").Scan(&status)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "lost" {
		t.Errorf("expected status=lost, got %q", status)
	}

	// Verify event was recorded.
	var evCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='node.lost' AND subject_id='node-stale'`).Scan(&evCount)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if evCount == 0 {
		t.Error("expected node.lost event in journal, got none")
	}
}

func TestMonitorIgnoresFreshNode(t *testing.T) {
	h, db := newTestEnv(t)
	_ = h

	// Insert a node with a recent heartbeat.
	freshMs := time.Now().Add(-10 * time.Second).UnixMilli()
	_, err := db.Exec(`
		INSERT INTO nodes (id, hostname, arch, cpu_cores, ram_mb, status, last_heartbeat,
		                   node_secret_hash, created_at)
		VALUES ('node-fresh','fresh-host','amd64',4,8192,'ready',?,
		        'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef',?)`,
		freshMs, nowMs())
	if err != nil {
		t.Fatalf("insert fresh node: %v", err)
	}

	m := NewNodeLossMonitor(db, 30*time.Second)
	m.check(context.Background())

	var status string
	err = db.QueryRow("SELECT status FROM nodes WHERE id = 'node-fresh'").Scan(&status)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "ready" {
		t.Errorf("expected status=ready, got %q", status)
	}
}

func TestMonitorIgnoresAlreadyLostNode(t *testing.T) {
	h, db := newTestEnv(t)
	_ = h

	staleMs := time.Now().Add(-10 * time.Minute).UnixMilli()
	_, err := db.Exec(`
		INSERT INTO nodes (id, hostname, arch, cpu_cores, ram_mb, status, last_heartbeat,
		                   node_secret_hash, created_at)
		VALUES ('node-already-lost','lost-host','amd64',4,8192,'lost',?,
		        'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef',?)`,
		staleMs, nowMs())
	if err != nil {
		t.Fatalf("insert lost node: %v", err)
	}

	m := NewNodeLossMonitor(db, 30*time.Second)
	m.check(context.Background())

	// No new event should be emitted for already-lost nodes.
	var evCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='node.lost' AND subject_id='node-already-lost'`).Scan(&evCount)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if evCount != 0 {
		t.Errorf("expected no node.lost event for already-lost node, got %d", evCount)
	}
}

func TestMonitorIgnoresNullHeartbeat(t *testing.T) {
	h, db := newTestEnv(t)
	_ = h

	// A joining node has no heartbeat yet — should not be marked lost.
	_, err := db.Exec(`
		INSERT INTO nodes (id, hostname, arch, cpu_cores, ram_mb, status, last_heartbeat,
		                   node_secret_hash, created_at)
		VALUES ('node-joining','joining-host','amd64',4,8192,'joining',NULL,
		        'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef',?)`,
		nowMs())
	if err != nil {
		t.Fatalf("insert joining node: %v", err)
	}

	m := NewNodeLossMonitor(db, 30*time.Second)
	m.check(context.Background())

	var status string
	err = db.QueryRow("SELECT status FROM nodes WHERE id = 'node-joining'").Scan(&status)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "joining" {
		t.Errorf("expected status=joining, got %q", status)
	}
}

func TestMonitorRunCancels(t *testing.T) {
	_, db := newTestEnv(t)
	m := NewNodeLossMonitor(db, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after context cancellation")
	}
}
