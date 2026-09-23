package tracker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// buildTestControlServer wires a ControlServer with all swarm deps backed by
// in-memory fakes. Avoids real SQLite; suitable for unit tests.
func buildTestControlServer(t *testing.T) *ControlServer {
	t.Helper()
	cfg := &Config{SitePassword: "testpass"}
	worker := &Worker{
		Config:   cfg,
		Torrents: NewTorrentList(),
		Users:    NewUserList(),
		Stats:    &Stats{StartTime: time.Now()},
	}
	cs := NewControlServer(cfg, worker)
	nodes := NewNodeRegistry()
	replicas := NewNodeReplicaMap()
	admission := NewSwarmAdmissionPolicy()
	artifacts := NewArtifactList()
	controller := NewSwarmPolicyController(
		artifacts, worker.Torrents, nodes, replicas, admission, 60*time.Second,
	)
	cs.AttachControllerDeps(nodes, replicas, admission, controller)
	cs.RegisterAgentRoutes()
	return cs
}

func authedRequest(t *testing.T, method, path string, body interface{}) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Authorization", "Bearer testpass")
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestControlServerUnauthorized(t *testing.T) {
	cs := buildTestControlServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	// No Authorization header
	w := httptest.NewRecorder()
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 without token, got %d", w.Code)
	}
}

func TestControlServerWrongToken(t *testing.T) {
	cs := buildTestControlServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	r.Header.Set("Authorization", "Bearer wrongtoken")
	w := httptest.NewRecorder()
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 with wrong token, got %d", w.Code)
	}
}

func TestHandleNodesRoundTrip(t *testing.T) {
	cs := buildTestControlServer(t)

	// Register a node.
	w1 := httptest.NewRecorder()
	r1 := authedRequest(t, http.MethodPost, "/api/v1/nodes",
		map[string]interface{}{
			"node_id":  42,
			"hostname": "test.node",
			"passkey":  "pk32charslongpadpadpadpadpadpaa",
			"tier":     "core",
		})
	cs.httpSrv.Handler.ServeHTTP(w1, r1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("register: want 201, got %d: %s", w1.Code, w1.Body.String())
	}

	// List nodes — must see it.
	w2 := httptest.NewRecorder()
	r2 := authedRequest(t, http.MethodGet, "/api/v1/nodes", nil)
	cs.httpSrv.Handler.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", w2.Code)
	}
	var nodes []map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&nodes)
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	if nodes[0]["hostname"] != "test.node" {
		t.Errorf("hostname mismatch: %v", nodes[0]["hostname"])
	}
	if nodes[0]["tier"] != "CORE" {
		t.Errorf("tier mismatch: %v", nodes[0]["tier"])
	}
}

func TestHandleNodeRegisterMissingFields(t *testing.T) {
	cs := buildTestControlServer(t)
	w := httptest.NewRecorder()
	r := authedRequest(t, http.MethodPost, "/api/v1/nodes",
		map[string]interface{}{"hostname": ""}) // node_id = 0, hostname = ""
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing required fields, got %d", w.Code)
	}
}

func TestHandleNodeUnregister(t *testing.T) {
	cs := buildTestControlServer(t)

	// Register first.
	r1 := authedRequest(t, http.MethodPost, "/api/v1/nodes",
		map[string]interface{}{"node_id": 7, "hostname": "tmp.node"})
	cs.httpSrv.Handler.ServeHTTP(httptest.NewRecorder(), r1)

	// Unregister.
	w := httptest.NewRecorder()
	r2 := authedRequest(t, http.MethodDelete, "/api/v1/nodes?node_id=7", nil)
	cs.httpSrv.Handler.ServeHTTP(w, r2)
	if w.Code != http.StatusOK {
		t.Errorf("want 200 on unregister, got %d", w.Code)
	}

	// List should be empty again.
	w2 := httptest.NewRecorder()
	r3 := authedRequest(t, http.MethodGet, "/api/v1/nodes", nil)
	cs.httpSrv.Handler.ServeHTTP(w2, r3)
	var nodes []map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&nodes)
	if len(nodes) != 0 {
		t.Errorf("want 0 nodes after unregister, got %d", len(nodes))
	}
}

func TestHandleReplicasRequiresInfoHash(t *testing.T) {
	cs := buildTestControlServer(t)
	w := httptest.NewRecorder()
	r := authedRequest(t, http.MethodGet, "/api/v1/replicas", nil)
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 without info_hash, got %d", w.Code)
	}
}

