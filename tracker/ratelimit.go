package tracker

import (
	"hash/fnv"
	"net/http"
	"sync"

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

// ── ShardedRateLimiter ────────────────────────────────────────────────────────
//
// ShardedRateLimiter is a high-concurrency alternative to RateLimiter that
// divides the IP space across 256 independent shards, each with its own mutex.
// This eliminates the single sync.Map / LRU lock that becomes a bottleneck at
// > 50k concurrent IPs under high-QPS announce traffic.
//
// RULING-08 mitigation: replaces the global sync.Map with 256-shard map so lock
// contention is bounded to 1/256 of total RPS per shard under uniform load.
const nRLShards = 256

type rlShard struct {
	mu sync.Mutex
	m  map[string]*rate.Limiter
}

// ShardedRateLimiter distributes per-IP limiters across nRLShards independent
// shards, each protected by its own mutex.
type ShardedRateLimiter struct {
	shards  [nRLShards]rlShard
	rps     rate.Limit
	burst   int
	logger  *Logger
}

// NewShardedRateLimiter creates a ShardedRateLimiter with rps tokens/second and
// burst burst capacity.
func NewShardedRateLimiter(rps, burst int) *ShardedRateLimiter {
	s := &ShardedRateLimiter{
		rps:    rate.Limit(rps),
		burst:  burst,
		logger: GetDefaultLogger(),
	}
	for i := range s.shards {
		s.shards[i].m = make(map[string]*rate.Limiter)
	}
	return s
}

func (s *ShardedRateLimiter) shardIndex(ip string) int {
	h := fnv.New32a()
	h.Write([]byte(ip))
	return int(h.Sum32()) % nRLShards
}

// Allow returns true if the request from ip should be allowed.
func (s *ShardedRateLimiter) Allow(ip string) bool {
	idx := s.shardIndex(ip)
	sh := &s.shards[idx]
	sh.mu.Lock()
	l, ok := sh.m[ip]
	if !ok {
		l = rate.NewLimiter(s.rps, s.burst)
		sh.m[ip] = l
	}
	sh.mu.Unlock()
	return l.Allow()
}

// Len returns the total number of tracked IPs across all shards.
func (s *ShardedRateLimiter) Len() int {
	total := 0
	for i := range s.shards {
		s.shards[i].mu.Lock()
		total += len(s.shards[i].m)
		s.shards[i].mu.Unlock()
	}
	return total
}
