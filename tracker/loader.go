package tracker

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"time"
)

// LoaderInterface defines methods for loading initial tracker state from database
type LoaderInterface interface {
	LoadTorrents() error
	LoadUsers() error
	LoadPeers() error
	LoadWhitelist() error
}

// Loader handles loading initial state from SQLite database on tracker startup
// This bridges the gap identified in Phase 1: Database → Tracker initial load
type Loader struct {
	db        *SQLiteShardManager
	torrents  *TorrentList
	users     *UserList
	whitelist *Whitelist
}

// NewLoader creates a new database loader
func NewLoader(db *SQLiteShardManager, torrents *TorrentList, users *UserList, whitelist *Whitelist) *Loader {
	return &Loader{
		db:        db,
		torrents:  torrents,
		users:     users,
		whitelist: whitelist,
	}
}

// LoadAll loads all initial data from database
func (l *Loader) LoadAll() error {
	log.Println("Loading initial state from database...")

	if err := l.LoadTorrents(); err != nil {
		return fmt.Errorf("failed to load torrents: %w", err)
	}

	if err := l.LoadUsers(); err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	if err := l.LoadPeers(); err != nil {
		return fmt.Errorf("failed to load peers: %w", err)
	}

	if err := l.LoadWhitelist(); err != nil {
		return fmt.Errorf("failed to load whitelist: %w", err)
	}

	log.Println("✅ Initial state loaded successfully")
	return nil
}

// LoadTorrents loads all active torrents from database
// Query: SELECT DISTINCT torrent_id FROM torrents ORDER BY torrent_id
func (l *Loader) LoadTorrents() error {
	l.db.mu.RLock()
	db := l.db.currentDB
	l.db.mu.RUnlock()

	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Query distinct torrents
	rows, err := db.Query(`
		SELECT DISTINCT torrent_id, seeders, leechers, snatched, balance, free_type
		FROM torrents
		ORDER BY torrent_id
	`)
	if err != nil {
		// Table might not exist yet - not an error
		return nil
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			torrentID TorrentID
			seeders   uint32
			leechers  uint32
			snatched  int
			balance   int64
			freeType  int
		)

		if err := rows.Scan(&torrentID, &seeders, &leechers, &snatched, &balance, &freeType); err != nil {
			log.Printf("Warning: Failed to scan torrent row: %v", err)
			continue
		}

		// Create torrent (info_hash will be filled from peers table)
		torrent := NewTorrent(torrentID)
		torrent.Completed = uint32(snatched)
		torrent.Balance = balance
		if freeType >= 0 && freeType <= 2 {
			torrent.FreeType = FreeType(freeType)
		}

		// Store with temporary key (will be updated when loading peers)
		tempKey := fmt.Sprintf("__temp_%d", torrentID)
		l.torrents.Set(tempKey, torrent)
		count++
	}

	log.Printf("  Loaded %d torrents", count)
	return nil
}

// LoadUsers loads all active users from database
// Query: SELECT DISTINCT user_id FROM peers UNION SELECT DISTINCT user_id FROM transfers
func (l *Loader) LoadUsers() error {
	l.db.mu.RLock()
	db := l.db.currentDB
	l.db.mu.RUnlock()

	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Query distinct user IDs from peers and transfers
	rows, err := db.Query(`
		SELECT DISTINCT user_id FROM peers
		UNION
		SELECT DISTINCT user_id FROM transfers
		ORDER BY user_id
	`)
	if err != nil {
		// Tables might not exist yet - not an error
		return nil
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var userID UserID
		if err := rows.Scan(&userID); err != nil {
			log.Printf("Warning: Failed to scan user row: %v", err)
			continue
		}

		// Create user with default privileges (passkey will be set via admin panel)
		user := NewUser(userID, true, false)

		// Generate a temporary passkey (format: temp_<user_id>_<timestamp>)
		tempPasskey := fmt.Sprintf("temp_%010d_%016x", userID, time.Now().UnixNano())
		l.users.Set(tempPasskey, user)
		count++
	}

	log.Printf("  Loaded %d users (temporary passkeys - update via admin panel)", count)
	return nil
}

