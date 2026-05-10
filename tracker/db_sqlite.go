package tracker

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite implementation (no CGO!)
)

const (
	// MaxDBSize is the maximum size before rotating to a new database
	MaxDBSize = 84 * 1024 * 1024 * 1024 // 84 GB

	// WALCheckpointPages triggers a checkpoint every N pages
	WALCheckpointPages = 1000
)

// SQLiteShardManager manages multiple SQLite databases with automatic sharding
type SQLiteShardManager struct {
	mu            sync.RWMutex
	currentDB     *sql.DB
	currentPath   string
	currentSize   int64
	historicalDBs map[string]*sql.DB
	dbDir         string

	// Prepared statements for current DB (cached)
	stmtPeer    *sql.Stmt
	stmtUser    *sql.Stmt
	stmtTorrent *sql.Stmt
	stmtSnatch  *sql.Stmt
	stmtToken   *sql.Stmt
}

// NewSQLiteShardManager creates a new SQLite shard manager
func NewSQLiteShardManager(dbDir string) (*SQLiteShardManager, error) {
	sm := &SQLiteShardManager{
		dbDir:         dbDir,
		historicalDBs: make(map[string]*sql.DB),
	}

	// Ensure directory exists
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// Load existing historical databases
	if err := sm.loadHistoricalDBs(); err != nil {
		return nil, fmt.Errorf("failed to load historical databases: %w", err)
	}

	// Open or create current database
	if err := sm.openCurrentDB(); err != nil {
		return nil, fmt.Errorf("failed to open current database: %w", err)
	}

	// Prepare statements
	if err := sm.prepareStatements(); err != nil {
		return nil, fmt.Errorf("failed to prepare statements: %w", err)
	}

	return sm, nil
}

// openDB opens a SQLite database with optimal settings
func (sm *SQLiteShardManager) openDB(path string) (*sql.DB, error) {
	// Connection string with pragmas for performance
	// Using modernc.org/sqlite (pure Go, no CGO dependencies)
	dsn := fmt.Sprintf("file:%s?cache=shared&mode=rwc", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool parameters
	// WAL mode allows multiple concurrent readers
	db.SetMaxOpenConns(25) // Multiple readers + 1 writer
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)

	// Apply PRAGMA settings for performance
	pragmas := []string{
		"PRAGMA journal_mode=WAL",        // Write-Ahead Logging (10× faster writes)
		"PRAGMA synchronous=NORMAL",      // Balance between safety and speed
		"PRAGMA cache_size=-64000",       // 64 MB page cache
		"PRAGMA temp_store=MEMORY",       // Temporary tables in RAM
		"PRAGMA mmap_size=268435456",     // 256 MB memory-mapped I/O
		"PRAGMA page_size=4096",          // Match filesystem block size
		"PRAGMA wal_autocheckpoint=1000", // Checkpoint every 1000 pages (~4 MB)
		"PRAGMA busy_timeout=5000",       // Wait 5s if database is locked
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to execute %s: %w", pragma, err)
		}
	}

	return db, nil
}

// openCurrentDB opens the current active database (creates if needed)
func (sm *SQLiteShardManager) openCurrentDB() error {
	now := time.Now()
	month := now.Format("2006-01")
	path := filepath.Join(sm.dbDir, fmt.Sprintf("ocelot-%s.db", month))

	// Check if we need to rotate (existing DB is too large)
	if sm.currentPath != "" {
		size, err := sm.getDBSize(sm.currentPath)
		if err == nil && size >= MaxDBSize {
			// Close current DB and move to historical
			if err := sm.closeCurrentDB(); err != nil {
				return err
			}

			// Create new database for next period
			nextMonth := now.AddDate(0, 1, 0).Format("2006-01")
			path = filepath.Join(sm.dbDir, fmt.Sprintf("ocelot-%s.db", nextMonth))

			fmt.Printf("Rotating database: %s is full (>84GB), creating %s\n", sm.currentPath, path)
		}
	}

	db, err := sm.openDB(path)
	if err != nil {
		return err
	}

	// Initialize schema if new database
	if err := sm.initSchema(db); err != nil {
		db.Close()
		return fmt.Errorf("failed to initialize schema: %w", err)
	}

	sm.mu.Lock()
	sm.currentDB = db
	sm.currentPath = path
	sm.mu.Unlock()

	fmt.Printf("Opened database: %s\n", path)
	return nil
}

