package tracker

import (
	"testing"
	"time"
)

func TestRateLimiterAllow(t *testing.T) {
	limiter := NewRateLimiter(10, 20) // 10 req/sec, burst 20

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
	limiter := NewRateLimiter(10, 10) // 10 req/sec, burst 10

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
	limiter := NewRateLimiter(10, 10)

	// Different IPs should have independent limits
	if !limiter.Allow("192.0.2.1") {
		t.Error("IP1 first request should be allowed")
	}

	if !limiter.Allow("192.0.2.2") {
		t.Error("IP2 first request should be allowed")
	}
}

// Stress Tests

func TestRateLimiterSustainedLoad(t *testing.T) {
	limiter := NewRateLimiter(100, 10) // 100 req/sec, burst 10

	ip := "192.0.2.100"

	// Exhaust initial burst
	for i := 0; i < 10; i++ {
		if !limiter.Allow(ip) {
			t.Errorf("Burst request %d should be allowed", i)
		}
	}

	// Try sustained load at rate limit
	// Should allow ~10 requests per 100ms
	duration := 500 * time.Millisecond
	start := time.Now()
	allowed := 0
	denied := 0

	for time.Since(start) < duration {
		if limiter.Allow(ip) {
			allowed++
		} else {
			denied++
		}
		time.Sleep(10 * time.Millisecond) // 100 req/sec = 1 req per 10ms
	}

	// Should allow approximately 50 requests in 500ms at 100/sec
	if allowed < 40 || allowed > 60 {
		t.Errorf("Expected ~50 allowed requests, got %d (denied: %d)", allowed, denied)
	}
}

func TestRateLimiterBurstHandling(t *testing.T) {
	limiter := NewRateLimiter(10, 20) // 10 req/sec, burst 20

	ip := "192.0.2.101"

	// Large burst should be capped at burst size
	allowed := 0
	for i := 0; i < 100; i++ {
		if limiter.Allow(ip) {
			allowed++
		}
	}

	if allowed != 20 {
		t.Errorf("Expected exactly 20 requests allowed in burst, got %d", allowed)
	}

	// Wait for some refill
	time.Sleep(1 * time.Second) // 1 second at 10/sec = 10 tokens

	// Should allow approximately 10 more requests
	newlyAllowed := 0
	for i := 0; i < 20; i++ {
		if limiter.Allow(ip) {
			newlyAllowed++
		}
	}

	if newlyAllowed < 8 || newlyAllowed > 12 {
		t.Errorf("Expected ~10 requests after refill, got %d", newlyAllowed)
	}
}

func TestRateLimiterConcurrentIPs(t *testing.T) {
	limiter := NewRateLimiter(50, 10) // 50 req/sec, burst 10

	// Simulate 100 different IPs making requests concurrently
	done := make(chan bool)
	errors := make(chan string, 100)

	for i := 0; i < 100; i++ {
		go func(id int) {
			ip := string(rune(192)) + "." + string(rune(0)) + "." + string(rune(id/256)) + "." + string(rune(id%256))

			// Each IP should be able to make burst requests
			allowed := 0
			for j := 0; j < 10; j++ {
				if limiter.Allow(ip) {
					allowed++
				}
			}

			if allowed != 10 {
				errors <- string(rune(id))
			}

			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}

	close(errors)
	errorCount := len(errors)

	if errorCount > 0 {
		t.Errorf("%d IPs had incorrect burst allowance", errorCount)
	}
}

func TestRateLimiterTokenBucketAccuracy(t *testing.T) {
	limiter := NewRateLimiter(10, 5) // 10 req/sec, burst 5

	ip := "192.0.2.102"

	// Consume initial burst
	for i := 0; i < 5; i++ {
		if !limiter.Allow(ip) {
			t.Errorf("Initial burst request %d failed", i)
		}
	}

	// Verify bucket is empty
	if limiter.Allow(ip) {
		t.Error("Request should be denied immediately after burst")
	}

	// Wait exactly 100ms (should refill 1 token at 10/sec)
	time.Sleep(100 * time.Millisecond)

	// Should allow exactly 1 request
	if !limiter.Allow(ip) {
		t.Error("Should allow 1 request after 100ms refill")
	}

	// Next request should fail
	if limiter.Allow(ip) {
		t.Error("Should deny request immediately after single refill used")
	}

	// Wait 500ms (should refill 5 tokens at 10/sec)
	time.Sleep(500 * time.Millisecond)

	// Should allow exactly 5 requests (capped at burst size)
	allowed := 0
	for i := 0; i < 10; i++ {
		if limiter.Allow(ip) {
			allowed++
		}
	}

	if allowed != 5 {
		t.Errorf("Expected exactly 5 allowed after 500ms refill, got %d", allowed)
	}
}
