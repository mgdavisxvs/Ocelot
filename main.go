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

	// Load config from file (path via -c flag, defaults to ocelot.conf)
	cfgPath := tracker.ParseFlags()
	fileCfg, err := tracker.ParseConfigFile(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Allow environment variables to override file config for secrets
	if v := os.Getenv("SITE_PASSWORD"); v != "" {
		fileCfg.SitePassword = v
	}
	if v := os.Getenv("REPORT_PASSWORD"); v != "" {
		fileCfg.ReportPassword = v
	}
	if v := os.Getenv("GAZELLE_URL"); v != "" {
		fileCfg.GazelleURL = v
	}
	if v := os.Getenv("DB_DIR"); v != "" {
		fileCfg.DBDir = v
	}

	// Refuse insecure default credentials at startup
	if fileCfg.SitePassword == "changeme" || fileCfg.SitePassword == "00000000000000000000000000000000" {
		log.Fatal("FATAL: site_password is set to the default value. Set a strong password via ocelot.conf or SITE_PASSWORD env var before starting.")
	}
	if fileCfg.ReportPassword == "changeme" || fileCfg.ReportPassword == "00000000000000000000000000000000" {
		log.Fatal("FATAL: report_password is set to the default value. Set a strong password via ocelot.conf or REPORT_PASSWORD env var before starting.")
	}

	config := fileCfg.ToTrackerConfig()
	config.GazelleURL = fileCfg.GazelleURL
	config.ScheduleInterval = fileCfg.ScheduleInterval

	// Initialize data structures
	torrents := tracker.NewTorrentList()
	users := tracker.NewUserList()
	whitelist := tracker.NewWhitelist()
	stats := &tracker.Stats{
		StartTime: time.Now(),
	}

	// Create SQLite database with 84GB sharding
	db, err := tracker.NewSQLiteShardManager(fileCfg.DBDir)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	log.Printf("SQLite database initialized in %s", fileCfg.DBDir)

	// Wire real Gazelle site communication when a URL is provided;
	// fall back to a no-op implementation that logs instead of calling out.
	var siteComm tracker.SiteCommInterface
	if fileCfg.GazelleURL != "" {
		gazellePass := os.Getenv("GAZELLE_PASSWORD")
		if gazellePass == "" {
			gazellePass = fileCfg.SitePassword // fallback to shared site password
		}
		siteComm = tracker.NewGazelleSiteComm(fileCfg.GazelleURL, gazellePass)
		log.Printf("Gazelle site comm active: %s", fileCfg.GazelleURL)
	} else {
		siteComm = &tracker.NoOpSiteComm{}
		log.Println("Gazelle site comm: no-op (set gazelle_url in config to enable)")
	}

	// Wire audit logger against the active SQLite shard
	auditLogger := tracker.NewAuditLogger(db.CurrentDB())

	// Create worker with all dependencies
	worker := &tracker.Worker{
		Config:    config,
		DB:        db,
		SiteComm:  siteComm,
		Torrents:  torrents,
		Users:     users,
		Whitelist: whitelist,
		Stats:     stats,
		Audit:     auditLogger,
	}

	// Create loader and load initial state from database
	loader := tracker.NewLoader(db, torrents, users, whitelist)

	// Create schema if needed
	if err := loader.CreateSchemaIfNeeded(); err != nil {
		log.Printf("Warning: Failed to create schema: %v", err)
	}

	// Load initial data from database
	log.Println("Loading initial state from database...")
	if err := loader.LoadAll(); err != nil {
		log.Printf("Warning: Failed to load initial state: %v", err)
		log.Println("Starting with empty state - add torrents and users via admin panel")
	} else {
		log.Println("Initial state loaded from database")
	}

	// Create server
	server := tracker.NewServer(config, worker)

	// Start server in background
	go func() {
		log.Printf("Starting tracker on %s", config.ListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Print statistics periodically
	go printStats(stats)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down gracefully...")
	if err := server.Shutdown(); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	log.Println("Shutdown complete")
}

// printStats logs tracker statistics every 30 seconds.
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
