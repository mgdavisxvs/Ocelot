package tracker

// ratelimit_guc_test.go — GUC test coverage for ShardedRateLimiter / RateLimiter
//
// Lenses:
//   Knuth  (~5): algorithmic correctness, loop invariants, data structure invariants
//   Turing (~5): termination conditions, halting behavior, decidability
//   Church (~5): functional purity, side-effect isolation, referential transparency
//   Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"hash/fnv"
	"sync"
	"testing"
	"time"
)

// ── Knuth: algorithmic correctness, data structure invariants ─────────────────

// TestGUC_ShardedRL_AllowUnderLimit verifies that Allow returns true for each
// call while the running total is still within the burst window.
func TestGUC_ShardedRL_AllowUnderLimit(t *testing.T) {
	cases := []struct {
		name  string
		rps   int
		burst int
	}{
		{"rps1_burst1", 1, 1},
		{"rps10_burst5", 10, 5},
		{"rps100_burst10", 100, 10},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := NewShardedRateLimiter(tc.rps, tc.burst)
			for i := 0; i < tc.burst; i++ {
				if !s.Allow("192.168.1.1") {
					t.Errorf("Allow() returned false on call %d/%d (burst=%d)",
						i+1, tc.burst, tc.burst)
				}
			}
		})
	}
}

// TestGUC_ShardedRL_AllowAtLimit verifies that Allow returns false immediately
// after the burst capacity is fully consumed.
func TestGUC_ShardedRL_AllowAtLimit(t *testing.T) {
	s := NewShardedRateLimiter(1, 3)
	ip := "10.0.0.1"
	for i := 0; i < 3; i++ {
		s.Allow(ip)
	}
	if s.Allow(ip) {
		t.Error("Allow() returned true after burst exhausted; expected false")
	}
}

// TestGUC_ShardedRL_BurstAllowance verifies the loop invariant that exactly
// burst successful calls are granted before blocking begins.
func TestGUC_ShardedRL_BurstAllowance(t *testing.T) {
	const burst = 5
	s := NewShardedRateLimiter(1, burst)
	ip := "10.0.0.2"
	allowed := 0
	for i := 0; i < burst*2; i++ {
		if s.Allow(ip) {
			allowed++
		}
	}
	if allowed != burst {
		t.Errorf("expected exactly %d allowed calls, got %d", burst, allowed)
	}
}

// TestGUC_ShardedRL_LenTracksIPs verifies that Len() correctly counts the
// number of distinct IPs registered across all 256 shards.
func TestGUC_ShardedRL_LenTracksIPs(t *testing.T) {
	s := NewShardedRateLimiter(100, 10)
	ips := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "192.168.1.1", "172.16.0.1"}
	for _, ip := range ips {
		s.Allow(ip)
	}
	if got := s.Len(); got != len(ips) {
		t.Errorf("Len() = %d, want %d", got, len(ips))
	}
}

// TestGUC_ShardedRL_ShardIndexFnv32a verifies the algorithmic correctness of
// shardIndex: it must equal FNV-32a(ip) mod nRLShards for each known input.
func TestGUC_ShardedRL_ShardIndexFnv32a(t *testing.T) {
	s := NewShardedRateLimiter(10, 10)
	cases := []string{
		"1.2.3.4", "192.168.0.1", "::1", "", "255.255.255.255",
	}
	for _, ip := range cases {
		h := fnv.New32a()
		h.Write([]byte(ip))
		want := int(h.Sum32()) % nRLShards
		got := s.shardIndex(ip)
		if got != want {
			t.Errorf("shardIndex(%q) = %d, want %d", ip, got, want)
		}
	}
}

// ── Turing: termination, halting behavior, decidability ──────────────────────

// TestGUC_ShardedRL_TokenRefillOverTime verifies that the limiter halts
// rejection after sufficient time elapses for new tokens to accumulate.
func TestGUC_ShardedRL_TokenRefillOverTime(t *testing.T) {
	// rps=100 → one token per 10 ms; burst=1 so we exhaust immediately.
	s := NewShardedRateLimiter(100, 1)
	ip := "10.1.1.1"
	if !s.Allow(ip) {
		t.Fatal("first Allow() should succeed (burst=1)")
	}
	if s.Allow(ip) {
		t.Fatal("second Allow() should fail immediately after burst exhausted")
	}
	time.Sleep(25 * time.Millisecond) // ≥2 tokens at 100 rps
	if !s.Allow(ip) {
		t.Error("Allow() should succeed after token replenishment (25 ms at 100 rps)")
	}
}

// TestGUC_ShardedRL_LimitZeroBlocksAll verifies that rps=0 and burst=0 produce
// a limiter that terminates every Allow() call with false — no tokens ever.
func TestGUC_ShardedRL_LimitZeroBlocksAll(t *testing.T) {
	s := NewShardedRateLimiter(0, 0)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		if s.Allow(ip) {
			t.Errorf("Allow(%q) returned true with rps=0 burst=0; expected false", ip)
		}
	}
}

