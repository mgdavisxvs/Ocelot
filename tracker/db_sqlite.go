package tracker

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	MaxDBSize          = 84 * 1024 * 1024 * 1024 // 84 GB per shard
	WALCheckpointPages = 1000
)

// torrentLoadRow is the data transfer object for restoring a torrent from DB.
type torrentLoadRow struct {
	id        TorrentID
	infoHash  string
	completed uint32
	balance   int64
	freeType  FreeType
}

// userLoadRow is the data transfer object for restoring a user from DB.
type userLoadRow struct {
	id        UserID
	passkey   string
	canLeech  bool
	protectIP bool
}

// SQLiteShardManager manages sharded SQLite databases with automatic rotation
// at 84 GB per shard and WAL mode for concurrent read performance.
type SQLiteShardManager struct {
	mu            sync.RWMutex
	currentDB     *sql.DB
	currentPath   string
	currentSize   int64
	historicalDBs map[string]*sql.DB
	dbDir         string

	stmtPeer    *sql.Stmt
	stmtUser    *sql.Stmt
	stmtTorrent *sql.Stmt
	stmtSnatch  *sql.Stmt
	stmtToken   *sql.Stmt
}

func NewSQLiteShardManager(dbDir string) (*SQLiteShardManager, error) {
	sm := &SQLiteShardManager{
		dbDir:         dbDir,
		historicalDBs: make(map[string]*sql.DB),
	}
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	if err := sm.loadHistoricalDBs(); err != nil {
		return nil, fmt.Errorf("load historical dbs: %w", err)
	}
	if err := sm.openCurrentDB(); err != nil {
		return nil, fmt.Errorf("open current db: %w", err)
	}
	if err := sm.prepareStatements(); err != nil {
		return nil, fmt.Errorf("prepare statements: %w", err)
	}
	return sm, nil
}

func (sm *SQLiteShardManager) openDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?cache=shared&mode=rwc", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-64000",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA mmap_size=268435456",
		"PRAGMA page_size=4096",
		"PRAGMA wal_autocheckpoint=1000",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
	}
	return db, nil
}

func (sm *SQLiteShardManager) openCurrentDB() error {
	now := time.Now()
	month := now.Format("2006-01")
	path := filepath.Join(sm.dbDir, fmt.Sprintf("ocelot-%s.db", month))

	if sm.currentPath != "" {
		size, err := sm.getDBSize(sm.currentPath)
		if err == nil && size >= MaxDBSize {
			if err := sm.closeCurrentDB(); err != nil {
				return err
			}
			nextMonth := now.AddDate(0, 1, 0).Format("2006-01")
			path = filepath.Join(sm.dbDir, fmt.Sprintf("ocelot-%s.db", nextMonth))
			fmt.Printf("Rotating DB: %s is full, creating %s\n", sm.currentPath, path)
		}
	}

	db, err := sm.openDB(path)
	if err != nil {
		return err
	}
	if err := sm.initSchema(db); err != nil {
		db.Close()
		return fmt.Errorf("init schema: %w", err)
	}

	sm.mu.Lock()
	sm.currentDB = db
	sm.currentPath = path
	sm.mu.Unlock()

	fmt.Printf("Opened database: %s\n", path)
	return nil
}

func (sm *SQLiteShardManager) closeCurrentDB() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.currentDB == nil {
		return nil
	}
	for _, stmt := range []*sql.Stmt{sm.stmtPeer, sm.stmtUser, sm.stmtTorrent, sm.stmtSnatch, sm.stmtToken} {
		if stmt != nil {
			stmt.Close()
		}
	}
	sm.currentDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	basename := filepath.Base(sm.currentPath)
	sm.historicalDBs[basename] = sm.currentDB
	sm.currentDB = nil
	sm.currentPath = ""
	return nil
}

