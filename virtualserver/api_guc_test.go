package virtualserver_test

// GUC Test Coverage — virtualserver/api (HTTP handlers for services, nodes, instances)
// Knuth  (~5): algorithmic correctness, loop invariants, data structure invariants
// Turing (~5): termination conditions, halting behavior, decidability of key operations
// Church (~5): functional purity, side-effect isolation, referential transparency
// Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vsapi "github.com/mgdavisxvs/Ocelot/virtualserver/api"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/store"
)

// ---------------------------------------------------------------------------
// Minimal mock store satisfying api.HandlerStore
// ---------------------------------------------------------------------------

type gucStore struct {
	mu         sync.Mutex
	namespaces []string
	nodes      map[string]*domain.Node
	services   map[int64]*domain.Service
	instances  map[string]*domain.ServiceInstance
	volumes    map[string]*domain.Volume
	mounts     map[int64]*domain.VolumeMount
	snapshots  map[string]*domain.VolumeSnapshot
	nextSvcID  int64
}

func newGUCStore() *gucStore {
	return &gucStore{
		nodes:     make(map[string]*domain.Node),
		services:  make(map[int64]*domain.Service),
		instances: make(map[string]*domain.ServiceInstance),
		volumes:   make(map[string]*domain.Volume),
		mounts:    make(map[int64]*domain.VolumeMount),
		snapshots: make(map[string]*domain.VolumeSnapshot),
		nextSvcID: 1,
	}
}

func (s *gucStore) CreateNamespace(_ context.Context, name, _ string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.namespaces {
		if n == name {
			return 0, store.ErrConflict
		}
	}
	s.namespaces = append(s.namespaces, name)
	return int64(len(s.namespaces)), nil
}

func (s *gucStore) ListNamespaces(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.namespaces))
	copy(out, s.namespaces)
	return out, nil
}

func (s *gucStore) CreateNode(_ context.Context, n domain.Node) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := "node-" + n.Name
	n.ID = id
	s.nodes[id] = &n
	return id, nil
}

