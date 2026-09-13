package tracker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func decodeHealth(t *testing.T, rec *httptest.ResponseRecorder) HealthResponse {
	t.Helper()

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("probe body is not valid JSON: %q: %v", rec.Body.String(), err)
	}
	return resp
}

func probe(t *testing.T, handler http.HandlerFunc, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// Liveness

func TestLivenessProbeReportsAlive(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	hc := NewHealthChecker(db)
	rec := probe(t, hc.LivenessHandler, "/health")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	resp := decodeHealth(t, rec)
	if resp.Status != "alive" {
		t.Errorf("status = %q, want %q", resp.Status, "alive")
	}
	if resp.UptimeSeconds < 0 {
		t.Errorf("uptime_seconds = %f, want a non-negative value", resp.UptimeSeconds)
	}
}

func TestLivenessProbeSurvivesBrokenDatabase(t *testing.T) {
	db := createTestDB(t)
	db.Close() // A dead database must not get the process restarted.

	hc := NewHealthChecker(db)
	rec := probe(t, hc.LivenessHandler, "/health")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d — liveness must not depend on the database",
			rec.Code, http.StatusOK)
	}
	if resp := decodeHealth(t, rec); resp.Status != "alive" {
		t.Errorf("status = %q, want %q", resp.Status, "alive")
	}
}

func TestLivenessProbeWithNilDatabase(t *testing.T) {
	hc := NewHealthChecker(nil)
	rec := probe(t, hc.LivenessHandler, "/health")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// Readiness

func TestReadinessProbeUnreadyBeforeMarkReady(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	hc := NewHealthChecker(db)
	rec := probe(t, hc.ReadinessHandler, "/ready")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	resp := decodeHealth(t, rec)
	if resp.Status != "not_ready" {
		t.Errorf("status = %q, want %q", resp.Status, "not_ready")
	}
	if resp.Error == "" {
		t.Error("expected an explanation in the error field")
	}
}

func TestReadinessProbeReadyAfterMarkReady(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	hc := NewHealthChecker(db)
	hc.MarkReady()

	rec := probe(t, hc.ReadinessHandler, "/ready")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	resp := decodeHealth(t, rec)
	if resp.Status != "ready" {
		t.Errorf("status = %q, want %q", resp.Status, "ready")
	}
	if resp.Error != "" {
		t.Errorf("error = %q, want it empty on success", resp.Error)
	}
}

func TestReadinessProbeFailsWhenDatabaseIsClosed(t *testing.T) {
	db := createTestDB(t)

	hc := NewHealthChecker(db)
	hc.MarkReady()

	// Ready flag is set, but the database has gone away.
	db.Close()

	rec := probe(t, hc.ReadinessHandler, "/ready")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	resp := decodeHealth(t, rec)
	if resp.Status != "not_ready" {
		t.Errorf("status = %q, want %q", resp.Status, "not_ready")
	}
	if resp.Error == "" {
		t.Error("expected the database error to be surfaced")
	}
}

func TestReadinessProbeDrainsOnMarkNotReady(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	hc := NewHealthChecker(db)
	hc.MarkReady()

	if rec := probe(t, hc.ReadinessHandler, "/ready"); rec.Code != http.StatusOK {
		t.Fatalf("precondition failed: status = %d, want %d", rec.Code, http.StatusOK)
	}

	hc.MarkNotReady()

	rec := probe(t, hc.ReadinessHandler, "/ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status after drain = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadinessProbeSkipsPingWithNilDatabase(t *testing.T) {
	hc := NewHealthChecker(nil)
	hc.MarkReady()

	rec := probe(t, hc.ReadinessHandler, "/ready")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if resp := decodeHealth(t, rec); resp.Status != "ready" {
		t.Errorf("status = %q, want %q", resp.Status, "ready")
	}
}

// Startup

func TestStartupProbeReportsStartingBeforeLoad(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	hc := NewHealthChecker(db)
	rec := probe(t, hc.StartupHandler, "/startup")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if resp := decodeHealth(t, rec); resp.Status != "starting" {
		t.Errorf("status = %q, want %q", resp.Status, "starting")
	}
}

func TestStartupProbeReportsStartedAfterLoad(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	hc := NewHealthChecker(db)
	hc.MarkStarted()

	rec := probe(t, hc.StartupHandler, "/startup")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if resp := decodeHealth(t, rec); resp.Status != "started" {
		t.Errorf("status = %q, want %q", resp.Status, "started")
	}
}

func TestStartupProbeIsIndependentOfReadiness(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	hc := NewHealthChecker(db)
	hc.MarkStarted()

	// Started but deliberately not ready: startup passes, readiness does not.
	if rec := probe(t, hc.StartupHandler, "/startup"); rec.Code != http.StatusOK {
		t.Errorf("startup status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec := probe(t, hc.ReadinessHandler, "/ready"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readiness status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// Routing and concurrency

func TestHealthCheckerRegisterHandlers(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	hc := NewHealthChecker(db)
	hc.MarkReady()
	hc.MarkStarted()

	mux := http.NewServeMux()
	hc.RegisterHandlers(mux)

	tests := []struct {
		path       string
		wantStatus string
	}{
		{"/health", "alive"},
		{"/ready", "ready"},
		{"/startup", "started"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
			}
			if resp := decodeHealth(t, rec); resp.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", resp.Status, tt.wantStatus)
			}
		})
	}
}

func TestHealthProbesAreConcurrencySafe(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	hc := NewHealthChecker(db)
	hc.MarkReady()
	hc.MarkStarted()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			rec := httptest.NewRecorder()
			hc.LivenessHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

			// Flip readiness while other goroutines are probing it.
			if id%2 == 0 {
				hc.MarkNotReady()
			} else {
				hc.MarkReady()
			}

			rec = httptest.NewRecorder()
			hc.ReadinessHandler(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
		}(i)
	}
	wg.Wait()
}
