package tracker

import (
	"context"
	"errors"
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker
type CircuitState int

const (
	StateClosed CircuitState = iota
	StateOpen
	StateHalfOpen
)

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name          string
	maxFailures   uint32
	resetTimeout  time.Duration
	halfOpenMax   uint32
	mu            sync.RWMutex
	state         CircuitState
	failures      uint32
	lastFailTime  time.Time
	halfOpenCount uint32
	logger        *Logger
}

// CircuitBreakerConfig holds circuit breaker configuration
type CircuitBreakerConfig struct {
	Name         string
	MaxFailures  uint32        // Number of failures before opening
	ResetTimeout time.Duration // Time before trying half-open
	HalfOpenMax  uint32        // Max requests in half-open state
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	if config.MaxFailures == 0 {
		config.MaxFailures = 5
	}
	if config.ResetTimeout == 0 {
		config.ResetTimeout = 30 * time.Second
	}
	if config.HalfOpenMax == 0 {
		config.HalfOpenMax = 3
	}

	return &CircuitBreaker{
		name:         config.Name,
		maxFailures:  config.MaxFailures,
		resetTimeout: config.ResetTimeout,
		halfOpenMax:  config.HalfOpenMax,
		state:        StateClosed,
		logger:       GetDefaultLogger(),
	}
}

// Execute runs the given function with circuit breaker protection
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if !cb.canExecute() {
		return ErrCircuitOpen.WithDetail("circuit breaker %s is open", cb.name)
	}

	err := fn()
	cb.recordResult(err)
	return err
}

// ExecuteWithContext runs the function with context
func (cb *CircuitBreaker) ExecuteWithContext(ctx context.Context, fn func(context.Context) error) error {
	if !cb.canExecute() {
		return ErrCircuitOpen.WithDetail("circuit breaker %s is open", cb.name)
	}

	err := fn(ctx)
	cb.recordResult(err)
	return err
}

// canExecute checks if a request can be executed
func (cb *CircuitBreaker) canExecute() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if enough time has passed to try half-open
		if time.Since(cb.lastFailTime) >= cb.resetTimeout {
			cb.logger.Info("circuit breaker transitioning to half-open",
				"name", cb.name,
			)
			cb.state = StateHalfOpen
			cb.halfOpenCount = 1 // Count this transition request
			return true
		}
		return false

	case StateHalfOpen:
		// Allow limited requests in half-open state
		if cb.halfOpenCount < cb.halfOpenMax {
			cb.halfOpenCount++
			return true
		}
		return false

	default:
		return false
	}
}

// recordResult records the result of an execution
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err == nil {
		// Success
		cb.onSuccess()
	} else {
		// Failure
		cb.onFailure()
	}
}

// onSuccess handles a successful execution
func (cb *CircuitBreaker) onSuccess() {
	switch cb.state {
	case StateClosed:
		cb.failures = 0

	case StateHalfOpen:
		// If all half-open requests succeed, close the circuit
		if cb.halfOpenCount >= cb.halfOpenMax {
			cb.logger.Info("circuit breaker closing after successful half-open test",
				"name", cb.name,
			)
			cb.state = StateClosed
			cb.failures = 0
			cb.halfOpenCount = 0
		}
	}
}

// onFailure handles a failed execution
func (cb *CircuitBreaker) onFailure() {
	cb.failures++
	cb.lastFailTime = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.failures >= cb.maxFailures {
			cb.logger.Warn("circuit breaker opening due to failures",
				"name", cb.name,
				"failures", cb.failures,
			)
			cb.state = StateOpen
		}

	case StateHalfOpen:
		// Any failure in half-open state immediately opens the circuit
		cb.logger.Warn("circuit breaker re-opening after half-open failure",
			"name", cb.name,
		)
		cb.state = StateOpen
		cb.halfOpenCount = 0
	}
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetStateString returns the state as a string
func (cb *CircuitBreaker) GetStateString() string {
	state := cb.GetState()
	switch state {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.logger.Info("circuit breaker manually reset", "name", cb.name)
	cb.state = StateClosed
	cb.failures = 0
	cb.halfOpenCount = 0
}

// ErrCircuitBreakerOpen is returned when the circuit breaker is open
var ErrCircuitBreakerOpen = errors.New("circuit breaker is open")
