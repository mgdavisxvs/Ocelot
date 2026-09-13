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
	fmt.Println("🐆 Ocelot BitTorrent Tracker (Go Edition)")
	fmt.Println("Ported from C++ with innovative approaches from:")
	fmt.Println("  • Donald Knuth - Algorithm efficiency")
	fmt.Println("  • Ronald Graham - Combinatorial optimization")
	fmt.Println("  • Linus Torvalds - Collaborative systems")
	fmt.Println("  • Stephen Wolfram - Computational modeling")
	fmt.Println()

	// Configuration: defaults, then ocelot.conf, then OCELOT_* env vars.
	configPath := os.Getenv("OCELOT_CONFIG")
	if configPath == "" {
		configPath = "ocelot.conf"
	}

	config, err := tracker.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	log.Printf("Configuration loaded from %s", configPath)

	// Placeholder credentials leave the admin API open, so say so loudly
	// rather than starting silently with a password baked into the image.
	for _, warning := range config.InsecureWarnings() {
		log.Printf("⚠️  INSECURE: %s", warning)
	}

	// Initialize data structures
	torrents := tracker.NewTorrentList()
	users := tracker.NewUserList()
	whitelist := tracker.NewWhitelist()
	stats := &tracker.Stats{
		StartTime: time.Now(),
	}

	// Create SQLite database with 84GB sharding
	dbDir := "./data/db"
	db, err := tracker.NewSQLiteShardManager(dbDir)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	log.Printf("SQLite database initialized in %s", dbDir)

	// Admin listener: Prometheus metrics and the health probes. Kept off the
	// tracker port, which only routes /{passkey}/{action}.
	health := tracker.NewHealthChecker(db.DB())
	if config.MetricsAddr != "" {
		go func() {
			if err := tracker.StartMetricsServer(config.MetricsAddr, health); err != nil {
				log.Printf("Admin listener error: %v", err)
			}
		}()
		log.Printf("Admin listener (metrics, health) on %s", config.MetricsAddr)
	}

	// Create mock site communication (replace with real Gazelle integration)
	siteComm := &MockSiteComm{}

	// Create worker with all dependencies
	worker := &tracker.Worker{
		Config:    config,
		DB:        db,
		SiteComm:  siteComm,
		Torrents:  torrents,
		Users:     users,
		Whitelist: whitelist,
		Stats:     stats,
	}

	// Create loader and load initial state from database
	loader := tracker.NewLoader(db, torrents, users, whitelist)

	// Create schema if needed
	if err := loader.CreateSchemaIfNeeded(); err != nil {
		log.Printf("Warning: Failed to create schema: %v", err)
	}

	// Load initial data from database. A failure here is fatal: serving with
	// partial state silently rejects real users and hands out wrong peer
	// lists, which is worse than not starting.
	log.Println("Loading initial state from database...")
	if err := loader.LoadAll(); err != nil {
		log.Fatalf("Failed to load initial state: %v", err)
	}
	log.Printf("✅ Loaded %d torrents and %d users", torrents.Size(), users.Size())

	// An empty database is normal for a fresh install; the site pushes data in
	// over /update. Sample data is opt-in because it creates a user whose
	// passkey is published in this source file.
	if os.Getenv("OCELOT_DEV_SAMPLE_DATA") == "1" {
		log.Println("⚠️  INSECURE: OCELOT_DEV_SAMPLE_DATA is set, loading a well-known demo passkey")
		loadSampleData(torrents, users, whitelist)
	}

	// Initial load is done; the startup probe can stop holding off liveness.
	health.MarkStarted()

	// Create server
	server := tracker.NewServer(config, worker)

	// Audit trail for authentication and authorization failures.
	if err := tracker.CreateAuditLogTable(db.DB()); err != nil {
		log.Printf("Warning: failed to create audit_log table: %v", err)
	} else {
		audit := tracker.NewAuditLogger(db.DB())
		server.SetAuditLogger(audit)
		audit.StartPruning(context.Background(), config.AuditRetentionDays)
		log.Printf("Audit logging enabled (retention: %d days)", config.AuditRetentionDays)
	}

	// Start server in background
	go func() {
		log.Printf("Starting tracker on %s", config.ListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// TLS listener, when a keypair is configured. Passkeys travel in the
	// request path, so plaintext hands a credential to anyone on the wire.
	if config.TLSEnabled() {
		go func() {
			log.Printf("Starting TLS tracker on %s", config.TLSAddr)
			if err := server.ListenAndServeTLS(); err != nil {
				log.Fatalf("TLS server error: %v", err)
			}
		}()
	}

	health.MarkReady()

	// Reap peers that stopped announcing. Without this, peers that vanish
	// without sending event=stopped stay in the swarm forever.
	reaper := tracker.NewReaper(torrents, db, stats,
		time.Duration(config.PeersTimeout)*time.Second,
		time.Duration(config.ReapInterval)*time.Second,
	)
	reaper.Start()

	// Print statistics periodically
	go printStats(stats)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n🛑 Shutting down gracefully...")

	// Fail readiness first so load balancers stop sending new work, then give
	// in-flight announces a moment to finish before tearing the server down.
	health.MarkNotReady()
	time.Sleep(2 * time.Second)

	reaper.Stop()

	if err := server.Shutdown(); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	fmt.Println("✅ Shutdown complete")
}

// loadSampleData loads sample torrents and users for testing
func loadSampleData(torrents *tracker.TorrentList, users *tracker.UserList, whitelist *tracker.Whitelist) {
	// Add sample user
	user := tracker.NewUser(1, true, false)
	users.Set("0123456789abcdef0123456789abcdef", user)

	// Add sample torrent
	torrent := tracker.NewTorrent(1)
	torrents.Set("sampleinfohash12345", torrent)

	fmt.Println("✅ Loaded sample data:")
	fmt.Println("   • 1 user (passkey: 0123456789abcdef0123456789abcdef)")
	fmt.Println("   • 1 torrent")
	fmt.Println()
}

// printStats displays tracker statistics every 30 seconds
func printStats(stats *tracker.Stats) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		uptime := time.Since(stats.StartTime)
		announces := stats.Announcements.Load()
		successAnnounces := stats.SuccAnnouncements.Load()
		scrapes := stats.Scrapes.Load()
		connections := stats.OpenConnections.Load()
		seeders := stats.Seeders.Load()
		leechers := stats.Leechers.Load()

		fmt.Printf("\n📊 Tracker Statistics (uptime: %s)\n", uptime.Round(time.Second))
		fmt.Printf("   Connections: %d active\n", connections)
		fmt.Printf("   Announces: %d total, %d successful\n", announces, successAnnounces)
		fmt.Printf("   Scrapes: %d\n", scrapes)
		fmt.Printf("   Peers: %d seeders, %d leechers\n", seeders, leechers)
		fmt.Println()
	}
}

// Note: MockDatabase removed - now using real SQLite implementation

// MockSiteComm implements a simple mock for site communication
type MockSiteComm struct{}

func (sc *MockSiteComm) ExpireToken(torrentID tracker.TorrentID, userID tracker.UserID) {
	// In production: Send HTTP request to Gazelle to expire freeleech token
	log.Printf("Expired token for user %d on torrent %d", userID, torrentID)
}
