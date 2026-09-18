package main

import (
	"context"
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
	if err := tracker.CreateAPIKeysTable(db.CurrentDB()); err != nil {
		log.Printf("Warning: could not create api_keys table: %v", err)
	}
	auditLog := tracker.NewAuditLogger(db.CurrentDB())

	// ── Batch writer ──────────────────────────────────────────────────────────
	// BatchWriterDB routes hot-path announce writes through the async queue
	// (QueuePeerAnnounce / QueueTorrentUpdate) and delegates everything else to
	// the underlying SQLiteShardManager.
	batchWriter := tracker.NewBatchWriter(db.CurrentDB(), 100, 5*time.Second)
	defer batchWriter.Stop()
	batchWriterDB := tracker.NewBatchWriterDB(db, batchWriter)

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

	// ── Anomaly detection + adaptive threshold poller [ML-01] ────────────────
	markovCtx, markovCancel := context.WithCancel(context.Background())
	anomalyDetector, rawDetector := tracker.NewAnomalyDetectorPair()

	if config.MarkovAPIURL != "" {
		markovClient := tracker.NewMarkovClient(config.MarkovAPIURL)
		log.Printf("Markov API client configured: %s", config.MarkovAPIURL)
		tracker.AdaptiveThresholdPoller(markovCtx, markovClient, rawDetector,
			config.FreeleechPollSec)
		log.Println("adaptive threshold poller started")
	}

	// ── Worker ────────────────────────────────────────────────────────────────
	worker := &tracker.Worker{
		Config:         config,
		DB:             batchWriterDB, // hot-path writes routed through BatchWriterDB
		SiteComm:       siteComm,
		Torrents:       torrents,
		Users:          users,
		Whitelist:      whitelist,
		Stats:          stats,
		RateLimiter:    rateLimiter,
		CircuitBreak:   circuitBreaker,
		AuditLog:       auditLog,
		Metrics:        metrics,
		Detector:       anomalyDetector,
		ClientDetector: tracker.NewClientDetector(),
		SwarmPredictor: tracker.NewSwarmHealthPredictor(),
		PeerScorer:     tracker.NewPeerScorer(),
		TorrentCache:   tracker.NewTorrentCache(5 * time.Minute),
		UserCache:      tracker.NewUserCache(5 * time.Minute),
	}

	// ── Background subsystems ─────────────────────────────────────────────────
	reaper := tracker.NewReaper(torrents, stats, config.ReapPeersInterval, config.PeersTimeout)
	reaper.Start()
	defer reaper.Stop()

	scheduler := tracker.NewScheduler(db, config.ScheduleInterval, config.DelReasonLifetime)
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

	// ── Domain adapters ───────────────────────────────────────────────────────
	// Load any vocab configs from the domains/ directory (relative to the
	// config file's location) and register a ConfiguredAdapter for each.
	// The built-in BT announce/scrape fast paths remain unchanged; adapters
	// with conflicting action names are silently skipped to protect them.
	server := tracker.NewServer(config, worker)

	domainsDir := "domains"
	vocabConfigs, err := tracker.LoadAllVocabConfigs(domainsDir)
	if err != nil {
		log.Printf("Warning: failed to load domain configs from %q: %v", domainsDir, err)
	} else {
		btActions := map[string]bool{"announce": true, "scrape": true,
			"update": true, "stats": true, "torrents": true, "peers": true, "whitelist": true}
		for _, vc := range vocabConfigs {
			if btActions[vc.Actions.Event] || btActions[vc.Actions.Query] {
				continue // never override built-in BT routes
			}
			adapter := tracker.NewConfiguredAdapter(vc, whitelist, server)
			server.RegisterAdapter(adapter)
			log.Printf("Domain adapter registered: %s (event=%s, query=%s, format=%s)",
				vc.Domain, vc.Actions.Event, vc.Actions.Query, vc.WireFormat.Format)
		}
	}

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	// ── Peer snapshot — restore swarm state ──────────────────────────────────
	snapshotPath := fc.DBDir + "/swarm.snap"
	if err := tracker.LoadSnapshot(snapshotPath, torrents); err != nil {
		log.Printf("Warning: peer snapshot load failed: %v", err)
	}

	go func() {
		log.Printf("Listening on %s", config.ListenAddr)
		var serveErr error
		switch {
		case config.TLS.AutoTLS:
			serveErr = server.ListenAndServeAutoTLS(config.TLS.Domain, config.TLS.CacheDir)
		case config.TLS.CertFile != "":
			serveErr = server.ListenAndServeTLS(config.TLS.CertFile, config.TLS.KeyFile)
		default:
			serveErr = server.ListenAndServe()
		}
		if serveErr != nil {
			log.Printf("Server error: %v", serveErr)
		}
	}()

	<-shutdownCh
	log.Println("Shutdown signal received — draining connections...")
	markovCancel()
	if err := server.Shutdown(); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	if err := tracker.SaveSnapshot(snapshotPath, torrents); err != nil {
		log.Printf("Warning: peer snapshot save failed: %v", err)
	}

	log.Println("Shutdown complete")
}
