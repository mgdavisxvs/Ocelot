package virtualserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
)

// newTestRegistry returns a fresh Prometheus registry to avoid conflicts.
func newTestRegistry() prometheus.Registerer {
	return prometheus.NewRegistry()
}

func newTestAPI(t *testing.T, token string) (*APIServer, *db) {
	t.Helper()
	d, err := openDB(filepath.Join(t.TempDir(), "vs.db"))
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { d.close() })
	adapters := NewAdapterRegistry()
	adapters.Register(NoOpAdapter{})
	stor := storage.NewRegistry()
	metrics := NewVSMetrics(newTestRegistry())
	return newAPIServer(d, adapters, stor, metrics, token), d
}

func doRequest(t *testing.T, srv http.Handler, method, path string, body interface{}, token string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// ── auth ──────────────────────────────────────────────────────────────────────

func TestAPI_AuthRequired(t *testing.T) {
	srv, _ := newTestAPI(t, "secret")

	w := doRequest(t, srv, http.MethodGet, "/v1/namespaces", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	w = doRequest(t, srv, http.MethodGet, "/v1/namespaces", nil, "wrong-token")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", w.Code)
	}

	w = doRequest(t, srv, http.MethodGet, "/v1/namespaces", nil, "secret")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d", w.Code)
	}
}

func TestAPI_NoAuthWhenTokenEmpty(t *testing.T) {
	srv, _ := newTestAPI(t, "")
	w := doRequest(t, srv, http.MethodGet, "/v1/namespaces", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (no auth required), got %d", w.Code)
	}
}

// ── namespaces ────────────────────────────────────────────────────────────────

