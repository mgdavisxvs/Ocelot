package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"./tracker"
)

func main() {
	fmt.Println("🐆 Ocelot BitTorrent Tracker (Go Edition)")
	fmt.Println("Ported from C++ with innovative approaches from:")
	fmt.Println("  • Donald Knuth - Algorithm efficiency")
	fmt.Println("  • Ronald Graham - Combinatorial optimization")
	fmt.Println("  • Linus Torvalds - Collaborative systems")
	fmt.Println("  • Stephen Wolfram - Computational modeling")
	fmt.Println()

	// Initialize configuration
	config := &tracker.Config{
		ListenAddr:       ":34000",
		AnnounceInterval: 1800, // 30 minutes
		PeersTimeout:     7200, // 2 hours
		MaxMiddlemen:     20000,
		NumWantLimit:     50,
		KeepaliveTimeout: 60 * time.Second,
		SitePassword:     "changeme",
		ReportPassword:   "changeme",
		ReadTimeout:      30 * time.Second,
		WriteTimeout:     30 * time.Second,
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

	// Create mock site communication (replace with real Gazelle integration)
	siteComm := &MockSiteComm{}

	// Load initial data (in production, load from database)
	loadSampleData(torrents, users, whitelist)

	// Create worker
	worker := &tracker.Worker{
		// Note: These would be properly initialized in production
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

	fmt.Println("\n🛑 Shutting down gracefully...")
	if err := server.Shutdown(); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	fmt.Println("✅ Shutdown complete")
}

// loadSampleData loads sample torrents and users for testing
func loadSampleData(torrents *tracker.TorrentList, users *tracker.UserList, whitelist *tracker.Whitelist) {
	// Add sample user
	user := tracker.NewUser(1, true, false)
	users.mu.Lock()
	users.users["0123456789abcdef0123456789abcdef"] = user
	users.mu.Unlock()

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
