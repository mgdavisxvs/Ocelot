package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

const testJWTSecret = "test-jwt-secret-32-bytes-minimum!"

func newWorkloadTestEnv(t *testing.T) (*WorkloadAPI, *AuthHandler, *sql.DB) {
	t.Helper()
	_, db := newTestEnv(t) // seeds org-test via agent_handler_test.go
	// Insert a test account so workload submission account_id FK passes.
	if _, err := db.Exec(`
		INSERT INTO accounts (id, org_id, username, role, secret_hash, created_at)
		VALUES ('acc-operator', 'org-test', 'testop', 'operator', 'dummy', ?)`, nowMs()); err != nil {
		t.Fatalf("seed test account: %v", err)
	}
	auth := NewAuthHandler(db, testBootstrap, []byte(testJWTSecret))
	return NewWorkloadAPI(db, auth), auth, db
}

// withTestClaims injects ocelotClaims into the request context directly,
// bypassing JWT parsing for handler unit tests.
func withTestClaims(r *http.Request, orgID, accountID, role string) *http.Request {
	claims := &ocelotClaims{AccountID: accountID, OrgID: orgID, Role: role}
	return r.WithContext(context.WithValue(r.Context(), ctxKeyAuth{}, claims))
}

func newWorkloadRequest(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
	}
	var r *http.Request
	if b != nil {
		r = httptest.NewRequest(method, url, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, url, nil)
	}
	return withTestClaims(r, "org-test", "acc-operator", "operator")
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v\nbody: %s", err, w.Body.String())
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestSubmitWorkload_OK(t *testing.T) {
	wAPI, _, _ := newWorkloadTestEnv(t)

	reqBody := map[string]any{
		"name": "test-job",
		"manifest": map[string]any{
			"type": "binary",
			"resources": map[string]any{
				"cpu_millicores": 500,
				"ram_mb":         256,
			},
		},
	}
	r := newWorkloadRequest(t, http.MethodPost, "/v1/workloads", reqBody)
	w := httptest.NewRecorder()
	wAPI.handleSubmit(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, w, &resp)
	if resp["id"] == "" || resp["id"] == nil {
		t.Error("response missing id")
	}
	if resp["status"] != "submitted" {
		t.Errorf("want status=submitted, got %v", resp["status"])
	}
}