// closeCurrentDB closes the current database and moves it to historical
func (sm *SQLiteShardManager) closeCurrentDB() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.currentDB == nil {
		return nil
	}

	// Close prepared statements
	if sm.stmtPeer != nil {
		sm.stmtPeer.Close()
	}
	if sm.stmtUser != nil {
		sm.stmtUser.Close()
	}
	if sm.stmtTorrent != nil {
		sm.stmtTorrent.Close()
	}
	if sm.stmtSnatch != nil {
		sm.stmtSnatch.Close()
	}
	if sm.stmtToken != nil {
		sm.stmtToken.Close()
	}

	// Checkpoint WAL before closing
	sm.currentDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

	// Move to historical databases
	basename := filepath.Base(sm.currentPath)
	sm.historicalDBs[basename] = sm.currentDB

	sm.currentDB = nil
	sm.currentPath = ""

	return nil
}

// initSchema initializes the database schema
func (sm *SQLiteShardManager) initSchema(db *sql.DB) error {
	schema := `
	-- Peers table (high write frequency)
	-- WITHOUT ROWID optimization for compound primary keys
	CREATE TABLE IF NOT EXISTS peers (
		user_id INTEGER NOT NULL,
		torrent_id INTEGER NOT NULL,
		active INTEGER DEFAULT 1,
		uploaded INTEGER DEFAULT 0,
		downloaded INTEGER DEFAULT 0,
		upspeed INTEGER DEFAULT 0,
		downspeed INTEGER DEFAULT 0,
		remaining INTEGER DEFAULT 0,
		corrupt INTEGER DEFAULT 0,
		timespent INTEGER DEFAULT 0,
		announces INTEGER DEFAULT 1,
		ip TEXT DEFAULT '',
		peer_id BLOB DEFAULT '',
		useragent TEXT DEFAULT '',
		last_announce INTEGER DEFAULT 0,
		PRIMARY KEY (user_id, torrent_id)
	) WITHOUT ROWID;

	-- Indexes for common queries
	CREATE INDEX IF NOT EXISTS idx_peers_torrent ON peers(torrent_id, active);
	CREATE INDEX IF NOT EXISTS idx_peers_last_announce ON peers(last_announce);

	-- Torrents table
	CREATE TABLE IF NOT EXISTS torrents (
		id INTEGER PRIMARY KEY,
		seeders INTEGER DEFAULT 0,
		leechers INTEGER DEFAULT 0,
		snatched INTEGER DEFAULT 0,
		balance INTEGER DEFAULT 0,
		last_action INTEGER DEFAULT 0
	) WITHOUT ROWID;

	-- Users table (upload/download statistics)
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY,
		uploaded INTEGER DEFAULT 0,
		downloaded INTEGER DEFAULT 0
	) WITHOUT ROWID;

	-- Snatches table (torrent completions)
	CREATE TABLE IF NOT EXISTS snatches (
		user_id INTEGER NOT NULL,
		torrent_id INTEGER NOT NULL,
		snatched_time INTEGER NOT NULL,
		ip TEXT DEFAULT '',
		PRIMARY KEY (user_id, torrent_id, snatched_time)
	) WITHOUT ROWID;

	-- Tokens table (freeleech tokens)
	CREATE TABLE IF NOT EXISTS tokens (
		user_id INTEGER NOT NULL,
		torrent_id INTEGER NOT NULL,
		downloaded INTEGER DEFAULT 0,
		PRIMARY KEY (user_id, torrent_id)
	) WITHOUT ROWID;
	`

	_, err := db.Exec(schema)
	return err
}

