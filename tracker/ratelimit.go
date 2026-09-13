package tracker

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter implements token bucket rate limiting per IP
type RateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	rate     rate.Limit // requests per second
	burst    int        // max burst size
	logger   *Logger
}

// NewRateLimiter creates a new rate limiter
// rps: requests per second allowed
// burst: maximum burst size
func NewRateLimiter(rps int, burst int) *RateLimiter {
	return &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rate:     rate.Limit(rps),
		burst:    burst,
		logger:   GetDefaultLogger(),
	}
}

// GetLimiter returns the rate limiter for the given IP
func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	rl.mu.RLock()
	limiter, exists := rl.limiters[ip]
	rl.mu.RUnlock()

	if !exists {
		rl.mu.Lock()
		// Double-check after acquiring write lock
		limiter, exists = rl.limiters[ip]
		if !exists {
			limiter = rate.NewLimiter(rl.rate, rl.burst)
			rl.limiters[ip] = limiter
		}
		rl.mu.Unlock()
	}

	return limiter
}

// Allow returns true if the request should be allowed
func (rl *RateLimiter) Allow(ip string) bool {
	return rl.GetLimiter(ip).Allow()
}

// Cleanup removes old limiters to prevent memory leaks
func (rl *RateLimiter) Cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		for range ticker.C {
			rl.mu.Lock()
			// Clear all limiters periodically
			// In production, use LRU cache or TTL-based cleanup
			if len(rl.limiters) > 10000 {
				rl.limiters = make(map[string]*rate.Limiter)
				rl.logger.Info("rate limiter cache cleared", "count", len(rl.limiters))
			}
			rl.mu.Unlock()
		}
	}()
}

// RateLimitMiddleware returns HTTP middleware for rate limiting
func RateLimitMiddleware(limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := GetClientIP(r)

			if !limiter.Allow(ip) {
				limiter.logger.Warn("rate limit exceeded",
					"ip", ip,
					"path", r.URL.Path,
				)
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetClientIP extracts the client IP from the request
func GetClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxies/load balancers)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}

	// Check X-Real-IP header
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}

	// Fall back to RemoteAddr
	return r.RemoteAddr
}
