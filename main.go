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

	// ── Configuration ────────────────────────────────────────────────────────
	cfgPath := tracker.ParseFlags()
	fileCfg, err := tracker.ParseConfigFile(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	config := fileCfg.ToTrackerConfig()
	log.Printf("config loaded from %s", cfgPath)

	// ── Database ─────────────────────────────────────────────────────────────
	rawDB, err := tracker.NewSQLiteShardManager(fileCfg.DBDir)
	if err != nil {
		log.Fatalf("database init: %v", err)
	}
	defer rawDB.Close()
	log.Printf("SQLite database initialised in %s", fileCfg.DBDir)

	// ── Audit log ─────────────────────────────────────────────────────────────
	if err := tracker.CreateAuditLogTable(rawDB.CurrentDB()); err != nil {
		log.Printf("warning: could not ensure audit_log table: %v", err)
	}
	auditLog := tracker.NewAuditLogger(rawDB.CurrentDB())

	// ── Circuit breaker [D] ───────────────────────────────────────────────────
	breaker := tracker.NewCircuitBreaker(tracker.CircuitBreakerConfig{
		Name:         "sqlite",
		MaxFailures:  5,
		ResetTimeout: 30 * time.Second,
		HalfOpenMax:  3,
	})

	// ── BufferedDB [A] ────────────────────────────────────────────────────────
	db := tracker.NewBufferedDB(rawDB, breaker, config.BatchBufferCap, 200*time.Millisecond)
	log.Printf("BufferedDB initialised (queue cap %d)", config.BatchBufferCap)

	// ── Metrics ───────────────────────────────────────────────────────────────
	metrics := tracker.GetMetricsRecorder()
	if config.MetricsPort != "" {
		go func() {
			log.Printf("Prometheus metrics on %s", config.MetricsPort)
			if err := tracker.StartMetricsServer(config.MetricsPort); err != nil {
				log.Printf("metrics server error: %v", err)
			}
		}()
	}

	// ── In-memory state ───────────────────────────────────────────────────────
	torrents := tracker.NewTorrentList()
	users := tracker.NewUserList()
	whitelist := tracker.NewWhitelist()
	stats := &tracker.Stats{StartTime: time.Now()}

	// ── Rate limiter [E] ──────────────────────────────────────────────────────
	rateLimiter := tracker.NewRateLimiter(
		config.RateLimitRPS,
		config.RateLimitBurst,
		100_000,
	)

	// ── Loader (reads use rawDB directly — startup never hits the buffer) ─────
	loader := tracker.NewLoader(rawDB, torrents, users, whitelist)
	if err := loader.LoadAll(); err != nil {
		log.Printf("warning: failed to load initial state: %v", err)
		log.Println("starting with empty state — add torrents and users via admin panel")
	} else {
		log.Printf("initial state loaded: %d torrents, %d users",
			torrents.Size(), users.Size())
	}

	// ── Site communication ────────────────────────────────────────────────────
	var siteComm tracker.SiteCommInterface
	if fileCfg.GazelleURL != "" {
		siteComm = tracker.NewGazelleSiteComm(fileCfg.GazelleURL, fileCfg.SitePassword)
		log.Printf("Gazelle callbacks enabled: %s", fileCfg.GazelleURL)
	} else {
		siteComm = &tracker.NoOpSiteComm{}
		log.Println("no gazelle_url configured; token expiry callbacks disabled")
	}

	// ── Worker ────────────────────────────────────────────────────────────────
	worker := &tracker.Worker{
		Config:       config,
		DB:           db, // [A+D] buffered + circuit-breaker protected
		SiteComm:     siteComm,
		Torrents:     torrents,
		Users:        users,
		Whitelist:    whitelist,
		Stats:        stats,
		RateLimiter:  rateLimiter,
		CircuitBreak: breaker,
		AuditLog:     auditLog,
		Metrics:      metrics,
		Detector:     tracker.NewAnomalyDetector(), // [F]
	}

	// [B] Reaper + [E] RateLimiter initialised inside Worker.Start().
	worker.Start()
	log.Printf("reaper started (interval=%ds timeout=%ds)",
		config.ReapPeersInterval, config.PeersTimeout)
	if config.RateLimitRPS > 0 {
		log.Printf("rate limiter active (%d RPS, burst %d)", config.RateLimitRPS, config.RateLimitBurst)
	}

	// [C] Scheduler — WAL checkpoint + shard rotation.
	sched := tracker.NewScheduler(rawDB, config.ScheduleInterval)
	sched.Start()
	log.Printf("scheduler started (interval=%ds)", config.ScheduleInterval)

	// ── Signal handlers ───────────────────────────────────────────────────────
	sigAdmin := make(chan os.Signal, 1)
	signal.Notify(sigAdmin, syscall.SIGHUP, syscall.SIGUSR1)
	go func() {
		for sig := range sigAdmin {
			switch sig {
			case syscall.SIGHUP:
				newFC, err := tracker.ParseConfigFile(cfgPath)
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

	go func() {
		log.Printf("tracker listening on %s", config.ListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("server: %v", err)
		}
	}()

	go printStats(stats, worker)

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	log.Println("shutdown signal received — draining connections...")

	if err := server.Shutdown(); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	sched.Stop()
	worker.Stop() // stops reaper + flushes buffered DB writes
	log.Println("shutdown complete")
}

// printStats logs tracker statistics every 30 seconds.
func printStats(stats *tracker.Stats, worker *tracker.Worker) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		uptime := time.Since(stats.StartTime).Round(time.Second)
		qDepth := 0
		if bdb, ok := worker.DB.(*tracker.BufferedDB); ok {
			qDepth = bdb.QueueDepth()
		}
		log.Printf("uptime=%s announces=%d scrapes=%d seeders=%d leechers=%d "+
			"evicted=%d anomalies=%d db_queue=%d",
			uptime,
			stats.Announcements.Load(),
			stats.Scrapes.Load(),
			stats.Seeders.Load(),
			stats.Leechers.Load(),
			stats.EvictedPeers.Load(),
			stats.AnomalyDetections.Load(),
			qDepth,
		)
	}
}
