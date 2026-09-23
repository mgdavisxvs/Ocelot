package tracker

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllow(t *testing.T) {
	limiter := NewRateLimiter(10, 20, 1000) // 10 req/sec, burst 20

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
	limiter := NewRateLimiter(10, 10, 1000) // 10 req/sec, burst 10

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
	limiter := NewRateLimiter(10, 10, 1000)

	// Different IPs should have independent limits
	if !limiter.Allow("192.0.2.1") {
		t.Error("IP1 first request should be allowed")
	}

	if !limiter.Allow("192.0.2.2") {
		t.Error("IP2 first request should be allowed")
	}
}

// ── Len ───────────────────────────────────────────────────────────────────────

func TestRateLimiter_Len(t *testing.T) {
	limiter := NewRateLimiter(10, 10, 1000)
	if limiter.Len() != 0 {
		t.Errorf("Len = %d, want 0 initially", limiter.Len())
	}
	limiter.Allow("10.0.0.1")
	limiter.Allow("10.0.0.2")
	if limiter.Len() != 2 {
		t.Errorf("Len = %d, want 2 after two IPs", limiter.Len())
	}
}

// ── GetClientIP ───────────────────────────────────────────────────────────────

func TestGetClientIP_XForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := GetClientIP(r); ip != "1.2.3.4" {
		t.Errorf("GetClientIP = %q, want \"1.2.3.4\"", ip)
	}
}

func TestGetClientIP_XRealIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Real-IP", "5.6.7.8")
	if ip := GetClientIP(r); ip != "5.6.7.8" {
		t.Errorf("GetClientIP = %q, want \"5.6.7.8\"", ip)
	}
}

func TestGetClientIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "9.10.11.12:1234"
	if ip := GetClientIP(r); ip != "9.10.11.12:1234" {
		t.Errorf("GetClientIP = %q, want \"9.10.11.12:1234\"", ip)
	}
}

// ── RateLimitMiddleware ───────────────────────────────────────────────────────

func TestRateLimitMiddleware_AllowsRequest(t *testing.T) {
	limiter := NewRateLimiter(100, 100, 1000)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimitMiddleware(limiter)(handler)

	req := httptest.NewRequest(http.MethodGet, "/announce", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.1")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
}

func TestRateLimitMiddleware_BlocksExceededRate(t *testing.T) {
	// Burst of 1 so the second request is rate-limited.
	limiter := NewRateLimiter(1, 1, 1000)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := RateLimitMiddleware(limiter)(handler)

	ip := "203.0.113.99"

	// First request consumes the burst.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("X-Forwarded-For", ip)
	rec1 := httptest.NewRecorder()
	mw.ServeHTTP(rec1, req1)

	// Second request should be rate-limited.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Forwarded-For", ip)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests, got %d", rec2.Code)
	}
}
