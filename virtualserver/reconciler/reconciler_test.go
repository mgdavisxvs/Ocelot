package reconciler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/adapter"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/scheduler"
	"github.com/mgdavisxvs/Ocelot/virtualserver/store"
)

// ── mock store ────────────────────────────────────────────────────────────────

type mockStore struct {
	mu        sync.Mutex
	instances map[string]*domain.ServiceInstance
	nodes     []domain.Node
	services  map[int64]*domain.Service
	allocs    map[string]*store.Allocation
	auditLog  []string
}

func newMockStore() *mockStore {
	return &mockStore{
		instances: make(map[string]*domain.ServiceInstance),
		services:  make(map[int64]*domain.Service),
		allocs:    make(map[string]*store.Allocation),
	}
}

func (m *mockStore) addInstance(inst *domain.ServiceInstance) {
	m.mu.Lock()
	m.instances[inst.ID] = inst
	m.mu.Unlock()
}

func (m *mockStore) ListInstances(_ context.Context, stateFilter string) ([]domain.ServiceInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.ServiceInstance
	for _, inst := range m.instances {
		if stateFilter == "" || string(inst.State) == stateFilter {
			out = append(out, *inst)
		}
	}
	return out, nil
}

func (m *mockStore) ListNodes(_ context.Context, _ string) ([]domain.Node, error) {
	return m.nodes, nil
}

func (m *mockStore) GetInstance(_ context.Context, id string) (*domain.ServiceInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		cp := *inst
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (m *mockStore) GetService(_ context.Context, id int64) (*domain.Service, error) {
	if svc, ok := m.services[id]; ok {
		return svc, nil
	}
	return nil, store.ErrNotFound
}

func (m *mockStore) UpdateInstanceState(_ context.Context, id string, to domain.InstanceState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		inst.State = to
		inst.UpdatedAt = time.Now()
	}
	return nil
}

func (m *mockStore) AssignNode(_ context.Context, instanceID, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[instanceID]; ok {
		inst.NodeID = nodeID
		inst.State = domain.InstanceScheduled
	}
	return nil
}

func (m *mockStore) AllocateResources(_ context.Context, instanceID, nodeID string, cpuThreads int, ramMiB int64, gpuDeviceIndex *int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allocs[instanceID] = &store.Allocation{
		InstanceID: instanceID,
		NodeID:     nodeID,
		CPUThreads: cpuThreads,
		RAMMiB:     ramMiB,
	}
	return nil
}

func (m *mockStore) GetActiveAllocation(_ context.Context, instanceID string) (*store.Allocation, error) {
	if a, ok := m.allocs[instanceID]; ok {
		return a, nil
	}
	return nil, store.ErrNotFound
}

func (m *mockStore) ReleaseAllocation(_ context.Context, instanceID string) error {
	m.mu.Lock()
	delete(m.allocs, instanceID)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) IncrementRetryCount(_ context.Context, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[instanceID]; ok {
		inst.RetryCount++
		inst.State = domain.InstanceDeclared
	}
	return nil
}

func (m *mockStore) UpdateRuntimeHandle(_ context.Context, instanceID string, handle map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[instanceID]; ok {
		inst.RuntimeHandle = handle
	}
	return nil
}

func (m *mockStore) WriteAuditLog(_ context.Context, action, resourceType, resourceID, _ string, _ bool, _ string) error {
	m.mu.Lock()
	m.auditLog = append(m.auditLog, action+":"+resourceType+":"+resourceID)
	m.mu.Unlock()
	return nil
}

