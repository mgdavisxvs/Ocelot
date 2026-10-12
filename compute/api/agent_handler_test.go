package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/compute/node"
	"github.com/mgdavisxvs/Ocelot/compute/schema"
)

const testBootstrap = "test-bootstrap-secret"

// ── Test infrastructure ───────────────────────────────────────────────────────

func newTestEnv(t *testing.T) (*AgentHandler, *sql.DB) {
	t.Helper()
	db, err := schema.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Seed org required for workload FK in desired-state tests.
	if _, err = db.Exec(`INSERT INTO orgs (id, name, created_at) VALUES ('org-test', 'Test Org', ?)`, nowMs()); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	return New(db, testBootstrap), db
}

// registerNode calls POST /v1/nodes/register and returns the parsed response.
func registerNode(t *testing.T, h *AgentHandler, req node.RegisterRequest) node.RegisterResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/v1/nodes/register", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testBootstrap)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleRegister(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp node.RegisterResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return resp
}

// signRequest adds X-Ocelot-* auth headers to r for the given node credentials.
func signRequest(r *http.Request, nodeID, secretHex string, body []byte) {
	secret, _ := hex.DecodeString(secretHex)
	tsStr := strconv.FormatInt(time.Now().UnixMilli(), 10)
	if body == nil {
		body = []byte{}
	}
	bodyHash := sha256.Sum256(body)
	message := nodeID + ":" + tsStr + ":" + hex.EncodeToString(bodyHash[:])
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(message))
	r.Header.Set("X-Ocelot-Node-ID", nodeID)
	r.Header.Set("X-Ocelot-Timestamp", tsStr)
	r.Header.Set("X-Ocelot-Signature", hex.EncodeToString(mac.Sum(nil)))
}

func doPost(h *AgentHandler, path, nodeID, secretHex string, reqBody any, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	body, _ := json.Marshal(reqBody)
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	signRequest(r, nodeID, secretHex, body)
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

// ── Registration ──────────────────────────────────────────────────────────────

func TestRegisterOK(t *testing.T) {
	h, db := newTestEnv(t)
	resp := registerNode(t, h, node.RegisterRequest{
		Hostname:     "node-01.test",
		Arch:         "amd64",
		AgentVersion: "1.0.0",
		CPUCores:     8,
		RAMMb:        32768,
		StorageGb:    500,
		GPUs: []node.GPUSpec{
			{DeviceIndex: 0, Model: "RTX 3090", VRAMMb: 24576, CUDACap: "8.6"},
		},
		Labels: map[string]string{"tier": "compute"},
	})

	if resp.NodeID == "" {
		t.Fatal("NodeID empty")
	}
	if len(resp.NodeSecret) != 64 {
		t.Fatalf("NodeSecret must be 64 hex chars, got %d", len(resp.NodeSecret))
	}
	if resp.HeartbeatIntervalSec != 30 {
		t.Fatalf("HeartbeatIntervalSec: want 30, got %d", resp.HeartbeatIntervalSec)
	}

	// Verify DB row.
	var hostname, arch string
	var cpuCores int
	if err := db.QueryRow("SELECT hostname, arch, cpu_cores FROM nodes WHERE id = ?", resp.NodeID).
		Scan(&hostname, &arch, &cpuCores); err != nil {
		t.Fatalf("db lookup: %v", err)
	}
	if hostname != "node-01.test" || arch != "amd64" || cpuCores != 8 {
		t.Fatalf("unexpected db values: hostname=%s arch=%s cores=%d", hostname, arch, cpuCores)
	}

	// GPU row.
	var gpuCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM node_gpus WHERE node_id = ?", resp.NodeID).Scan(&gpuCount); err != nil {
		t.Fatal(err)
	}
	if gpuCount != 1 {
		t.Fatalf("expected 1 GPU row, got %d", gpuCount)
	}
}

