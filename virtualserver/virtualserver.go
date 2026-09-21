package virtualserver

import (
	"context"
	"fmt"
	"log"

	"github.com/mgdavisxvs/Ocelot/virtualserver/adapter"
	vsapi "github.com/mgdavisxvs/Ocelot/virtualserver/api"
	"github.com/mgdavisxvs/Ocelot/virtualserver/catalog"
	"github.com/mgdavisxvs/Ocelot/virtualserver/config"
	"github.com/mgdavisxvs/Ocelot/virtualserver/reconciler"
	"github.com/mgdavisxvs/Ocelot/virtualserver/scheduler"
	"github.com/mgdavisxvs/Ocelot/virtualserver/store"
)

// DBProvider is the narrow interface the VS subsystem needs from the tracker.
type DBProvider interface {
	catalog.DBProvider
}

// VirtualServer is the top-level coordinator for the VS subsystem.
// It owns the SQLite store, reconciler, HTTP API, and adapter registry.
type VirtualServer struct {
	cfg        config.VSConfig
	store      *store.VSStore
	reconciler *reconciler.VSReconciler
	server     *vsapi.Server
	adapters   map[string]adapter.BackendAdapter
}

// New opens the VS SQLite database, runs migrations, and wires all components.
// tracker is used for artifact catalog lookups (read-only).
// Returns (nil, nil) when cfg.Enabled is false.
// Returns an error when cfg.AdminKey is empty, preventing an insecure startup.
func New(cfg config.VSConfig, tracker DBProvider) (*VirtualServer, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.AdminKey == "" {
		return nil, fmt.Errorf("virtualserver: AdminKey must not be empty")
	}

	s, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("vs store open: %w", err)
	}

	cat := catalog.New(tracker)
	sched := scheduler.New(scheduler.DefaultWeights())

	// Built-in adapters — extend by calling RegisterAdapter before Start.
	adapters := map[string]adapter.BackendAdapter{}

	rec := reconciler.New(reconciler.Config{
		Store:    s,
		Adapters: adapters,
		Sched:    sched,
		Catalog:  cat,
		Interval: cfg.ReconcileEvery,
	})

	srv := vsapi.NewServer(cfg, s)

	return &VirtualServer{
		cfg:        cfg,
		store:      s,
		reconciler: rec,
		server:     srv,
		adapters:   adapters,
	}, nil
}

// RegisterAdapter registers a BackendAdapter under its Name(). Must be called before Start.
func (vs *VirtualServer) RegisterAdapter(a adapter.BackendAdapter) {
	vs.adapters[a.Name()] = a
}

// Start launches the HTTP server and the reconciler loop.
func (vs *VirtualServer) Start() error {
	errCh := vs.server.Start()
	vs.reconciler.Start()
	log.Printf("VirtualServer started on %s", vs.cfg.Port)
	go func() {
		if err := <-errCh; err != nil {
			log.Printf("VirtualServer HTTP error: %v", err)
		}
	}()
	return nil
}

// Stop gracefully shuts down the reconciler and HTTP server.
func (vs *VirtualServer) Stop(ctx context.Context) error {
	var ferr error
	if err := vs.reconciler.Stop(ctx); err != nil {
		ferr = fmt.Errorf("reconciler stop: %w", err)
	}
	if err := vs.server.Stop(ctx); err != nil && ferr == nil {
		ferr = fmt.Errorf("server stop: %w", err)
	}
	return ferr
}

// Store exposes the underlying VSStore for integration tests.
func (vs *VirtualServer) Store() *store.VSStore {
	return vs.store
}