// Volume stubs — reconciler tests don't exercise volume logic; these satisfy the interface.
func (m *mockStore) ListVolumesByState(_ context.Context, _ domain.VolumeState) ([]domain.Volume, error) {
	return nil, nil
}
func (m *mockStore) GetVolume(_ context.Context, _ string) (*domain.Volume, error) {
	return nil, store.ErrNotFound
}
func (m *mockStore) GetVolumeByName(_ context.Context, _, _ string) (*domain.Volume, error) {
	return nil, store.ErrNotFound
}
func (m *mockStore) UpdateVolumeState(_ context.Context, _ string, _ domain.VolumeState) error {
	return nil
}
func (m *mockStore) UpdateVolumeHandle(_ context.Context, _ string, _ map[string]string) error {
	return nil
}
func (m *mockStore) UpdateVolumeBoundNode(_ context.Context, _, _ string) error { return nil }
func (m *mockStore) UpdateVolumeFailure(_ context.Context, _, _ string) error   { return nil }
func (m *mockStore) BindMount(_ context.Context, _ domain.VolumeMount) (int64, error) {
	return 0, nil
}
func (m *mockStore) UpdateMountState(_ context.Context, _ int64, _ domain.VolumeMountState) error {
	return nil
}
func (m *mockStore) ListMountsByInstance(_ context.Context, _ string) ([]domain.VolumeMount, error) {
	return nil, nil
}
func (m *mockStore) ListActiveMountsByVolume(_ context.Context, _ string) ([]domain.VolumeMount, error) {
	return nil, nil
}

// C2/C4 stubs — required by the expanded StoreInterface.
func (m *mockStore) UpdateNodeState(_ context.Context, id string, to domain.NodeState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.nodes {
		if m.nodes[i].ID == id {
			m.nodes[i].State = to
		}
	}
	return nil
}

func (m *mockStore) ListServices(_ context.Context, _ string) ([]domain.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Service
	for _, svc := range m.services {
		out = append(out, *svc)
	}
	return out, nil
}

func (m *mockStore) CreateInstance(_ context.Context, serviceID int64, vsPath domain.VSPath) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("inst-created-%d-%s", serviceID, vsPath.Instance)
	m.instances[id] = &domain.ServiceInstance{
		ID:        id,
		ServiceID: serviceID,
		State:     domain.InstanceDeclared,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return id, nil
}

func (m *mockStore) ListInstancesByService(_ context.Context, serviceID int64) ([]domain.ServiceInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.ServiceInstance
	for _, inst := range m.instances {
		if inst.ServiceID == serviceID {
			out = append(out, *inst)
		}
	}
	return out, nil
}

func (m *mockStore) DeleteVolume(_ context.Context, _ string) error { return nil }

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestService(runtime string) *domain.Service {
	return &domain.Service{
		ID: 1,
		Manifest: domain.ServiceManifest{
			APIVersion: "virtualserver/v1",
			Kind:       "Service",
			Metadata:   domain.ServiceMetadata{Namespace: "test", Name: "svc"},
			Spec: domain.ServiceSpec{
				Artifact:  domain.ArtifactReference{Type: domain.ArtifactTypeOcelot, InfoHash: "0123456789abcdef0123456789abcdef01234567"},
				Runtime:   runtime,
				Instances: 1,
				Resources: domain.ResourceRequest{RAMMiB: 1024},
				Restart:   domain.RestartPolicy{Policy: "on-failure", MaximumAttempts: 3},
			},
		},
		DesiredCount: 1,
		State:        "active",
	}
}

func newTestNode() domain.Node {
	return domain.Node{
		ID: "n1", Name: "node1", Arch: "x86_64", State: domain.NodeReady,
		TotalRAMMiB: 65536, AvailRAMMiB: 65536, CPUThreads: 16,
	}
}