// prepareStatements prepares commonly used statements for better performance
func (sm *SQLiteShardManager) prepareStatements() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var err error

	// Peer insert/update statement
	peerQuery := `
	INSERT INTO peers (user_id, torrent_id, active, uploaded, downloaded, upspeed, downspeed, remaining, corrupt, timespent, announces, ip, peer_id, useragent, last_announce)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(user_id, torrent_id) DO UPDATE SET
		active=excluded.active,
		uploaded=excluded.uploaded,
		downloaded=excluded.downloaded,
		upspeed=excluded.upspeed,
		downspeed=excluded.downspeed,
		remaining=excluded.remaining,
		corrupt=excluded.corrupt,
		timespent=excluded.timespent,
		announces=excluded.announces,
		ip=excluded.ip,
		peer_id=excluded.peer_id,
		useragent=excluded.useragent,
		last_announce=excluded.last_announce
	`
	sm.stmtPeer, err = sm.currentDB.Prepare(peerQuery)
	if err != nil {
		return fmt.Errorf("failed to prepare peer statement: %w", err)
	}

	// User stats statement
	userQuery := `
	INSERT INTO users (id, uploaded, downloaded)
	VALUES (?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		uploaded=users.uploaded+excluded.uploaded,
		downloaded=users.downloaded+excluded.downloaded
	`
	sm.stmtUser, err = sm.currentDB.Prepare(userQuery)
	if err != nil {
		return fmt.Errorf("failed to prepare user statement: %w", err)
	}

	// Torrent stats statement
	torrentQuery := `
	INSERT INTO torrents (id, seeders, leechers, snatched, balance, last_action)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		seeders=excluded.seeders,
		leechers=excluded.leechers,
		snatched=torrents.snatched+excluded.snatched,
		balance=excluded.balance,
		last_action=excluded.last_action
	`
	sm.stmtTorrent, err = sm.currentDB.Prepare(torrentQuery)
	if err != nil {
		return fmt.Errorf("failed to prepare torrent statement: %w", err)
	}

	// Snatch statement
	snatchQuery := `INSERT OR IGNORE INTO snatches (user_id, torrent_id, snatched_time, ip) VALUES (?, ?, ?, ?)`
	sm.stmtSnatch, err = sm.currentDB.Prepare(snatchQuery)
	if err != nil {
		return fmt.Errorf("failed to prepare snatch statement: %w", err)
	}

	// Token statement
	tokenQuery := `
	INSERT INTO tokens (user_id, torrent_id, downloaded)
	VALUES (?, ?, ?)
	ON CONFLICT(user_id, torrent_id) DO UPDATE SET
		downloaded=tokens.downloaded+excluded.downloaded
	`
	sm.stmtToken, err = sm.currentDB.Prepare(tokenQuery)
	if err != nil {
		return fmt.Errorf("failed to prepare token statement: %w", err)
	}

	return nil
}

// loadHistoricalDBs opens all existing historical database files
func (sm *SQLiteShardManager) loadHistoricalDBs() error {
	entries, err := os.ReadDir(sm.dbDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory doesn't exist yet, that's fine
		}
		return err
	}

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".db" {
			path := filepath.Join(sm.dbDir, entry.Name())

			// Skip if this might be the current DB
			month := time.Now().Format("2006-01")
			if entry.Name() == fmt.Sprintf("ocelot-%s.db", month) {
				continue
			}

			db, err := sm.openDB(path)
			if err != nil {
				fmt.Printf("Warning: failed to open historical DB %s: %v\n", path, err)
				continue
			}

			sm.historicalDBs[entry.Name()] = db
			fmt.Printf("Loaded historical database: %s\n", entry.Name())
		}
	}

	return nil
}

// getDBSize returns the size of a database file in bytes
func (sm *SQLiteShardManager) getDBSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// Write Operations (all go to current DB only)

// RecordPeer records or updates a peer's statistics
func (sm *SQLiteShardManager) RecordPeer(userID UserID, torrentID TorrentID, active int, uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64, announceTime, announces uint32, ip, peerID, userAgent string) error {
	sm.mu.RLock()
	stmt := sm.stmtPeer
	sm.mu.RUnlock()

	_, err := stmt.Exec(userID, torrentID, active, uploaded, downloaded, upSpeed, downSpeed, left, corrupt, announceTime, announces, ip, peerID, userAgent, time.Now().Unix())
	return err
}

// RecordPeerLight records a lightweight peer update (no full stats)
func (sm *SQLiteShardManager) RecordPeerLight(userID UserID, torrentID TorrentID, announceTime, announces uint32, peerID string) error {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()

	query := `UPDATE peers SET timespent=?, announces=?, last_announce=? WHERE user_id=? AND torrent_id=?`
	_, err := db.Exec(query, announceTime, announces, time.Now().Unix(), userID, torrentID)
	return err
}

// RecordUserStats updates a user's upload/download statistics
func (sm *SQLiteShardManager) RecordUserStats(userID UserID, uploaded, downloaded int64) error {
	sm.mu.RLock()
	stmt := sm.stmtUser
	sm.mu.RUnlock()

	_, err := stmt.Exec(userID, uploaded, downloaded)
	return err
}

