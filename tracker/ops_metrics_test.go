package tracker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── Feature 3: Ops Metrics Endpoint tests ─────────────────────────────────────

func buildOpsServer(t *testing.T) (*OpsServer, *readyFlag) {
	t.Helper()
	cfg := newTestConfig()
	cfg.OpsAddr = ":0"

	stats := &Stats{}
	stats.StartTime = time.Now()
	worker := &Worker{
		Config:    cfg,
		DB:        newMockDB(),
		SiteComm:  newMockSiteComm(),
		Torrents:  NewTorrentList(),
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     stats,
	}
	return NewOpsServer(cfg, worker)
}

func TestOpsMetricsJSONFields(t *testing.T) {
	os, _ := buildOpsServer(t)
	w := httptest.NewRecorder()
	os.handleMetrics(w, httptest.NewRequest("GET", "/metrics", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	required := []string{
		"uptime_seconds", "open_connections", "announcements", "seeders",
		"swarm_nodes_total", "swarm_nodes_reachable", "swarm_nodes_flapping",
		"swarm_nodes_unreachable", "swarm_replicas_seeding", "swarm_replicas_verified",
		"swarm_artifacts_total",
	}
	for _, key := range required {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in /metrics JSON", key)
		}
	}
}

func TestOpsMetricsSwarmGaugesReflectState(t *testing.T) {
	ops, _ := buildOpsServer(t)

	nodes := NewNodeRegistry()
	replicas := NewNodeReplicaMap()
	artifacts := NewArtifactList()

	// Register one REACHABLE and one UNREACHABLE node.
	n1 := NewNodeIdentity(1, "n1", "pk1", FailureDomainLabels{})
	n2 := NewNodeIdentity(2, "n2", "pk2", FailureDomainLabels{})
	n2.MarkUnreachable()
	nodes.Register(n1)
	nodes.Register(n2)

	// One artifact with one SEEDING replica.
	a := NewArtifact("metrichash")
	artifacts.Set("metrichash", a)
	r := replicas.GetOrCreate(1, "metrichash", ReplicaStandard)
	for _, s := range []NodeReplicaState{
		ReplicaStateRequested, ReplicaStateSwarming, ReplicaStateBTComplete,
		ReplicaStateHashVerify, ReplicaStateVerified, ReplicaStateSeeding,
	} {
		r.Transition(s) //nolint:errcheck
	}

	ops.AttachSwarmDeps(nodes, replicas, artifacts)
	w := httptest.NewRecorder()
	ops.handleMetrics(w, httptest.NewRequest("GET", "/metrics", nil))

	var m map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &m) //nolint:errcheck

	check := func(key string, want float64) {
		t.Helper()
		v, ok := m[key]
		if !ok {
			t.Errorf("key %q missing", key)
			return
		}
		if v.(float64) != want {
			t.Errorf("%s: want %v, got %v", key, want, v)
		}
	}
	check("swarm_nodes_total", 2)
	check("swarm_nodes_reachable", 1)
	check("swarm_nodes_unreachable", 1)
	check("swarm_replicas_seeding", 1)
	check("swarm_artifacts_total", 1)
}

func TestOpsMetricsPrometheusFormat(t *testing.T) {
	ops, _ := buildOpsServer(t)
	nodes := NewNodeRegistry()
	replicas := NewNodeReplicaMap()
	artifacts := NewArtifactList()
	ops.AttachSwarmDeps(nodes, replicas, artifacts)

	w := httptest.NewRecorder()
	ops.handleMetricsPrometheus(w, httptest.NewRequest("GET", "/metrics/prometheus", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("want text/plain content-type, got %q", ct)
	}
	body := w.Body.String()
	required := []string{
		"# HELP ocelot_uptime_seconds",
		"# TYPE ocelot_uptime_seconds gauge",
		"ocelot_uptime_seconds",
		"# HELP ocelot_swarm_nodes_total",
		"ocelot_swarm_nodes_total",
		"ocelot_swarm_replicas_seeding",
		"ocelot_swarm_artifacts_total",
	}
	for _, line := range required {
		if !strings.Contains(body, line) {
			t.Errorf("Prometheus output missing %q", line)
		}
	}
}

func TestOpsMetricsNoSwarmDepsOK(t *testing.T) {
	ops, _ := buildOpsServer(t)
	// No AttachSwarmDeps — swarm gauges should all be zero, no panic.

	w := httptest.NewRecorder()
	ops.handleMetrics(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["swarm_nodes_total"].(float64) != 0 {
		t.Errorf("swarm_nodes_total want 0 when no deps attached")
	}
}