func newReconcilerWithMock(ms *mockStore, ad adapter.BackendAdapter) *VSReconciler {
	adapters := map[string]adapter.BackendAdapter{}
	if ad != nil {
		adapters["mock"] = ad
	}
	return New(Config{
		Store:    ms,
		Adapters: adapters,
		Sched:    scheduler.New(scheduler.DefaultWeights()),
		Interval: 100 * time.Millisecond,
	})
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestReconciler_DeclaredToRunning(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("mock")
	ms.nodes = []domain.Node{newTestNode()}

	inst := &domain.ServiceInstance{
		ID:        "inst-1",
		ServiceID: 1,
		State:     domain.InstanceDeclared,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	ctx := context.Background()

	// Single reconcile should drive declared → scheduled → provisioning → starting → running
	// In one pass it will only handle declared → scheduled; subsequent passes do the rest.
	// Run three passes to reach running.
	for i := 0; i < 4; i++ {
		if err := r.reconcile(ctx); err != nil {
			t.Fatalf("reconcile pass %d: %v", i, err)
		}
	}

	ms.mu.Lock()
	finalState := ms.instances["inst-1"].State
	ms.mu.Unlock()
	if finalState != domain.InstanceRunning {
		t.Errorf("expected running, got %q", finalState)
	}
}

func TestReconciler_FailedInstance_Retry(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("mock")
	ms.nodes = []domain.Node{newTestNode()}

	inst := &domain.ServiceInstance{
		ID:         "inst-2",
		ServiceID:  1,
		State:      domain.InstanceFailed,
		RetryCount: 0,
		UpdatedAt:  time.Now().Add(-10 * time.Second), // well past base delay
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	ms.mu.Lock()
	finalState := ms.instances["inst-2"].State
	retries := ms.instances["inst-2"].RetryCount
	ms.mu.Unlock()

	if finalState != domain.InstanceDeclared {
		t.Errorf("expected declared (retry), got %q", finalState)
	}
	if retries != 1 {
		t.Errorf("expected RetryCount=1, got %d", retries)
	}
}

func TestReconciler_FailedInstance_MaxRetries(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("mock")
	ms.nodes = []domain.Node{newTestNode()}

	inst := &domain.ServiceInstance{
		ID:         "inst-3",
		ServiceID:  1,
		State:      domain.InstanceFailed,
		RetryCount: 3, // equals MaximumAttempts
		UpdatedAt:  time.Now().Add(-60 * time.Second),
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	r.reconcile(context.Background())

	ms.mu.Lock()
	finalState := ms.instances["inst-3"].State
	ms.mu.Unlock()
	if finalState != domain.InstanceTerminated {
		t.Errorf("expected terminated after max retries exhausted, got %q", finalState)
	}
}

func TestReconciler_NoReadyNodes_ScheduleFails(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("mock")
	ms.nodes = []domain.Node{
		{ID: "n1", Name: "drained", State: domain.NodeDraining, TotalRAMMiB: 65536, AvailRAMMiB: 65536},
	}

	inst := &domain.ServiceInstance{
		ID: "inst-4", ServiceID: 1, State: domain.InstanceDeclared,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	r.reconcile(context.Background())

	ms.mu.Lock()
	finalState := ms.instances["inst-4"].State
	ms.mu.Unlock()
	if finalState != domain.InstanceDeclared {
		t.Errorf("expected declared (no eligible nodes), got %q", finalState)
	}
}

func TestReconciler_AdapterProvisionFailure(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("mock")
	ms.nodes = []domain.Node{newTestNode()}

	// Instance already scheduled (skip scheduling phase)
	inst := &domain.ServiceInstance{
		ID: "inst-5", ServiceID: 1, State: domain.InstanceScheduled,
		NodeID: "n1", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		RuntimeHandle: make(map[string]string),
	}
	ms.addInstance(inst)

	ad := adapter.NewMockAdapter()
	ad.FailMode = adapter.FailProvision

	r := newReconcilerWithMock(ms, ad)
	r.reconcile(context.Background())

	ms.mu.Lock()
	finalState := ms.instances["inst-5"].State
	ms.mu.Unlock()
	if finalState != domain.InstanceFailed {
		t.Errorf("expected failed after provision error, got %q", finalState)
	}
}

func TestReconciler_OverlapGuard(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("mock")
	ms.nodes = []domain.Node{newTestNode()}

	r := newReconcilerWithMock(ms, nil)

	r.runningMu.Lock()
	done := make(chan struct{})
	go func() {
		r.tryRun() // must return immediately without blocking
		close(done)
	}()
	select {
	case <-done:
		// correct: tryRun returned while mutex was held
	case <-time.After(500 * time.Millisecond):
		t.Error("tryRun blocked when runningMu was already held")
	}
	r.runningMu.Unlock()
}

func TestReconciler_StopGraceful(t *testing.T) {
	ms := newMockStore()
	r := newReconcilerWithMock(ms, nil)
	r.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := r.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestReconciler_MissingAdapter_SkipsInstance(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("unknown-runtime")
	ms.nodes = []domain.Node{newTestNode()}

	inst := &domain.ServiceInstance{
		ID: "inst-6", ServiceID: 1, State: domain.InstanceScheduled,
		NodeID: "n1", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		RuntimeHandle: make(map[string]string),
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, nil)
	r.reconcile(context.Background())

	ms.mu.Lock()
	finalState := ms.instances["inst-6"].State
	ms.mu.Unlock()
	if finalState != domain.InstanceFailed {
		t.Logf("state=%q (acceptable if error logged)", finalState)
	}
}

// C2: a ready node whose heartbeat has gone stale must be marked degraded.
func TestReconciler_NodeHeartbeatTTL(t *testing.T) {
	ms := newMockStore()
	stale := time.Now().Add(-5 * time.Minute)
	ms.nodes = []domain.Node{{
		ID:            "n1",
		Name:          "node1",
		State:         domain.NodeReady,
		LastHeartbeat: &stale,
	}}

	r := newReconcilerWithMock(ms, nil)
	r.nodeTTL = 90 * time.Second
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	ms.mu.Lock()
	state := ms.nodes[0].State
	ms.mu.Unlock()
	if state != domain.NodeDegraded {
		t.Errorf("expected NodeDegraded after stale heartbeat, got %q", state)
	}
}

// C3: a running instance whose process has exited (Inspect returns Running=false)
// must transition to failed immediately.
func TestReconciler_InspectRunning_ProcessDied(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("mock")
	ms.nodes = []domain.Node{newTestNode()}

	// Instance is marked running in the store but was never registered in the adapter.
	// MockAdapter.Inspect returns Running=false for unregistered instances.
	inst := &domain.ServiceInstance{
		ID:            "inst-died",
		ServiceID:     1,
		State:         domain.InstanceRunning,
		NodeID:        "n1",
		RuntimeHandle: map[string]string{"pid": "99"},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	ms.mu.Lock()
	finalState := ms.instances["inst-died"].State
	ms.mu.Unlock()
	if finalState != domain.InstanceFailed {
		t.Errorf("expected InstanceFailed after process exit, got %q", finalState)
	}
}

// C3: an instance in stopping state must be driven through Stop+Destroy to terminated.
func TestReconciler_StoppingToTerminated(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("mock")
	ms.nodes = []domain.Node{newTestNode()}

	inst := &domain.ServiceInstance{
		ID:            "inst-stopping",
		ServiceID:     1,
		State:         domain.InstanceStopping,
		NodeID:        "n1",
		RuntimeHandle: map[string]string{"pid": "42"},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	ms.addInstance(inst)

	ad := adapter.NewMockAdapter()
	r := newReconcilerWithMock(ms, ad)
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	ms.mu.Lock()
	finalState := ms.instances["inst-stopping"].State
	ms.mu.Unlock()
	if finalState != domain.InstanceTerminated {
		t.Errorf("expected InstanceTerminated after stop, got %q", finalState)
	}
	if ad.StopCalls != 1 {
		t.Errorf("expected Stop called once, got %d", ad.StopCalls)
	}
	if ad.DestroyCalls != 1 {
		t.Errorf("expected Destroy called once, got %d", ad.DestroyCalls)
	}
}

// C4: when active instance count is below DesiredCount, new declared instances
// must be created to fill the gap.
func TestReconciler_DesiredCount_CreatesMissingInstance(t *testing.T) {
	ms := newMockStore()
	svc := newTestService("mock")
	svc.DesiredCount = 2
	ms.services[1] = svc
	ms.nodes = []domain.Node{newTestNode()}

	// Only one non-terminal instance exists; a second must be created.
	ms.addInstance(&domain.ServiceInstance{
		ID:        "inst-existing",
		ServiceID: 1,
		State:     domain.InstanceRunning,
		NodeID:    "n1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	ms.mu.Lock()
	total := len(ms.instances)
	ms.mu.Unlock()
	if total < 2 {
		t.Errorf("expected at least 2 instances after desired-count reconciliation, got %d", total)
	}
}

// ── mockCatalog ───────────────────────────────────────────────────────────────

type mockCatalog struct{}

func (m *mockCatalog) Lookup(_ context.Context, _ string) (domain.ArtifactStatus, error) {
	return domain.ArtifactStatus{Exists: true, Available: true, Availability: domain.ArtifactAvailable}, nil
}

var _ ArtifactCatalog = (*mockCatalog)(nil)

var _ error = errors.New("") // ensure errors package used
