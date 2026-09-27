package tracker

import (
	"container/heap"
	"sync"
	"time"
)

// cacheHeapEntry is an element in the expiry min-heap.
type cacheHeapEntry struct {
	key       string
	expiresAt time.Time
	index     int // maintained by heap.Interface
}

// cacheHeap implements heap.Interface ordered by earliest expiry.
type cacheHeap []*cacheHeapEntry

func (h cacheHeap) Len() int            { return len(h) }
func (h cacheHeap) Less(i, j int) bool  { return h[i].expiresAt.Before(h[j].expiresAt) }
func (h cacheHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *cacheHeap) Push(x interface{}) {
	e := x.(*cacheHeapEntry)
	e.index = len(*h)
	*h = append(*h, e)
}
func (h *cacheHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

// Cache provides an in-memory TTL cache with O(log n) expiry eviction.
type Cache struct {
	items   map[string]*cacheItem
	expHeap cacheHeap
	mu      sync.Mutex
	ttl     time.Duration
}

type cacheItem struct {
	value      interface{}
	expiration time.Time
	heapEntry  *cacheHeapEntry
}

// NewCache creates a new cache with the given TTL.
func NewCache(ttl time.Duration) *Cache {
	c := &Cache{
		items: make(map[string]*cacheItem),
		ttl:   ttl,
	}
	heap.Init(&c.expHeap)
	go c.cleanup()
	return c
}

// Get retrieves a value from the cache.
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, found := c.items[key]
	if !found {
		return nil, false
	}
	if time.Now().After(item.expiration) {
		return nil, false
	}
	return item.value, true
}

// Set stores a value in the cache with the default TTL.
func (c *Cache) Set(key string, value interface{}) {
	c.SetWithTTL(key, value, c.ttl)
}

// SetWithTTL stores a value with a custom TTL.
func (c *Cache) SetWithTTL(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	exp := time.Now().Add(ttl)
	if existing, ok := c.items[key]; ok {
		existing.value = value
		existing.expiration = exp
		existing.heapEntry.expiresAt = exp
		heap.Fix(&c.expHeap, existing.heapEntry.index)
		return
	}
	entry := &cacheHeapEntry{key: key, expiresAt: exp}
	c.items[key] = &cacheItem{value: value, expiration: exp, heapEntry: entry}
	heap.Push(&c.expHeap, entry)
}

// Delete removes a value from the cache.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteLocked(key)
}

func (c *Cache) deleteLocked(key string) {
	if item, ok := c.items[key]; ok {
		heap.Remove(&c.expHeap, item.heapEntry.index)
		delete(c.items, key)
	}
}

// Clear removes all items from the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*cacheItem)
	c.expHeap = c.expHeap[:0]
	heap.Init(&c.expHeap)
}

// cleanup pops expired entries from the min-heap — O(k log n) where k is the
// number of expired items, rather than the O(n) full-scan of the prior design.
func (c *Cache) cleanup() {
	ticker := time.NewTicker(c.ttl / 2)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		c.mu.Lock()
		for c.expHeap.Len() > 0 && c.expHeap[0].expiresAt.Before(now) {
			entry := heap.Pop(&c.expHeap).(*cacheHeapEntry)
			delete(c.items, entry.key)
		}
		c.mu.Unlock()
	}
}

// Size returns the number of items in the cache.
func (c *Cache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
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

// Clear evicts all cached torrents.
func (tc *TorrentCache) Clear() {
	tc.cache.Clear()
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

// Clear evicts all cached users.
func (uc *UserCache) Clear() {
	uc.cache.Clear()
}
