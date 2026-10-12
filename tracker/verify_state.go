package tracker

import (
	"errors"
	"time"
)

// VerifyState is the complete, explicit state machine for artifact verification.
// Every state has defined transitions; VERIFIED and FAILED are the only terminals.
type VerifyState uint8

const (
	StateDownloading VerifyState = iota // artifact being downloaded to node
	StateComplete                        // download done, queued for verification
	StateVerifying                       // SHA-256 verification in progress
	StateVerified                        // terminal OK — artifact is good
	StateCorrupt                         // verification failed
	StateDeleting                        // corrupt artifact being removed
	StateRetrying                        // waiting backoff before re-download
	StateFailed                          // terminal ERROR — max retries exceeded
)

// MaxVerifyRetries is the maximum number of CORRUPT→DELETE→RETRY cycles
// before the artifact is permanently marked StateFailed.
const MaxVerifyRetries = 3

// RetryBackoff is the minimum wait between retry attempts.
const RetryBackoff = 5 * time.Minute

var (
	ErrMaxRetriesExceeded = errors.New("max verify retries exceeded")
	ErrInvalidTransition  = errors.New("invalid state transition")
	ErrTerminalState      = errors.New("state machine is in a terminal state")
)

func (s VerifyState) String() string {
	switch s {
	case StateDownloading:
		return "DOWNLOADING"
	case StateComplete:
		return "COMPLETE"
	case StateVerifying:
		return "VERIFYING"
	case StateVerified:
		return "VERIFIED"
	case StateCorrupt:
		return "CORRUPT"
	case StateDeleting:
		return "DELETING"
	case StateRetrying:
		return "RETRYING"
	case StateFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// IsTerminal returns true when no further transitions are possible.
func (s VerifyState) IsTerminal() bool {
	return s == StateVerified || s == StateFailed
}

// validTransitions is the complete, explicit transition table.
// Any (current → next) pair not in this map is forbidden.
//
// Full state machine:
//
//	DOWNLOADING → COMPLETE → VERIFYING → VERIFIED (terminal: ok)
//	                              ↓
//	                           CORRUPT → DELETING → RETRYING ↩ DOWNLOADING
//	                                        ↓
//	                                    FAILED (terminal: error, retries exhausted)
var validTransitions = map[VerifyState]map[VerifyState]bool{
	StateDownloading: {StateComplete: true},
	StateComplete:    {StateVerifying: true},
	StateVerifying:   {StateVerified: true, StateCorrupt: true},
	StateVerified:    {},
	StateCorrupt:     {StateDeleting: true},
	StateDeleting:    {StateRetrying: true, StateFailed: true},
	StateRetrying:    {StateDownloading: true},
	StateFailed:      {},
}

// Transition advances the state machine from current to next.
// retryCount is the number of CORRUPT cycles already completed.
// When transitioning from DELETING and retryCount >= MaxVerifyRetries,
// the transition is forced to StateFailed regardless of the requested next state.
func Transition(current, next VerifyState, retryCount int) (VerifyState, error) {
	if current.IsTerminal() {
		return current, ErrTerminalState
	}
	if !validTransitions[current][next] {
		return current, ErrInvalidTransition
	}
	// Enforce retry guard: DELETING can only go to StateFailed once exhausted.
	if current == StateDeleting && retryCount >= MaxVerifyRetries {
		return StateFailed, ErrMaxRetriesExceeded
	}
	return next, nil
}