func (sm *SQLiteShardManager) initSchema(db *sql.DB) error {
	_, err := db.Exec(`
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

	CREATE INDEX IF NOT EXISTS idx_peers_torrent_cover ON peers(
		torrent_id, user_id, last_announce, uploaded, downloaded, remaining
	) WHERE active = 1;
	CREATE INDEX IF NOT EXISTS idx_peers_last_announce ON peers(last_announce) WHERE active = 1;
	CREATE INDEX IF NOT EXISTS idx_peers_user ON peers(user_id, torrent_id, last_announce);

	CREATE TABLE IF NOT EXISTS torrents (
		id INTEGER PRIMARY KEY,
		seeders INTEGER DEFAULT 0,
		leechers INTEGER DEFAULT 0,
		snatched INTEGER DEFAULT 0,
		balance INTEGER DEFAULT 0,
		free_type INTEGER DEFAULT 0,
		last_action INTEGER DEFAULT 0
	) WITHOUT ROWID;

	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY,
		uploaded INTEGER DEFAULT 0,
		downloaded INTEGER DEFAULT 0
	) WITHOUT ROWID;

	CREATE TABLE IF NOT EXISTS snatches (
		user_id INTEGER NOT NULL,
		torrent_id INTEGER NOT NULL,
		snatched_time INTEGER NOT NULL,
		ip TEXT DEFAULT '',
		PRIMARY KEY (user_id, torrent_id, snatched_time)
	) WITHOUT ROWID;

	CREATE TABLE IF NOT EXISTS tokens (
		user_id INTEGER NOT NULL,
		torrent_id INTEGER NOT NULL,
		downloaded INTEGER DEFAULT 0,
		PRIMARY KEY (user_id, torrent_id)
	) WITHOUT ROWID;

	-- Torrent info_hash ↔ id mapping for state reload after restart.
	CREATE TABLE IF NOT EXISTS torrent_hashes (
		torrent_id INTEGER PRIMARY KEY,
		info_hash TEXT NOT NULL UNIQUE
	) WITHOUT ROWID;

	-- User passkeys and permissions for state reload after restart.
	CREATE TABLE IF NOT EXISTS user_passkeys (
		user_id INTEGER PRIMARY KEY,
		passkey TEXT NOT NULL UNIQUE,
		can_leech INTEGER NOT NULL DEFAULT 1,
		protect_ip INTEGER NOT NULL DEFAULT 0
	) WITHOUT ROWID;

	-- BitTorrent client whitelist (peer_id prefixes).
	CREATE TABLE IF NOT EXISTS whitelist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		prefix TEXT NOT NULL UNIQUE
	);
	`)
	return err
}

