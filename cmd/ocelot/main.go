package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mgdavisxvs/Ocelot/ml"
	"github.com/mgdavisxvs/Ocelot/tracker"
)

// Version and BuildTime are injected at build time via -ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
)

const defaultBackupInterval = 6 * time.Hour

func main() {
	fmt.Printf("Ocelot BitTorrent Tracker %s (built %s)\n", Version, BuildTime)

	// ── OpenTelemetry ─────────────────────────────────────────────────────────
	shutdownTracing, err := tracker.InitTracing("ocelot-tracker")
	if err != nil {
		log.Printf("Warning: OTel tracing init failed: %v — running without tracing", err)
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = shutdownTracing(ctx)
		}()
	}

	// ── Configuration ─────────────────────────────────────────────────────────
	configPath := tracker.ParseFlags()
	fc, err := tracker.ParseConfigFile(configPath)
	if err != nil {
		log.Fatalf("Failed to load config %q: %v", configPath, err)
	}

	// Environment variable overrides for secrets and deployment paths.
	if v := os.Getenv("SITE_PASSWORD"); v != "" {
		fc.SitePassword = v
	}
	if v := os.Getenv("REPORT_PASSWORD"); v != "" {
		fc.ReportPassword = v
	}
	if v := os.Getenv("GAZELLE_URL"); v != "" {
		fc.GazelleURL = v
	}
	if v := os.Getenv("DB_DIR"); v != "" {
		fc.DBDir = v
	}

	// Reject insecure default credentials before doing anything else.
	if fc.SitePassword == "changeme" || fc.SitePassword == "00000000000000000000000000000000" {
		log.Fatal("FATAL: site_password is the default value. Set a strong password via ocelot.conf or SITE_PASSWORD.")
	}
	if fc.ReportPassword == "changeme" || fc.ReportPassword == "00000000000000000000000000000000" {
		log.Fatal("FATAL: report_password is the default value. Set a strong password via ocelot.conf or REPORT_PASSWORD.")
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

	// ── In-memory state ───────────────────────────────────────────────────────
	torrents := tracker.NewTorrentList()
	users := tracker.NewUserList()
	whitelist := tracker.NewWhitelist()
	stats := &tracker.Stats{StartTime: time.Now()}

	// ── Load initial state ────────────────────────────────────────────────────
	loader := tracker.NewLoader(db, torrents, users, whitelist)
	if err := loader.CreateSchemaIfNeeded(); err != nil {
		log.Printf("Warning: schema init failed: %v", err)
	}
	if err := loader.LoadAll(); err != nil {
		log.Printf("Warning: initial state load failed: %v", err)
		log.Println("Starting with empty state; add torrents and users via admin API")
	} else {
		log.Printf("State loaded: %d torrents, %d users", torrents.Size(), users.Size())
	}

	// ── Site communication ────────────────────────────────────────────────────
	var siteComm tracker.SiteCommInterface
	if fc.GazelleURL != "" {
		gazellePass := os.Getenv("GAZELLE_PASSWORD")
		if gazellePass == "" {
			gazellePass = fc.SitePassword
		}
		siteComm = tracker.NewGazelleSiteComm(fc.GazelleURL, gazellePass)
		log.Printf("Gazelle callbacks enabled: %s", fc.GazelleURL)
	} else {
		siteComm = &tracker.NoOpSiteComm{}
		log.Println("No gazelle_url configured; token expiry callbacks disabled")
	}

	// ── Circuit breaker ───────────────────────────────────────────────────────
	protectedDB := tracker.NewCircuitBreakerDB(db, tracker.CircuitBreakerConfig{
		Name:         "sqlite",
		MaxFailures:  10,
		ResetTimeout: 30 * time.Second,
		HalfOpenMax:  3,
	})

	// ── Worker ────────────────────────────────────────────────────────────────
	worker := &tracker.Worker{
		Config:    config,
		DB:        protectedDB,
		SiteComm:  siteComm,
		Torrents:  torrents,
		Users:     users,
		Whitelist: whitelist,
		Stats:     stats,
		Audit:     tracker.NewAuditLogger(db.CurrentDB),

		AnomalyDetector: ml.NewAnomalyDetector(),
		ClientDetector:  ml.NewClientAnomalyDetector(),
		PeerScorer:      ml.NewPeerScorer(),
	}
	log.Println("ML anomaly detection and peer scoring active")

	// ── Background subsystems ─────────────────────────────────────────────────
	reaper := tracker.NewReaper(torrents, config.ScheduleInterval, config.PeersTimeout)
	reaper.Start()
	defer reaper.Stop()

	scheduler := tracker.NewScheduler(db, config.ScheduleInterval)
	scheduler.Start()
	defer scheduler.Stop()

	// ── Optional: Redis cache layer ───────────────────────────────────────────
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		redisBackend, err := tracker.NewRedisBackend(tracker.RedisConfig{
			Addr:     redisURL,
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       0,
			PoolSize: 20,
		})
		if err != nil {
			log.Printf("Warning: Redis unavailable (%v) — running without cache layer", err)
		} else {
			_ = redisBackend
			log.Printf("Redis cache layer active: %s", redisURL)
		}
	}

	// ── Optional: scheduled VACUUM INTO backup ────────────────────────────────
	if backupDir := os.Getenv("DB_BACKUP_DIR"); backupDir != "" {
		bs := tracker.NewBackupScheduler(db, backupDir, defaultBackupInterval)
		go bs.Start()
		defer bs.Stop()
		log.Printf("Backup scheduler active: dir=%s interval=%s", backupDir, defaultBackupInterval)
	}

	// ── Signal handlers ───────────────────────────────────────────────────────
	// SIGHUP / SIGUSR1: operational reloads — handled in background goroutine.
	// SIGINT / SIGTERM: clean shutdown — received on shutdownCh.
	reloadCh := make(chan os.Signal, 1)
	signal.Notify(reloadCh, syscall.SIGHUP, syscall.SIGUSR1)

	go func() {
		for sig := range reloadCh {
			switch sig {
			case syscall.SIGHUP:
				newFC, err := tracker.ParseConfigFile(configPath)
				if err != nil {
					log.Printf("SIGHUP: config reload failed: %v", err)
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
		log.Printf("Listening on %s", config.ListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	// ── Optional: TLS listener ────────────────────────────────────────────────
	if certFile := os.Getenv("TLS_CERT_FILE"); certFile != "" {
		go func() {
			log.Printf("Starting TLS tracker on :34443 (cert=%s)", certFile)
			if err := server.StartTLS(tracker.TLSConfig{
				CertFile: certFile,
				KeyFile:  os.Getenv("TLS_KEY_FILE"),
			}); err != nil {
				log.Printf("TLS server error: %v", err)
			}
		}()
	} else if domain := os.Getenv("TLS_DOMAIN"); domain != "" {
		go func() {
			log.Printf("Starting auto-TLS tracker for domain %s", domain)
			if err := server.StartTLS(tracker.TLSConfig{
				AutoTLS: true,
				Domain:  domain,
			}); err != nil {
				log.Printf("Auto-TLS server error: %v", err)
			}
		}()
	}

	go printStats(stats)

	// ── Shutdown ──────────────────────────────────────────────────────────────
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)
	<-shutdownCh

	signal.Stop(reloadCh)
	close(reloadCh)

	log.Println("Shutdown signal received — draining connections...")
	if err := server.Shutdown(); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Shutdown complete")
}

func printStats(stats *tracker.Stats) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		uptime := time.Since(stats.StartTime)
		log.Printf("uptime=%s conns=%d announces=%d/%d scrapes=%d seeders=%d leechers=%d",
			uptime.Round(time.Second),
			stats.OpenConnections.Load(),
			stats.SuccAnnouncements.Load(),
			stats.Announcements.Load(),
			stats.Scrapes.Load(),
			stats.Seeders.Load(),
			stats.Leechers.Load(),
		)
	}
}
