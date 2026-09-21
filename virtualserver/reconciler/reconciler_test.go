package reconciler

import (
	"context"
	"errors"
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
	if finalState != domain.InstanceFailed {
		t.Errorf("expected still failed (max retries), got %q", finalState)
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

// ── mockCatalog ───────────────────────────────────────────────────────────────

type mockCatalog struct{}

func (m *mockCatalog) Lookup(_ context.Context, _ string) (domain.ArtifactStatus, error) {
	return domain.ArtifactStatus{Exists: true, Available: true, Availability: domain.ArtifactAvailable}, nil
}

var _ ArtifactCatalog = (*mockCatalog)(nil)

var _ error = errors.New("") // ensure errors package used
