package tracker

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerCreation(t *testing.T) {
	config := &Config{
		ListenAddr:       ":34000",
		AnnounceInterval: 1800,
		PeersTimeout:     7200,
		MaxMiddlemen:     10000,
		NumWantLimit:     50,
		KeepaliveTimeout: 60 * time.Second,
	}

	db := createTestDB(t)
	defer db.Close()

	worker := createTestWorker(t, db)
	server := NewServer(config, worker)

	if server == nil {
		t.Fatal("Server creation failed")
	}

	if server.config.ListenAddr != ":34000" {
		t.Errorf("Expected listen addr :34000, got %s", server.config.ListenAddr)
	}
}

func TestAnnounceHandler(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	worker := createTestWorker(t, db)
	server := NewServer(&Config{}, worker)

	// Create test torrent
	torrent := &Torrent{
		ID:       1,
		InfoHash: "test_info_hash_12345",
		FreeType: 0,
	}
	worker.Torrents.Store(torrent.InfoHash, torrent)

	// Create test user
	user := &User{
		ID:      1,
		Passkey: "test_passkey_32_characters_long",
	}
	user.CanLeech.Store(true)
	worker.Users.Store(user.Passkey, user)

	// Test valid announce
	req := httptest.NewRequest("GET", "/announce?info_hash=test_info_hash_12345&peer_id=test_peer_id_123456&port=6881&uploaded=0&downloaded=0&left=1000&passkey=test_passkey_32_characters_long", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestScrapeHandler(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	worker := createTestWorker(t, db)
	server := NewServer(&Config{}, worker)

	// Create test torrent
	torrent := &Torrent{
		ID:       1,
		InfoHash: "test_info_hash_12345",
		Seeders:  10,
		Leechers: 5,
		Snatched: 100,
	}
	worker.Torrents.Store(torrent.InfoHash, torrent)

	req := httptest.NewRequest("GET", "/scrape?info_hash=test_info_hash_12345", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestMissingInfoHash(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	worker := createTestWorker(t, db)
	server := NewServer(&Config{}, worker)

	req := httptest.NewRequest("GET", "/announce?peer_id=test&port=6881", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("Expected error for missing info_hash")
	}
}

func TestInvalidPasskey(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	worker := createTestWorker(t, db)
	server := NewServer(&Config{}, worker)

	req := httptest.NewRequest("GET", "/announce?info_hash=test&peer_id=test&port=6881&passkey=invalid", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("Expected error for invalid passkey")
	}
}

// Helper functions

func createTestDB(t *testing.T) *Database {
	// Create in-memory SQLite database for testing
	db, err := NewDatabase(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	return db
}

func createTestWorker(t *testing.T, db *Database) *Worker {
	config := &Config{
		AnnounceInterval: 1800,
		NumWantLimit:     50,
	}

	worker := NewWorker(config, db)
	return worker
}
