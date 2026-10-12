package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mgdavisxvs/ocelot/markov/internal/api"
	"github.com/mgdavisxvs/ocelot/markov/internal/config"
	"github.com/mgdavisxvs/ocelot/markov/internal/db"
	"github.com/mgdavisxvs/ocelot/markov/internal/engine"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfgPath := flag.String("config", "ocelot-markov.conf", "path to JSON config file")
	logLevel := flag.String("log-level", "info", "log level: debug|info|warn|error")
	flag.Parse()

	setupLogger(*logLevel)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("load config", "path", *cfgPath, "err", err)
		os.Exit(1)
	}
	// Environment variable overrides for Redis (secrets must not live in config file).
	if v := os.Getenv("REDIS_URL"); v != "" {
		cfg.RedisURL = v
	}
	if v := os.Getenv("REDIS_PASSWORD"); v != "" {
		cfg.RedisPassword = v
	}
	slog.Info("config loaded",
		"poll_interval_sec", cfg.PollIntervalSec,
		"persist_interval_sec", cfg.PersistIntervalSec,
		"listen_addr", cfg.ListenAddr)

	database, err := db.Open(cfg)
	if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	eng, err := engine.New(cfg, database)
	if err != nil {
		slog.Error("init engine", "err", err)
		os.Exit(1)
	}

	// Wire Redis EventPublisher if configured — publishes anomaly/freeleech/interval events
	// to the tracker's in-process EventBus via Redis Pub/Sub.
	if cfg.RedisURL != "" {
		opts, parseErr := redis.ParseURL(cfg.RedisURL)
		if parseErr != nil {
			// Treat as bare host:port.
			opts = &redis.Options{Addr: cfg.RedisURL}
		}
		if cfg.RedisPassword != "" {
			opts.Password = cfg.RedisPassword
		}
		rdb := redis.NewClient(opts)
		pub := engine.NewEventPublisher(rdb)
		eng.WireEventPublisher(pub, cfg.AnnounceIntervalSec)
		slog.Info("EventPublisher wired", "redis_url", cfg.RedisURL)
	}

	apiServer := api.New(cfg.ListenAddr, eng)

	ctx, cancel := context.WithCancel(context.Background())

	// Engine loop in background.
	go eng.Run(ctx)

	// API server in background.
	apiErrCh := make(chan error, 1)
	go func() {
		apiErrCh <- apiServer.ListenAndServe()
	}()

	// Block on OS signal or API error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	select {
	case sig := <-sigCh:
		slog.Info("signal received, shutting down", "signal", sig)
	case err := <-apiErrCh:
		slog.Error("api server terminated", "err", err)
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("api shutdown", "err", err)
	}
}

func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}
