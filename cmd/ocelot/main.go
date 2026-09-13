package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mgdavisxvs/Ocelot/tracker"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	// ── Circuit Breaker (R-09) ────────────────────────────────────────────────
	cb := tracker.NewCircuitBreaker(tracker.CircuitBreakerConfig{
		Name:         "sqlite",
		MaxFailures:  5,
		ResetTimeout: 30 * time.Second,
		HalfOpenMax:  3,
	})

	// ── Batch Writer (R-08) ───────────────────────────────────────────────────
	bw := tracker.NewBatchWriter(db, 500, 2*time.Second)
	defer bw.Stop()

	// ── Rate Limiter (R-21) ───────────────────────────────────────────────────
	rl := tracker.NewRateLimiter(30, 60) // 30 req/s per IP, burst 60

	// ── Audit Logger (R-12) ───────────────────────────────────────────────────
	auditLogger, auditErr := tracker.NewAuditLoggerWithInit(db)
	if auditErr != nil {
		log.Printf("Warning: audit logger init failed: %v — audit disabled", auditErr)
		auditLogger = nil
	}

	// ── Event Bus ─────────────────────────────────────────────────────────────
	bus := tracker.NewEventBus()

	// ── Worker ────────────────────────────────────────────────────────────────
	worker := &tracker.Worker{
		Config:      config,
		DB:          db,
		BatchWriter: bw,
		CB:          cb,
		SiteComm:    siteComm,
		Torrents:    torrents,
		Users:       users,
		Whitelist:   whitelist,
		Stats:       stats,
		Audit:       auditLogger,
		Bus:         bus,
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
				config.ScheduleInterval = newCfg.ScheduleInterval
				reaper.SetTimeout(time.Duration(newCfg.PeersTimeout) * time.Second)
				scheduler.SetInterval(time.Duration(newCfg.ScheduleInterval) * time.Second)
				log.Printf("SIGHUP: reloaded — peers_timeout=%ds schedule_interval=%ds",
					newCfg.PeersTimeout, newCfg.ScheduleInterval)

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

	// ── Prometheus /metrics (R-22) ────────────────────────────────────────────
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "ok")
		})
		mux.Handle("/events", tracker.SSEHandler(bus, config.SitePassword))
		metricsAddr := fc.MetricsAddr
		if metricsAddr == "" {
			metricsAddr = ":6880"
		}
		log.Printf("Metrics/health endpoint on %s", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, mux); err != nil {
			log.Printf("Metrics server error: %v", err)
		}
	}()

	// ── UDP Tracker (BEP-15) ─────────────────────────────────────────────────
	if fc.UDPListenPort > 0 {
		udpAddr := fmt.Sprintf(":%d", fc.UDPListenPort)
		udpServer, err := tracker.NewUDPServer(udpAddr, worker)
		if err != nil {
			log.Fatalf("Failed to start UDP tracker on %s: %v", udpAddr, err)
		}
		go udpServer.Serve()
		defer udpServer.Stop()
		log.Printf("UDP tracker (BEP-15) listening on %s", udpAddr)
	}

	// ── Server ────────────────────────────────────────────────────────────────
	server := tracker.NewServer(config, worker)
	server.SetRateLimiter(rl)

	// ── TLS (R-23) ────────────────────────────────────────────────────────────
	tlsCfg := tracker.TLSConfig{
		CertFile: fc.TLSCertFile,
		KeyFile:  fc.TLSKeyFile,
		AutoTLS:  fc.TLSAuto,
		Domain:   fc.TLSDomain,
	}

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Listening on %s", config.ListenAddr)
		var serveErr error
		if tlsCfg.CertFile != "" || tlsCfg.AutoTLS {
			serveErr = server.StartTLS(tlsCfg)
		} else {
			serveErr = server.ListenAndServe()
		}
		if serveErr != nil {
			log.Printf("Server error: %v", serveErr)
		}
	}()

	<-shutdownCh
	log.Println("Shutdown signal received — draining connections...")
	if err := server.Shutdown(); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Shutdown complete")
}
