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

	// ── Database ─────────────────────────────────────────────────────────────
	rawDB, err := tracker.NewSQLiteShardManager(fileCfg.DBDir)
	if err != nil {
		log.Fatalf("database init: %v", err)
	}
	log.Printf("SQLite database initialised in %s", fileCfg.DBDir)

	// [D] Circuit breaker — protects all DB writes.
	breaker := tracker.NewCircuitBreaker(tracker.CircuitBreakerConfig{
		Name:         "sqlite",
		MaxFailures:  5,
		ResetTimeout: 30 * time.Second,
		HalfOpenMax:  3,
	})

	// [A] BufferedDB — async write queue wrapping the raw DB + circuit breaker.
	db := tracker.NewBufferedDB(rawDB, breaker, config.BatchBufferCap, 200*time.Millisecond)
	log.Printf("BufferedDB initialised (queue cap %d)", config.BatchBufferCap)

	// ── In-memory state ──────────────────────────────────────────────────────
	torrents := tracker.NewTorrentList()
	users := tracker.NewUserList()
	whitelist := tracker.NewWhitelist()
	stats := &tracker.Stats{StartTime: time.Now()}

	// ── Loader (reads use rawDB directly so startup never hits the buffer) ───
	loader := tracker.NewLoader(rawDB, torrents, users, whitelist)
	if err := loader.LoadAll(); err != nil {
		log.Printf("warning: failed to load initial state: %v", err)
		log.Println("starting with empty state — add torrents and users via admin panel")
	} else {
		log.Printf("initial state loaded: %d torrents, %d users",
			torrents.Size(), users.Size())
	}

	// Fall back to sample data when the database is empty.
	if torrents.Size() == 0 || users.Size() == 0 {
		log.Println("database empty, loading sample data")
		loadSampleData(torrents, users, whitelist)
	}

	// ── Site communication (replace with real Gazelle integration) ───────────
	siteComm := &MockSiteComm{}

	// ── Worker ───────────────────────────────────────────────────────────────
	worker := &tracker.Worker{
		Config:    config,
		DB:        db,  // [A+D] buffered + circuit-breaker protected
		SiteComm:  siteComm,
		Torrents:  torrents,
		Users:     users,
		Whitelist: whitelist,
		Stats:     stats,
		// [F] Anomaly detector
		Detector: tracker.NewAnomalyDetector(),
	}

	// [B] Reaper + [E] RateLimiter initialised inside Worker.Start().
	worker.Start()
	log.Println("reaper started")
	if config.RateLimitRPS > 0 {
		log.Printf("rate limiter active (%d RPS, burst %d)", config.RateLimitRPS, config.RateLimitBurst)
	}

	// [C] Scheduler — WAL checkpoint + shard rotation.
	sched := tracker.NewScheduler(rawDB, config.ScheduleInterval)
	sched.Start()
	log.Printf("scheduler started (interval %ds)", config.ScheduleInterval)

	// ── Server ───────────────────────────────────────────────────────────────
	server := tracker.NewServer(config, worker)

	go func() {
		log.Printf("tracker listening on %s", config.ListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	if config.MetricsPort != "" {
		go func() {
			log.Printf("metrics server on %s", config.MetricsPort)
			if err := tracker.StartMetricsServer(config.MetricsPort); err != nil {
				log.Printf("metrics server error: %v", err)
			}
		}()
	}

	go printStats(stats, worker)

	// ── Graceful shutdown ────────────────────────────────────────────────────
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nshutting down gracefully...")

	// Stop accepting new connections.
	if err := server.Shutdown(); err != nil {
		log.Printf("server shutdown: %v", err)
	}

	// Stop scheduler.
	sched.Stop()

	// Stop reaper and flush buffered DB writes (Worker.Stop handles both).
	worker.Stop()

	fmt.Println("shutdown complete")
}

// loadSampleData loads minimal sample data for development/testing.
func loadSampleData(torrents *tracker.TorrentList, users *tracker.UserList, whitelist *tracker.Whitelist) {
	users.Set("0123456789abcdef0123456789abcdef", tracker.NewUser(1, true, false))
	torrents.Set("sampleinfohash12345", tracker.NewTorrent(1))
	log.Println("sample data loaded (1 user, 1 torrent)")
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

// MockSiteComm is a no-op SiteCommInterface for development use.
// Replace with a real Gazelle HTTP client for production.
type MockSiteComm struct{}

func (sc *MockSiteComm) ExpireToken(torrentID tracker.TorrentID, userID tracker.UserID) {
	log.Printf("token expired: user=%d torrent=%d", userID, torrentID)
}
func (sc *MockSiteComm) NotifyFreeleech(torrentID int64, hours int) error  { return nil }
func (sc *MockSiteComm) ReportAnomaly(userID int64, score float64) error   { return nil }
func (sc *MockSiteComm) UpdateStats(seeders, leechers, completed int64) error { return nil }
func (sc *MockSiteComm) BanUser(userID int64) error                        { return nil }
func (sc *MockSiteComm) UnbanUser(userID int64) error                      { return nil }
