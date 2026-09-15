package tracker

import (
	"net/http"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/time/rate"
)

// RateLimiter implements per-IP token bucket rate limiting backed by an LRU
// cache so that eviction is automatic and proportional — no thundering-herd
// full-reset when the map grows large.
type RateLimiter struct {
	cache  *lru.Cache[string, *rate.Limiter]
	rate   rate.Limit
	burst  int
	logger *Logger
}

// NewRateLimiter creates a new rate limiter.
// rps: requests per second; burst: maximum burst size; maxEntries: LRU capacity.
func NewRateLimiter(rps, burst, maxEntries int) *RateLimiter {
	c, _ := lru.New[string, *rate.Limiter](maxEntries)
	return &RateLimiter{
		cache:  c,
		rate:   rate.Limit(rps),
		burst:  burst,
		logger: GetDefaultLogger(),
	}
}

// GetLimiter returns the rate limiter for the given IP, creating one if absent.
func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	if limiter, ok := rl.cache.Get(ip); ok {
		return limiter
	}
	limiter := rate.NewLimiter(rl.rate, rl.burst)
	rl.cache.Add(ip, limiter)
	return limiter
}

// Allow returns true if the request from ip should be allowed.
func (rl *RateLimiter) Allow(ip string) bool {
	return rl.GetLimiter(ip).Allow()
}

// Len returns the number of tracked IPs.
func (rl *RateLimiter) Len() int {
	return rl.cache.Len()
}

// RateLimitMiddleware returns HTTP middleware for rate limiting.
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

// GetClientIP extracts the client IP from the request.
func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}
	return r.RemoteAddr
}