func (sm *SQLiteShardManager) prepareStatements() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var err error

	sm.stmtPeer, err = sm.currentDB.Prepare(`
	INSERT INTO peers (user_id,torrent_id,active,uploaded,downloaded,upspeed,downspeed,remaining,corrupt,timespent,announces,ip,peer_id,useragent,last_announce)
	VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(user_id,torrent_id) DO UPDATE SET
		active=excluded.active, uploaded=excluded.uploaded, downloaded=excluded.downloaded,
		upspeed=excluded.upspeed, downspeed=excluded.downspeed, remaining=excluded.remaining,
		corrupt=excluded.corrupt, timespent=excluded.timespent, announces=excluded.announces,
		ip=excluded.ip, peer_id=excluded.peer_id, useragent=excluded.useragent,
		last_announce=excluded.last_announce`)
	if err != nil {
		return fmt.Errorf("prepare peer stmt: %w", err)
	}

	sm.stmtUser, err = sm.currentDB.Prepare(`
	INSERT INTO users (id,uploaded,downloaded) VALUES (?,?,?)
	ON CONFLICT(id) DO UPDATE SET
		uploaded=users.uploaded+excluded.uploaded,
		downloaded=users.downloaded+excluded.downloaded`)
	if err != nil {
		return fmt.Errorf("prepare user stmt: %w", err)
	}

	sm.stmtTorrent, err = sm.currentDB.Prepare(`
	INSERT INTO torrents (id,seeders,leechers,snatched,balance,last_action)
	VALUES (?,?,?,?,?,?)
	ON CONFLICT(id) DO UPDATE SET
		seeders=excluded.seeders, leechers=excluded.leechers,
		snatched=torrents.snatched+excluded.snatched,
		balance=excluded.balance, last_action=excluded.last_action`)
	if err != nil {
		return fmt.Errorf("prepare torrent stmt: %w", err)
	}

	sm.stmtSnatch, err = sm.currentDB.Prepare(
		`INSERT OR IGNORE INTO snatches (user_id,torrent_id,snatched_time,ip) VALUES (?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare snatch stmt: %w", err)
	}

	sm.stmtToken, err = sm.currentDB.Prepare(`
	INSERT INTO tokens (user_id,torrent_id,downloaded) VALUES (?,?,?)
	ON CONFLICT(user_id,torrent_id) DO UPDATE SET downloaded=tokens.downloaded+excluded.downloaded`)
	if err != nil {
		return fmt.Errorf("prepare token stmt: %w", err)
	}

	return nil
}

func (sm *SQLiteShardManager) loadHistoricalDBs() error {
	entries, err := os.ReadDir(sm.dbDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	month := time.Now().Format("2006-01")
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".db" {
			continue
		}
		if entry.Name() == fmt.Sprintf("ocelot-%s.db", month) {
			continue
		}
		path := filepath.Join(sm.dbDir, entry.Name())
		db, err := sm.openDB(path)
		if err != nil {
			fmt.Printf("Warning: failed to open historical DB %s: %v\n", path, err)
			continue
		}
		sm.historicalDBs[entry.Name()] = db
		fmt.Printf("Loaded historical database: %s\n", path)
	}
	return nil
}

func (sm *SQLiteShardManager) getDBSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// ── Write operations ──────────────────────────────────────────────────────────

func (sm *SQLiteShardManager) RecordPeer(userID UserID, torrentID TorrentID, active int, uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64, announceTime, announces uint32, ip, peerID, userAgent string) error {
	sm.mu.RLock()
	stmt := sm.stmtPeer
	sm.mu.RUnlock()
	_, err := stmt.Exec(userID, torrentID, active, uploaded, downloaded, upSpeed, downSpeed, left, corrupt, announceTime, announces, ip, peerID, userAgent, time.Now().Unix())
	return err
}

func (sm *SQLiteShardManager) RecordPeerLight(userID UserID, torrentID TorrentID, announceTime, announces uint32, peerID string) error {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()
	_, err := db.Exec(`UPDATE peers SET timespent=?,announces=?,last_announce=? WHERE user_id=? AND torrent_id=?`,
		announceTime, announces, time.Now().Unix(), userID, torrentID)
	return err
}

func (sm *SQLiteShardManager) RecordUserStats(userID UserID, uploaded, downloaded int64) error {
	sm.mu.RLock()
	stmt := sm.stmtUser
	sm.mu.RUnlock()
	_, err := stmt.Exec(userID, uploaded, downloaded)
	return err
}

func (sm *SQLiteShardManager) RecordTorrent(torrentID TorrentID, seeders, leechers uint32, snatched int, balance int64) error {
	sm.mu.RLock()
	stmt := sm.stmtTorrent
	sm.mu.RUnlock()
	_, err := stmt.Exec(torrentID, seeders, leechers, snatched, balance, time.Now().Unix())
	return err
}

func (sm *SQLiteShardManager) RecordSnatch(userID UserID, torrentID TorrentID, snatchTime time.Time, ip string) error {
	sm.mu.RLock()
	stmt := sm.stmtSnatch
	sm.mu.RUnlock()
	_, err := stmt.Exec(userID, torrentID, snatchTime.Unix(), ip)
	return err
}

func (sm *SQLiteShardManager) RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error {
	sm.mu.RLock()
	stmt := sm.stmtToken
	sm.mu.RUnlock()
	_, err := stmt.Exec(userID, torrentID, downloaded)
	return err
}

// RecordTorrentHash persists the info_hash ↔ torrent_id mapping.
func (sm *SQLiteShardManager) RecordTorrentHash(id TorrentID, infoHash string) error {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()
	_, err := db.Exec(`INSERT OR REPLACE INTO torrent_hashes (torrent_id, info_hash) VALUES (?, ?)`, id, infoHash)
	return err
}

// RecordUserPasskey persists a user's passkey and permissions.
func (sm *SQLiteShardManager) RecordUserPasskey(id UserID, passkey string, canLeech, protectIP bool) error {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()
	canLeechInt := 0
	if canLeech {
		canLeechInt = 1
	}
	protectIPInt := 0
	if protectIP {
		protectIPInt = 1
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO user_passkeys (user_id, passkey, can_leech, protect_ip) VALUES (?, ?, ?, ?)`,
		id, passkey, canLeechInt, protectIPInt)
	return err
}

// AddWhitelistEntry adds a peer_id prefix to the persisted whitelist.
func (sm *SQLiteShardManager) AddWhitelistEntry(prefix string) error {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()
	_, err := db.Exec(`INSERT OR IGNORE INTO whitelist (prefix) VALUES (?)`, prefix)
	return err
}

// RemoveWhitelistEntry removes a peer_id prefix from the persisted whitelist.
func (sm *SQLiteShardManager) RemoveWhitelistEntry(prefix string) error {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()
	_, err := db.Exec(`DELETE FROM whitelist WHERE prefix = ?`, prefix)
	return err
}

// ── Read / load operations ────────────────────────────────────────────────────

