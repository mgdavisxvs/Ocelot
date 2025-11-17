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
	UserID         UserID
	Uploaded       int64
	Downloaded     int64
	Corrupt        int64
	Left           int64
	LastAnnounced  time.Time
	FirstAnnounced time.Time
	Announces      uint32
	Port           uint16
	IP             net.IP      // Go's native IP type (safer than manual parsing)
	IPPort         []byte      // Compact 6-byte format: 4-byte IPv4 + 2-byte port
	Visible        bool
	InvalidIP      bool
}

// PeerKey generates a unique, randomized key for a peer
// This distributes peers across the map for better hash performance
func PeerKey(peerID []byte, userID UserID, torrentID TorrentID) string {
	// Randomize using a byte from peer_id based on torrent_id
	// This matches C++ line 315: peer_id[12 + (tor.id & 7)]
	randomByte := peerID[12+(torrentID&7)]

	// Create key: randomByte + userID + full peerID
	// This ensures uniqueness while distributing hash buckets
	key := make([]byte, 1+4+len(peerID))
	key[0] = randomByte
	key[1] = byte(userID >> 24)
	key[2] = byte(userID >> 16)
	key[3] = byte(userID >> 8)
	key[4] = byte(userID)
	copy(key[5:], peerID)
	return string(key)
}

// CompactIPPort creates the 6-byte compact peer format
// Format: 4 bytes IP (big-endian) + 2 bytes port (big-endian)
func CompactIPPort(ip net.IP, port uint16) []byte {
	// Get IPv4 representation (4 bytes)
	ipv4 := ip.To4()
	if ipv4 == nil {
		return nil // IPv6 not supported in compact format
	}

	compact := make([]byte, 6)
	copy(compact[0:4], ipv4)
	compact[4] = byte(port >> 8)
	compact[5] = byte(port & 0xFF)
	return compact
}

// PeerList is a concurrent-safe map of peers
type PeerList struct {
	mu    sync.RWMutex
	peers map[string]*Peer
}

func NewPeerList() *PeerList {
	return &PeerList{
		peers: make(map[string]*Peer),
	}
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

// ForEach iterates over all peers (read-locked)
func (pl *PeerList) ForEach(fn func(key string, peer *Peer) bool) {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	for k, p := range pl.peers {
		if !fn(k, p) {
			break
		}
	}
}

// Torrent represents a tracked torrent and its swarms
type Torrent struct {
	mu sync.RWMutex

	ID                 TorrentID
	Completed          uint32
	Balance            int64
	FreeType           FreeType
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

// User represents a tracker user
type User struct {
	ID         UserID
	Deleted    atomic.Bool
	CanLeech   atomic.Bool
	ProtectIP  atomic.Bool
	Leeching   atomic.Uint32
	Seeding    atomic.Uint32
}

func NewUser(id UserID, canLeech, protectIP bool) *User {
	u := &User{ID: id}
	u.CanLeech.Store(canLeech)
	u.ProtectIP.Store(protectIP)
	return u
}

// TorrentList is a concurrent-safe map of torrents by info_hash
type TorrentList struct {
	mu       sync.RWMutex
	torrents map[string]*Torrent
}

func NewTorrentList() *TorrentList {
	return &TorrentList{
		torrents: make(map[string]*Torrent),
	}
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

// UserList is a concurrent-safe map of users by passkey
type UserList struct {
	mu    sync.RWMutex
	users map[string]*User
}

func NewUserList() *UserList {
	return &UserList{
		users: make(map[string]*User),
	}
}

func (ul *UserList) Get(passkey string) (*User, bool) {
	ul.mu.RLock()
	defer ul.mu.RUnlock()
	u, ok := ul.users[passkey]
	return u, ok
}

// Stats tracks global tracker statistics
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
	StartTime         time.Time
}

// Whitelist is a concurrent-safe list of allowed client peer_id prefixes
type Whitelist struct {
	mu       sync.RWMutex
	prefixes []string
}

func NewWhitelist() *Whitelist {
	return &Whitelist{
		prefixes: make([]string, 0),
	}
}

func (wl *Whitelist) IsAllowed(peerID []byte) bool {
	wl.mu.RLock()
	defer wl.mu.RUnlock()

	if len(wl.prefixes) == 0 {
		return true // Empty whitelist = allow all
	}

	peerIDStr := string(peerID)
	for _, prefix := range wl.prefixes {
		if len(peerIDStr) >= len(prefix) && peerIDStr[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
