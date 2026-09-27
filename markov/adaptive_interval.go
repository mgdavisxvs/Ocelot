package markov

import (
	"math/rand"
	"sync"
	"time"
)

// AdaptiveInterval computes an announce interval with exponential backoff on
// failure and automatic reset on success. Thread-safe.
//
// Typical usage:
//
//	ai := NewAdaptiveInterval(30*time.Second, 2.0, 10*time.Minute, 0.1)
//	for {
//	    time.Sleep(ai.Next())
//	    if err := announce(); err != nil {
//	        ai.Failure()
//	    } else {
//	        ai.Success()
//	    }
//	}
type AdaptiveInterval struct {
	mu      sync.Mutex
	base    time.Duration
	factor  float64
	max     time.Duration // 0 = unlimited
	jitter  float64       // fraction of current interval added as random noise
	current time.Duration
}

// NewAdaptiveInterval creates an AdaptiveInterval.
//
//   - base:   starting (and reset-to) interval. Zero is valid.
//   - factor: backoff multiplier applied by each Failure() call. Values < 1.0
//     are clamped to 1.0 (prevents shrinkage on failure).
//   - max:    upper cap on the interval; 0 means no cap (unlimited growth).
//   - jitter: fraction of the current interval to add as uniform random noise,
//     in [0.0, 1.0]; values outside that range are clamped.
func NewAdaptiveInterval(base time.Duration, factor float64, max time.Duration, jitter float64) *AdaptiveInterval {
	if factor < 1.0 {
		factor = 1.0
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1.0 {
		jitter = 1.0
	}
	return &AdaptiveInterval{
		base:    base,
		factor:  factor,
		max:     max,
		jitter:  jitter,
		current: base,
	}
}

// Next returns the current interval with jitter applied. It never returns a
// negative duration. Safe to call concurrently.
func (a *AdaptiveInterval) Next() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.current
	if a.jitter > 0 && d > 0 {
		d += time.Duration(float64(d) * a.jitter * rand.Float64())
	}
	if d < 0 {
		d = 0
	}
	return d
}

// Current returns the current base interval without any jitter applied. Safe
// to call concurrently.
func (a *AdaptiveInterval) Current() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current
}

// Failure signals that an attempt failed. The current interval is multiplied
// by factor; if a non-zero max is configured, the result is capped to max.
func (a *AdaptiveInterval) Failure() {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := time.Duration(float64(a.current) * a.factor)
	if a.max > 0 && next > a.max {
		next = a.max
	}
	a.current = next
}

// Success signals that an attempt succeeded. The current interval is reset to
// the base interval unconditionally.
func (a *AdaptiveInterval) Success() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.current = a.base
}
