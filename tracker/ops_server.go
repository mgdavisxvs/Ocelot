package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* on DefaultServeMux
	"strings"
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

	// Swarm-plane deps — attached after construction via AttachSwarmDeps.
	nodes     *NodeRegistry
	replicas  *NodeReplicaMap
	artifacts *ArtifactList
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

// AttachSwarmDeps wires in the swarm-plane registries so /metrics can report
// node, replica, and artifact gauges. Call before ListenAndServe.
func (os *OpsServer) AttachSwarmDeps(nodes *NodeRegistry, replicas *NodeReplicaMap, artifacts *ArtifactList) {
	os.nodes = nodes
	os.replicas = replicas
	os.artifacts = artifacts
}

func NewOpsServer(config *Config, worker *Worker) (*OpsServer, *readyFlag) {
	rf := newReadyFlag()
	os := &OpsServer{worker: worker, config: config, ready: rf}

	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", os.handleLive)
	mux.HandleFunc("/health/ready", os.handleReady)
	mux.HandleFunc("/metrics", os.handleMetrics)
	mux.HandleFunc("/metrics/prometheus", os.handleMetricsPrometheus)

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

// swarmGauges collects swarm-plane counters from the optional node/replica/artifact deps.
func (os *OpsServer) swarmGauges() (nodeTotal, nodeReachable, nodeFlapping, nodeUnreachable, replicaSeeding, replicaVerified, artifactCount int) {
	if os.nodes != nil {
		os.nodes.ForEach(func(n *NodeIdentity) bool {
			nodeTotal++
			switch n.GetReachState() {
			case NodeReachable:
				nodeReachable++
			case NodeFlapping:
				nodeFlapping++
			case NodeUnreachable:
				nodeUnreachable++
			}
			return true
		})
	}
	if os.replicas != nil && os.artifacts != nil {
		os.artifacts.ForEach(func(infoHash string, _ *Artifact) bool {
			artifactCount++
			os.replicas.ForHash(infoHash, func(r *NodeReplica) bool {
				switch r.GetState() {
				case ReplicaStateSeeding:
					replicaSeeding++
				case ReplicaStateVerified:
					replicaVerified++
				}
				return true
			})
			return true
		})
	}
	return
}

func (os *OpsServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	stats := os.worker.Stats
	uptime := time.Since(stats.StartTime).Seconds()

	nodeTotal, nodeReachable, nodeFlapping, nodeUnreachable, replicaSeeding, replicaVerified, artifactCount := os.swarmGauges()

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
		"swarm_nodes_total":        nodeTotal,
		"swarm_nodes_reachable":    nodeReachable,
		"swarm_nodes_flapping":     nodeFlapping,
		"swarm_nodes_unreachable":  nodeUnreachable,
		"swarm_replicas_seeding":   replicaSeeding,
		"swarm_replicas_verified":  replicaVerified,
		"swarm_artifacts_total":    artifactCount,
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

// handleMetricsPrometheus exposes all metrics in Prometheus text exposition format.
// GET /metrics/prometheus
func (os *OpsServer) handleMetricsPrometheus(w http.ResponseWriter, r *http.Request) {
	stats := os.worker.Stats
	uptime := time.Since(stats.StartTime).Seconds()
	nodeTotal, nodeReachable, nodeFlapping, nodeUnreachable, replicaSeeding, replicaVerified, artifactCount := os.swarmGauges()

	var sb strings.Builder

	writeLine := func(name, help, typ string, value interface{}) {
		fmt.Fprintf(&sb, "# HELP %s %s\n# TYPE %s %s\n%s %v\n", name, help, name, typ, name, value)
	}

	writeLine("ocelot_uptime_seconds", "Tracker process uptime in seconds", "gauge", uptime)
	writeLine("ocelot_open_connections", "Currently open TCP connections", "gauge", stats.OpenConnections.Load())
	writeLine("ocelot_opened_connections_total", "Total TCP connections accepted since start", "counter", stats.OpenedConnections.Load())
	writeLine("ocelot_announcements_total", "Total announce requests received", "counter", stats.Announcements.Load())
	writeLine("ocelot_succ_announcements_total", "Total successful announce responses", "counter", stats.SuccAnnouncements.Load())
	writeLine("ocelot_scrapes_total", "Total scrape requests received", "counter", stats.Scrapes.Load())
	writeLine("ocelot_leechers", "Current leecher count across all torrents", "gauge", stats.Leechers.Load())
	writeLine("ocelot_seeders", "Current seeder count across all torrents", "gauge", stats.Seeders.Load())
	writeLine("ocelot_torrent_count", "Number of registered torrents", "gauge", os.worker.Torrents.Size())
	writeLine("ocelot_user_count", "Number of registered users", "gauge", os.worker.Users.Size())
	writeLine("ocelot_bytes_read_total", "Total bytes read from clients", "counter", stats.BytesRead.Load())
	writeLine("ocelot_bytes_written_total", "Total bytes written to clients", "counter", stats.BytesWritten.Load())
	writeLine("ocelot_requests_total", "Total HTTP requests handled", "counter", stats.Requests.Load())
	writeLine("ocelot_swarm_nodes_total", "Total managed nodes registered", "gauge", nodeTotal)
	writeLine("ocelot_swarm_nodes_reachable", "Managed nodes in REACHABLE state", "gauge", nodeReachable)
	writeLine("ocelot_swarm_nodes_flapping", "Managed nodes in FLAPPING state", "gauge", nodeFlapping)
	writeLine("ocelot_swarm_nodes_unreachable", "Managed nodes in UNREACHABLE state", "gauge", nodeUnreachable)
	writeLine("ocelot_swarm_replicas_seeding", "Managed replicas in SEEDING state", "gauge", replicaSeeding)
	writeLine("ocelot_swarm_replicas_verified", "Managed replicas in VERIFIED state", "gauge", replicaVerified)
	writeLine("ocelot_swarm_artifacts_total", "Number of registered artifacts", "gauge", artifactCount)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(sb.String()))
}