// TestGUC_ShardedRL_HighLimitAllowsAll verifies that a very high rate limit
// decides true for every request without ever blocking.
func TestGUC_ShardedRL_HighLimitAllowsAll(t *testing.T) {
	const calls = 1000
	s := NewShardedRateLimiter(1_000_000, calls)
	ip := "10.2.2.2"
	for i := 0; i < calls; i++ {
		if !s.Allow(ip) {
			t.Errorf("Allow() returned false at call %d with rps=1_000_000 burst=%d",
				i+1, calls)
			return
		}
	}
}

// TestGUC_ShardedRL_ExtremeIPsTerminate verifies that boundary IP strings
// ("0.0.0.0", "255.255.255.255") are processed without panicking and are
// each tracked as a distinct entry.
func TestGUC_ShardedRL_ExtremeIPsTerminate(t *testing.T) {
	s := NewShardedRateLimiter(10, 5)
	extremes := []string{"0.0.0.0", "255.255.255.255"}
	for _, ip := range extremes {
		_ = s.Allow(ip) // must not panic
	}
	if got := s.Len(); got != len(extremes) {
		t.Errorf("Len() = %d, want %d after extreme IPs", got, len(extremes))
	}
}

// TestGUC_ShardedRL_EmptyIPTerminates verifies that an empty-string IP is
// accepted without panic, is tracked, and consumes tokens normally.
func TestGUC_ShardedRL_EmptyIPTerminates(t *testing.T) {
	s := NewShardedRateLimiter(10, 5)
	if !s.Allow("") {
		t.Error("Allow(\"\") returned false on first call with burst=5")
	}
	if s.Len() != 1 {
		t.Errorf("Len() = %d after Allow(\"\"), want 1", s.Len())
	}
}

// ── Church: functional purity, side-effect isolation ─────────────────────────

// TestGUC_ShardedRL_DifferentIPsIndependent verifies that exhausting one IP's
// bucket has no observable effect on a distinct IP's allowance.
func TestGUC_ShardedRL_DifferentIPsIndependent(t *testing.T) {
	s := NewShardedRateLimiter(1, 2)
	ip1 := "10.10.10.1"
	ip2 := "10.10.10.2"
	// exhaust ip1 burst
	s.Allow(ip1)
	s.Allow(ip1)
	if s.Allow(ip1) {
		t.Error("ip1 should be exhausted after 2 calls with burst=2")
	}
	// ip2 must still have its full burst
	if !s.Allow(ip2) {
		t.Error("ip2 Allow() returned false; bucket must be independent of ip1")
	}
	if !s.Allow(ip2) {
		t.Error("ip2 second Allow() returned false; burst=2 not yet consumed")
	}
}

// TestGUC_ShardedRL_IPv6Address verifies that IPv6 addresses are hashed,
// tracked, and limited independently from IPv4 addresses.
func TestGUC_ShardedRL_IPv6Address(t *testing.T) {
	s := NewShardedRateLimiter(10, 5)
	ipv6 := "2001:db8::1"
	ipv4 := "192.168.1.1"
	if !s.Allow(ipv6) {
		t.Error("Allow(IPv6) returned false on first call with burst=5")
	}
	if !s.Allow(ipv4) {
		t.Error("Allow(IPv4) returned false on first call with burst=5")
	}
	if s.Len() != 2 {
		t.Errorf("Len() = %d, want 2 (IPv6 and IPv4 must be distinct keys)", s.Len())
	}
}

// TestGUC_ShardedRL_AllowDoesNotPolluteCrossShard verifies that calling Allow
// on one IP produces no side effects on an unrelated IP's first-call allowance.
func TestGUC_ShardedRL_AllowDoesNotPolluteCrossShard(t *testing.T) {
	s := NewShardedRateLimiter(1, 1)
	ips := make([]string, 50)
	for i := 0; i < 50; i++ {
		ips[i] = "172.16." + itoa(i/256) + "." + itoa(i%256)
	}
	for _, ip := range ips {
		if !s.Allow(ip) {
			t.Errorf("Allow(%q) returned false on first call (burst=1); cross-shard pollution suspected", ip)
		}
	}
}

// TestGUC_ShardedRL_PerShardIsolation verifies that two IPs residing in
// different shards maintain truly independent token buckets.
func TestGUC_ShardedRL_PerShardIsolation(t *testing.T) {
	s := NewShardedRateLimiter(1, 3)
	candidates := []string{
		"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4",
		"10.0.0.5", "10.0.0.6", "10.0.0.7", "10.0.0.8",
	}
	var ipA, ipB string
outer:
	for i, a := range candidates {
		for _, b := range candidates[i+1:] {
			if s.shardIndex(a) != s.shardIndex(b) {
				ipA, ipB = a, b
				break outer
			}
		}
	}
	if ipA == "" {
		t.Skip("could not find two IPs in different shards among candidates")
	}
	// Exhaust ipA (burst=3).
	for j := 0; j < 3; j++ {
		s.Allow(ipA)
	}
	if s.Allow(ipA) {
		t.Error("ipA should be exhausted after consuming burst=3")
	}
	// ipB in a different shard must still have its full burst.
	if !s.Allow(ipB) {
		t.Errorf("ipB (%q, shard %d) Allow() returned false; shard isolation violated",
			ipB, s.shardIndex(ipB))
	}
}

