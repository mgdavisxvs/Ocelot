package adapter

import (
	"context"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// StopMode controls how a running instance is stopped.
type StopMode string

const (
	StopGraceful StopMode = "graceful"
	StopForce    StopMode = "force"
)

// RuntimeHandle contains adapter-specific opaque data identifying a provisioned runtime.
type RuntimeHandle struct {
	AdapterName string
	InstanceID  string
	Data        map[string]string
}

// RuntimeStatus is the observed state of a provisioned runtime.
type RuntimeStatus struct {
	Running     bool
	ExitCode    *int
	StartedAt   *time.Time
	Description string
}

// HealthResult is the outcome of a health check.
type HealthResult struct {
	Healthy bool
	Message string
}

// ProvisionRequest contains all information needed to provision an instance.
type ProvisionRequest struct {
	InstanceID string
	NodeID     string
	NodeName   string
	Manifest   domain.ServiceManifest
	Allocation Allocation
}

// Allocation holds the resources reserved for a provision request.
type Allocation struct {
	CPUThreads     int
	RAMMiB         int64
	GPUDeviceIndex *int
}

// BackendAdapter is the interface every compute backend must implement.
// All methods accept context.Context for cancellation and deadline propagation.
// No method may initiate database writes — state changes are the caller's responsibility.
type BackendAdapter interface {
	// Name returns the unique adapter identifier.
	Name() string

	// Probe checks whether the backend is reachable and the node is accessible.
	Probe(ctx context.Context, node domain.Node) error

	// Capabilities queries the node for its current resource inventory.
	Capabilities(ctx context.Context, node domain.Node) (domain.Node, error)

	// Provision allocates the runtime environment for an instance.
	Provision(ctx context.Context, req ProvisionRequest) (RuntimeHandle, error)

	// Start initiates execution of a provisioned instance.
	Start(ctx context.Context, handle RuntimeHandle) error

	// Stop requests shutdown of a running instance.
	Stop(ctx context.Context, handle RuntimeHandle, mode StopMode) error

	// Inspect returns the current observed state of a runtime.
	Inspect(ctx context.Context, handle RuntimeHandle) (RuntimeStatus, error)

	// Health performs a health check against the runtime.
	Health(ctx context.Context, handle RuntimeHandle) (HealthResult, error)

	// Destroy removes all resources for a stopped or failed instance.
	Destroy(ctx context.Context, handle RuntimeHandle) error
}
