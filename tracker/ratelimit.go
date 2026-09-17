package tracker

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// rateLimiterEntry wraps a rate.Limiter with last-seen time for TTL eviction.
type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter implements token bucket rate limiting per IP
type RateLimiter struct {
	limiters map[string]*rateLimiterEntry
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
		limiters: make(map[string]*rateLimiterEntry),
		rate:     rate.Limit(rps),
		burst:    burst,
		logger:   GetDefaultLogger(),
	}
}

// GetLimiter returns the rate limiter for the given IP, updating last-seen time.
func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	now := time.Now()

	rl.mu.RLock()
	entry, exists := rl.limiters[ip]
	rl.mu.RUnlock()

	if exists {
		rl.mu.Lock()
		if e, ok := rl.limiters[ip]; ok {
			e.lastSeen = now
			rl.mu.Unlock()
			return e.limiter
		}
		rl.mu.Unlock()
	}

	rl.mu.Lock()
	if entry, exists = rl.limiters[ip]; !exists {
		entry = &rateLimiterEntry{
			limiter:  rate.NewLimiter(rl.rate, rl.burst),
			lastSeen: now,
		}
		rl.limiters[ip] = entry
	}
	rl.mu.Unlock()

	return entry.limiter
}

// Allow returns true if the request should be allowed
func (rl *RateLimiter) Allow(ip string) bool {
	return rl.GetLimiter(ip).Allow()
}

// Cleanup evicts per-IP limiters that have not been seen for 10 minutes.
// This bounds memory growth without discarding active client state.
func (rl *RateLimiter) Cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		for range ticker.C {
			cutoff := time.Now().Add(-10 * time.Minute)
			rl.mu.Lock()
			evicted := 0
			for ip, entry := range rl.limiters {
				if entry.lastSeen.Before(cutoff) {
					delete(rl.limiters, ip)
					evicted++
				}
			}
			if evicted > 0 {
				rl.logger.Info("rate limiter TTL eviction", "evicted", evicted, "remaining", len(rl.limiters))
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
				rateLimitExceeded.WithLabelValues(ip).Inc()
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