// TestGUC_RateLimiter_AllowUnderLimit verifies that the LRU-backed RateLimiter
// also provides correct burst behaviour as a pure function of (rps, burst).
func TestGUC_RateLimiter_AllowUnderLimit(t *testing.T) {
	rl := NewRateLimiter(10, 5, 1000)
	for i := 0; i < 5; i++ {
		if !rl.Allow("192.0.2.1") {
			t.Errorf("RateLimiter.Allow() returned false on call %d with burst=5", i+1)
		}
	}
	if rl.Allow("192.0.2.1") {
		t.Error("RateLimiter.Allow() returned true after burst=5 exhausted")
	}
}

// ── Gödel: formal consistency, invariant preservation, contradiction checks ───

// TestGUC_ShardedRL_ConcurrentAllowRace exercises Allow() from many goroutines
// concurrently; the -race detector will flag any data race in shard locking.
func TestGUC_ShardedRL_ConcurrentAllowRace(t *testing.T) {
	s := NewShardedRateLimiter(1_000_000, 1_000_000)
	const goroutines = 50
	const callsPerGoroutine = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip := "10.0." + itoa(g/256) + "." + itoa(g%256)
			for i := 0; i < callsPerGoroutine; i++ {
				s.Allow(ip)
			}
		}()
	}
	wg.Wait()
	if s.Len() != goroutines {
		t.Errorf("Len() = %d after %d goroutines, want %d",
			s.Len(), goroutines, goroutines)
	}
}

// TestGUC_ShardedRL_LenNeverNegative asserts the invariant that Len() is
// always non-negative, both on an empty limiter and after incremental inserts.
func TestGUC_ShardedRL_LenNeverNegative(t *testing.T) {
	s := NewShardedRateLimiter(10, 10)
	if l := s.Len(); l < 0 {
		t.Errorf("Len() = %d on empty limiter; must be >= 0", l)
	}
	for i := 0; i < 10; i++ {
		s.Allow("10.0.0." + itoa(i))
		if l := s.Len(); l < 0 {
			t.Errorf("Len() = %d after %d IPs; must be >= 0", l, i+1)
		}
	}
}

// TestGUC_ShardedRL_ShardIndexBounds asserts the formal invariant that
// shardIndex always maps any string into [0, nRLShards-1].
func TestGUC_ShardedRL_ShardIndexBounds(t *testing.T) {
	s := NewShardedRateLimiter(10, 10)
	inputs := []string{
		"",
		"0.0.0.0",
		"255.255.255.255",
		"::1",
		"2001:db8::1",
		"192.168.1.1",
		"10.0.0.1",
		"a very long string that is not an IP address at all",
		"\x00\xff\xfe\xfd",
	}
	for _, ip := range inputs {
		idx := s.shardIndex(ip)
		if idx < 0 || idx >= nRLShards {
			t.Errorf("shardIndex(%q) = %d; out of [0, %d)", ip, idx, nRLShards)
		}
	}
}

// TestGUC_ShardedRL_MetricsOnRejection verifies formal consistency between
// the configured burst limit and the count of rejected Allow() calls.
func TestGUC_ShardedRL_MetricsOnRejection(t *testing.T) {
	const burst = 3
	const total = 10
	s := NewShardedRateLimiter(1, burst)
	ip := "10.99.99.99"
	rejected := 0
	for i := 0; i < total; i++ {
		if !s.Allow(ip) {
			rejected++
		}
	}
	expected := total - burst
	if rejected != expected {
		t.Errorf("rejected = %d, want %d (total=%d burst=%d)", rejected, expected, total, burst)
	}
}

// TestGUC_ShardedRL_BurstZeroBlocksAll asserts the Gödel invariant that
// burst=0 makes every Allow() return false regardless of the rps value,
// detecting any impossible state where tokens are granted from an empty burst.
func TestGUC_ShardedRL_BurstZeroBlocksAll(t *testing.T) {
	cases := []struct {
		name string
		rps  int
	}{
		{"rps0", 0},
		{"rps1", 1},
		{"rps1000", 1000},
		{"rps1000000", 1_000_000},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := NewShardedRateLimiter(tc.rps, 0)
			for i := 0; i < 10; i++ {
				if s.Allow("10.0.0.1") {
					t.Errorf("Allow() returned true with burst=0 (rps=%d call=%d)",
						tc.rps, i+1)
				}
			}
		})
	}
}
