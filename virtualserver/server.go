package virtualserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
)

// Config holds all parameters needed to construct a VirtualServer.
type Config struct {
	// DBPath is the file path for the SQLite database (WAL mode).
	DBPath string
	// ListenAddr is the TCP address the API server listens on (e.g. ":8080").
	ListenAddr string
	// BearerToken is required on all /v1/ API requests.
	// Empty string disables bearer authentication.
	BearerToken string
	// ReconcileInterval controls how often the state-machine loop runs.
	// Defaults to 5 s when zero.
	ReconcileInterval time.Duration
	// PrometheusRegisterer is used to register metrics. Defaults to
	// prometheus.DefaultRegisterer when nil.
	PrometheusRegisterer prometheus.Registerer
}

// VirtualServer coordinates the control loop, API server, and plugin registries.
type VirtualServer struct {
	cfg        Config
	db         *db
	adapters   *AdapterRegistry
	storage    *storage.Registry
	catalog    CatalogProvider
	metrics    *VSMetrics
	reconciler *Reconciler
	api        *APIServer
	httpServer *http.Server
	log        *slog.Logger
}

// New constructs a VirtualServer from cfg without starting it.
// Returns an error if the database cannot be opened or migrated.
func New(cfg Config, log *slog.Logger) (*VirtualServer, error) {
	if log == nil {
		log = slog.Default()
	}
	if cfg.DBPath == "" {
		return nil, fmt.Errorf("virtualserver: DBPath required")
	}

	d, err := openDB(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("virtualserver: open db: %w", err)
	}

	reg := cfg.PrometheusRegisterer
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	metrics := NewVSMetrics(reg)

	adapters := NewAdapterRegistry()
	stor := storage.NewRegistry()

	vs := &VirtualServer{
		cfg:      cfg,
		db:       d,
		adapters: adapters,
		storage:  stor,
		metrics:  metrics,
		log:      log,
	}
	return vs, nil
}

// RegisterAdapter adds a backend adapter to the registry.
// Must be called before Start.
func (vs *VirtualServer) RegisterAdapter(a BackendAdapter) {
	vs.adapters.Register(a)
}

// RegisterStorageDriver adds a storage driver to the registry.
// Must be called before Start.
func (vs *VirtualServer) RegisterStorageDriver(d storage.StorageDriver) {
	vs.storage.Register(d)
}

// SetCatalog attaches a CatalogProvider used to gate provisioning on artifact
// swarm availability. Optional; when nil, gating is skipped.
func (vs *VirtualServer) SetCatalog(c CatalogProvider) {
	vs.catalog = c
}

// Adapters returns the BackendAdapter registry (for external adapter registration).
func (vs *VirtualServer) Adapters() *AdapterRegistry { return vs.adapters }

// Storage returns the StorageDriver registry.
func (vs *VirtualServer) Storage() *storage.Registry { return vs.storage }

// Metrics returns the Prometheus instrumentation bundle.
func (vs *VirtualServer) Metrics() *VSMetrics { return vs.metrics }

// Start launches the reconciler loop and HTTP API server.
// It returns when ctx is cancelled; callers should call Stop after this returns.
func (vs *VirtualServer) Start(ctx context.Context) error {
	// Ensure a default adapter exists so the reconciler can function.
	if _, err := vs.adapters.Default(); err != nil {
		vs.log.Warn("virtualserver: no backend adapter registered; running in dry-run mode")
		vs.adapters.Register(NoOpAdapter{})
	}

	vs.reconciler = newReconciler(
		vs.db, vs.adapters, vs.storage, vs.catalog,
		vs.metrics, vs.cfg.ReconcileInterval, vs.log,
	)
	vs.api = newAPIServer(vs.db, vs.adapters, vs.storage, vs.metrics, vs.cfg.BearerToken)

	if vs.cfg.ListenAddr != "" {
		vs.httpServer = &http.Server{
			Addr:         vs.cfg.ListenAddr,
			Handler:      vs.api,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
		}
		go func() {
			vs.log.Info("virtualserver: API listening", "addr", vs.cfg.ListenAddr)
			if err := vs.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				vs.log.Error("virtualserver: HTTP server error", "err", err)
			}
		}()
	}

	// Run the reconciler; this blocks until ctx is cancelled.
	vs.reconciler.Run(ctx)
	return nil
}

// Stop gracefully shuts down the HTTP server and closes the database.
func (vs *VirtualServer) Stop(ctx context.Context) error {
	if vs.httpServer != nil {
		if err := vs.httpServer.Shutdown(ctx); err != nil {
			vs.log.Warn("virtualserver: HTTP shutdown error", "err", err)
		}
	}
	return vs.db.close()
}
