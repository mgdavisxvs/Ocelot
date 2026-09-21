package api

import (
	"context"
	"net/http"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/config"
)

// Server wraps the VS HTTP API listener.
type Server struct {
	http     *http.Server
	cfg      config.VSConfig
	handlers *Handlers
}

// NewServer creates a Server with the full VS API mounted.
// The server does not start listening until Start is called.
func NewServer(cfg config.VSConfig, store HandlerStore) *Server {
	h := NewHandlers(store)
	mux := buildMux(h)

	authMw := authMiddleware(cfg.AdminKey)
	maxBodyMw := maxBodyMiddleware(cfg.MaxBodyBytes)
	handler := chain(mux, requestIDMiddleware, metricsMiddleware, authMw, maxBodyMw)

	srv := &http.Server{
		Addr:         cfg.Port,
		Handler:      handler,
		ReadTimeout:  cfg.RequestTimeout,
		WriteTimeout: cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return &Server{http: srv, cfg: cfg, handlers: h}
}

// Start begins listening in a background goroutine.
// It returns immediately; errors from ListenAndServe (other than ErrServerClosed)
// are sent to the returned error channel.
func (s *Server) Start() <-chan error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	return errCh
}

// Stop initiates a graceful shutdown with the provided context deadline.
func (s *Server) Stop(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// buildMux constructs the VS route table.
// All routes are under /v1/.
func buildMux(h *Handlers) *http.ServeMux {
	mux := http.NewServeMux()

	// Namespaces
	mux.HandleFunc("/v1/namespaces", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.ListNamespaces(w, r)
		case http.MethodPost:
			h.CreateNamespace(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Nodes — collection
	mux.HandleFunc("/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.ListNodes(w, r)
		case http.MethodPost:
			h.RegisterNode(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Nodes — singleton + sub-resources
	mux.HandleFunc("/v1/nodes/", func(w http.ResponseWriter, r *http.Request) {
		if hasSuffix(r.URL.Path, "/state") {
			if r.Method != http.MethodPut {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			h.UpdateNodeState(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.GetNode(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Services — collection
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.ListServices(w, r)
		case http.MethodPost:
			h.DeclareService(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Services — singleton + sub-resources
	mux.HandleFunc("/v1/services/", func(w http.ResponseWriter, r *http.Request) {
		if hasSuffix(r.URL.Path, "/instances") {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			h.ListServiceInstances(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.GetService(w, r)
		case http.MethodDelete:
			h.DeleteService(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Instances
	mux.HandleFunc("/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.ListInstances(w, r)
	})

	mux.HandleFunc("/v1/instances/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.GetInstance(w, r)
	})

	// Healthcheck
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

func hasSuffix(path, suffix string) bool {
	n := len(path)
	s := len(suffix)
	return n >= s && path[n-s:] == suffix
}