// LoadPeers loads recent peers from database (last 2 hours)
// This rebuilds the in-memory peer lists for active torrents
// Complexity: O(n) where n = active peers
func (l *Loader) LoadPeers() error {
	l.db.mu.RLock()
	db := l.db.currentDB
	l.db.mu.RUnlock()

	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Query recent peers (last 2 hours = 7200 seconds)
	cutoff := time.Now().Add(-2 * time.Hour).Unix()
	rows, err := db.Query(`
		SELECT
			user_id,
			torrent_id,
			uploaded,
			downloaded,
			torrent_left,
			torrent_corrupt,
			ip,
			port,
			peer_id,
			announces,
			timestamp
		FROM peers
		WHERE timestamp > ?
		ORDER BY torrent_id, user_id
	`, cutoff)
	if err != nil {
		// Table might not exist yet - not an error
		return nil
	}
	defer rows.Close()

	type peerData struct {
		userID     UserID
		torrentID  TorrentID
		uploaded   int64
		downloaded int64
		left       int64
		corrupt    int64
		ip         string
		port       uint16
		peerID     string
		announces  uint32
		timestamp  int64
	}

	peers := make(map[TorrentID][]peerData)
	for rows.Next() {
		var pd peerData

		if err := rows.Scan(
			&pd.userID,
			&pd.torrentID,
			&pd.uploaded,
			&pd.downloaded,
			&pd.left,
			&pd.corrupt,
			&pd.ip,
			&pd.port,
			&pd.peerID,
			&pd.announces,
			&pd.timestamp,
		); err != nil {
			log.Printf("Warning: Failed to scan peer row: %v", err)
			continue
		}

		peers[pd.torrentID] = append(peers[pd.torrentID], pd)
	}

	// Rebuild peer lists for each torrent
	totalPeers := 0
	for torrentID, peerList := range peers {
		// Find torrent by ID
		var torrent *Torrent
		l.torrents.mu.RLock()
		for _, t := range l.torrents.torrents {
			if t.ID == torrentID {
				torrent = t
				break
			}
		}
		l.torrents.mu.RUnlock()

		if torrent == nil {
			// Torrent not loaded - skip
			continue
		}

		// Add each peer to appropriate list (seeders or leechers)
		for _, pd := range peerList {
			peer := &Peer{
				UserID:        pd.userID,
				Uploaded:      pd.uploaded,
				Downloaded:    pd.downloaded,
				Left:          pd.left,
				Corrupt:       pd.corrupt,
				Port:          pd.port,
				Announces:     pd.announces,
				LastAnnounced: time.Unix(pd.timestamp, 0),
				Visible:       true,
				InvalidIP:     false,
			}

			// Parse IP
			if pd.ip != "" {
				peer.IP = net.ParseIP(pd.ip)
				peer.IPPort = CompactIPPort(peer.IP, pd.port)
				if peer.IPPort == nil {
					peer.InvalidIP = true
				}
			}

			// Generate peer key
			peerKey := PeerKey([]byte(pd.peerID), pd.userID, torrentID)

			// Add to appropriate list
			torrent.mu.Lock()
			if pd.left > 0 {
				torrent.Leechers.Set(peerKey, peer)
			} else {
				torrent.Seeders.Set(peerKey, peer)
			}
			torrent.mu.Unlock()

			totalPeers++
		}
	}

	log.Printf("  Loaded %d active peers across %d torrents", totalPeers, len(peers))
	return nil
}

// LoadWhitelist loads allowed peer_id prefixes from database or config
// For now, returns empty whitelist (allow all)
// TODO: Add whitelist table to database schema
func (l *Loader) LoadWhitelist() error {
	// Placeholder: Load from whitelist table when implemented
	// For now, empty whitelist = allow all clients

	l.db.mu.RLock()
	db := l.db.currentDB
	l.db.mu.RUnlock()

	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Try to load from whitelist table
	rows, err := db.Query(`SELECT prefix FROM whitelist`)
	if err != nil {
		// Table doesn't exist - not an error
		log.Println("  No whitelist table found (allowing all clients)")
		return nil
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var prefix string
		if err := rows.Scan(&prefix); err != nil {
			continue
		}
		l.whitelist.Add(prefix)
		count++
	}

	if count > 0 {
		log.Printf("  Loaded %d whitelist entries", count)
	} else {
		log.Println("  Empty whitelist (allowing all clients)")
	}

	return nil
}

// LoadUserByPasskey loads a specific user by passkey from a users table
// This assumes a users table exists with schema: (user_id, passkey, can_leech, protect_ip)
func (l *Loader) LoadUserByPasskey(passkey string) (*User, error) {
	l.db.mu.RLock()
	db := l.db.currentDB
	l.db.mu.RUnlock()

	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var (
		userID    UserID
		canLeech  bool
		protectIP bool
	)

	err := db.QueryRow(`
		SELECT user_id, can_leech, protect_ip
		FROM users
		WHERE passkey = ?
	`, passkey).Scan(&userID, &canLeech, &protectIP)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}

	user := NewUser(userID, canLeech, protectIP)
	return user, nil
}

// CreateSchemaIfNeeded creates missing tables for metadata storage
// This includes users, whitelist, and configuration tables
func (l *Loader) CreateSchemaIfNeeded() error {
	l.db.mu.RLock()
	db := l.db.currentDB
	l.db.mu.RUnlock()

	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Create users table (for passkey storage)
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			user_id INTEGER PRIMARY KEY,
			passkey TEXT UNIQUE NOT NULL,
			can_leech BOOLEAN DEFAULT 1,
			protect_ip BOOLEAN DEFAULT 0,
			created_at INTEGER DEFAULT (strftime('%s', 'now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create users table: %w", err)
	}

	// Create whitelist table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS whitelist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			prefix TEXT UNIQUE NOT NULL,
			added_at INTEGER DEFAULT (strftime('%s', 'now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create whitelist table: %w", err)
	}

	// Create config table (for tracker settings)
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER DEFAULT (strftime('%s', 'now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create config table: %w", err)
	}

	log.Println("  Database schema validated")
	return nil
}
