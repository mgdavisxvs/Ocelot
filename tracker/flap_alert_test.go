package tracker

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ── OPP-D: Flapping Alert Webhook tests ──────────────────────────────────────

func TestFlapAlertFiredOnFirstMiss(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := NewNodeRegistry()
	n := NewNodeIdentity(99, "alert.test", "pk32charslongpadpadpadpadpadpaaa", FailureDomainLabels{})
	n.mu.Lock()
	n.LastSeen = time.Now().Add(-10 * time.Minute)
	n.mu.Unlock()
	reg.Register(n)

	c := &SwarmPolicyController{}
	c.SetAlertWebhookURL(srv.URL)

	reg.MarkStale(2*time.Minute, c.sendFlapAlert)
	time.Sleep(80 * time.Millisecond) // goroutine must complete

	if atomic.LoadInt32(&called) < 1 {
		t.Error("alert webhook should be called on REACHABLE → FLAPPING transition")
	}
	if n.GetReachState() != NodeFlapping {
		t.Errorf("node should be FLAPPING, got %s", n.GetReachState())
	}
}

func TestFlapAlertNotFiredOnSubsequentMiss(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := NewNodeRegistry()
	n := NewNodeIdentity(99, "alert2.test", "pk32charslongpadpadpadpadpadpaaa", FailureDomainLabels{})
	n.mu.Lock()
	n.LastSeen = time.Now().Add(-10 * time.Minute)
	n.ReachState = NodeFlapping // already flapping; this miss should not fire alert
	n.FlapCount = 1
	n.mu.Unlock()
	reg.Register(n)

	c := &SwarmPolicyController{}
	c.SetAlertWebhookURL(srv.URL)

	reg.MarkStale(2*time.Minute, c.sendFlapAlert)
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&called) != 0 {
		t.Error("alert webhook must NOT fire on FLAPPING → FLAPPING/UNREACHABLE transition")
	}
}

func TestFlapAlertNotFiredWhenURLEmpty(t *testing.T) {
	reg := NewNodeRegistry()
	n := NewNodeIdentity(99, "noalert.test", "pk32charslongpadpadpadpadpadpaaa", FailureDomainLabels{})
	n.mu.Lock()
	n.LastSeen = time.Now().Add(-10 * time.Minute)
	n.mu.Unlock()
	reg.Register(n)

	c := &SwarmPolicyController{} // alertWebhookURL is "" — sendFlapAlert is a no-op

	// Must not panic.
	reg.MarkStale(2*time.Minute, c.sendFlapAlert)
	time.Sleep(20 * time.Millisecond)
}

func TestFlapAlertRetryOn5xx(t *testing.T) {
	origDelay := flapAlertBaseDelay
	flapAlertBaseDelay = 0
	defer func() { flapAlertBaseDelay = origDelay }()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &SwarmPolicyController{}
	c.SetAlertWebhookURL(srv.URL)
	c.sendFlapAlert(1, "test.node", 1)
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("want 3 attempts (2×5xx + 1 success), got %d", atomic.LoadInt32(&attempts))
	}
}

func TestFlapAlertNoRetryOn4xx(t *testing.T) {
	origDelay := flapAlertBaseDelay
	flapAlertBaseDelay = 0
	defer func() { flapAlertBaseDelay = origDelay }()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := &SwarmPolicyController{}
	c.SetAlertWebhookURL(srv.URL)
	c.sendFlapAlert(1, "test.node", 1)
	time.Sleep(80 * time.Millisecond)

	if atomic.LoadInt32(&attempts) != 1 {
		t.Errorf("4xx should not be retried, want 1 attempt, got %d", atomic.LoadInt32(&attempts))
	}
}
