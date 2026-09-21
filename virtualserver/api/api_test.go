package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/store"
)

// ── mock store ────────────────────────────────────────────────────────────────

type testStore struct {
	namespaces []string
	nodes      map[string]*domain.Node
	services   map[int64]*domain.Service
	instances  map[string]*domain.ServiceInstance
	nextSvcID  int64
}

func newTestStore() *testStore {
	return &testStore{
		nodes:     make(map[string]*domain.Node),
		services:  make(map[int64]*domain.Service),
		instances: make(map[string]*domain.ServiceInstance),
		nextSvcID: 1,
	}
}

func (s *testStore) CreateNamespace(_ context.Context, name, _ string) (int64, error) {
	for _, n := range s.namespaces {
		if n == name {
			return 0, store.ErrConflict
		}
	}
	s.namespaces = append(s.namespaces, name)
	return int64(len(s.namespaces)), nil
}
func (s *testStore) ListNamespaces(_ context.Context) ([]string, error) { return s.namespaces, nil }

func (s *testStore) CreateNode(_ context.Context, n domain.Node) (string, error) {
	n.ID = "node-" + n.Name
	s.nodes[n.ID] = &n
	return n.ID, nil
}
func (s *testStore) GetNode(_ context.Context, id string) (*domain.Node, error) {
	if n, ok := s.nodes[id]; ok {
		return n, nil
	}
	return nil, store.ErrNotFound
}
func (s *testStore) ListNodes(_ context.Context, _ string) ([]domain.Node, error) {
	var out []domain.Node
	for _, n := range s.nodes {
		out = append(out, *n)
	}
	return out, nil
}
func (s *testStore) UpdateNodeState(_ context.Context, id string, to domain.NodeState) error {
	if n, ok := s.nodes[id]; ok {
		n.State = to
		return nil
	}
	return store.ErrNotFound
}

func (s *testStore) CreateService(_ context.Context, m domain.ServiceManifest) (int64, error) {
	id := s.nextSvcID
	s.nextSvcID++
	s.services[id] = &domain.Service{ID: id, Manifest: m, DesiredCount: m.Spec.Instances, State: "active"}
	return id, nil
}
func (s *testStore) GetService(_ context.Context, id int64) (*domain.Service, error) {
	if svc, ok := s.services[id]; ok {
		return svc, nil
	}
	return nil, store.ErrNotFound
}
func (s *testStore) ListServices(_ context.Context, _ string) ([]domain.Service, error) {
	var out []domain.Service
	for _, svc := range s.services {
		out = append(out, *svc)
	}
	return out, nil
}
func (s *testStore) DeleteService(_ context.Context, id int64) error {
	if _, ok := s.services[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.services, id)
	return nil
}

func (s *testStore) GetInstance(_ context.Context, id string) (*domain.ServiceInstance, error) {
	if inst, ok := s.instances[id]; ok {
		return inst, nil
	}
	return nil, store.ErrNotFound
}
func (s *testStore) ListInstancesByService(_ context.Context, _ int64) ([]domain.ServiceInstance, error) {
	return nil, nil
}
func (s *testStore) ListInstances(_ context.Context, _ string) ([]domain.ServiceInstance, error) {
	var out []domain.ServiceInstance
	for _, inst := range s.instances {
		out = append(out, *inst)
	}
	return out, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

const testAdminKey = "test-secret-key"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := newTestStore()
	h := NewHandlers(ts)
	mux := buildMux(h)
	authMw := authMiddleware(testAdminKey)
	handler := chain(mux, requestIDMiddleware, authMw)
	return httptest.NewServer(handler)
}

func authHeader() string { return "Bearer " + testAdminKey }

func doRequest(t *testing.T, srv *httptest.Server, method, path string, body interface{}) *http.Response {
	t.Helper()
	var b bytes.Buffer
	if body != nil {
		json.NewEncoder(&b).Encode(body)
	}
	req, _ := http.NewRequest(method, srv.URL+path, &b)
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return resp
}