// RecordTorrent updates a torrent's statistics
func (sm *SQLiteShardManager) RecordTorrent(torrentID TorrentID, seeders, leechers uint32, snatched int, balance int64) error {
	sm.mu.RLock()
	stmt := sm.stmtTorrent
	sm.mu.RUnlock()

	_, err := stmt.Exec(torrentID, seeders, leechers, snatched, balance, time.Now().Unix())
	return err
}

// RecordSnatch records a torrent completion (snatch)
func (sm *SQLiteShardManager) RecordSnatch(userID UserID, torrentID TorrentID, snatchTime time.Time, ip string) error {
	sm.mu.RLock()
	stmt := sm.stmtSnatch
	sm.mu.RUnlock()

	_, err := stmt.Exec(userID, torrentID, snatchTime.Unix(), ip)
	return err
}

// RecordToken records freeleech token usage
func (sm *SQLiteShardManager) RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error {
	sm.mu.RLock()
	stmt := sm.stmtToken
	sm.mu.RUnlock()

	_, err := stmt.Exec(userID, torrentID, downloaded)
	return err
}

// Read Operations (may query historical DBs)

// GetUserStats retrieves a user's total upload/download from all databases
func (sm *SQLiteShardManager) GetUserStats(userID uint32) (uploaded, downloaded int64, err error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	query := `SELECT COALESCE(SUM(uploaded), 0), COALESCE(SUM(downloaded), 0) FROM users WHERE id = ?`

	// Query current DB
	err = sm.currentDB.QueryRow(query, userID).Scan(&uploaded, &downloaded)
	if err != nil && err != sql.ErrNoRows {
		return 0, 0, err
	}

	// Query historical DBs (rare, but needed for complete stats)
	for _, db := range sm.historicalDBs {
		var histUp, histDown int64
		err := db.QueryRow(query, userID).Scan(&histUp, &histDown)
		if err == nil {
			uploaded += histUp
			downloaded += histDown
		}
	}

	return uploaded, downloaded, nil
}

// Maintenance Operations

// CheckpointWAL forces a WAL checkpoint (moves WAL → main DB)
func (sm *SQLiteShardManager) CheckpointWAL() error {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()

	_, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// CheckRotation checks if the current DB needs rotation (>84GB)
func (sm *SQLiteShardManager) CheckRotation() error {
	sm.mu.RLock()
	currentPath := sm.currentPath
	sm.mu.RUnlock()

	if currentPath == "" {
		return nil
	}

	size, err := sm.getDBSize(currentPath)
	if err != nil {
		return err
	}

	if size >= MaxDBSize {
		fmt.Printf("Database rotation triggered: %s is %d bytes (limit: %d)\n", currentPath, size, MaxDBSize)
		return sm.openCurrentDB() // Automatically rotates
	}

	return nil
}

// GetDBStats returns statistics about the database files
func (sm *SQLiteShardManager) GetDBStats() (currentSize int64, numHistorical int, totalSize int64, err error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Current DB size
	if sm.currentPath != "" {
		currentSize, err = sm.getDBSize(sm.currentPath)
		if err != nil {
			return 0, 0, 0, err
		}
		totalSize = currentSize
	}

	// Historical DB sizes
	numHistorical = len(sm.historicalDBs)
	for name := range sm.historicalDBs {
		path := filepath.Join(sm.dbDir, name)
		size, err := sm.getDBSize(path)
		if err == nil {
			totalSize += size
		}
	}

	return currentSize, numHistorical, totalSize, nil
}

// Close closes all database connections and checkpoints WAL
func (sm *SQLiteShardManager) Close() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Close prepared statements
	if sm.stmtPeer != nil {
		sm.stmtPeer.Close()
	}
	if sm.stmtUser != nil {
		sm.stmtUser.Close()
	}
	if sm.stmtTorrent != nil {
		sm.stmtTorrent.Close()
	}
	if sm.stmtSnatch != nil {
		sm.stmtSnatch.Close()
	}
	if sm.stmtToken != nil {
		sm.stmtToken.Close()
	}

	// Checkpoint and close current DB
	if sm.currentDB != nil {
		sm.currentDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		sm.currentDB.Close()
	}

	// Close historical DBs
	for _, db := range sm.historicalDBs {
		db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		db.Close()
	}

	fmt.Println("All databases closed successfully")
	return nil
}
