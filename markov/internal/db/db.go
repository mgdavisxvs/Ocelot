package db

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/mgdavisxvs/ocelot/markov/internal/config"

	_ "modernc.org/sqlite"
)

// DB wraps a *sql.DB with helpers for the Markov service.
type DB struct {
	pool *sql.DB
	cfg  *config.Config
}

func Open(cfg *config.Config) (*DB, error) {
	// Open the existing ocelot SQLite shard; WAL mode is already set by the tracker.
	pool, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	// SQLite performs best with a single writer connection.
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	if err := pool.Ping(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	d := &DB{pool: pool, cfg: cfg}
	if err := d.createSchema(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	slog.Info("database connected", "path", cfg.DBPath)
	return d, nil
}

func (d *DB) Close() error { return d.pool.Close() }

func (d *DB) createSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS markov_chain_counts (
			chain_name  TEXT    NOT NULL,
			from_state  INTEGER NOT NULL,
			to_state    INTEGER NOT NULL,
			count       REAL    NOT NULL DEFAULT 0,
			PRIMARY KEY (chain_name, from_state, to_state)
		)`,

		`CREATE TABLE IF NOT EXISTS markov_peer_states (
			torrent_id  INTEGER NOT NULL,
			uid         INTEGER NOT NULL,
			state       INTEGER NOT NULL,
			observed_at INTEGER NOT NULL,
			PRIMARY KEY (torrent_id, uid)
		)`,

		`CREATE TABLE IF NOT EXISTS markov_torrent_states (
			torrent_id  INTEGER NOT NULL PRIMARY KEY,
			state       INTEGER NOT NULL,
			observed_at INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS markov_user_states (
			uid         INTEGER NOT NULL PRIMARY KEY,
			state       INTEGER NOT NULL,
			path_json   TEXT,
			observed_at INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS markov_predictions (
			torrent_id           INTEGER NOT NULL PRIMARY KEY,
			health_state         INTEGER NOT NULL,
			pi_json              TEXT    NOT NULL,
			pi_24h_json          TEXT    NOT NULL,
			pi_72h_json          TEXT    NOT NULL,
			dead_prob_24h        REAL    NOT NULL,
			dead_prob_72h        REAL    NOT NULL,
			expected_dead_hours  REAL    NOT NULL,
			entropy              REAL    NOT NULL,
			recommended_interval INTEGER NOT NULL,
			updated_at           INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS markov_user_anomaly (
			uid                  INTEGER NOT NULL PRIMARY KEY,
			anomaly_score        REAL    NOT NULL,
			path_log_likelihood  REAL    NOT NULL,
			flagged              INTEGER NOT NULL DEFAULT 0,
			updated_at           INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS markov_freeleech_candidates (
			torrent_id     INTEGER NOT NULL PRIMARY KEY,
			priority_score REAL    NOT NULL,
			dead_prob_72h  REAL    NOT NULL,
			recommended    INTEGER NOT NULL DEFAULT 0,
			updated_at     INTEGER NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := d.pool.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s[:40], err)
		}
	}
	return nil
}
