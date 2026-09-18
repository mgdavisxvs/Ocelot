package tracker

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Core type definitions for the Ocelot tracker

type TorrentID uint32
type UserID uint32

// FreeType represents the freeleech status of a torrent
type FreeType uint8

const (
	FreeNormal  FreeType = iota // Normal: all traffic counted
	FreeFree                    // Free: downloads don't count
	FreeNeutral                 // Neutral: no stats counted
)

// Peer represents a single peer in a swarm
type Peer struct {
	UserID          UserID
	Uploaded        int64
	Downloaded      int64
	Corrupt         int64
	Left            int64
	LastAnnounced   time.Time
	FirstAnnounced  time.Time
	Announces       uint32
	Port            uint16
	IP              net.IP // Go's native IP type (safer than manual parsing)
	IPPort          []byte // Compact 6-byte format: 4-byte IPv4 + 2-byte port
	Visible         bool
	InvalidIP       bool
	ConnectionTimes []time.Time // timestamps of each announce; used for rapid-reconnect detection
}

// PeerKey generates a unique, randomized key for a peer.
// Randomization distributes peers across hash buckets for better map performance,
// matching the C++ worker.cpp:315 logic.
func PeerKey(peerID []byte, userID UserID, torrentID TorrentID) string {
	randomByte := peerID[12+(torrentID&7)]
	key := make([]byte, 1+4+len(peerID))
	key[0] = randomByte
	key[1] = byte(userID >> 24)
	key[2] = byte(userID >> 16)
	key[3] = byte(userID >> 8)
	key[4] = byte(userID)
	copy(key[5:], peerID)
	return string(key)
}

// CompactIPPort creates the 6-byte compact peer format (BEP 23).
// Returns nil for non-IPv4 addresses (IPv6 not supported).
func CompactIPPort(ip net.IP, port uint16) []byte {
	ipv4 := ip.To4()
	if ipv4 == nil {
		return nil
	}
	compact := make([]byte, 6)
	copy(compact[0:4], ipv4)
	compact[4] = byte(port >> 8)
	compact[5] = byte(port & 0xFF)
	return compact
}

// PeerList is a concurrent-safe map of peers.
type PeerList struct {
	mu    sync.RWMutex
	peers map[string]*Peer
}

func NewPeerList() *PeerList {
	return &PeerList{peers: make(map[string]*Peer)}
}

func (pl *PeerList) Get(key string) (*Peer, bool) {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	peer, ok := pl.peers[key]
	return peer, ok
}

func (pl *PeerList) Set(key string, peer *Peer) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.peers[key] = peer
}

func (pl *PeerList) Delete(key string) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	delete(pl.peers, key)
}

func (pl *PeerList) Size() int {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return len(pl.peers)
}

// ForEach iterates all peers under a read lock. Return false from fn to stop.
func (pl *PeerList) ForEach(fn func(key string, peer *Peer) bool) {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	for k, p := range pl.peers {
		if !fn(k, p) {
			break
		}
	}
}

// Torrent represents a tracked torrent and its live peer swarms.
type Torrent struct {
	mu sync.RWMutex

	ID                 TorrentID
	Completed          uint32
	Balance            int64
	FreeType           FreeType
	Size               int64 // total torrent size in bytes; used by anomaly detection
	LastFlushed        time.Time
	Seeders            *PeerList
	Leechers           *PeerList
	LastSelectedSeeder string
	TokenedUsers       map[UserID]struct{}
}

func NewTorrent(id TorrentID) *Torrent {
	return &Torrent{
		ID:           id,
		Seeders:      NewPeerList(),
		Leechers:     NewPeerList(),
		TokenedUsers: make(map[UserID]struct{}),
		FreeType:     FreeNormal,
	}
}

// User represents a tracker user.
type User struct {
	ID        UserID
	Deleted   atomic.Bool
	CanLeech  atomic.Bool
	ProtectIP atomic.Bool
	Leeching  atomic.Uint32
	Seeding   atomic.Uint32
}

func NewUser(id UserID, canLeech, protectIP bool) *User {
	u := &User{ID: id}
	u.CanLeech.Store(canLeech)
	u.ProtectIP.Store(protectIP)
	return u
}

// TorrentList is a concurrent-safe map of torrents keyed by info_hash.
type TorrentList struct {
	mu       sync.RWMutex
	torrents map[string]*Torrent
}

func NewTorrentList() *TorrentList {
	return &TorrentList{torrents: make(map[string]*Torrent)}
}

func (tl *TorrentList) Get(infoHash string) (*Torrent, bool) {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	t, ok := tl.torrents[infoHash]
	return t, ok
}

func (tl *TorrentList) Set(infoHash string, torrent *Torrent) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.torrents[infoHash] = torrent
}

func (tl *TorrentList) Delete(infoHash string) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	delete(tl.torrents, infoHash)
}

func (tl *TorrentList) Size() int {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	return len(tl.torrents)
}

// ForEach iterates all torrents under a read lock. Return false from fn to stop.
func (tl *TorrentList) ForEach(fn func(hash string, t *Torrent) bool) {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	for h, t := range tl.torrents {
		if !fn(h, t) {
			break
		}
	}
}

// Reset clears all torrents. Used during full list reloads.
func (tl *TorrentList) Reset() {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.torrents = make(map[string]*Torrent)
}

