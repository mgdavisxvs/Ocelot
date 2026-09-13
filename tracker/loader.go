package tracker

import (
	"database/sql"
	"fmt"
	"log"
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

	// Query distinct torrents. Only rows carrying an info_hash are usable:
	// clients announce by hash, so a torrent without one can never be matched.
	rows, err := db.Query(`
		SELECT id, info_hash, seeders, leechers, snatched, balance, free_type
		FROM torrents
		WHERE info_hash != ''
		ORDER BY id
	`)
	if err != nil {
		return fmt.Errorf("failed to query torrents: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			torrentID TorrentID
			infoHash  string
			seeders   uint32
			leechers  uint32
			snatched  int
			balance   int64
			freeType  int
		)

		if err := rows.Scan(&torrentID, &infoHash, &seeders, &leechers, &snatched, &balance, &freeType); err != nil {
			log.Printf("Warning: Failed to scan torrent row: %v", err)
			continue
		}

		// Keyed by info_hash, which is how announces look torrents up.
		torrent := NewTorrent(torrentID)
		torrent.Completed = uint32(snatched)
		torrent.Balance = balance
		if freeType >= 0 && freeType <= 2 {
			torrent.FreeType = FreeType(freeType)
		}

		l.torrents.Set(infoHash, torrent)
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

	// Only users with a stored passkey can authenticate. Rows without one
	// were written before passkeys were persisted and are unusable.
	rows, err := db.Query(`
		SELECT id, passkey, can_leech, protect_ip
		FROM users
		WHERE passkey != '' AND deleted = 0
		ORDER BY id
	`)
	if err != nil {
		return fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			userID    UserID
			passkey   string
			canLeech  bool
			protectIP bool
		)

		if err := rows.Scan(&userID, &passkey, &canLeech, &protectIP); err != nil {
			return fmt.Errorf("failed to scan user row: %w", err)
		}

		l.users.Set(passkey, NewUser(userID, canLeech, protectIP))
		count++
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to read users: %w", err)
	}

	log.Printf("  Loaded %d users", count)
	return nil
}

// LoadPeers is intentionally a no-op.
//
// Peers are not restored across a restart. The peers table has no port
// column, so a compact IP:port cannot be reconstructed from it, and every
// live client re-announces within announce_interval anyway. Restoring stale
// rows would hand out addresses that no longer serve, which is the very
// problem the reaper exists to prevent.
//
// This previously issued a query naming four columns that do not exist
// (torrent_left, torrent_corrupt, port, timestamp) and discarded the error,
// so it had never loaded a peer.
func (l *Loader) LoadPeers() error {
	return nil
}

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

	// The users table is created by the shard manager's schema, which owns
	// both identity and stats columns. Defining it here too produced two
	// conflicting CREATE TABLE IF NOT EXISTS statements for one name.

	// Create whitelist table
	_, err := db.Exec(`
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