func TestRegisterDuplicateHostname(t *testing.T) {
	h, _ := newTestEnv(t)
	req := node.RegisterRequest{
		Hostname: "dup-host.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 100,
	}
	registerNode(t, h, req) // first: OK

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/v1/nodes/register", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testBootstrap)
	w := httptest.NewRecorder()
	h.handleRegister(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	var errResp node.ErrorResponse
	json.NewDecoder(w.Body).Decode(&errResp) //nolint:errcheck
	if errResp.Error.Code != "HOSTNAME_CONFLICT" {
		t.Fatalf("wrong error code: %s", errResp.Error.Code)
	}
}

func TestRegisterBadBootstrap(t *testing.T) {
	h, _ := newTestEnv(t)
	body, _ := json.Marshal(node.RegisterRequest{
		Hostname: "x.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})
	r := httptest.NewRequest(http.MethodPost, "/v1/nodes/register", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	h.handleRegister(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestRegisterValidation(t *testing.T) {
	h, _ := newTestEnv(t)
	cases := []struct {
		name string
		req  node.RegisterRequest
	}{
		{"empty hostname", node.RegisterRequest{Arch: "amd64", AgentVersion: "1.0.0", CPUCores: 1, RAMMb: 1, StorageGb: 0}},
		{"bad arch", node.RegisterRequest{Hostname: "x", Arch: "sparc", AgentVersion: "1.0.0", CPUCores: 1, RAMMb: 1}},
		{"zero cpu", node.RegisterRequest{Hostname: "x", Arch: "amd64", AgentVersion: "1.0.0", CPUCores: 0, RAMMb: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.req)
			r := httptest.NewRequest(http.MethodPost, "/v1/nodes/register", bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+testBootstrap)
			w := httptest.NewRecorder()
			h.handleRegister(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d", w.Code)
			}
		})
	}
}

// ── Re-registration ───────────────────────────────────────────────────────────

func TestReregister(t *testing.T) {
	h, db := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "rekey-node.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})
	oldSecret := reg.NodeSecret

	r := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+reg.NodeID+"/reregister", http.NoBody)
	r.SetPathValue("id", reg.NodeID)
	r.Header.Set("Authorization", "Bearer "+testBootstrap)
	w := httptest.NewRecorder()
	h.handleReregister(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("reregister: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if resp["node_secret"] == "" || resp["node_secret"] == oldSecret {
		t.Fatal("expected new non-empty secret different from old")
	}

	// DB must reflect the new secret.
	var storedHex string
	db.QueryRow("SELECT node_secret_hash FROM nodes WHERE id = ?", reg.NodeID).Scan(&storedHex) //nolint:errcheck
	if storedHex != resp["node_secret"] {
		t.Fatal("stored secret does not match response")
	}
}

// ── Heartbeat ─────────────────────────────────────────────────────────────────

func TestHeartbeat(t *testing.T) {
	h, db := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "hb-node.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})

	before := time.Now().UnixMilli()
	w := doPost(h, "/v1/nodes/"+reg.NodeID+"/heartbeat", reg.NodeID, reg.NodeSecret,
		node.HeartbeatRequest{Ts: before, Status: node.StatusReady, LoadAvg1m: 0.5},
		func(rw http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", reg.NodeID)
			h.handleHeartbeat(rw, r)
		})
	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat: want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp node.HeartbeatResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if !resp.OK {
		t.Fatal("expected ok: true")
	}
	if resp.ServerTimeMs <= 0 {
		t.Fatal("expected server_time_ms > 0")
	}

	// DB status must be updated.
	var status string
	db.QueryRow("SELECT status FROM nodes WHERE id = ?", reg.NodeID).Scan(&status) //nolint:errcheck
	if status != "ready" {
		t.Fatalf("expected status 'ready', got %s", status)
	}
}

func TestHeartbeatReplayWindow(t *testing.T) {
	h, _ := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "replay-node.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})

	body, _ := json.Marshal(node.HeartbeatRequest{Ts: nowMs(), Status: node.StatusReady})
	r := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+reg.NodeID+"/heartbeat", bytes.NewReader(body))
	r.SetPathValue("id", reg.NodeID)

	// Manually set a stale timestamp (2 minutes ago).
	staleTs := strconv.FormatInt(time.Now().Add(-2*time.Minute).UnixMilli(), 10)
	secret, _ := hex.DecodeString(reg.NodeSecret)
	bodyHash := sha256.Sum256(body)
	message := reg.NodeID + ":" + staleTs + ":" + hex.EncodeToString(bodyHash[:])
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(message))
	r.Header.Set("X-Ocelot-Node-ID", reg.NodeID)
	r.Header.Set("X-Ocelot-Timestamp", staleTs)
	r.Header.Set("X-Ocelot-Signature", hex.EncodeToString(mac.Sum(nil)))

	w := httptest.NewRecorder()
	h.handleHeartbeat(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replay: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHeartbeatSignatureMismatch(t *testing.T) {
	h, _ := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "badsig-node.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})

	body, _ := json.Marshal(node.HeartbeatRequest{Ts: nowMs(), Status: node.StatusReady})
	r := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+reg.NodeID+"/heartbeat", bytes.NewReader(body))
	r.SetPathValue("id", reg.NodeID)
	// Sign with a wrong secret.
	wrongSecret := make([]byte, 32)
	signRequest(r, reg.NodeID, hex.EncodeToString(wrongSecret), body)

	w := httptest.NewRecorder()
	h.handleHeartbeat(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("badsig: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHeartbeatUnknownNode(t *testing.T) {
	h, _ := newTestEnv(t)
	// Register once to get a valid secret, then heartbeat with a different (ghost) nodeID.
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "ghost-src.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})
	ghostID := "00000000-0000-0000-0000-000000000000"
	body, _ := json.Marshal(node.HeartbeatRequest{Ts: nowMs(), Status: node.StatusReady})
	r := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+ghostID+"/heartbeat", bytes.NewReader(body))
	r.SetPathValue("id", ghostID)
	signRequest(r, ghostID, reg.NodeSecret, body) // wrong nodeID in path but matching sig doesn't exist
	w := httptest.NewRecorder()
	h.handleHeartbeat(w, r)
	// Should get 401 (node_id mismatch) or 410 (not found); either is a 4xx.
	if w.Code == http.StatusOK {
		t.Fatalf("expected rejection for unknown node, got 200")
	}
}

// ── Inventory ─────────────────────────────────────────────────────────────────

func TestInventory(t *testing.T) {
	h, db := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "inv-node.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})

	invReq := node.InventoryRequest{
		Ts:           nowMs(),
		CPUCores:     8,
		RAMMb:        16384,
		StorageGb:    200,
		CPUFreePct:   50.0,
		RAMFreeMb:    8192,
		DiskFreeGb:   190,
		AgentVersion: "1.0.1",
		GPUs: []node.InventoryGPU{
			{DeviceIndex: 0, Model: "RTX 3090", VRAMMb: 24576, VRAMFreeMb: 20000, Health: node.GPUHealthReady},
		},
	}
	w := doPost(h, "/v1/nodes/"+reg.NodeID+"/inventory", reg.NodeID, reg.NodeSecret, invReq,
		func(rw http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", reg.NodeID)
			h.handleInventory(rw, r)
		})
	if w.Code != http.StatusOK {
		t.Fatalf("inventory: want 200, got %d: %s", w.Code, w.Body.String())
	}

	var cpuCores int
	var agentVersion string
	db.QueryRow("SELECT cpu_cores, agent_version FROM nodes WHERE id = ?", reg.NodeID). //nolint:errcheck
		Scan(&cpuCores, &agentVersion)
	if cpuCores != 8 {
		t.Fatalf("cpu_cores: want 8, got %d", cpuCores)
	}
	if agentVersion != "1.0.1" {
		t.Fatalf("agent_version: want 1.0.1, got %s", agentVersion)
	}

	// GPU row must be upserted.
	var gpuCount int
	db.QueryRow("SELECT COUNT(*) FROM node_gpus WHERE node_id = ?", reg.NodeID).Scan(&gpuCount) //nolint:errcheck
	if gpuCount != 1 {
		t.Fatalf("expected 1 GPU row, got %d", gpuCount)
	}
}

// ── Desired state ─────────────────────────────────────────────────────────────

func TestDesiredEmpty(t *testing.T) {
	h, _ := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "desired-empty.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/nodes/"+reg.NodeID+"/desired", http.NoBody)
	r.SetPathValue("id", reg.NodeID)
	signRequest(r, reg.NodeID, reg.NodeSecret, nil)
	w := httptest.NewRecorder()
	h.handleDesired(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("desired: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp node.DesiredStateResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if resp.SchemaVersion != 1 {
		t.Fatalf("schema_version: want 1, got %d", resp.SchemaVersion)
	}
	if len(resp.Workloads) != 0 {
		t.Fatalf("expected 0 workloads, got %d", len(resp.Workloads))
	}
}

func TestDesiredWithWorkload(t *testing.T) {
	h, db := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "desired-wl.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})

	// Insert a dispatched workload directly.
	manifest := node.WorkloadManifest{
		Name:       "test-job",
		Type:       "script",
		Entrypoint: "/usr/bin/test.sh",
	}
	manifestJSON, _ := json.Marshal(manifest)
	wlID := fmt.Sprintf("wl-%s", reg.NodeID[:8])
	_, err := db.Exec(`
		INSERT INTO workloads (id, org_id, name, status, priority, manifest, node_id, submitted_at)
		VALUES (?, 'org-test', 'test-job', 'dispatched', 50, ?, ?, ?)`,
		wlID, string(manifestJSON), reg.NodeID, nowMs())
	if err != nil {
		t.Fatalf("insert workload: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/nodes/"+reg.NodeID+"/desired", http.NoBody)
	r.SetPathValue("id", reg.NodeID)
	signRequest(r, reg.NodeID, reg.NodeSecret, nil)
	w := httptest.NewRecorder()
	h.handleDesired(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("desired: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp node.DesiredStateResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if len(resp.Workloads) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(resp.Workloads))
	}
	if resp.Workloads[0].ID != wlID {
		t.Fatalf("wrong workload_id: %s", resp.Workloads[0].ID)
	}
	if resp.Workloads[0].Action != node.ActionRun {
		t.Fatalf("expected action=run, got %s", resp.Workloads[0].Action)
	}
	if resp.Workloads[0].Manifest.Name != "test-job" {
		t.Fatalf("manifest name mismatch: %s", resp.Workloads[0].Manifest.Name)
	}
}

// ── Health events ─────────────────────────────────────────────────────────────

func seedWorkload(t *testing.T, db *sql.DB, nodeID, wlID string) {
	t.Helper()
	manifest := node.WorkloadManifest{Name: "health-job", Type: "script", Entrypoint: "/bin/test"}
	manifestJSON, _ := json.Marshal(manifest)
	if _, err := db.Exec(`
		INSERT INTO workloads (id, org_id, name, status, priority, manifest, node_id, submitted_at)
		VALUES (?, 'org-test', 'health-job', 'dispatched', 50, ?, ?, ?)`,
		wlID, string(manifestJSON), nodeID, nowMs()); err != nil {
		t.Fatalf("seed workload: %v", err)
	}
}

func TestHealthStarted(t *testing.T) {
	h, db := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "health-start.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})
	wlID := "wl-started-001"
	seedWorkload(t, db, reg.NodeID, wlID)

	ec := 0
	healthReq := node.HealthRequest{
		Ts: nowMs(),
		Events: []node.HealthEvent{
			{WorkloadID: wlID, Kind: node.EventStarted, Ts: nowMs(), PID: 1234, ExitCode: &ec},
		},
	}
	w := doPost(h, "/v1/nodes/"+reg.NodeID+"/health", reg.NodeID, reg.NodeSecret, healthReq,
		func(rw http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", reg.NodeID)
			h.handleHealth(rw, r)
		})
	if w.Code != http.StatusOK {
		t.Fatalf("health: want 200, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	db.QueryRow("SELECT status FROM workloads WHERE id = ?", wlID).Scan(&status) //nolint:errcheck
	if status != "running" {
		t.Fatalf("expected status 'running', got %s", status)
	}
}

func TestHealthCompleted(t *testing.T) {
	h, db := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "health-done.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})
	wlID := "wl-done-001"
	seedWorkload(t, db, reg.NodeID, wlID)

	ec := 0
	healthReq := node.HealthRequest{
		Ts: nowMs(),
		Events: []node.HealthEvent{
			{WorkloadID: wlID, Kind: node.EventCompleted, Ts: nowMs(), ExitCode: &ec},
		},
	}
	w := doPost(h, "/v1/nodes/"+reg.NodeID+"/health", reg.NodeID, reg.NodeSecret, healthReq,
		func(rw http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", reg.NodeID)
			h.handleHealth(rw, r)
		})
	if w.Code != http.StatusOK {
		t.Fatalf("health: want 200, got %d", w.Code)
	}

	var status string
	var finishedAt sql.NullInt64
	db.QueryRow("SELECT status, finished_at FROM workloads WHERE id = ?", wlID). //nolint:errcheck
		Scan(&status, &finishedAt)
	if status != "completed" {
		t.Fatalf("expected 'completed', got %s", status)
	}
	if !finishedAt.Valid {
		t.Fatal("finished_at must be set after completion event")
	}
}

func TestHealthFailed(t *testing.T) {
	h, db := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "health-fail.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})
	wlID := "wl-fail-001"
	seedWorkload(t, db, reg.NodeID, wlID)

	ec := 1
	healthReq := node.HealthRequest{
		Ts: nowMs(),
		Events: []node.HealthEvent{
			{WorkloadID: wlID, Kind: node.EventFailed, Ts: nowMs(), ExitCode: &ec, Message: "OOM"},
		},
	}
	w := doPost(h, "/v1/nodes/"+reg.NodeID+"/health", reg.NodeID, reg.NodeSecret, healthReq,
		func(rw http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", reg.NodeID)
			h.handleHealth(rw, r)
		})
	if w.Code != http.StatusOK {
		t.Fatalf("health: want 200, got %d", w.Code)
	}
	var status string
	var exitCode sql.NullInt64
	db.QueryRow("SELECT status, exit_code FROM workloads WHERE id = ?", wlID). //nolint:errcheck
		Scan(&status, &exitCode)
	if status != "failed" {
		t.Fatalf("expected 'failed', got %s", status)
	}
	if !exitCode.Valid || exitCode.Int64 != 1 {
		t.Fatalf("exit_code: want 1, got %v", exitCode)
	}
}

// ── Metrics ───────────────────────────────────────────────────────────────────

func TestMetrics(t *testing.T) {
	h, db := newTestEnv(t)
	reg := registerNode(t, h, node.RegisterRequest{
		Hostname: "metrics-node.test", Arch: "amd64", AgentVersion: "1.0.0",
		CPUCores: 4, RAMMb: 8192, StorageGb: 0,
	})
	wlID := "wl-metrics-001"
	seedWorkload(t, db, reg.NodeID, wlID)

	ts := nowMs()
	metricsReq := node.MetricsRequest{
		Ts:          ts,
		IntervalSec: 60,
		Workloads: []node.WorkloadMetrics{
			{WorkloadID: wlID, CPUMillicoreSec: 60000, RAMMbSec: 245760},
		},
		Node: node.NodeMetrics{CPUMillicoreSec: 240000, RAMMbSec: 983040, WattsSec: 8640},
	}
	w := doPost(h, "/v1/nodes/"+reg.NodeID+"/metrics", reg.NodeID, reg.NodeSecret, metricsReq,
		func(rw http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", reg.NodeID)
			h.handleMetrics(rw, r)
		})
	if w.Code != http.StatusOK {
		t.Fatalf("metrics: want 200, got %d: %s", w.Code, w.Body.String())
	}

	var wlCount, nodeCount int
	db.QueryRow("SELECT COUNT(*) FROM workload_metrics WHERE workload_id = ?", wlID).Scan(&wlCount) //nolint:errcheck
	db.QueryRow("SELECT COUNT(*) FROM node_metrics WHERE node_id = ?", reg.NodeID).Scan(&nodeCount) //nolint:errcheck
	if wlCount != 1 {
		t.Fatalf("expected 1 workload_metrics row, got %d", wlCount)
	}
	if nodeCount != 1 {
		t.Fatalf("expected 1 node_metrics row, got %d", nodeCount)
	}
}
