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

	torrent := NewTorrent(1)

	infoHash := "test123"
	cache.Set(infoHash, torrent)

	retrieved, found := cache.Get(infoHash)
	if !found {
		t.Error("Expected to find torrent in cache")
	}

	if retrieved.ID != 1 {
		t.Errorf("Expected torrent ID 1, got %d", retrieved.ID)
	}
}

func TestTorrentCache_Delete(t *testing.T) {
	cache := NewTorrentCache(5 * time.Second)
	cache.Set("abc", NewTorrent(2))
	cache.Delete("abc")
	if _, found := cache.Get("abc"); found {
		t.Error("torrent should be gone after Delete")
	}
}

// ── UserCache ─────────────────────────────────────────────────────────────────

func TestUserCache_SetGet(t *testing.T) {
	uc := NewUserCache(5 * time.Second)
	u := NewUser(7, true, false)
	uc.Set("pk001", u)

	got, ok := uc.Get("pk001")
	if !ok {
		t.Fatal("expected to find user in cache")
	}
	if got.ID != 7 {
		t.Errorf("user ID = %d, want 7", got.ID)
	}
}

func TestUserCache_Get_Miss(t *testing.T) {
	uc := NewUserCache(5 * time.Second)
	if _, ok := uc.Get("missing"); ok {
		t.Error("expected cache miss for unknown passkey")
	}
}

func TestUserCache_Delete(t *testing.T) {
	uc := NewUserCache(5 * time.Second)
	uc.Set("pk002", NewUser(8, true, false))
	uc.Delete("pk002")
	if _, ok := uc.Get("pk002"); ok {
		t.Error("user should be gone after Delete")
	}
}

// ── Cache TTL expiry ──────────────────────────────────────────────────────────

func TestCache_Get_ExpiredItem(t *testing.T) {
	c := NewCache(10 * time.Millisecond)
	c.Set("expiring", "value")

	time.Sleep(25 * time.Millisecond)

	_, ok := c.Get("expiring")
	if ok {
		t.Error("expired item should return false from Get")
	}
}

func TestCache_SetWithTTL_OverridesDefault(t *testing.T) {
	c := NewCache(time.Hour)
	c.SetWithTTL("shortlived", 42, 10*time.Millisecond)

	time.Sleep(25 * time.Millisecond)

	_, ok := c.Get("shortlived")
	if ok {
		t.Error("item with short TTL override should be expired")
	}
}
