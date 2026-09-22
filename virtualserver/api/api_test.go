package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/store"
)

// ── mock store ────────────────────────────────────────────────────────────────

type testStore struct {
	namespaces []string
	nodes      map[string]*domain.Node
	services   map[int64]*domain.Service
	instances  map[string]*domain.ServiceInstance
	volumes    map[string]*domain.Volume
	mounts     map[int64]*domain.VolumeMount
	snapshots  map[string]*domain.VolumeSnapshot
	nextSvcID  int64
	nextMntID  int64
}

func newTestStore() *testStore {
	return &testStore{
		nodes:     make(map[string]*domain.Node),
		services:  make(map[int64]*domain.Service),
		instances: make(map[string]*domain.ServiceInstance),
		volumes:   make(map[string]*domain.Volume),
		mounts:    make(map[int64]*domain.VolumeMount),
		snapshots: make(map[string]*domain.VolumeSnapshot),
		nextSvcID: 1,
		nextMntID: 1,
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

// Volume methods
func (s *testStore) CreateVolume(_ context.Context, v domain.Volume) (string, error) {
	for _, existing := range s.volumes {
		if existing.Manifest.Metadata.Namespace == v.Manifest.Metadata.Namespace &&
			existing.Manifest.Metadata.Name == v.Manifest.Metadata.Name {
			return "", store.ErrConflict
		}
	}
	s.volumes[v.ID] = &v
	return v.ID, nil
}
func (s *testStore) GetVolume(_ context.Context, id string) (*domain.Volume, error) {
	if v, ok := s.volumes[id]; ok {
		return v, nil
	}
	return nil, store.ErrNotFound
}
func (s *testStore) ListVolumes(_ context.Context, ns string) ([]domain.Volume, error) {
	var out []domain.Volume
	for _, v := range s.volumes {
		if ns == "" || v.Manifest.Metadata.Namespace == ns {
			out = append(out, *v)
		}
	}
	return out, nil
}
func (s *testStore) UpdateVolumeState(_ context.Context, id string, to domain.VolumeState) error {
	if v, ok := s.volumes[id]; ok {
		v.State = to
		return nil
	}
	return store.ErrNotFound
}
func (s *testStore) ListActiveMountsByVolume(_ context.Context, volumeID string) ([]domain.VolumeMount, error) {
	var out []domain.VolumeMount
	for _, m := range s.mounts {
		if m.VolumeID == volumeID && m.State != domain.MountReleased {
			out = append(out, *m)
		}
	}
	return out, nil
}
func (s *testStore) DeleteVolume(_ context.Context, id string) error {
	if v, ok := s.volumes[id]; !ok {
		return store.ErrNotFound
	} else if v.State != domain.VolumeReleased {
		return store.ErrConflict
	}
	delete(s.volumes, id)
	return nil
}
func (s *testStore) CreateSnapshot(_ context.Context, snap domain.VolumeSnapshot) error {
	s.snapshots[snap.ID] = &snap
	return nil
}
func (s *testStore) GetSnapshot(_ context.Context, id string) (*domain.VolumeSnapshot, error) {
	if snap, ok := s.snapshots[id]; ok {
		return snap, nil
	}
	return nil, store.ErrNotFound
}
func (s *testStore) ListSnapshots(_ context.Context, volumeID string) ([]domain.VolumeSnapshot, error) {
	var out []domain.VolumeSnapshot
	for _, snap := range s.snapshots {
		if snap.VolumeID == volumeID {
			out = append(out, *snap)
		}
	}
	return out, nil
}
func (s *testStore) UpdateSnapshotState(_ context.Context, id string, state domain.SnapshotState, driverRef string, _ int64) error {
	if snap, ok := s.snapshots[id]; ok {
		snap.State = state
		snap.DriverRef = driverRef
		return nil
	}
	return store.ErrNotFound
}

// ── helpers ───────────────────────────────────────────────────────────────────

const testAdminKey = "test-secret-key"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := newTestStore()
	h := NewHandlers(ts)
	mux := buildMux(h)
	authMw := authMiddleware(testAdminKey)
	authedHandler := chain(mux, requestIDMiddleware, authMw)

	// Mirror the production two-mux pattern: /healthz is public, everything else requires auth.
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	publicMux.Handle("/", authedHandler)
	return httptest.NewServer(publicMux)
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
	// /healthz is on the public (unauthenticated) mux; use the full server.
	srv := newTestServer(t)
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

// ── volume tests ──────────────────────────────────────────────────────────────

func validVolumeManifest() domain.VolumeManifest {
	return domain.VolumeManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Volume",
		Metadata:   domain.VolumeMetadata{Namespace: "test", Name: "data-vol"},
		Spec: domain.VolumeSpec{
			Class:       "local",
			CapacityMiB: 1024,
			AccessMode:  domain.VolumeAccessRWO,
		},
	}
}

func TestAPI_DeclareVolume(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := doRequest(t, srv, http.MethodPost, "/v1/volumes", DeclareVolumeRequest{Manifest: validVolumeManifest()})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	var vr VolumeResponse
	decodeResponse(t, resp, &vr)
	if vr.ID == "" {
		t.Error("expected non-empty volume ID")
	}
	if vr.State != string(domain.VolumeDeclared) {
		t.Errorf("expected state=declared, got %q", vr.State)
	}
}

func TestAPI_DeclareVolume_InvalidManifest(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	bad := validVolumeManifest()
	bad.Kind = "Wrong"
	resp := doRequest(t, srv, http.MethodPost, "/v1/volumes", DeclareVolumeRequest{Manifest: bad})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_DeclareVolume_Conflict(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	doRequest(t, srv, http.MethodPost, "/v1/volumes", DeclareVolumeRequest{Manifest: validVolumeManifest()}).Body.Close()
	resp := doRequest(t, srv, http.MethodPost, "/v1/volumes", DeclareVolumeRequest{Manifest: validVolumeManifest()})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 for duplicate, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_GetVolume_NotFound(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := doRequest(t, srv, http.MethodGet, "/v1/volumes/no-such-id", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_ListVolumes(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	doRequest(t, srv, http.MethodPost, "/v1/volumes", DeclareVolumeRequest{Manifest: validVolumeManifest()}).Body.Close()

	resp := doRequest(t, srv, http.MethodGet, "/v1/volumes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var out map[string]interface{}
	decodeResponse(t, resp, &out)
	vols, _ := out["volumes"].([]interface{})
	if len(vols) != 1 {
		t.Errorf("expected 1 volume, got %d", len(vols))
	}
}

func TestAPI_DeleteVolume_Bound(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Declare volume, transition to bound so we can test the active-mounts guard.
	cr := doRequest(t, srv, http.MethodPost, "/v1/volumes", DeclareVolumeRequest{Manifest: validVolumeManifest()})
	var vr VolumeResponse
	decodeResponse(t, cr, &vr)

	// Inject an active mount directly into testStore.
	ts := newTestStore()
	ts.volumes[vr.ID] = &domain.Volume{
		ID:       vr.ID,
		Manifest: validVolumeManifest(),
		State:    domain.VolumeBound,
	}
	ts.mounts[1] = &domain.VolumeMount{
		ID:       1,
		VolumeID: vr.ID,
		State:    domain.MountActive,
	}
	h := NewHandlers(ts)
	mux := buildMux(h)
	authMw := authMiddleware(testAdminKey)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	publicMux.Handle("/", chain(mux, requestIDMiddleware, authMw))
	boundSrv := httptest.NewServer(publicMux)
	defer boundSrv.Close()

	resp := doRequest(t, boundSrv, http.MethodDelete, "/v1/volumes/"+vr.ID, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 for volume with active mounts, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_CreateSnapshot_VolumeNotReady(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	cr := doRequest(t, srv, http.MethodPost, "/v1/volumes", DeclareVolumeRequest{Manifest: validVolumeManifest()})
	var vr VolumeResponse
	decodeResponse(t, cr, &vr)

	// Volume is still declared — snapshot should be rejected.
	resp := doRequest(t, srv, http.MethodPost, "/v1/volumes/"+vr.ID+"/snapshots",
		CreateSnapshotRequest{Label: "snap-1"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_CreateSnapshot_Ready(t *testing.T) {
	ts := newTestStore()
	volID := "vol-abc"
	ts.volumes[volID] = &domain.Volume{
		ID:       volID,
		Manifest: validVolumeManifest(),
		State:    domain.VolumeReady,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	h := NewHandlers(ts)
	mux := buildMux(h)
	authMw := authMiddleware(testAdminKey)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	publicMux.Handle("/", chain(mux, requestIDMiddleware, authMw))
	snapSrv := httptest.NewServer(publicMux)
	defer snapSrv.Close()

	resp := doRequest(t, snapSrv, http.MethodPost, "/v1/volumes/"+volID+"/snapshots",
		CreateSnapshotRequest{Label: "snap-ready"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	var sr SnapshotResponse
	decodeResponse(t, resp, &sr)
	if sr.VolumeID != volID {
		t.Errorf("expected volumeId=%s, got %q", volID, sr.VolumeID)
	}
}

func newStoreServer(t *testing.T, ts *testStore) *httptest.Server {
	t.Helper()
	h := NewHandlers(ts)
	mux := buildMux(h)
	authMw := authMiddleware(testAdminKey)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	publicMux.Handle("/", chain(mux, requestIDMiddleware, authMw))
	srv := httptest.NewServer(publicMux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAPI_ListVolumeMounts_NotFound(t *testing.T) {
	ts := newTestStore()
	srv := newStoreServer(t, ts)

	resp := doRequest(t, srv, http.MethodGet, "/v1/volumes/missing/mounts", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAPI_ListVolumeMounts_Empty(t *testing.T) {
	ts := newTestStore()
	volID := "vol-mnt-1"
	ts.volumes[volID] = &domain.Volume{
		ID:        volID,
		Manifest:  validVolumeManifest(),
		State:     domain.VolumeReady,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	srv := newStoreServer(t, ts)

	resp := doRequest(t, srv, http.MethodGet, "/v1/volumes/"+volID+"/mounts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	decodeResponse(t, resp, &body)
	mounts, ok := body["mounts"]
	if !ok {
		t.Fatal("response missing 'mounts' key")
	}
	arr, ok := mounts.([]interface{})
	if !ok {
		t.Fatalf("mounts is not array: %T", mounts)
	}
	if len(arr) != 0 {
		t.Errorf("expected 0 mounts, got %d", len(arr))
	}
}

func TestAPI_ListVolumeMounts_WithActive(t *testing.T) {
	ts := newTestStore()
	volID := "vol-mnt-2"
	ts.volumes[volID] = &domain.Volume{
		ID:        volID,
		Manifest:  validVolumeManifest(),
		State:     domain.VolumeBound,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	now := time.Now()
	ts.mounts[1] = &domain.VolumeMount{
		ID:         1,
		VolumeID:   volID,
		InstanceID: "inst-abc",
		TargetPath: "/data",
		ReadOnly:   false,
		State:      domain.MountActive,
		MountedAt:  &now,
	}
	ts.mounts[2] = &domain.VolumeMount{
		ID:         2,
		VolumeID:   volID,
		InstanceID: "inst-def",
		TargetPath: "/cache",
		ReadOnly:   true,
		State:      domain.MountReleased, // should be excluded
		MountedAt:  &now,
	}
	srv := newStoreServer(t, ts)

	resp := doRequest(t, srv, http.MethodGet, "/v1/volumes/"+volID+"/mounts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Mounts []VolumeMountResponse `json:"mounts"`
	}
	decodeResponse(t, resp, &body)
	if len(body.Mounts) != 1 {
		t.Fatalf("expected 1 active mount, got %d", len(body.Mounts))
	}
	if body.Mounts[0].InstanceID != "inst-abc" {
		t.Errorf("unexpected instance id: %q", body.Mounts[0].InstanceID)
	}
	if body.Mounts[0].TargetPath != "/data" {
		t.Errorf("unexpected target path: %q", body.Mounts[0].TargetPath)
	}
}

func TestAPI_ListVolumeMounts_MethodNotAllowed(t *testing.T) {
	ts := newTestStore()
	volID := "vol-mnt-3"
	ts.volumes[volID] = &domain.Volume{
		ID:        volID,
		Manifest:  validVolumeManifest(),
		State:     domain.VolumeReady,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	srv := newStoreServer(t, ts)

	resp := doRequest(t, srv, http.MethodPost, "/v1/volumes/"+volID+"/mounts", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}
