package virtualserver

import (
	"context"
	"fmt"
	"sync"
)

// ResolvedMount carries a fully resolved storage binding ready for the backend.
type ResolvedMount struct {
	VolumeID  string
	MountPath string
	HostPath  string
	ReadOnly  bool
}

// ProvisionRequest describes the workload the adapter must set up.
type ProvisionRequest struct {
	InstanceID string
	ServiceID  string
	Image      string
	CPUMillicores int
	RAMBytes   int64
	GPUDevices []string
	Mounts     []ResolvedMount
	Env        map[string]string
}

// ProvisionResult carries the adapter-assigned runtime identifier.
type ProvisionResult struct {
	RuntimeID string
}

// BackendAdapter is the plugin interface for execution backends (container
// runtimes, VMs, bare-metal bootstrappers, etc.).
// Implementations must be safe for concurrent use.
type BackendAdapter interface {
	// Name returns the adapter's registered name (e.g. "docker", "firecracker").
	Name() string

	// Provision sets up the runtime environment for the instance but does not
	// start it. Returns the adapter's own opaque runtime ID.
	Provision(ctx context.Context, req *ProvisionRequest) (*ProvisionResult, error)

	// Start signals the provisioned runtime to begin execution.
	Start(ctx context.Context, instanceID, runtimeID string) error

	// Stop gracefully halts the runtime. Idempotent.
	Stop(ctx context.Context, instanceID, runtimeID string) error

	// Status polls the current lifecycle state of the instance.
	Status(ctx context.Context, instanceID, runtimeID string) (InstanceState, error)

	// Destroy tears down and removes all runtime artefacts. Idempotent.
	Destroy(ctx context.Context, instanceID, runtimeID string) error
}

// ── AdapterRegistry ───────────────────────────────────────────────────────────

// AdapterRegistry holds named BackendAdapter implementations.
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]BackendAdapter
}

// NewAdapterRegistry returns an empty registry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{adapters: make(map[string]BackendAdapter)}
}

// Register adds a under a.Name(). Panics on duplicate.
func (r *AdapterRegistry) Register(a BackendAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.adapters[a.Name()]; ok {
		panic(fmt.Sprintf("adapter: %q already registered", a.Name()))
	}
	r.adapters[a.Name()] = a
}

// Get returns the adapter named n.
func (r *AdapterRegistry) Get(n string) (BackendAdapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[n]
	if !ok {
		return nil, fmt.Errorf("adapter %q not registered", n)
	}
	return a, nil
}

// Default returns the first registered adapter, or an error if none exist.
func (r *AdapterRegistry) Default() (BackendAdapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.adapters {
		return a, nil
	}
	return nil, fmt.Errorf("no backend adapters registered")
}

// ── NoOpAdapter ───────────────────────────────────────────────────────────────

// NoOpAdapter is a no-operation adapter useful for tests and dry-run mode.
// Provision always succeeds; Status always returns Running.
type NoOpAdapter struct{}

func (NoOpAdapter) Name() string { return "noop" }

func (NoOpAdapter) Provision(_ context.Context, req *ProvisionRequest) (*ProvisionResult, error) {
	return &ProvisionResult{RuntimeID: "noop-" + req.InstanceID}, nil
}
func (NoOpAdapter) Start(context.Context, string, string) error  { return nil }
func (NoOpAdapter) Stop(context.Context, string, string) error   { return nil }
func (NoOpAdapter) Destroy(context.Context, string, string) error { return nil }

func (NoOpAdapter) Status(_ context.Context, _, _ string) (InstanceState, error) {
	return InstanceStateRunning, nil
}
