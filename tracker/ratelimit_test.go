package tracker

import (
	"testing"
	"time"
)

func TestRateLimiterAllow(t *testing.T) {
	limiter := NewRateLimiter(10, 20, 10_000) // 10 req/sec, burst 20

	ip := "192.0.2.1"

	// Should allow first requests (within burst)
	for i := 0; i < 20; i++ {
		if !limiter.Allow(ip) {
			t.Errorf("Request %d should be allowed (within burst)", i)
		}
	}

	// Next request should be denied (burst exhausted)
	if limiter.Allow(ip) {
		t.Error("Request should be denied (burst exhausted)")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	limiter := NewRateLimiter(10, 10, 10_000) // 10 req/sec, burst 10

	ip := "192.0.2.2"

	// Exhaust burst
	for i := 0; i < 10; i++ {
		limiter.Allow(ip)
	}

	// Wait for refill (100ms = 1 token at 10/sec)
	time.Sleep(200 * time.Millisecond)

	// Should allow 2 more requests
	if !limiter.Allow(ip) {
		t.Error("Request should be allowed after refill")
	}
}

func TestRateLimiterMultipleIPs(t *testing.T) {
	limiter := NewRateLimiter(10, 10, 10_000)

	// Different IPs should have independent limits
	if !limiter.Allow("192.0.2.1") {
		t.Error("IP1 first request should be allowed")
	}

	if !limiter.Allow("192.0.2.2") {
		t.Error("IP2 first request should be allowed")
	}
}