func TestHandleReplicasEmptyResult(t *testing.T) {
	cs := buildTestControlServer(t)
	w := httptest.NewRecorder()
	r := authedRequest(t, http.MethodGet, "/api/v1/replicas?info_hash=aabbccdd", nil)
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		InfoHash string        `json:"info_hash"`
		Verified int           `json:"verified"`
		Replicas []interface{} `json:"replicas"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.InfoHash != "aabbccdd" {
		t.Errorf("info_hash mismatch: %q", resp.InfoHash)
	}
	if resp.Verified != 0 {
		t.Errorf("empty registry should report verified=0, got %d", resp.Verified)
	}
	if len(resp.Replicas) != 0 {
		t.Errorf("want empty replicas slice, got %d entries", len(resp.Replicas))
	}
}

func TestHandleComputeEnsureMissingFields(t *testing.T) {
	cs := buildTestControlServer(t)
	w := httptest.NewRecorder()
	r := authedRequest(t, http.MethodPost, "/api/v1/compute/ensure",
		map[string]interface{}{"info_hash": "", "node_id": 0})
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing fields, got %d", w.Code)
	}
}

func TestHandleComputeEnsureUnknownArtifact(t *testing.T) {
	cs := buildTestControlServer(t)
	w := httptest.NewRecorder()
	r := authedRequest(t, http.MethodPost, "/api/v1/compute/ensure",
		map[string]interface{}{"info_hash": "nonexistent", "node_id": 1})
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for unknown artifact, got %d", w.Code)
	}
}

func TestHandleComputeEnsureMethodNotAllowed(t *testing.T) {
	cs := buildTestControlServer(t)
	w := httptest.NewRecorder()
	r := authedRequest(t, http.MethodGet, "/api/v1/compute/ensure", nil)
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestHandleAdmissionACLRoundTrip(t *testing.T) {
	cs := buildTestControlServer(t)

	// Set ACL.
	w1 := httptest.NewRecorder()
	r1 := authedRequest(t, http.MethodPost, "/api/v1/admission",
		map[string]interface{}{
			"info_hash": "deadbeef",
			"passkeys":  []string{"allowedkey"},
		})
	cs.httpSrv.Handler.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("want 200 on ACL set, got %d", w1.Code)
	}

	// Query ACL.
	w2 := httptest.NewRecorder()
	r2 := authedRequest(t, http.MethodGet, "/api/v1/admission?info_hash=deadbeef", nil)
	cs.httpSrv.Handler.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200 on ACL get, got %d", w2.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp["open"] != false {
		t.Errorf("ACL set with passkeys should report open=false")
	}
}

// ── GAP-06: ControlSecret auth tests ─────────────────────────────────────────

func TestAuthMiddlewarePrefersControlSecret(t *testing.T) {
	cfg := &Config{
		SitePassword:  "site-pass",
		ControlSecret: "ctrl-secret",
	}
	worker := &Worker{
		Config:    cfg,
		Torrents:  NewTorrentList(),
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     &Stats{StartTime: time.Now()},
	}
	cs := NewControlServer(cfg, worker)

	// SitePassword must be REJECTED when ControlSecret is set.
	r1 := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	r1.Header.Set("Authorization", "Bearer site-pass")
	w1 := httptest.NewRecorder()
	cs.httpSrv.Handler.ServeHTTP(w1, r1)
	if w1.Code != http.StatusUnauthorized {
		t.Error("SitePassword should be rejected when ControlSecret is configured")
	}

	// ControlSecret must be ACCEPTED.
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	r2.Header.Set("Authorization", "Bearer ctrl-secret")
	w2 := httptest.NewRecorder()
	cs.httpSrv.Handler.ServeHTTP(w2, r2)
	if w2.Code == http.StatusUnauthorized {
		t.Error("ControlSecret should be accepted")
	}
}

func TestAuthMiddlewareFallsBackToSitePassword(t *testing.T) {
	cfg := &Config{SitePassword: "site-pass"} // no ControlSecret
	worker := &Worker{
		Config:    cfg,
		Torrents:  NewTorrentList(),
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     &Stats{StartTime: time.Now()},
	}
	cs := NewControlServer(cfg, worker)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	r.Header.Set("Authorization", "Bearer site-pass")
	w := httptest.NewRecorder()
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code == http.StatusUnauthorized {
		t.Error("SitePassword fallback should work when ControlSecret is absent")
	}
}

func TestAuthMiddlewareEmptyToken(t *testing.T) {
	cs := buildTestControlServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	r.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	cs.httpSrv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("empty token should be rejected, got %d", w.Code)
	}
}
