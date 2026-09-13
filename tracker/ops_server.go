package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* on DefaultServeMux
	"time"
)

// OpsServer serves the operational plane on :34002.
//
// Endpoints:
//
//	GET /health/live   — liveness probe; always 200 if the process is running
//	GET /health/ready  — readiness probe; 200 once initial state is loaded
//	GET /metrics       — compact JSON dump of runtime stats
//	GET /debug/pprof/* — standard Go profiling endpoints (via DefaultServeMux)
type OpsServer struct {
	worker  *Worker
	config  *Config
	httpSrv *http.Server
	ready   *readyFlag
}

// readyFlag is set by calling SetReady() once initial state is loaded.
type readyFlag struct {
	ch chan struct{}
}

func newReadyFlag() *readyFlag { return &readyFlag{ch: make(chan struct{})} }

// SetReady marks the server as ready to serve traffic.
// Safe to call multiple times; subsequent calls are no-ops.
func (rf *readyFlag) SetReady() {
	select {
	case <-rf.ch:
	default:
		close(rf.ch)
	}
}

func (rf *readyFlag) IsReady() bool {
	select {
	case <-rf.ch:
		return true
	default:
		return false
	}
}

func NewOpsServer(config *Config, worker *Worker) (*OpsServer, *readyFlag) {
	rf := newReadyFlag()
	os := &OpsServer{worker: worker, config: config, ready: rf}

	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", os.handleLive)
	mux.HandleFunc("/health/ready", os.handleReady)
	mux.HandleFunc("/metrics", os.handleMetrics)

	// Delegate all /debug/pprof/* to DefaultServeMux, which net/http/pprof
	// populates at init time.
	mux.Handle("/debug/pprof/", http.DefaultServeMux)

	os.httpSrv = &http.Server{
		Addr:         config.OpsAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return os, rf
}

func (os *OpsServer) ListenAndServe() error {
	fmt.Printf("Ops plane listening on %s\n", os.config.OpsAddr)
	return os.httpSrv.ListenAndServe()
}

func (os *OpsServer) Shutdown(ctx context.Context) error {
	return os.httpSrv.Shutdown(ctx)
}

func (os *OpsServer) handleLive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"alive"}`))
}

func (os *OpsServer) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !os.ready.IsReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"starting"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ready"}`))
}

func (os *OpsServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	stats := os.worker.Stats
	uptime := time.Since(stats.StartTime).Seconds()

	m := map[string]interface{}{
		"uptime_seconds":      uptime,
		"open_connections":    stats.OpenConnections.Load(),
		"opened_connections":  stats.OpenedConnections.Load(),
		"announcements":       stats.Announcements.Load(),
		"succ_announcements":  stats.SuccAnnouncements.Load(),
		"scrapes":             stats.Scrapes.Load(),
		"leechers":            stats.Leechers.Load(),
		"seeders":             stats.Seeders.Load(),
		"torrent_count":       os.worker.Torrents.Size(),
		"user_count":          os.worker.Users.Size(),
		"bytes_read":          stats.BytesRead.Load(),
		"bytes_written":       stats.BytesWritten.Load(),
		"requests":            stats.Requests.Load(),
	}

	b, err := json.Marshal(m)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
