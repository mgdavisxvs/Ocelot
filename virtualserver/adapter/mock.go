package adapter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// FailMode controls which MockAdapter operations fail.
type FailMode int

const (
	FailNone      FailMode = 0
	FailProvision FailMode = 1 << iota
	FailStart
	FailStop
	FailInspect
	FailHealth
	FailDestroy
	FailProbe
)

// MockAdapter is a deterministic BackendAdapter for testing.
// All operations succeed by default; FailMode enables selective failure injection.
type MockAdapter struct {
	mu sync.Mutex

	FailMode       FailMode
	ProvisionDelay time.Duration
	StartDelay     time.Duration

	provisioned map[string]RuntimeHandle // instanceID → handle
	started     map[string]bool
	stopped     map[string]bool
	destroyed   map[string]bool

	// Call counters for assertions
	ProvisionCalls int
	StartCalls     int
	StopCalls      int
	InspectCalls   int
	HealthCalls    int
	DestroyCalls   int
	ProbeCalls     int
}

// NewMockAdapter creates a MockAdapter with no failure injection.
func NewMockAdapter() *MockAdapter {
	return &MockAdapter{
		provisioned: make(map[string]RuntimeHandle),
		started:     make(map[string]bool),
		stopped:     make(map[string]bool),
		destroyed:   make(map[string]bool),
	}
}

func (m *MockAdapter) Name() string { return "mock" }

func (m *MockAdapter) Probe(ctx context.Context, node domain.Node) error {
	m.mu.Lock()
	m.ProbeCalls++
	m.mu.Unlock()
	if m.FailMode&FailProbe != 0 {
		return fmt.Errorf("mock: probe intentionally failed for node %s", node.ID)
	}
	return ctx.Err()
}

func (m *MockAdapter) Capabilities(_ context.Context, node domain.Node) (domain.Node, error) {
	return node, nil
}

func (m *MockAdapter) Provision(ctx context.Context, req ProvisionRequest) (RuntimeHandle, error) {
	m.mu.Lock()
	m.ProvisionCalls++
	m.mu.Unlock()

	if m.ProvisionDelay > 0 {
		select {
		case <-time.After(m.ProvisionDelay):
		case <-ctx.Done():
			return RuntimeHandle{}, ctx.Err()
		}
	}
	if m.FailMode&FailProvision != 0 {
		return RuntimeHandle{}, fmt.Errorf("mock: provision intentionally failed")
	}
	handle := RuntimeHandle{
		AdapterName: "mock",
		InstanceID:  req.InstanceID,
		Data: map[string]string{
			"node_id": req.NodeID,
			"pid":     "mock-pid-" + req.InstanceID,
			"workdir": "/tmp/vs-mock/" + req.InstanceID,
		},
	}
	m.mu.Lock()
	m.provisioned[req.InstanceID] = handle
	m.mu.Unlock()
	return handle, nil
}

func (m *MockAdapter) Start(ctx context.Context, handle RuntimeHandle) error {
	m.mu.Lock()
	m.StartCalls++
	m.mu.Unlock()

	if m.StartDelay > 0 {
		select {
		case <-time.After(m.StartDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if m.FailMode&FailStart != 0 {
		return fmt.Errorf("mock: start intentionally failed")
	}
	m.mu.Lock()
	m.started[handle.InstanceID] = true
	m.mu.Unlock()
	return nil
}

func (m *MockAdapter) Stop(ctx context.Context, handle RuntimeHandle, mode StopMode) error {
	m.mu.Lock()
	m.StopCalls++
	m.mu.Unlock()
	if m.FailMode&FailStop != 0 {
		return fmt.Errorf("mock: stop intentionally failed")
	}
	m.mu.Lock()
	m.stopped[handle.InstanceID] = true
	m.mu.Unlock()
	return ctx.Err()
}

func (m *MockAdapter) Inspect(ctx context.Context, handle RuntimeHandle) (RuntimeStatus, error) {
	m.mu.Lock()
	m.InspectCalls++
	running := m.started[handle.InstanceID] && !m.stopped[handle.InstanceID]
	m.mu.Unlock()

	if m.FailMode&FailInspect != 0 {
		return RuntimeStatus{}, fmt.Errorf("mock: inspect intentionally failed")
	}
	return RuntimeStatus{Running: running}, ctx.Err()
}

func (m *MockAdapter) Health(ctx context.Context, handle RuntimeHandle) (HealthResult, error) {
	m.mu.Lock()
	m.HealthCalls++
	running := m.started[handle.InstanceID] && !m.stopped[handle.InstanceID]
	m.mu.Unlock()

	if m.FailMode&FailHealth != 0 {
		return HealthResult{}, fmt.Errorf("mock: health check intentionally failed")
	}
	return HealthResult{Healthy: running, Message: "mock ok"}, ctx.Err()
}

func (m *MockAdapter) Destroy(ctx context.Context, handle RuntimeHandle) error {
	m.mu.Lock()
	m.DestroyCalls++
	m.mu.Unlock()

	if m.FailMode&FailDestroy != 0 {
		return fmt.Errorf("mock: destroy intentionally failed")
	}
	m.mu.Lock()
	delete(m.provisioned, handle.InstanceID)
	delete(m.started, handle.InstanceID)
	m.destroyed[handle.InstanceID] = true
	m.mu.Unlock()
	return ctx.Err()
}

// IsProvisioned returns true if Provision was called for the given instance.
func (m *MockAdapter) IsProvisioned(instanceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.provisioned[instanceID]
	return ok
}

// IsStarted returns true if Start was called for the given instance.
func (m *MockAdapter) IsStarted(instanceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started[instanceID]
}

// Reset clears all state and counters.
func (m *MockAdapter) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.provisioned = make(map[string]RuntimeHandle)
	m.started = make(map[string]bool)
	m.stopped = make(map[string]bool)
	m.destroyed = make(map[string]bool)
	m.ProvisionCalls = 0
	m.StartCalls = 0
	m.StopCalls = 0
	m.InspectCalls = 0
	m.HealthCalls = 0
	m.DestroyCalls = 0
	m.ProbeCalls = 0
}
