package domain

import (
	"fmt"
	"time"
)

// InstanceState represents the lifecycle state of a service instance.
type InstanceState string

const (
	InstanceDeclared     InstanceState = "declared"
	InstanceScheduled    InstanceState = "scheduled"
	InstanceProvisioning InstanceState = "provisioning"
	InstanceStarting     InstanceState = "starting"
	InstanceRunning      InstanceState = "running"
	InstanceDegraded     InstanceState = "degraded"
	InstanceStopping     InstanceState = "stopping"
	InstanceFailed       InstanceState = "failed"
	InstanceTerminated   InstanceState = "terminated"
)

// allowedInstanceTransitions is the authoritative state machine for instances.
var allowedInstanceTransitions = map[InstanceState]map[InstanceState]bool{
	InstanceDeclared: {
		InstanceScheduled: true,
		InstanceFailed:    true,
	},
	InstanceScheduled: {
		InstanceProvisioning: true,
		InstanceFailed:       true,
	},
	InstanceProvisioning: {
		InstanceStarting: true,
		InstanceFailed:   true,
	},
	InstanceStarting: {
		InstanceRunning: true,
		InstanceFailed:  true,
	},
	InstanceRunning: {
		InstanceDegraded: true,
		InstanceStopping: true,
	},
	InstanceDegraded: {
		InstanceRunning:  true,
		InstanceStopping: true,
		InstanceFailed:   true,
	},
	InstanceStopping: {
		InstanceTerminated: true,
	},
	InstanceFailed: {
		InstanceDeclared:   true, // retry
		InstanceTerminated: true, // max retries exhausted
	},
	InstanceTerminated: {}, // terminal
}

// ErrInvalidInstanceTransition is returned when an instance state change is not permitted.
type ErrInvalidInstanceTransition struct {
	From InstanceState
	To   InstanceState
}

func (e ErrInvalidInstanceTransition) Error() string {
	return fmt.Sprintf("invalid instance state transition: %s → %s", e.From, e.To)
}

// ValidateInstanceTransition returns nil if from→to is a legal instance state transition.
func ValidateInstanceTransition(from, to InstanceState) error {
	allowed, ok := allowedInstanceTransitions[from]
	if !ok {
		return fmt.Errorf("unknown source instance state: %q", from)
	}
	if !allowed[to] {
		return ErrInvalidInstanceTransition{From: from, To: to}
	}
	return nil
}

// IsTerminalInstanceState returns true if the state cannot transition further.
func IsTerminalInstanceState(s InstanceState) bool {
	return s == InstanceTerminated
}

// ServiceInstance is a running (or pending) instance of a declared service.
type ServiceInstance struct {
	ID            string
	ServiceID     int64
	NodeID        string
	VSPath        VSPath
	State         InstanceState
	RetryCount    int
	RuntimeHandle map[string]string // adapter-specific handle data
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