func (s *gucStore) GetNode(_ context.Context, id string) (*domain.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.nodes[id]; ok {
		cp := *n
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (s *gucStore) ListNodes(_ context.Context, _ string) ([]domain.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Node
	for _, n := range s.nodes {
		out = append(out, *n)
	}
	return out, nil
}

func (s *gucStore) UpdateNodeState(_ context.Context, id string, to domain.NodeState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return store.ErrNotFound
	}
	if err := domain.ValidateNodeTransition(n.State, to); err != nil {
		return err
	}
	n.State = to
	return nil
}

func (s *gucStore) CreateService(_ context.Context, m domain.ServiceManifest) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, svc := range s.services {
		if svc.Manifest.Metadata.Namespace == m.Metadata.Namespace &&
			svc.Manifest.Metadata.Name == m.Metadata.Name {
			return 0, store.ErrConflict
		}
	}
	id := s.nextSvcID
	s.nextSvcID++
	now := time.Now()
	s.services[id] = &domain.Service{
		ID:           id,
		Manifest:     m,
		DesiredCount: m.Spec.Instances,
		State:        "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return id, nil
}

func (s *gucStore) GetService(_ context.Context, id int64) (*domain.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if svc, ok := s.services[id]; ok {
		cp := *svc
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (s *gucStore) ListServices(_ context.Context, ns string) ([]domain.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Service
	for _, svc := range s.services {
		if ns == "" || svc.Manifest.Metadata.Namespace == ns {
			out = append(out, *svc)
		}
	}
	return out, nil
}

func (s *gucStore) DeleteService(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.services[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.services, id)
	return nil
}

func (s *gucStore) GetInstance(_ context.Context, id string) (*domain.ServiceInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.instances[id]; ok {
		cp := *inst
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (s *gucStore) ListInstancesByService(_ context.Context, _ int64) ([]domain.ServiceInstance, error) {
	return nil, nil
}

func (s *gucStore) ListInstances(_ context.Context, _ string) ([]domain.ServiceInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.ServiceInstance
	for _, inst := range s.instances {
		out = append(out, *inst)
	}
	return out, nil
}

func (s *gucStore) CreateVolume(_ context.Context, v domain.Volume) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.volumes[v.ID] = &v
	return v.ID, nil
}

func (s *gucStore) GetVolume(_ context.Context, id string) (*domain.Volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.volumes[id]; ok {
		cp := *v
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (s *gucStore) ListVolumes(_ context.Context, ns string) ([]domain.Volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Volume
	for _, v := range s.volumes {
		if ns == "" || v.Manifest.Metadata.Namespace == ns {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (s *gucStore) UpdateVolumeState(_ context.Context, id string, to domain.VolumeState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.volumes[id]; ok {
		v.State = to
		return nil
	}
	return store.ErrNotFound
}

func (s *gucStore) ListActiveMountsByVolume(_ context.Context, volumeID string) ([]domain.VolumeMount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.VolumeMount
	for _, m := range s.mounts {
		if m.VolumeID == volumeID && m.State != domain.MountReleased {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (s *gucStore) StartVolumeRelease(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.volumes[id]
	if !ok {
		return store.ErrNotFound
	}
	for _, m := range s.mounts {
		if m.VolumeID == id && m.State != domain.MountReleased {
			return store.ErrConflict
		}
	}
	v.State = domain.VolumeReleasing
	return nil
}

func (s *gucStore) DeleteVolume(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.volumes[id]
	if !ok {
		return store.ErrNotFound
	}
	if v.State != domain.VolumeReleased {
		return store.ErrConflict
	}
	delete(s.volumes, id)
	return nil
}

func (s *gucStore) CreateSnapshot(_ context.Context, snap domain.VolumeSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[snap.ID] = &snap
	return nil
}

func (s *gucStore) GetSnapshot(_ context.Context, id string) (*domain.VolumeSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snap, ok := s.snapshots[id]; ok {
		cp := *snap
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (s *gucStore) ListSnapshots(_ context.Context, volumeID string) ([]domain.VolumeSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.VolumeSnapshot
	for _, snap := range s.snapshots {
		if snap.VolumeID == volumeID {
			out = append(out, *snap)
		}
	}
	return out, nil
}

func (s *gucStore) UpdateSnapshotState(_ context.Context, id string, state domain.SnapshotState, driverRef string, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snap, ok := s.snapshots[id]; ok {
		snap.State = state
		snap.DriverRef = driverRef
		return nil
	}
	return store.ErrNotFound
}

func (s *gucStore) GetOperation(_ context.Context, _ string) (*domain.Operation, error) {
	return nil, store.ErrNotFound
}

func (s *gucStore) ListInstanceOperations(_ context.Context, _ string) ([]domain.Operation, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Shared test helpers
// ---------------------------------------------------------------------------

func gucValidManifest(ns, name string) domain.ServiceManifest {
	return domain.ServiceManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Service",
		Metadata:   domain.ServiceMetadata{Namespace: ns, Name: name},
		Spec: domain.ServiceSpec{
			Artifact:  domain.ArtifactReference{Type: domain.ArtifactTypeOcelot, InfoHash: "0123456789abcdef0123456789abcdef01234567"},
			Runtime:   "wasm",
			Instances: 1,
			Resources: domain.ResourceRequest{RAMMiB: 512, CPUThreads: 2},
			Restart:   domain.RestartPolicy{Policy: "on-failure", MaximumAttempts: 3},
		},
	}
}

// gucDispatch routes method+path to the appropriate exported Handlers method.
func gucDispatch(h *vsapi.Handlers, method, path string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body) //nolint:errcheck
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	switch {
	case method == http.MethodGet && path == "/v1/services":
		h.ListServices(w, r)
	case method == http.MethodPost && path == "/v1/services":
		h.DeclareService(w, r)
	case method == http.MethodGet && strings.HasPrefix(path, "/v1/services/") && strings.HasSuffix(path, "/instances"):
		h.ListServiceInstances(w, r)
	case method == http.MethodGet && strings.HasPrefix(path, "/v1/services/"):
		h.GetService(w, r)
	case method == http.MethodDelete && strings.HasPrefix(path, "/v1/services/"):
		h.DeleteService(w, r)
	case method == http.MethodGet && path == "/v1/instances":
		h.ListInstances(w, r)
	case method == http.MethodGet && strings.HasPrefix(path, "/v1/instances/"):
		h.GetInstance(w, r)
	}
	return w
}

func gucDecodeJSON(t *testing.T, w *httptest.ResponseRecorder, out interface{}) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(out); err != nil {
		t.Fatalf("decode response (body=%q): %v", w.Body.String(), err)
	}
}

// ---------------------------------------------------------------------------
// KNUTH — algorithmic correctness, loop invariants, data structure invariants
// ---------------------------------------------------------------------------

// TestGUC_Services_ListReturnsServicesKey verifies the list handler's data
// structure invariant: the response always contains a "services" array key,
// even when the store is empty. The schema contract is never violated.
func TestGUC_Services_ListReturnsServicesKey(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	w := gucDispatch(h, http.MethodGet, "/v1/services", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	gucDecodeJSON(t, w, &body)
	if _, ok := body["services"]; !ok {
		t.Error("response must contain 'services' key")
	}
}

// TestGUC_Services_DeclareReturns201WithPositiveID checks that the creation
// algorithm assigns a positive integer ID and returns HTTP 201.
func TestGUC_Services_DeclareReturns201WithPositiveID(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	w := gucDispatch(h, http.MethodPost, "/v1/services",
		vsapi.DeclareServiceRequest{Manifest: gucValidManifest("ns-a", "svc-a")})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp vsapi.ServiceResponse
	gucDecodeJSON(t, w, &resp)
	if resp.ID < 1 {
		t.Errorf("expected positive service ID, got %d", resp.ID)
	}
}

// TestGUC_Services_GetByIDMatchesCreated asserts the read-after-write invariant:
// the record returned by GetService must match what was stored at creation.
func TestGUC_Services_GetByIDMatchesCreated(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	cr := gucDispatch(h, http.MethodPost, "/v1/services",
		vsapi.DeclareServiceRequest{Manifest: gucValidManifest("ns-b", "svc-b")})
	if cr.Code != http.StatusCreated {
		t.Fatalf("declare failed: %d %s", cr.Code, cr.Body.String())
	}
	var created vsapi.ServiceResponse
	gucDecodeJSON(t, cr, &created)

	idStr := strconv.FormatInt(created.ID, 10)
	gr := gucDispatch(h, http.MethodGet, "/v1/services/"+idStr, nil)
	if gr.Code != http.StatusOK {
		t.Fatalf("get failed: %d %s", gr.Code, gr.Body.String())
	}
	var fetched vsapi.ServiceResponse
	gucDecodeJSON(t, gr, &fetched)
	if fetched.ID != created.ID {
		t.Errorf("ID mismatch: create=%d get=%d", created.ID, fetched.ID)
	}
	if fetched.Manifest.Metadata.Name != created.Manifest.Metadata.Name {
		t.Errorf("name mismatch: create=%q get=%q", created.Manifest.Metadata.Name, fetched.Manifest.Metadata.Name)
	}
}

// TestGUC_Services_DeleteReturns204 confirms the delete algorithm terminates
// correctly with 204 No Content for a valid existing service ID.
func TestGUC_Services_DeleteReturns204(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	cr := gucDispatch(h, http.MethodPost, "/v1/services",
		vsapi.DeclareServiceRequest{Manifest: gucValidManifest("ns-c", "svc-c")})
	var resp vsapi.ServiceResponse
	gucDecodeJSON(t, cr, &resp)

	idStr := strconv.FormatInt(resp.ID, 10)
	del := gucDispatch(h, http.MethodDelete, "/v1/services/"+idStr, nil)
	if del.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", del.Code, del.Body.String())
	}
}

// TestGUC_Services_TableDriven_InvalidManifests uses a table to assert that the
// validation algorithm rejects every structurally invalid manifest with 400.
// The loop invariant: each invalid case must produce exactly one 400 response.
func TestGUC_Services_TableDriven_InvalidManifests(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m *domain.ServiceManifest)
	}{
		{"wrong_api_version", func(m *domain.ServiceManifest) { m.APIVersion = "v0" }},
		{"wrong_kind", func(m *domain.ServiceManifest) { m.Kind = "Deployment" }},
		{"empty_runtime", func(m *domain.ServiceManifest) { m.Spec.Runtime = "" }},
		{"zero_instances", func(m *domain.ServiceManifest) { m.Spec.Instances = 0 }},
		{"bad_infohash", func(m *domain.ServiceManifest) { m.Spec.Artifact.InfoHash = "tooshort" }},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := gucValidManifest("ns", "svc")
			tc.mutate(&m)
			h := vsapi.NewHandlers(newGUCStore())
			w := gucDispatch(h, http.MethodPost, "/v1/services",
				vsapi.DeclareServiceRequest{Manifest: m})
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d (body=%s)", w.Code, w.Body.String())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TURING — termination conditions, halting behavior, decidability
// ---------------------------------------------------------------------------

// TestGUC_Services_MalformedJSONHaltsWithBadRequest ensures the handler
// terminates cleanly with 400 rather than panicking or hanging on invalid JSON.
func TestGUC_Services_MalformedJSONHaltsWithBadRequest(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	r := httptest.NewRequest(http.MethodPost, "/v1/services",
		strings.NewReader("{this is definitely not valid json}"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.DeclareService(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestGUC_Services_UnknownIDHaltsWithNotFound verifies looking up a
// non-existent service terminates decidably with 404.
func TestGUC_Services_UnknownIDHaltsWithNotFound(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	w := gucDispatch(h, http.MethodGet, "/v1/services/999999", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestGUC_Services_DeleteUnknownIDHaltsWithNotFound confirms that deleting a
// non-existent service halts decidably with 404 rather than diverging.
func TestGUC_Services_DeleteUnknownIDHaltsWithNotFound(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	w := gucDispatch(h, http.MethodDelete, "/v1/services/88888", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestGUC_Services_NonNumericIDHaltsWithBadRequest verifies that a non-numeric
// service ID terminates with 400 (decidably invalid input) rather than crashing.
func TestGUC_Services_NonNumericIDHaltsWithBadRequest(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	w := gucDispatch(h, http.MethodGet, "/v1/services/not-a-number", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// TestGUC_Services_ConcurrentGetHaltsCleanly launches N concurrent read
// requests to verify all handler invocations terminate without panics or
// deadlocks (Turing: every valid input path must halt).
func TestGUC_Services_ConcurrentGetHaltsCleanly(t *testing.T) {
	st := newGUCStore()
	id, err := st.CreateService(context.Background(), gucValidManifest("ns-conc", "svc-conc"))
	if err != nil {
		t.Fatalf("seed service: %v", err)
	}
	h := vsapi.NewHandlers(st)
	idStr := strconv.FormatInt(id, 10)

	const workers = 20
	var wg sync.WaitGroup
	var failures int64
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			w := gucDispatch(h, http.MethodGet, "/v1/services/"+idStr, nil)
			if w.Code != http.StatusOK {
				atomic.AddInt64(&failures, 1)
			}
		}()
	}
	wg.Wait()
	if failures > 0 {
		t.Errorf("%d/%d concurrent GETs returned non-200", failures, workers)
	}
}

// ---------------------------------------------------------------------------
// CHURCH — functional purity, side-effect isolation, referential transparency
// ---------------------------------------------------------------------------

// TestGUC_Services_ResponseContentTypeIsJSON asserts that all handler response
// paths carry "application/json" — the Content-Type is a pure function of the
// handler output, independent of mutable state.
func TestGUC_Services_ResponseContentTypeIsJSON(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	cases := []struct {
		method string
		path   string
		body   interface{}
	}{
		{http.MethodGet, "/v1/services", nil},
		{http.MethodGet, "/v1/instances", nil},
		{http.MethodGet, "/v1/services/9999", nil}, // triggers 404 error path
	}
	for _, tc := range cases {
		w := gucDispatch(h, tc.method, tc.path, tc.body)
		ct := w.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s %s: Content-Type=%q, want application/json prefix", tc.method, tc.path, ct)
		}
	}
}

// TestGUC_Services_ErrorResponseHasErrorField verifies the error path is a
// pure function: for any error trigger, the output body always contains an
// "error" string field — the schema is stable regardless of error cause.
func TestGUC_Services_ErrorResponseHasErrorField(t *testing.T) {
	cases := []struct {
		label  string
		method string
		path   string
		setup  func(h *vsapi.Handlers, w http.ResponseWriter, r *http.Request)
	}{
		{
			label: "not_found",
			method: http.MethodGet, path: "/v1/services/9999",
			setup: func(h *vsapi.Handlers, w http.ResponseWriter, r *http.Request) { h.GetService(w, r) },
		},
		{
			label: "bad_numeric_id",
			method: http.MethodGet, path: "/v1/services/xyz",
			setup: func(h *vsapi.Handlers, w http.ResponseWriter, r *http.Request) { h.GetService(w, r) },
		},
		{
			label: "malformed_json",
			method: http.MethodPost, path: "/v1/services",
			setup: func(h *vsapi.Handlers, w http.ResponseWriter, r *http.Request) { h.DeclareService(w, r) },
		},
		{
			label: "invalid_manifest",
			method: http.MethodPost, path: "/v1/services",
			setup: func(h *vsapi.Handlers, w http.ResponseWriter, r *http.Request) { h.DeclareService(w, r) },
		},
	}
	bodies := []string{
		"", // not_found: no body needed
		"", // bad_numeric_id: no body needed
		"{invalid json}", // malformed_json
		`{"manifest":{"apiVersion":"bad","kind":"Service","metadata":{"namespace":"n","name":"s"},"spec":{"artifact":{"type":"ocelot","infoHash":"abc"},"runtime":"r","instances":1,"resources":{}}}}`,
	}
	for i, tc := range cases {
		i, tc := i, tc
		t.Run(tc.label, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(bodies[i]))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h := vsapi.NewHandlers(newGUCStore())
			tc.setup(h, w, r)
			var errResp vsapi.ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
				t.Fatalf("decode error body: %v (raw=%s)", err, w.Body.String())
			}
			if errResp.Error == "" {
				t.Errorf("%s: error response missing non-empty 'error' field", tc.label)
			}
		})
	}
}

// TestGUC_Services_ListIsReferentiallyTransparent calls ListServices twice on
// an unchanged store and verifies identical results — the handler has no
// observable side effects that would alter subsequent reads.
func TestGUC_Services_ListIsReferentiallyTransparent(t *testing.T) {
	st := newGUCStore()
	if _, err := st.CreateService(context.Background(), gucValidManifest("ref", "svc-ref")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := vsapi.NewHandlers(st)

	w1 := gucDispatch(h, http.MethodGet, "/v1/services", nil)
	w2 := gucDispatch(h, http.MethodGet, "/v1/services", nil)

	if w1.Code != w2.Code {
		t.Errorf("status codes differ: %d vs %d", w1.Code, w2.Code)
	}
	var b1, b2 map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &b1) //nolint:errcheck
	json.Unmarshal(w2.Body.Bytes(), &b2) //nolint:errcheck
	s1, _ := json.Marshal(b1["services"])
	s2, _ := json.Marshal(b2["services"])
	if string(s1) != string(s2) {
		t.Errorf("list not referentially transparent: %s != %s", s1, s2)
	}
}

// TestGUC_Services_GetDoesNotMutateStore confirms that a GET request is a pure
// read with no side effects — store cardinality before and after must be equal.
func TestGUC_Services_GetDoesNotMutateStore(t *testing.T) {
	st := newGUCStore()
	ctx := context.Background()
	id, err := st.CreateService(ctx, gucValidManifest("iso", "svc-iso"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := vsapi.NewHandlers(st)

	before, _ := st.ListServices(ctx, "")
	gucDispatch(h, http.MethodGet, "/v1/services/"+strconv.FormatInt(id, 10), nil)
	after, _ := st.ListServices(ctx, "")

	if len(before) != len(after) {
		t.Errorf("GET mutated store: before=%d after=%d services", len(before), len(after))
	}
}

// TestGUC_Services_CreateDoesNotMutateInputManifest verifies the referential
// transparency of the handler: the manifest struct passed as input is unmodified
// after DeclareService returns — the handler must not capture a pointer.
func TestGUC_Services_CreateDoesNotMutateInputManifest(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	m := gucValidManifest("imm", "svc-imm")
	origName := m.Metadata.Name
	origNS := m.Metadata.Namespace
	origRuntime := m.Spec.Runtime
	origInstances := m.Spec.Instances

	gucDispatch(h, http.MethodPost, "/v1/services", vsapi.DeclareServiceRequest{Manifest: m})

	if m.Metadata.Name != origName {
		t.Errorf("Metadata.Name mutated: %q -> %q", origName, m.Metadata.Name)
	}
	if m.Metadata.Namespace != origNS {
		t.Errorf("Metadata.Namespace mutated")
	}
	if m.Spec.Runtime != origRuntime {
		t.Errorf("Spec.Runtime mutated")
	}
	if m.Spec.Instances != origInstances {
		t.Errorf("Spec.Instances mutated")
	}
}

// ---------------------------------------------------------------------------
// GÖDEL — formal consistency, invariant preservation, impossible-state detection
// ---------------------------------------------------------------------------

// TestGUC_Services_IDConsistencyBetweenCreateAndGet asserts the formal
// invariant: the ID assigned at creation equals the ID returned by a subsequent
// read. A mismatch indicates a consistency violation in the storage layer.
func TestGUC_Services_IDConsistencyBetweenCreateAndGet(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	cr := gucDispatch(h, http.MethodPost, "/v1/services",
		vsapi.DeclareServiceRequest{Manifest: gucValidManifest("cons", "svc-cons")})
	if cr.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", cr.Code, cr.Body.String())
	}
	var created vsapi.ServiceResponse
	gucDecodeJSON(t, cr, &created)

	gr := gucDispatch(h, http.MethodGet, "/v1/services/"+strconv.FormatInt(created.ID, 10), nil)
	if gr.Code != http.StatusOK {
		t.Fatalf("get failed: %d %s", gr.Code, gr.Body.String())
	}
	var fetched vsapi.ServiceResponse
	gucDecodeJSON(t, gr, &fetched)

	if created.ID != fetched.ID {
		t.Errorf("Gödel consistency invariant violated: create_id=%d get_id=%d", created.ID, fetched.ID)
	}
	if fetched.Manifest.Metadata.Namespace != created.Manifest.Metadata.Namespace {
		t.Errorf("namespace inconsistent between create and get")
	}
}

// TestGUC_Services_DeletedServiceIsGone asserts the impossibility of the state
// "service is both deleted and accessible" — two contradictory states that must
// never coexist.
func TestGUC_Services_DeletedServiceIsGone(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	cr := gucDispatch(h, http.MethodPost, "/v1/services",
		vsapi.DeclareServiceRequest{Manifest: gucValidManifest("del", "svc-del")})
	var resp vsapi.ServiceResponse
	gucDecodeJSON(t, cr, &resp)

	idStr := strconv.FormatInt(resp.ID, 10)
	del := gucDispatch(h, http.MethodDelete, "/v1/services/"+idStr, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete failed: %d %s", del.Code, del.Body.String())
	}

	// GetService on deleted ID must return 404 (impossible to read deleted record)
	gr := gucDispatch(h, http.MethodGet, "/v1/services/"+idStr, nil)
	if gr.Code != http.StatusNotFound {
		t.Errorf("deleted service still readable: GET returned %d", gr.Code)
	}

	// ListServices must not contain the deleted service ID
	lr := gucDispatch(h, http.MethodGet, "/v1/services", nil)
	var body struct {
		Services []vsapi.ServiceResponse `json:"services"`
	}
	gucDecodeJSON(t, lr, &body)
	for _, svc := range body.Services {
		if svc.ID == resp.ID {
			t.Errorf("deleted service ID=%d still present in list — impossible state", resp.ID)
		}
	}
}

// TestGUC_Services_EmptyStoreListInvariant checks that an empty store returns
// an empty array rather than null — a null services field and an empty array
// are two representations of the same logical state, which would be a
// formal consistency violation.
func TestGUC_Services_EmptyStoreListInvariant(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	w := gucDispatch(h, http.MethodGet, "/v1/services", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `"services":null`) {
		t.Errorf("services list must not be null; got: %s", body)
	}
	if !strings.Contains(body, `"services"`) {
		t.Errorf("services key absent from response: %s", body)
	}
}

// TestGUC_Services_SuccessAndErrorNeverCoexist tests the Gödel invariant that
// a response cannot simultaneously carry both a valid service payload and an
// error payload — mutually exclusive states that cannot both be true.
func TestGUC_Services_SuccessAndErrorNeverCoexist(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	cr := gucDispatch(h, http.MethodPost, "/v1/services",
		vsapi.DeclareServiceRequest{Manifest: gucValidManifest("cex", "svc-cex")})
	if cr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", cr.Code, cr.Body.String())
	}
	var raw map[string]interface{}
	gucDecodeJSON(t, cr, &raw)

	_, hasError := raw["error"]
	_, hasID := raw["id"]
	if hasError && hasID {
		t.Errorf("response contains both 'error' and 'id' keys — contradiction: %v", raw)
	}
	if !hasID {
		t.Errorf("successful create response missing 'id' key: %v", raw)
	}
}

// TestGUC_Services_ConflictOnDuplicateDeclare tests the formal invariant that
// two services with the same (namespace, name) cannot coexist — the system
// must reject the contradiction before it enters an inconsistent state.
func TestGUC_Services_ConflictOnDuplicateDeclare(t *testing.T) {
	h := vsapi.NewHandlers(newGUCStore())
	manifest := gucValidManifest("dup", "svc-dup")

	w1 := gucDispatch(h, http.MethodPost, "/v1/services", vsapi.DeclareServiceRequest{Manifest: manifest})
	if w1.Code != http.StatusCreated {
		t.Fatalf("first declare: expected 201, got %d %s", w1.Code, w1.Body.String())
	}

	w2 := gucDispatch(h, http.MethodPost, "/v1/services", vsapi.DeclareServiceRequest{Manifest: manifest})
	if w2.Code != http.StatusConflict {
		t.Errorf("duplicate declare: expected 409, got %d — Gödel: contradictory state must be rejected", w2.Code)
	}
	var errResp vsapi.ErrorResponse
	if err := json.NewDecoder(w2.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if errResp.Error == "" {
		t.Error("conflict response must include non-empty error message")
	}
}
