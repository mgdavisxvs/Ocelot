package tracker

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PostgresDB provides PostgreSQL backend for scalable deployments
type PostgresDB struct {
	db      *sql.DB
	logger  *Logger
	metrics *MetricsRecorder
}

// PostgresConfig holds PostgreSQL configuration
type PostgresConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
	PoolSize int
}

// NewPostgresDB creates a new PostgreSQL connection
func NewPostgresDB(config PostgresConfig) (*PostgresDB, error) {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.User, config.Password, config.Database, config.SSLMode)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Connection pool settings
	db.SetMaxOpenConns(config.PoolSize)
	db.SetMaxIdleConns(config.PoolSize / 4)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &PostgresDB{
		db:      db,
		logger:  GetDefaultLogger(),
		metrics: GetMetricsRecorder(),
	}, nil
}

// CreateSchema creates the PostgreSQL schema
func (p *PostgresDB) CreateSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS torrents (
		id SERIAL PRIMARY KEY,
		info_hash VARCHAR(40) UNIQUE NOT NULL,
		torrent_id INTEGER,
		free_type INTEGER DEFAULT 0,
		seeders INTEGER DEFAULT 0,
		leechers INTEGER DEFAULT 0,
		snatched INTEGER DEFAULT 0,
		created_at BIGINT NOT NULL,
		last_action BIGINT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_torrents_info_hash ON torrents(info_hash);
	CREATE INDEX IF NOT EXISTS idx_torrents_torrent_id ON torrents(torrent_id);

	CREATE TABLE IF NOT EXISTS peers (
		id SERIAL PRIMARY KEY,
		info_hash VARCHAR(40) NOT NULL,
		peer_id VARCHAR(20) NOT NULL,
		user_id INTEGER NOT NULL,
		ip VARCHAR(45) NOT NULL,
		port INTEGER NOT NULL,
		uploaded BIGINT DEFAULT 0,
		downloaded BIGINT DEFAULT 0,
		remaining BIGINT DEFAULT 0,
		last_announce BIGINT NOT NULL,
		active BOOLEAN DEFAULT TRUE,
		UNIQUE(info_hash, peer_id)
	);

	CREATE INDEX IF NOT EXISTS idx_peers_info_hash ON peers(info_hash);
	CREATE INDEX IF NOT EXISTS idx_peers_user ON peers(user_id);
	CREATE INDEX IF NOT EXISTS idx_peers_last_announce ON peers(last_announce);

	-- Covering index for peer queries
	CREATE INDEX IF NOT EXISTS idx_peers_torrent_cover ON peers(
		info_hash, user_id, last_announce, uploaded, downloaded, remaining
	) WHERE active = TRUE;

	CREATE TABLE IF NOT EXISTS users (
		id SERIAL PRIMARY KEY,
		user_id INTEGER UNIQUE NOT NULL,
		passkey VARCHAR(32) UNIQUE NOT NULL,
		can_leech BOOLEAN DEFAULT TRUE,
		protect_ip BOOLEAN DEFAULT FALSE,
		uploaded BIGINT DEFAULT 0,
		downloaded BIGINT DEFAULT 0,
		leeching INTEGER DEFAULT 0,
		seeding INTEGER DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_users_passkey ON users(passkey);
	CREATE INDEX IF NOT EXISTS idx_users_user_id ON users(user_id);

	CREATE TABLE IF NOT EXISTS whitelist (
		id SERIAL PRIMARY KEY,
		peer_id_prefix VARCHAR(8) UNIQUE NOT NULL
	);

	CREATE TABLE IF NOT EXISTS audit_log (
		id SERIAL PRIMARY KEY,
		timestamp BIGINT NOT NULL,
		user_id INTEGER,
		action VARCHAR(100) NOT NULL,
		resource_type VARCHAR(50) NOT NULL,
		resource_id VARCHAR(100),
		ip_address VARCHAR(45),
		success BOOLEAN NOT NULL,
		error_message TEXT,
		metadata JSONB
	);

	CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp);
	CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_log(user_id);
	CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action);

	CREATE TABLE IF NOT EXISTS api_keys (
		id SERIAL PRIMARY KEY,
		key_hash VARCHAR(64) UNIQUE NOT NULL,
		user_id INTEGER NOT NULL,
		permissions TEXT NOT NULL,
		created_at BIGINT NOT NULL,
		expires_at BIGINT,
		last_used_at BIGINT,
		revoked BOOLEAN DEFAULT FALSE
	);

	CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);
	CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id);
	`

	_, err := p.db.Exec(schema)
	return err
}

// StorePeer stores a peer announce in PostgreSQL
func (p *PostgresDB) StorePeer(peer *Peer) error {
	query := `INSERT INTO peers
		(info_hash, peer_id, user_id, ip, port, uploaded, downloaded, remaining, last_announce, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, TRUE)
		ON CONFLICT (info_hash, peer_id)
		DO UPDATE SET
			uploaded = $6,
			downloaded = $7,
			remaining = $8,
			last_announce = $9,
			active = TRUE`

	start := time.Now()
	_, err := p.db.Exec(query,
		peer.InfoHash,
		string(peer.PeerID),
		peer.UserID,
		peer.IP.String(),
		peer.Port,
		peer.Uploaded.Load(),
		peer.Downloaded.Load(),
		peer.Left.Load(),
		time.Now().Unix(),
	)

	p.metrics.RecordDBQuery("store_peer", time.Since(start), err)
	return err
}

// LoadTorrents loads all active torrents
func (p *PostgresDB) LoadTorrents() ([]*Torrent, error) {
	query := `SELECT info_hash, torrent_id, free_type, seeders, leechers, snatched
		FROM torrents
		WHERE last_action > $1`

	// Load torrents active in last 30 days
	cutoff := time.Now().AddDate(0, 0, -30).Unix()

	start := time.Now()
	rows, err := p.db.Query(query, cutoff)
	if err != nil {
		p.metrics.RecordDBQuery("load_torrents", time.Since(start), err)
		return nil, err
	}
	defer rows.Close()

	torrents := []*Torrent{}
	for rows.Next() {
		var t Torrent
		var infoHash string
		var torrentID sql.NullInt64

		err := rows.Scan(&infoHash, &torrentID, &t.FreeType, &t.Seeders, &t.Leechers, &t.Snatched)
		if err != nil {
			continue
		}

		t.InfoHash = infoHash
		if torrentID.Valid {
			t.ID = TorrentID(torrentID.Int64)
		}

		torrents = append(torrents, &t)
	}

	p.metrics.RecordDBQuery("load_torrents", time.Since(start), nil)
	return torrents, nil
}

// Close closes the database connection
func (p *PostgresDB) Close() error {
	return p.db.Close()
}

// Ping tests the database connection
func (p *PostgresDB) Ping() error {
	return p.db.Ping()
}

// PostgresCluster manages read replicas
type PostgresCluster struct {
	master   *sql.DB
	replicas []*sql.DB
	nextIdx  uint32
	logger   *Logger
}

// NewPostgresCluster creates a cluster with master and read replicas
func NewPostgresCluster(masterConfig PostgresConfig, replicaConfigs []PostgresConfig) (*PostgresCluster, error) {
	master, err := sql.Open("postgres", formatConnStr(masterConfig))
	if err != nil {
		return nil, err
	}

	replicas := make([]*sql.DB, len(replicaConfigs))
	for i, config := range replicaConfigs {
		replica, err := sql.Open("postgres", formatConnStr(config))
		if err != nil {
			return nil, err
		}
		replicas[i] = replica
	}

	return &PostgresCluster{
		master:   master,
		replicas: replicas,
		logger:   GetDefaultLogger(),
	}, nil
}

// Write executes a write query on the master
func (c *PostgresCluster) Write(query string, args ...interface{}) (sql.Result, error) {
	return c.master.Exec(query, args...)
}

// Read executes a read query on a replica (round-robin)
func (c *PostgresCluster) Read(query string, args ...interface{}) (*sql.Rows, error) {
	if len(c.replicas) == 0 {
		return c.master.Query(query, args...)
	}

	// Simple round-robin selection
	idx := int(c.nextIdx) % len(c.replicas)
	c.nextIdx++

	return c.replicas[idx].Query(query, args...)
}

func formatConnStr(config PostgresConfig) string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.User, config.Password, config.Database, config.SSLMode)
}