func TestAPI_Namespace_CRUD(t *testing.T) {
	srv, _ := newTestAPI(t, "")

	// List empty.
	w := doRequest(t, srv, http.MethodGet, "/v1/namespaces", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var list []Namespace
	json.NewDecoder(w.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}

	// Create.
	w = doRequest(t, srv, http.MethodPost, "/v1/namespaces", map[string]string{"name": "prod"}, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	// List again.
	w = doRequest(t, srv, http.MethodGet, "/v1/namespaces", nil, "")
	json.NewDecoder(w.Body).Decode(&list)
	if len(list) != 1 || list[0].Name != "prod" {
		t.Fatalf("after create: %v", list)
	}

	// Delete.
	w = doRequest(t, srv, http.MethodDelete, "/v1/namespaces/prod", nil, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}

	w = doRequest(t, srv, http.MethodGet, "/v1/namespaces", nil, "")
	json.NewDecoder(w.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("after delete: %v", list)
	}
}

func TestAPI_Namespace_MissingName(t *testing.T) {
	srv, _ := newTestAPI(t, "")
	w := doRequest(t, srv, http.MethodPost, "/v1/namespaces", map[string]string{}, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ── nodes ─────────────────────────────────────────────────────────────────────

func TestAPI_Node_CRUD(t *testing.T) {
	srv, _ := newTestAPI(t, "")

	// Register.
	n := sampleNode("n1")
	n.ID = "" // let server assign
	w := doRequest(t, srv, http.MethodPost, "/v1/nodes", n, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("register node: %d %s", w.Code, w.Body.String())
	}
	var created Node
	json.NewDecoder(w.Body).Decode(&created)
	if created.ID == "" {
		t.Fatal("server should assign ID")
	}
	id := created.ID

	// Get.
	w = doRequest(t, srv, http.MethodGet, "/v1/nodes/"+id, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get node: %d", w.Code)
	}
	var got Node
	json.NewDecoder(w.Body).Decode(&got)
	if got.Arch != "amd64" {
		t.Fatalf("arch not persisted: %s", got.Arch)
	}

	// List.
	w = doRequest(t, srv, http.MethodGet, "/v1/nodes", nil, "")
	var nodes []*Node
	json.NewDecoder(w.Body).Decode(&nodes)
	if len(nodes) != 1 {
		t.Fatalf("list: expected 1, got %d", len(nodes))
	}

	// Heartbeat.
	w = doRequest(t, srv, http.MethodPut, "/v1/nodes/"+id+"/heartbeat", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d", w.Code)
	}

	// Delete.
	w = doRequest(t, srv, http.MethodDelete, "/v1/nodes/"+id, nil, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete node: %d", w.Code)
	}

	w = doRequest(t, srv, http.MethodGet, "/v1/nodes/"+id, nil, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", w.Code)
	}
}

// ── services ──────────────────────────────────────────────────────────────────

func TestAPI_Service_CRUD(t *testing.T) {
	srv, d := newTestAPI(t, "")
	d.createNamespace(&Namespace{Name: "default", CreatedAt: time.Now().UTC()})

	svc := sampleService("", "default")
	svc.ID = ""
	w := doRequest(t, srv, http.MethodPost, "/v1/services", svc, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create service: %d %s", w.Code, w.Body.String())
	}
	var created Service
	json.NewDecoder(w.Body).Decode(&created)
	if created.ID == "" {
		t.Fatal("server should assign ID")
	}
	id := created.ID

	// Get.
	w = doRequest(t, srv, http.MethodGet, "/v1/services/"+id, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get service: %d", w.Code)
	}

	// List.
	w = doRequest(t, srv, http.MethodGet, "/v1/services?namespace=default", nil, "")
	var svcs []*Service
	json.NewDecoder(w.Body).Decode(&svcs)
	if len(svcs) != 1 {
		t.Fatalf("list: expected 1, got %d", len(svcs))
	}

	// Update.
	updated := sampleService(id, "default")
	updated.Image = "new-image:v2"
	w = doRequest(t, srv, http.MethodPut, "/v1/services/"+id, updated, "")
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d", w.Code)
	}
	var patched Service
	json.NewDecoder(w.Body).Decode(&patched)
	if patched.Image != "new-image:v2" {
		t.Fatalf("image not updated: %s", patched.Image)
	}

	// Delete.
	w = doRequest(t, srv, http.MethodDelete, "/v1/services/"+id, nil, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete service: %d", w.Code)
	}
}

func TestAPI_Service_NoNamespace(t *testing.T) {
	srv, _ := newTestAPI(t, "")
	svc := sampleService("", "")
	svc.ID = ""
	svc.Namespace = ""
	w := doRequest(t, srv, http.MethodPost, "/v1/services", svc, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ── instances ─────────────────────────────────────────────────────────────────

func TestAPI_Instance_CreateGetStop(t *testing.T) {
	srv, d := newTestAPI(t, "")
	d.createNamespace(&Namespace{Name: "default", CreatedAt: time.Now().UTC()})
	d.upsertService(sampleService("s1", "default"))

	// Create instance.
	w := doRequest(t, srv, http.MethodPost, "/v1/instances",
		map[string]string{"service_id": "s1"}, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create instance: %d %s", w.Code, w.Body.String())
	}
	var inst Instance
	json.NewDecoder(w.Body).Decode(&inst)
	if inst.ID == "" {
		t.Fatal("server should assign ID")
	}
	id := inst.ID
	if inst.State != InstanceStateScheduled {
		t.Fatalf("expected scheduled, got %s", inst.State)
	}

	// Get.
	w = doRequest(t, srv, http.MethodGet, "/v1/instances/"+id, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d", w.Code)
	}

	// Advance to running so we can stop it.
	fetched, _ := d.getInstance(id)
	fetched.State = InstanceStateRunning
	d.upsertInstance(fetched)

	// Stop.
	w = doRequest(t, srv, http.MethodPost, "/v1/instances/"+id+"/stop", nil, "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	var stopped Instance
	json.NewDecoder(w.Body).Decode(&stopped)
	if stopped.State != InstanceStateStopping {
		t.Fatalf("expected stopping, got %s", stopped.State)
	}
}

func TestAPI_Instance_StopNonRunning(t *testing.T) {
	srv, d := newTestAPI(t, "")
	d.createNamespace(&Namespace{Name: "default", CreatedAt: time.Now().UTC()})
	d.upsertService(sampleService("s1", "default"))
	now := time.Now().UTC()
	inst := &Instance{ID: "i1", ServiceID: "s1", Namespace: "default", State: InstanceStateStopped, CreatedAt: now, UpdatedAt: now}
	d.upsertInstance(inst)

	w := doRequest(t, srv, http.MethodPost, "/v1/instances/i1/stop", nil, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for stopped instance, got %d", w.Code)
	}
}

func TestAPI_Instance_List(t *testing.T) {
	srv, d := newTestAPI(t, "")
	d.createNamespace(&Namespace{Name: "default", CreatedAt: time.Now().UTC()})
	d.upsertService(sampleService("s1", "default"))
	now := time.Now().UTC()
	for _, id := range []string{"i1", "i2"} {
		d.upsertInstance(&Instance{ID: id, ServiceID: "s1", Namespace: "default", State: InstanceStateScheduled, CreatedAt: now, UpdatedAt: now})
	}

	w := doRequest(t, srv, http.MethodGet, "/v1/instances", nil, "")
	var insts []*Instance
	json.NewDecoder(w.Body).Decode(&insts)
	if len(insts) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(insts))
	}
}

func TestAPI_Instance_UnknownService(t *testing.T) {
	srv, _ := newTestAPI(t, "")
	w := doRequest(t, srv, http.MethodPost, "/v1/instances",
		map[string]string{"service_id": "nonexistent"}, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── operations ────────────────────────────────────────────────────────────────

func TestAPI_Operation_Get(t *testing.T) {
	srv, d := newTestAPI(t, "")
	now := time.Now().UTC()
	op := &Operation{ID: "op1", Type: "snapshot", ResourceID: "v1", State: OpStatePending, CreatedAt: now, UpdatedAt: now}
	d.createOperation(op)

	w := doRequest(t, srv, http.MethodGet, "/v1/operations/op1", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get operation: %d", w.Code)
	}
	var got Operation
	json.NewDecoder(w.Body).Decode(&got)
	if got.ID != "op1" || got.State != OpStatePending {
		t.Fatalf("wrong operation: %+v", got)
	}
}

func TestAPI_Operation_NotFound(t *testing.T) {
	srv, _ := newTestAPI(t, "")
	w := doRequest(t, srv, http.MethodGet, "/v1/operations/missing", nil, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── volumes ───────────────────────────────────────────────────────────────────

func TestAPI_Volume_CreateDelete(t *testing.T) {
	tmp := t.TempDir()
	d, _ := openDB(filepath.Join(tmp, "vs.db"))
	defer d.close()
	d.createNamespace(&Namespace{Name: "default", CreatedAt: time.Now().UTC()})

	adapters := NewAdapterRegistry()
	adapters.Register(NoOpAdapter{})
	stor := storage.NewRegistry()
	drv, _ := storage.NewLocalDriver(tmp, "n1")
	stor.Register(drv)
	metrics := NewVSMetrics(newTestRegistry())
	srv := newAPIServer(d, adapters, stor, metrics, "")

	// Create volume.
	vol := &Volume{Namespace: "default", Name: "data", DriverName: "local", SizeBytes: 1024}
	w := doRequest(t, srv, http.MethodPost, "/v1/volumes", vol, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create volume: %d %s", w.Code, w.Body.String())
	}
	var created Volume
	json.NewDecoder(w.Body).Decode(&created)
	if created.ID == "" || created.Handle == "" {
		t.Fatalf("ID/Handle should be set: %+v", created)
	}
	id := created.ID

	// Get.
	w = doRequest(t, srv, http.MethodGet, "/v1/volumes/"+id, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get volume: %d", w.Code)
	}

	// List.
	w = doRequest(t, srv, http.MethodGet, "/v1/volumes", nil, "")
	var vols []*Volume
	json.NewDecoder(w.Body).Decode(&vols)
	if len(vols) != 1 {
		t.Fatalf("list: expected 1, got %d", len(vols))
	}

	// Delete.
	w = doRequest(t, srv, http.MethodDelete, "/v1/volumes/"+id, nil, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete volume: %d %s", w.Code, w.Body.String())
	}
}

func TestAPI_Volume_UnknownDriver(t *testing.T) {
	srv, d := newTestAPI(t, "")
	d.createNamespace(&Namespace{Name: "default", CreatedAt: time.Now().UTC()})

	vol := &Volume{Namespace: "default", Name: "data", DriverName: "nfs"}
	w := doRequest(t, srv, http.MethodPost, "/v1/volumes", vol, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown driver, got %d", w.Code)
	}
}

func TestAPI_Volume_SnapshotRestore(t *testing.T) {
	tmp := t.TempDir()
	d, _ := openDB(filepath.Join(tmp, "vs.db"))
	defer d.close()
	d.createNamespace(&Namespace{Name: "default", CreatedAt: time.Now().UTC()})

	stor := storage.NewRegistry()
	drv, _ := storage.NewLocalDriver(tmp, "n1")
	stor.Register(drv)
	adapters := NewAdapterRegistry()
	adapters.Register(NoOpAdapter{})
	srv := newAPIServer(d, adapters, stor, NewVSMetrics(newTestRegistry()), "")

	// Create volume.
	vol := &Volume{Namespace: "default", Name: "snap-test", DriverName: "local"}
	w := doRequest(t, srv, http.MethodPost, "/v1/volumes", vol, "")
	var created Volume
	json.NewDecoder(w.Body).Decode(&created)

	// Snapshot (async).
	w = doRequest(t, srv, http.MethodPost, "/v1/volumes/"+created.ID+"/snapshot", nil, "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("snapshot: %d %s", w.Code, w.Body.String())
	}
	var op Operation
	json.NewDecoder(w.Body).Decode(&op)
	if op.ID == "" {
		t.Fatal("operation ID should be set")
	}

	// Wait briefly for the goroutine.
	deadline := time.Now().Add(2 * time.Second)
	var snapOp *Operation
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		fetched, _ := d.getOperation(op.ID)
		if fetched.State == OpStateSucceeded || fetched.State == OpStateFailed {
			snapOp = fetched
			break
		}
	}
	if snapOp == nil || snapOp.State != OpStateSucceeded {
		t.Fatalf("snapshot op did not succeed: %+v", snapOp)
	}

	snapshotID := snapOp.Message
	if snapshotID == "" {
		t.Fatal("snapshot ID in operation message")
	}

	// List snapshots.
	w = doRequest(t, srv, http.MethodGet, "/v1/volumes/"+created.ID+"/snapshots", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list snapshots: %d", w.Code)
	}
	var snaps []*Snapshot
	json.NewDecoder(w.Body).Decode(&snaps)
	if len(snaps) == 0 {
		t.Fatal("expected at least one snapshot")
	}
	if snaps[0].ID != snapshotID {
		t.Fatalf("snapshot ID mismatch: %s vs %s", snaps[0].ID, snapshotID)
	}

	// Restore (async).
	w = doRequest(t, srv, http.MethodPost, "/v1/volumes/"+created.ID+"/restore/"+snapshotID, nil, "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("restore: %d %s", w.Code, w.Body.String())
	}
	var restoreOp Operation
	json.NewDecoder(w.Body).Decode(&restoreOp)

	deadline = time.Now().Add(2 * time.Second)
	var finalOp *Operation
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		fetched, _ := d.getOperation(restoreOp.ID)
		if fetched.State == OpStateSucceeded || fetched.State == OpStateFailed {
			finalOp = fetched
			break
		}
	}
	if finalOp == nil || finalOp.State != OpStateSucceeded {
		t.Fatalf("restore op did not succeed: %+v", finalOp)
	}
}

// ── writeErr / writeJSON ──────────────────────────────────────────────────────

func TestAPI_MethodNotAllowed(t *testing.T) {
	srv, _ := newTestAPI(t, "")
	w := doRequest(t, srv, http.MethodPatch, "/v1/namespaces", nil, "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestAPI_ContentTypeJSON(t *testing.T) {
	srv, _ := newTestAPI(t, "")
	w := doRequest(t, srv, http.MethodGet, "/v1/namespaces", nil, "")
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json, got %s", ct)
	}
}