func TestSubmitWorkload_MissingName(t *testing.T) {
	wAPI, _, _ := newWorkloadTestEnv(t)

	reqBody := map[string]any{
		"manifest": map[string]any{
			"type": "binary",
			"resources": map[string]any{"cpu_millicores": 500, "ram_mb": 256},
		},
	}
	r := newWorkloadRequest(t, http.MethodPost, "/v1/workloads", reqBody)
	w := httptest.NewRecorder()
	wAPI.handleSubmit(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestSubmitWorkload_InvalidManifestType(t *testing.T) {
	wAPI, _, _ := newWorkloadTestEnv(t)

	reqBody := map[string]any{
		"name": "bad-type",
		"manifest": map[string]any{
			"type": "unknown_type",
			"resources": map[string]any{"cpu_millicores": 500, "ram_mb": 256},
		},
	}
	r := newWorkloadRequest(t, http.MethodPost, "/v1/workloads", reqBody)
	w := httptest.NewRecorder()
	wAPI.handleSubmit(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitWorkload_GPUCountWithoutVRAM(t *testing.T) {
	wAPI, _, _ := newWorkloadTestEnv(t)

	reqBody := map[string]any{
		"name": "needs-vram",
		"manifest": map[string]any{
			"type": "binary",
			"resources": map[string]any{
				"cpu_millicores": 500,
				"ram_mb":         256,
				"gpu_count":      1,
				// gpu_vram_mb intentionally omitted
			},
		},
	}
	r := newWorkloadRequest(t, http.MethodPost, "/v1/workloads", reqBody)
	w := httptest.NewRecorder()
	wAPI.handleSubmit(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitWorkload_DefaultPriority(t *testing.T) {
	wAPI, _, db := newWorkloadTestEnv(t)

	reqBody := map[string]any{
		"name":     "default-priority",
		"manifest": map[string]any{"type": "binary", "resources": map[string]any{"cpu_millicores": 100, "ram_mb": 128}},
	}
	r := newWorkloadRequest(t, http.MethodPost, "/v1/workloads", reqBody)
	w := httptest.NewRecorder()
	wAPI.handleSubmit(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("submit: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, w, &resp)
	id := resp["id"].(string)

	var priority int
	db.QueryRow("SELECT priority FROM workloads WHERE id = ?", id).Scan(&priority)
	if priority != 50 {
		t.Errorf("want default priority 50, got %d", priority)
	}
}

func TestListWorkloads_Empty(t *testing.T) {
	wAPI, _, _ := newWorkloadTestEnv(t)

	r := newWorkloadRequest(t, http.MethodGet, "/v1/workloads", nil)
	w := httptest.NewRecorder()
	wAPI.handleList(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	decodeJSON(t, w, &resp)
	items := resp["workloads"].([]any)
	if len(items) != 0 {
		t.Errorf("want empty list, got %d items", len(items))
	}
}

func TestListWorkloads_FilterByStatus(t *testing.T) {
	wAPI, _, db := newWorkloadTestEnv(t)
	m := simpleManifest("filter-test", 100, 128)
	wid := insertTestWorkload(t, db, "org-test", m, 50)
	// advance to running
	db.Exec("UPDATE workloads SET status='running' WHERE id=?", wid)

	r := newWorkloadRequest(t, http.MethodGet, "/v1/workloads?status=running", nil)
	w := httptest.NewRecorder()
	wAPI.handleList(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	decodeJSON(t, w, &resp)
	items := resp["workloads"].([]any)
	if len(items) != 1 {
		t.Errorf("want 1 running workload, got %d", len(items))
	}
}

func TestGetWorkload_OK(t *testing.T) {
	wAPI, _, db := newWorkloadTestEnv(t)
	m := simpleManifest("get-test", 200, 512)
	wid := insertTestWorkload(t, db, "org-test", m, 70)

	r := newWorkloadRequest(t, http.MethodGet, "/v1/workloads/"+wid, nil)
	r.SetPathValue("id", wid)
	w := httptest.NewRecorder()
	wAPI.handleGet(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp WorkloadDetail
	decodeJSON(t, w, &resp)
	if resp.ID != wid {
		t.Errorf("want id=%s, got %s", wid, resp.ID)
	}
}

func TestGetWorkload_NotFound(t *testing.T) {
	wAPI, _, _ := newWorkloadTestEnv(t)

	r := newWorkloadRequest(t, http.MethodGet, "/v1/workloads/nonexistent", nil)
	r.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	wAPI.handleGet(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestCancelWorkload_Queued(t *testing.T) {
	wAPI, _, db := newWorkloadTestEnv(t)
	m := simpleManifest("cancel-queued", 100, 128)
	wid := insertTestWorkload(t, db, "org-test", m, 50)

	r := newWorkloadRequest(t, http.MethodDelete, "/v1/workloads/"+wid, nil)
	r.SetPathValue("id", wid)
	w := httptest.NewRecorder()
	wAPI.handleCancel(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	decodeJSON(t, w, &resp)
	if resp["status"] != "cancelled" {
		t.Errorf("want status=cancelled, got %s", resp["status"])
	}
}

func TestCancelWorkload_RunningBecomeStopping(t *testing.T) {
	wAPI, _, db := newWorkloadTestEnv(t)
	m := simpleManifest("cancel-running", 100, 128)
	wid := insertTestWorkload(t, db, "org-test", m, 50)
	db.Exec("UPDATE workloads SET status='running' WHERE id=?", wid)

	r := newWorkloadRequest(t, http.MethodDelete, "/v1/workloads/"+wid, nil)
	r.SetPathValue("id", wid)
	w := httptest.NewRecorder()
	wAPI.handleCancel(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	decodeJSON(t, w, &resp)
	if resp["status"] != "stopping" {
		t.Errorf("want status=stopping, got %s", resp["status"])
	}
}

func TestCancelWorkload_AlreadyTerminal(t *testing.T) {
	wAPI, _, db := newWorkloadTestEnv(t)
	m := simpleManifest("cancel-terminal", 100, 128)
	wid := insertTestWorkload(t, db, "org-test", m, 50)
	db.Exec("UPDATE workloads SET status='completed' WHERE id=?", wid)

	r := newWorkloadRequest(t, http.MethodDelete, "/v1/workloads/"+wid, nil)
	r.SetPathValue("id", wid)
	w := httptest.NewRecorder()
	wAPI.handleCancel(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
}

func TestStopWorkload_Running(t *testing.T) {
	wAPI, _, db := newWorkloadTestEnv(t)
	m := simpleManifest("stop-running", 100, 128)
	wid := insertTestWorkload(t, db, "org-test", m, 50)
	db.Exec("UPDATE workloads SET status='running' WHERE id=?", wid)

	r := newWorkloadRequest(t, http.MethodPost, "/v1/workloads/"+wid+"/stop", nil)
	r.SetPathValue("id", wid)
	w := httptest.NewRecorder()
	wAPI.handleStop(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	decodeJSON(t, w, &resp)
	if resp["status"] != "stopping" {
		t.Errorf("want status=stopping, got %s", resp["status"])
	}
}

func TestStopWorkload_NotRunning(t *testing.T) {
	wAPI, _, db := newWorkloadTestEnv(t)
	m := simpleManifest("stop-queued", 100, 128)
	wid := insertTestWorkload(t, db, "org-test", m, 50) // status=submitted

	r := newWorkloadRequest(t, http.MethodPost, "/v1/workloads/"+wid+"/stop", nil)
	r.SetPathValue("id", wid)
	w := httptest.NewRecorder()
	wAPI.handleStop(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
}
