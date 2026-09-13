package tracker

import (
	"testing"
	"time"
)

func TestCacheSetGet(t *testing.T) {
	cache := NewCache(5 * time.Second)

	// Set a value
	cache.Set("key1", "value1")

	// Get the value
	val, found := cache.Get("key1")
	if !found {
		t.Error("Expected to find key1 in cache")
	}

	if val != "value1" {
		t.Errorf("Expected value1, got %v", val)
	}
}

func TestCacheExpiration(t *testing.T) {
	cache := NewCache(100 * time.Millisecond)

	cache.Set("key1", "value1")

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	_, found := cache.Get("key1")
	if found {
		t.Error("Expected key1 to be expired")
	}
}

func TestCacheDelete(t *testing.T) {
	cache := NewCache(5 * time.Second)

	cache.Set("key1", "value1")
	cache.Delete("key1")

	_, found := cache.Get("key1")
	if found {
		t.Error("Expected key1 to be deleted")
	}
}

func TestCacheClear(t *testing.T) {
	cache := NewCache(5 * time.Second)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")

	cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("Expected cache size 0, got %d", cache.Size())
	}
}

func TestTorrentCache(t *testing.T) {
	cache := NewTorrentCache(5 * time.Second)

	torrent := &Torrent{
		ID:       1,
		InfoHash: "test123",
		Seeders:  10,
		Leechers: 5,
	}

	cache.Set(torrent.InfoHash, torrent)

	retrieved, found := cache.Get(torrent.InfoHash)
	if !found {
		t.Error("Expected to find torrent in cache")
	}

	if retrieved.ID != 1 {
		t.Errorf("Expected torrent ID 1, got %d", retrieved.ID)
	}
}
