package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	if err := tracker.CreateAPIKeysTable(rawDB.CurrentDB()); err != nil {
		log.Printf("warning: could not ensure api_keys table: %v", err)
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

	// ── Peer snapshot [AI-05] — restore swarm state from previous run ─────────
	snapshotPath := filepath.Join(fileCfg.DBDir, "swarm.snap")
	if err := tracker.LoadSnapshot(snapshotPath, torrents); err != nil {
		log.Printf("warning: could not load peer snapshot: %v", err)
	} else {
		log.Printf("peer snapshot loaded from %s", snapshotPath)
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

	// ── Markov engine integration [HI-01/HI-06/ML-01] ────────────────────────
	// The Markov engine runs as a separate process (ocelot-markov).
	// When markov_api_url is configured the tracker communicates with it via
	// HTTP for freeleech candidate polling (HI-06) and adaptive anomaly
	// threshold adjustment (ML-01).
	markovCtx, markovCancel := context.WithCancel(context.Background())

	var markovClient *tracker.MarkovClient
	anomalyDetector, rawDetector := tracker.NewAnomalyDetectorPair()

	if config.MarkovAPIURL != "" {
		markovClient = tracker.NewMarkovClient(config.MarkovAPIURL)
		log.Printf("Markov API client configured: %s", config.MarkovAPIURL)

		// [HI-06] freeleech candidate → NotifyFreeleech pipeline
		if config.FreeleechPollSec > 0 {
			tracker.FreeleechPoller(markovCtx, markovClient, siteComm,
				config.FreeleechPollSec, config.FreeleechNotifyHours)
			log.Printf("freeleech poller started (interval=%ds notify_hours=%d)",
				config.FreeleechPollSec, config.FreeleechNotifyHours)
		}

		// [ML-01] adaptive anomaly thresholds driven by Markov population stats
		tracker.AdaptiveThresholdPoller(markovCtx, markovClient, rawDetector,
			config.FreeleechPollSec)
		log.Println("adaptive threshold poller started")
	}

	// ── Worker ────────────────────────────────────────────────────────────────
	worker := &tracker.Worker{
		Config:         config,
		DB:             db, // [A+D] buffered + circuit-breaker protected
		SiteComm:       siteComm,
		Torrents:       torrents,
		Users:          users,
		Whitelist:      whitelist,
		Stats:          stats,
		RateLimiter:    rateLimiter,
		CircuitBreak:   breaker,
		AuditLog:       auditLog,
		Metrics:        metrics,
		Detector:       anomalyDetector,                                            // [F+ML-01] behaviour anomaly (adaptive)
		ClientDetector: tracker.NewClientDetector(),                                // [ML-04] client anomaly
		SwarmPredictor: tracker.NewSwarmHealthPredictor(),                          // [ML-02] swarm health scoring
		PeerScorer:     tracker.NewPeerScorer(),                                    // [ML-03] ML peer scoring
		TorrentCache:   tracker.NewTorrentCache(5 * time.Minute),                  // L1 hot-torrent cache
		UserCache:      tracker.NewUserCache(5 * time.Minute),                     // L1 passkey→user cache
	}

	// [B] Reaper + [E] RateLimiter initialised inside Worker.Start().
	worker.Start()
	log.Printf("reaper started (interval=%ds timeout=%ds)",
		config.ReapPeersInterval, config.PeersTimeout)
	if config.RateLimitRPS > 0 {
		log.Printf("rate limiter active (%d RPS, burst %d)", config.RateLimitRPS, config.RateLimitBurst)
	}

	// [C] Scheduler — WAL checkpoint + shard rotation.
	sched := tracker.NewScheduler(rawDB, config.ScheduleInterval, config.DelReasonLifetime)
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
					// Evict stale L1 cache entries so the fresh DB state is served.
					if worker.TorrentCache != nil {
						worker.TorrentCache.Clear()
					}
					if worker.UserCache != nil {
						worker.UserCache.Clear()
					}
					log.Printf("SIGUSR1: reload complete — %d torrents, %d users",
						torrents.Size(), users.Size())
				}
			}
		}
	}()

	// ── Domain adapters ───────────────────────────────────────────────────────
	// Load vocab JSON files from a domains/ subdirectory (relative to CWD).
	// Adapters with action names that collide with the BT fast-paths are skipped
	// to prevent accidentally shadowing announce/scrape.
	server := tracker.NewServer(config, worker)
	server.StartAdminAPIServer(rawDB.CurrentDB())

	btBuiltins := map[string]bool{
		"announce": true, "scrape": true, "update": true,
		"stats": true, "torrents": true, "peers": true,
		"whitelist": true, "report": true,
	}
	if vocabConfigs, err := tracker.LoadAllVocabConfigs("domains"); err != nil {
		log.Printf("domain adapters: %v (skip)", err)
	} else {
		for _, vc := range vocabConfigs {
			if btBuiltins[vc.Actions.Event] || btBuiltins[vc.Actions.Query] {
				continue
			}
			server.RegisterAdapter(tracker.NewConfiguredAdapter(vc, whitelist, server))
			log.Printf("domain adapter: %s (event=%s query=%s fmt=%s)",
				vc.Domain, vc.Actions.Event, vc.Actions.Query, vc.WireFormat.Format)
		}
	}

	go func() {
		log.Printf("tracker listening on %s", config.ListenAddr)
		var serveErr error
		switch {
		case config.TLS.AutoTLS:
			log.Printf("TLS: auto via Let's Encrypt (domain=%s cache=%s)", config.TLS.Domain, config.TLS.CacheDir)
			serveErr = server.ListenAndServeAutoTLS(config.TLS.Domain, config.TLS.CacheDir)
		case config.TLS.CertFile != "":
			log.Printf("TLS: manual cert %s / key %s", config.TLS.CertFile, config.TLS.KeyFile)
			// Start an HTTP-to-HTTPS redirect on :80 alongside the TLS listener.
			go func() {
				log.Println("TLS: HTTP→HTTPS redirect on :80")
				redirectSrv := &http.Server{
					Addr:         ":80",
					Handler:      tracker.RedirectHTTPToHTTPS(),
					ReadTimeout:  5 * time.Second,
					WriteTimeout: 5 * time.Second,
				}
				if err := redirectSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("TLS redirect server: %v", err)
				}
			}()
			serveErr = server.ListenAndServeTLS(config.TLS.CertFile, config.TLS.KeyFile)
		default:
			serveErr = server.ListenAndServe()
		}
		if serveErr != nil {
			log.Printf("server: %v", serveErr)
		}
	}()

	go printStats(stats, worker)

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	log.Println("shutdown signal received — draining connections...")

	markovCancel() // stop freeleech poller and adaptive threshold goroutines

	if err := server.Shutdown(); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	sched.Stop()
	worker.Stop() // stops reaper + flushes buffered DB writes

	// [AI-05] persist swarm state for fast restart
	if err := tracker.SaveSnapshot(snapshotPath, torrents); err != nil {
		log.Printf("warning: could not save peer snapshot: %v", err)
	} else {
		log.Printf("peer snapshot saved to %s", snapshotPath)
	}

	log.Println("shutdown complete")
}

// printStats logs tracker statistics every 30 seconds.
func printStats(stats *tracker.Stats, worker *tracker.Worker) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		uptime := time.Since(stats.StartTime).Round(time.Second)
		qDepth := 0
		switch db := worker.DB.(type) {
		case *tracker.BufferedDB:
			qDepth = db.QueueDepth()
		case *tracker.BatchWriterDB:
			qDepth = db.QueueDepth()
		}
		swarmHealth := worker.SwarmHealthSummary()
		healthStr := "n/a"
		if swarmHealth >= 0 {
			healthStr = fmt.Sprintf("%d", swarmHealth)
		}
		log.Printf("uptime=%s announces=%d scrapes=%d seeders=%d leechers=%d "+
			"evicted=%d anomalies=%d db_queue=%d swarm_health=%s",
			uptime,
			stats.Announcements.Load(),
			stats.Scrapes.Load(),
			stats.Seeders.Load(),
			stats.Leechers.Load(),
			stats.EvictedPeers.Load(),
			stats.AnomalyDetections.Load(),
			qDepth,
			healthStr,
		)
	}
}
