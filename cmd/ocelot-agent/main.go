package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mgdavisxvs/Ocelot/compute/agent"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := agent.ParseFlags()
	if err != nil {
		slog.Error("configuration error", "err", err)
		os.Exit(1)
	}

	a, err := agent.New(cfg)
	if err != nil {
		slog.Error("agent initialisation failed", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("signal received, shutting down", "signal", sig)
		cancel()
	}()

	a.Run(ctx)
	slog.Info("agent stopped")
}
