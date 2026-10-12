package domain

import (
	"fmt"
	"time"
)

// OperationState is the state of an async backend operation.
type OperationState string

const (
	OperationPending   OperationState = "pending"
	OperationRunning   OperationState = "running"
	OperationSucceeded OperationState = "succeeded"
	OperationFailed    OperationState = "failed"
	OperationCancelled OperationState = "cancelled"
)

// OperationType classifies the kind of adapter operation.
type OperationType string

const (
	OpProvision OperationType = "provision"
	OpStart     OperationType = "start"
	OpStop      OperationType = "stop"
	OpDestroy   OperationType = "destroy"
	OpInspect   OperationType = "inspect"
)

var allowedOperationTransitions = map[OperationState]map[OperationState]bool{
	OperationPending: {
		OperationRunning:   true,
		OperationCancelled: true,
	},
	OperationRunning: {
		OperationSucceeded: true,
		OperationFailed:    true,
		OperationCancelled: true,
	},
	OperationSucceeded: {},
	OperationFailed:    {},
	OperationCancelled: {},
}

// ValidateOperationTransition returns nil if from→to is legal.
func ValidateOperationTransition(from, to OperationState) error {
	allowed, ok := allowedOperationTransitions[from]
	if !ok {
		return fmt.Errorf("unknown operation state: %q", from)
	}
	if !allowed[to] {
		return fmt.Errorf("invalid operation state transition: %s → %s", from, to)
	}
	return nil
}

// OperationEvent is a timestamped log entry attached to an operation.
type OperationEvent struct {
	ID          int64
	OperationID string
	EventType   string
	Message     string
	Payload     map[string]interface{}
	CreatedAt   time.Time
}

// Operation tracks an asynchronous adapter command.
type Operation struct {
	ID          string
	InstanceID  string
	Type        OperationType
	State       OperationState
	Adapter     string
	Events      []OperationEvent
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// PlacementDecision is the output of the scheduler for a single scheduling attempt.
type PlacementDecision struct {
	SelectedNodeID   string
	Score            float64
	Reasons          []string            // why this node was chosen
	RejectionReasons map[string][]string // nodeID → reasons for rejection
}
