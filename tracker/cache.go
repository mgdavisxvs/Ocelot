package tracker

import (
	"sync"
	"time"
)

// Cache provides an in-memory cache with TTL
type Cache struct {
	items map[string]*cacheItem
	mu    sync.RWMutex
	ttl   time.Duration
}

type cacheItem struct {
	value      interface{}
	expiration time.Time
}

// NewCache creates a new cache with the given TTL
func NewCache(ttl time.Duration) *Cache {
	c := &Cache{
		items: make(map[string]*cacheItem),
		ttl:   ttl,
	}

	// Start cleanup goroutine
	go c.cleanup()

	return c
}

// Get retrieves a value from the cache
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, found := c.items[key]
	if !found {
		return nil, false
	}

	// Check if expired
	if time.Now().After(item.expiration) {
		return nil, false
	}

	return item.value, true
}

// Set stores a value in the cache
func (c *Cache) Set(key string, value interface{}) {
	c.SetWithTTL(key, value, c.ttl)
}

// SetWithTTL stores a value with custom TTL
func (c *Cache) SetWithTTL(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = &cacheItem{
		value:      value,
		expiration: time.Now().Add(ttl),
	}
}

// Delete removes a value from the cache
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, key)
}

// Clear removes all items from the cache
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*cacheItem)
}

// cleanup periodically removes expired items
func (c *Cache) cleanup() {
	ticker := time.NewTicker(c.ttl / 2)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, item := range c.items {
			if now.After(item.expiration) {
				delete(c.items, key)
			}
		}
		c.mu.Unlock()
	}
}

// Size returns the number of items in the cache
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// TorrentCache provides caching for torrent objects
type TorrentCache struct {
	cache *Cache
}

// NewTorrentCache creates a new torrent cache
func NewTorrentCache(ttl time.Duration) *TorrentCache {
	return &TorrentCache{
		cache: NewCache(ttl),
	}
}

// Get retrieves a torrent from cache
func (tc *TorrentCache) Get(infoHash string) (*Torrent, bool) {
	val, found := tc.cache.Get("torrent:" + infoHash)
	if !found {
		return nil, false
	}

	torrent, ok := val.(*Torrent)
	return torrent, ok
}

// Set stores a torrent in cache
func (tc *TorrentCache) Set(infoHash string, torrent *Torrent) {
	tc.cache.Set("torrent:"+infoHash, torrent)
}

// Delete removes a torrent from cache
func (tc *TorrentCache) Delete(infoHash string) {
	tc.cache.Delete("torrent:" + infoHash)
}

// UserCache provides caching for user objects
type UserCache struct {
	cache *Cache
}

// NewUserCache creates a new user cache
func NewUserCache(ttl time.Duration) *UserCache {
	return &UserCache{
		cache: NewCache(ttl),
	}
}

// Get retrieves a user from cache
func (uc *UserCache) Get(passkey string) (*User, bool) {
	val, found := uc.cache.Get("user:" + passkey)
	if !found {
		return nil, false
	}

	user, ok := val.(*User)
	return user, ok
}

// Set stores a user in cache
func (uc *UserCache) Set(passkey string, user *User) {
	uc.cache.Set("user:"+passkey, user)
}

// Delete removes a user from cache
func (uc *UserCache) Delete(passkey string) {
	uc.cache.Delete("user:" + passkey)
}