// LoadTorrents loads all torrent metadata for restart state recovery.
func (sm *SQLiteShardManager) LoadTorrents() ([]torrentLoadRow, error) {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()

	rows, err := db.Query(`
		SELECT t.id, h.info_hash, t.snatched, t.balance, t.free_type
		FROM torrents t
		JOIN torrent_hashes h ON t.id = h.torrent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []torrentLoadRow
	for rows.Next() {
		var r torrentLoadRow
		var ft uint8
		if err := rows.Scan(&r.id, &r.infoHash, &r.completed, &r.balance, &ft); err != nil {
			return nil, err
		}
		r.freeType = FreeType(ft)
		result = append(result, r)
	}
	return result, rows.Err()
}

// LoadUsers loads all user passkeys and permissions for restart state recovery.
func (sm *SQLiteShardManager) LoadUsers() ([]userLoadRow, error) {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()

	rows, err := db.Query(`SELECT user_id, passkey, can_leech, protect_ip FROM user_passkeys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []userLoadRow
	for rows.Next() {
		var r userLoadRow
		var canLeech, protectIP int
		if err := rows.Scan(&r.id, &r.passkey, &canLeech, &protectIP); err != nil {
			return nil, err
		}
		r.canLeech = canLeech != 0
		r.protectIP = protectIP != 0
		result = append(result, r)
	}
	return result, rows.Err()
}

// LoadWhitelist returns all persisted peer_id prefixes.
func (sm *SQLiteShardManager) LoadWhitelist() ([]string, error) {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()

	rows, err := db.Query(`SELECT prefix FROM whitelist ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prefixes []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		prefixes = append(prefixes, p)
	}
	return prefixes, rows.Err()
}

// LoadTokens returns all active freeleech tokens keyed by info_hash.
func (sm *SQLiteShardManager) LoadTokens() (map[string][]UserID, error) {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()

	rows, err := db.Query(`
		SELECT h.info_hash, t.user_id
		FROM tokens t
		JOIN torrent_hashes h ON t.torrent_id = h.torrent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]UserID)
	for rows.Next() {
		var infoHash string
		var userID UserID
		if err := rows.Scan(&infoHash, &userID); err != nil {
			return nil, err
		}
		result[infoHash] = append(result[infoHash], userID)
	}
	return result, rows.Err()
}

// ── Maintenance ───────────────────────────────────────────────────────────────

func (sm *SQLiteShardManager) CheckpointWAL() error {
	sm.mu.RLock()
	db := sm.currentDB
	sm.mu.RUnlock()
	_, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

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
		fmt.Printf("DB rotation: %s is %d bytes (limit %d)\n", currentPath, size, MaxDBSize)
		return sm.openCurrentDB()
	}
	return nil
}

func (sm *SQLiteShardManager) GetUserStats(userID uint32) (uploaded, downloaded int64, err error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	query := `SELECT COALESCE(SUM(uploaded),0), COALESCE(SUM(downloaded),0) FROM users WHERE id=?`
	err = sm.currentDB.QueryRow(query, userID).Scan(&uploaded, &downloaded)
	if err != nil && err != sql.ErrNoRows {
		return 0, 0, err
	}
	for _, db := range sm.historicalDBs {
		var hu, hd int64
		if e := db.QueryRow(query, userID).Scan(&hu, &hd); e == nil {
			uploaded += hu
			downloaded += hd
		}
	}
	return uploaded, downloaded, nil
}

func (sm *SQLiteShardManager) GetDBStats() (currentSize int64, numHistorical int, totalSize int64, err error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.currentPath != "" {
		currentSize, err = sm.getDBSize(sm.currentPath)
		if err != nil {
			return
		}
		totalSize = currentSize
	}
	numHistorical = len(sm.historicalDBs)
	for name := range sm.historicalDBs {
		path := filepath.Join(sm.dbDir, name)
		if size, e := sm.getDBSize(path); e == nil {
			totalSize += size
		}
	}
	return
}

func (sm *SQLiteShardManager) Close() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, stmt := range []*sql.Stmt{sm.stmtPeer, sm.stmtUser, sm.stmtTorrent, sm.stmtSnatch, sm.stmtToken} {
		if stmt != nil {
			stmt.Close()
		}
	}
	if sm.currentDB != nil {
		sm.currentDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		sm.currentDB.Close()
	}
	for _, db := range sm.historicalDBs {
		db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		db.Close()
	}
	fmt.Println("All databases closed.")
	return nil
}
