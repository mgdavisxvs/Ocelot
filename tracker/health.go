package tracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// readinessPingTimeout bounds the database check so a wedged SQLite file
// fails the probe instead of hanging it.
const readinessPingTimeout = 2 * time.Second

// HealthChecker serves liveness, readiness, and startup probes.
type HealthChecker struct {
	db        *sql.DB
	startedAt time.Time
	ready     atomic.Bool
	started   atomic.Bool
}

// NewHealthChecker creates a health checker for the given database. A nil db
// skips the readiness ping, which suits in-memory-only deployments.
func NewHealthChecker(db *sql.DB) *HealthChecker {
	return &HealthChecker{
		db:        db,
		startedAt: time.Now(),
	}
}

// MarkStarted reports that the initial data load has finished.
func (h *HealthChecker) MarkStarted() {
	h.started.Store(true)
}

// MarkReady allows the tracker to receive traffic.
func (h *HealthChecker) MarkReady() {
	h.ready.Store(true)
}

// MarkNotReady removes the tracker from rotation without killing the process,
// which is how a graceful drain begins.
func (h *HealthChecker) MarkNotReady() {
	h.ready.Store(false)
}

// HealthResponse is the JSON body returned by every probe.
type HealthResponse struct {
	Status        string  `json:"status"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	Error         string  `json:"error,omitempty"`
}

func (h *HealthChecker) write(w http.ResponseWriter, code int, resp HealthResponse) {
	resp.UptimeSeconds = time.Since(h.startedAt).Seconds()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}

// LivenessHandler reports whether the process is running. It deliberately does
// not touch the database: a slow query should shed traffic via readiness, not
// get the process restarted.
func (h *HealthChecker) LivenessHandler(w http.ResponseWriter, r *http.Request) {
	h.write(w, http.StatusOK, HealthResponse{Status: "alive"})
}

// ReadinessHandler reports whether the tracker can serve requests, which
// requires both an explicit ready signal and a reachable database.
func (h *HealthChecker) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() {
		h.write(w, http.StatusServiceUnavailable, HealthResponse{
			Status: "not_ready",
			Error:  "tracker is not accepting traffic",
		})
		return
	}

	if h.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), readinessPingTimeout)
		defer cancel()

		if err := h.db.PingContext(ctx); err != nil {
			h.write(w, http.StatusServiceUnavailable, HealthResponse{
				Status: "not_ready",
				Error:  err.Error(),
			})
			return
		}
	}

	h.write(w, http.StatusOK, HealthResponse{Status: "ready"})
}

// StartupHandler reports whether the initial load has finished, so that a slow
// start does not trip the liveness probe.
func (h *HealthChecker) StartupHandler(w http.ResponseWriter, r *http.Request) {
	if !h.started.Load() {
		h.write(w, http.StatusServiceUnavailable, HealthResponse{Status: "starting"})
		return
	}

	h.write(w, http.StatusOK, HealthResponse{Status: "started"})
}

// RegisterHandlers wires the probe endpoints onto mux.
func (h *HealthChecker) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.LivenessHandler)
	mux.HandleFunc("/ready", h.ReadinessHandler)
	mux.HandleFunc("/startup", h.StartupHandler)
}