// UserList is a concurrent-safe map of users keyed by passkey.
type UserList struct {
	mu    sync.RWMutex
	users map[string]*User
}

func NewUserList() *UserList {
	return &UserList{users: make(map[string]*User)}
}

func (ul *UserList) Get(passkey string) (*User, bool) {
	ul.mu.RLock()
	defer ul.mu.RUnlock()
	u, ok := ul.users[passkey]
	return u, ok
}

func (ul *UserList) Set(passkey string, user *User) {
	ul.mu.Lock()
	defer ul.mu.Unlock()
	ul.users[passkey] = user
}

func (ul *UserList) Delete(passkey string) {
	ul.mu.Lock()
	defer ul.mu.Unlock()
	delete(ul.users, passkey)
}

func (ul *UserList) Size() int {
	ul.mu.RLock()
	defer ul.mu.RUnlock()
	return len(ul.users)
}

// ForEach iterates all users under a read lock. Return false from fn to stop.
func (ul *UserList) ForEach(fn func(passkey string, u *User) bool) {
	ul.mu.RLock()
	defer ul.mu.RUnlock()
	for p, u := range ul.users {
		if !fn(p, u) {
			break
		}
	}
}

// Reset clears all users. Used during full list reloads.
func (ul *UserList) Reset() {
	ul.mu.Lock()
	defer ul.mu.Unlock()
	ul.users = make(map[string]*User)
}

// SetBanned marks a user as banned (banned=true) or reinstates them (banned=false).
// A banned user has CanLeech cleared and Deleted set; both are reversed on unban.
// The user is looked up by numeric ID — O(n) scan, acceptable for low-frequency
// admin operations.
func (ul *UserList) SetBanned(id UserID, banned bool) {
	ul.mu.RLock()
	defer ul.mu.RUnlock()
	for _, u := range ul.users {
		if u.ID == id {
			u.Deleted.Store(banned)
			u.CanLeech.Store(!banned)
			return
		}
	}
}

// Stats tracks global tracker statistics using atomics — no mutex required.
type Stats struct {
	OpenConnections   atomic.Uint32
	OpenedConnections atomic.Uint64
	Leechers          atomic.Uint32
	Seeders           atomic.Uint32
	Requests          atomic.Uint64
	Announcements     atomic.Uint64
	SuccAnnouncements atomic.Uint64
	Scrapes           atomic.Uint64
	BytesRead         atomic.Uint64
	BytesWritten      atomic.Uint64
	// EvictedPeers is incremented by the Reaper each time a stale peer is removed.
	EvictedPeers atomic.Uint64
	// AnomalyDetections is incremented each time the BehaviorDetector fires.
	AnomalyDetections atomic.Uint64
	StartTime         time.Time
}

// Whitelist is a concurrent-safe list of allowed BitTorrent client peer_id prefixes.
type Whitelist struct {
	mu       sync.RWMutex
	prefixes []string
	trie     *WhitelistTrie // O(k) lookup; rebuilt whenever prefixes change
}

func NewWhitelist() *Whitelist {
	return &Whitelist{prefixes: make([]string, 0)}
}

// rebuildTrie rebuilds the trie from the current prefixes slice.
// Must be called with wl.mu held for writing.
func (wl *Whitelist) rebuildTrie() {
	wl.trie = BuildTrieFromSlice(wl.prefixes)
}

// IsAllowed returns true if the peer_id matches any whitelisted prefix,
// or if the whitelist is empty (allow-all mode).
// Uses the O(k) trie when available; falls back to linear scan.
func (wl *Whitelist) IsAllowed(peerID []byte) bool {
	wl.mu.RLock()
	defer wl.mu.RUnlock()
	if len(wl.prefixes) == 0 {
		return true
	}
	if wl.trie != nil {
		return wl.trie.IsAllowed(peerID)
	}
	peerIDStr := string(peerID)
	for _, prefix := range wl.prefixes {
		if len(peerIDStr) >= len(prefix) && peerIDStr[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// Add appends a prefix to the whitelist (idempotent).
func (wl *Whitelist) Add(prefix string) {
	wl.mu.Lock()
	defer wl.mu.Unlock()
	for _, p := range wl.prefixes {
		if p == prefix {
			return
		}
	}
	wl.prefixes = append(wl.prefixes, prefix)
	wl.rebuildTrie()
}

// Remove deletes a prefix from the whitelist.
func (wl *Whitelist) Remove(prefix string) {
	wl.mu.Lock()
	defer wl.mu.Unlock()
	out := wl.prefixes[:0]
	for _, p := range wl.prefixes {
		if p != prefix {
			out = append(out, p)
		}
	}
	wl.prefixes = out
	wl.rebuildTrie()
}

// Reset replaces the entire prefix list atomically.
func (wl *Whitelist) Reset(prefixes []string) {
	wl.mu.Lock()
	defer wl.mu.Unlock()
	if prefixes == nil {
		wl.prefixes = make([]string, 0)
	} else {
		wl.prefixes = prefixes
	}
	wl.rebuildTrie()
}

// GetAll returns a snapshot of the current prefix list.
func (wl *Whitelist) GetAll() []string {
	wl.mu.RLock()
	defer wl.mu.RUnlock()
	out := make([]string, len(wl.prefixes))
	copy(out, wl.prefixes)
	return out
}
