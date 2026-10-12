// ocelot-cp is the Ocelot compute-plane control server.
// It exposes the agent API (AGENT_PROTOCOL.md), runs the node-loss monitor,
// and runs the workload scheduler.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mgdavisxvs/Ocelot/compute/api"
	"github.com/mgdavisxvs/Ocelot/compute/schema"
)

func main() {
	listen := flag.String("listen", ":8080", "TCP address to listen on")
	dbPath := flag.String("db", "./compute.db", "Path to compute.db (SQLite)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM)")
	tlsKey := flag.String("tls-key", "", "TLS private key file (PEM)")
	heartbeatSec := flag.Int("heartbeat-interval", 30, "Expected heartbeat interval in seconds")
	scheduleSec  := flag.Int("schedule-interval", 5, "Scheduler tick interval in seconds")
	timeoutSec   := flag.Int("timeout-interval", 30, "Workload deadline check interval in seconds")
	flag.Parse()

	bootstrapToken := os.Getenv("OCELOT_CP_BOOTSTRAP_TOKEN")
	if bootstrapToken == "" {
		slog.Error("OCELOT_CP_BOOTSTRAP_TOKEN must be set")
		os.Exit(1)
	}
	jwtSecret := os.Getenv("OCELOT_CP_JWT_SECRET")
	if jwtSecret == "" {
		slog.Error("OCELOT_CP_JWT_SECRET must be set")
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	db, err := schema.Open(*dbPath)
	if err != nil {
		slog.Error("open database", "path", *dbPath, "err", err)
		os.Exit(1)
	}
	defer db.Close()

	heartbeatInterval := time.Duration(*heartbeatSec) * time.Second
	scheduleInterval  := time.Duration(*scheduleSec) * time.Second
	timeoutInterval   := time.Duration(*timeoutSec) * time.Second

	mux := http.NewServeMux()

	handler := api.New(db, bootstrapToken)
	handler.Mount(mux)

	auth := api.NewAuthHandler(db, bootstrapToken, []byte(jwtSecret))
	auth.Mount(mux)

	api.NewWorkloadAPI(db, auth).Mount(mux)
	api.NewNodeAPI(db, auth).Mount(mux)
	api.NewArtifactAPI(db, auth).Mount(mux)
	api.NewEventAPI(db, auth).Mount(mux)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			http.Error(w, "db unreachable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:         *listen,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if *tlsCert != "" || *tlsKey != "" {
		if *tlsCert == "" || *tlsKey == "" {
			slog.Error("both --tls-cert and --tls-key must be provided together")
			os.Exit(1)
		}
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	monitor        := api.NewNodeLossMonitor(db, heartbeatInterval)
	scheduler      := api.NewScheduler(db, scheduleInterval)
	timeoutMonitor := api.NewWorkloadTimeoutMonitor(db, timeoutInterval)

	go monitor.Run(ctx)
	go scheduler.Run(ctx)
	go timeoutMonitor.Run(ctx)

	errCh := make(chan error, 1)
	go func() {
		if *tlsCert != "" {
			slog.Info("ocelot-cp listening (TLS)", "addr", *listen)
			errCh <- srv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			slog.Warn("ocelot-cp listening (plaintext — use TLS in production)", "addr", *listen)
			errCh <- srv.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("ocelot-cp shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("shutdown error", "err", err)
			os.Exit(1)
		}
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}
}