func decodeResponse(t *testing.T, resp *http.Response, out interface{}) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// ── auth tests ────────────────────────────────────────────────────────────────

func TestAPI_AuthRequired(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/nodes", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAPI_WrongKey(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/nodes", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// ── namespace tests ───────────────────────────────────────────────────────────

func TestAPI_CreateNamespace(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := doRequest(t, srv, http.MethodPost, "/v1/namespaces", map[string]string{"name": "test-ns"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_CreateNamespace_InvalidName(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := doRequest(t, srv, http.MethodPost, "/v1/namespaces", map[string]string{"name": "UPPERCASE"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── node tests ────────────────────────────────────────────────────────────────

func TestAPI_RegisterNode(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req := RegisterNodeRequest{
		Name:        "node1",
		Arch:        "x86_64",
		CPUThreads:  16,
		TotalRAMMiB: 65536,
	}
	resp := doRequest(t, srv, http.MethodPost, "/v1/nodes", req)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	var nr NodeResponse
	decodeResponse(t, resp, &nr)
	if nr.Name != "node1" {
		t.Errorf("expected name=node1, got %q", nr.Name)
	}
}

func TestAPI_RegisterNode_MissingArch(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req := RegisterNodeRequest{Name: "node-noarch", CPUThreads: 4, TotalRAMMiB: 1024}
	resp := doRequest(t, srv, http.MethodPost, "/v1/nodes", req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_ListNodes(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	doRequest(t, srv, http.MethodPost, "/v1/nodes", RegisterNodeRequest{
		Name: "n1", Arch: "x86_64", CPUThreads: 4, TotalRAMMiB: 4096,
	}).Body.Close()

	resp := doRequest(t, srv, http.MethodGet, "/v1/nodes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var out map[string]interface{}
	decodeResponse(t, resp, &out)
	if _, ok := out["nodes"]; !ok {
		t.Error("expected 'nodes' key in response")
	}
}

// ── service tests ─────────────────────────────────────────────────────────────

func validManifest() domain.ServiceManifest {
	return domain.ServiceManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Service",
		Metadata:   domain.ServiceMetadata{Namespace: "test", Name: "my-svc"},
		Spec: domain.ServiceSpec{
			Artifact:  domain.ArtifactReference{Type: domain.ArtifactTypeOcelot, InfoHash: "aabbccddee112233aabbccddee112233aabbccdd"},
			Runtime:   "mock",
			Instances: 1,
			Resources: domain.ResourceRequest{RAMMiB: 1024},
		},
	}
}

func TestAPI_DeclareService(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := doRequest(t, srv, http.MethodPost, "/v1/services", DeclareServiceRequest{Manifest: validManifest()})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_DeclareService_InvalidManifest(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	bad := validManifest()
	bad.APIVersion = "wrong"
	resp := doRequest(t, srv, http.MethodPost, "/v1/services", DeclareServiceRequest{Manifest: bad})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid manifest, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_GetService_NotFound(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := doRequest(t, srv, http.MethodGet, "/v1/services/9999", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_DeleteService(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Create then delete
	cr := doRequest(t, srv, http.MethodPost, "/v1/services", DeclareServiceRequest{Manifest: validManifest()})
	var svcResp ServiceResponse
	decodeResponse(t, cr, &svcResp)

	idStr := strconv.FormatInt(svcResp.ID, 10)
	resp := doRequest(t, srv, http.MethodDelete, "/v1/services/"+idStr, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── method-not-allowed tests ──────────────────────────────────────────────────

func TestAPI_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := doRequest(t, srv, http.MethodDelete, "/v1/nodes", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── healthz ───────────────────────────────────────────────────────────────────

func TestAPI_Healthz(t *testing.T) {
	ts := newTestStore()
	h := NewHandlers(ts)
	mux := buildMux(h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
