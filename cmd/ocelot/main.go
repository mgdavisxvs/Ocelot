package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mgdavisxvs/Ocelot/tracker"
)

func main() {
	fmt.Println("Ocelot BitTorrent Tracker (Go Edition)")

	// ── Configuration ─────────────────────────────────────────────────────────
	configPath := tracker.ParseFlags()

	fc, err := tracker.ParseConfigFile(configPath)
	if err != nil {
		log.Fatalf("Failed to load config %q: %v", configPath, err)
	}
	config := fc.ToTrackerConfig()
	log.Printf("Config loaded from %s", configPath)

	// ── Database ──────────────────────────────────────────────────────────────
	db, err := tracker.NewSQLiteShardManager(fc.DBDir)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()
	log.Printf("SQLite database ready in %s", fc.DBDir)

	// ── Audit log ─────────────────────────────────────────────────────────────
	if err := tracker.CreateAuditLogTable(db.CurrentDB()); err != nil {
		log.Printf("Warning: could not create audit log table: %v", err)
	}
	auditLog := tracker.NewAuditLogger(db.CurrentDB())

	// ── Batch writer ──────────────────────────────────────────────────────────
	batchWriter := tracker.NewBatchWriter(db.CurrentDB(), 100, 5*time.Second)
	defer batchWriter.Stop()

	// ── Rate limiter ──────────────────────────────────────────────────────────
	rateLimiter := tracker.NewRateLimiter(10, 30, 100_000)

	// ── Circuit breaker ───────────────────────────────────────────────────────
	circuitBreaker := tracker.NewCircuitBreaker(tracker.CircuitBreakerConfig{
		Name:         "db",
		MaxFailures:  5,
		ResetTimeout: 30 * time.Second,
		HalfOpenMax:  3,
	})

	// ── Metrics ───────────────────────────────────────────────────────────────
	metrics := tracker.GetMetricsRecorder()
	if config.MetricsPort != "" {
		go func() {
			log.Printf("Prometheus metrics on %s", config.MetricsPort)
			if err := tracker.StartMetricsServer(config.MetricsPort); err != nil {
				log.Printf("Metrics server error: %v", err)
			}
		}()
	}

	// ── In-memory state ───────────────────────────────────────────────────────
	torrents := tracker.NewTorrentList()
	users := tracker.NewUserList()
	whitelist := tracker.NewWhitelist()
	stats := &tracker.Stats{StartTime: time.Now()}

	// ── Load initial state ────────────────────────────────────────────────────
	loader := tracker.NewLoader(db, torrents, users, whitelist)
	if err := loader.LoadAll(); err != nil {
		log.Printf("Warning: initial state load failed: %v", err)
		log.Println("Starting with empty state; add torrents and users via admin API")
	} else {
		log.Printf("State loaded: %d torrents, %d users", torrents.Size(), users.Size())
	}

	// ── Site communication ────────────────────────────────────────────────────
	var siteComm tracker.SiteCommInterface
	if config.GazelleURL != "" {
		siteComm = tracker.NewGazelleSiteComm(config.GazelleURL, config.SitePassword)
		log.Printf("Gazelle callbacks enabled: %s", config.GazelleURL)
	} else {
		siteComm = &tracker.NoOpSiteComm{}
		log.Println("No gazelle_url configured; token expiry callbacks disabled")
	}

	// ── Worker ────────────────────────────────────────────────────────────────
	worker := &tracker.Worker{
		Config:       config,
		DB:           db,
		SiteComm:     siteComm,
		Torrents:     torrents,
		Users:        users,
		Whitelist:    whitelist,
		Stats:        stats,
		RateLimiter:  rateLimiter,
		CircuitBreak: circuitBreaker,
		AuditLog:     auditLog,
		Metrics:      metrics,
	}

	// ── Background subsystems ─────────────────────────────────────────────────
	reaper := tracker.NewReaper(torrents, config.ScheduleInterval, config.PeersTimeout)
	reaper.Start()
	defer reaper.Stop()

	scheduler := tracker.NewScheduler(db, config.ScheduleInterval)
	scheduler.Start()
	defer scheduler.Stop()

	// ── Signal handlers ───────────────────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGUSR1)

	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGHUP:
				newFC, err := tracker.ParseConfigFile(configPath)
				if err != nil {
					log.Printf("SIGHUP: failed to reload config: %v", err)
					continue
				}
				newCfg := newFC.ToTrackerConfig()
				config.SitePassword = newCfg.SitePassword
				config.ReportPassword = newCfg.ReportPassword
				config.NumWantLimit = newCfg.NumWantLimit
				config.AnnounceInterval = newCfg.AnnounceInterval
				config.PeersTimeout = newCfg.PeersTimeout
				log.Println("SIGHUP: configuration reloaded")

			case syscall.SIGUSR1:
				log.Println("SIGUSR1: reloading torrent/user/whitelist state...")
				if err := loader.Reload(); err != nil {
					log.Printf("SIGUSR1: reload failed: %v", err)
				} else {
					log.Printf("SIGUSR1: reload complete — %d torrents, %d users",
						torrents.Size(), users.Size())
				}
			}
		}
	}()

	// ── Server ────────────────────────────────────────────────────────────────
	server := tracker.NewServer(config, worker)

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Listening on %s", config.ListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	<-shutdownCh
	log.Println("Shutdown signal received — draining connections...")
	if err := server.Shutdown(); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Shutdown complete")
}
