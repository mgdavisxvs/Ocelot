package db

import (
	"context"
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

// ExpireDeadPeerStates removes peer state rows in PeerDead state (4) older than olderThanUnix.
// Note: state=4 here is PeerDead (peer lifecycle), not TorrentUnavailable (torrent health).
// Called once per persist cycle to bound table growth (FR-008).
func (d *DB) ExpireDeadPeerStates(ctx context.Context, olderThanUnix int64) error {
	_, err := d.pool.ExecContext(ctx,
		`DELETE FROM markov_peer_states WHERE state=4 AND observed_at<?`, olderThanUnix)
	return err
}

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

		`CREATE TABLE IF NOT EXISTS peer_quality (
			uid        INTEGER NOT NULL,
			torrent_id INTEGER NOT NULL,
			alpha      REAL    NOT NULL DEFAULT 1.0,
			beta       REAL    NOT NULL DEFAULT 1.0,
			obs_count  INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (uid, torrent_id)
		)`,

		// Model governance tables (Req 7)
		`CREATE TABLE IF NOT EXISTS markov_model_metadata (
			chain_name          TEXT    NOT NULL PRIMARY KEY,
			schema_version      INTEGER NOT NULL DEFAULT 1,
			decay_lambda        REAL    NOT NULL,
			smoothing_alpha     REAL    NOT NULL,
			obs_interval_sec    INTEGER NOT NULL,
			matrix_version      INTEGER NOT NULL DEFAULT 1,
			eff_samples_json    TEXT,
			created_at          INTEGER NOT NULL,
			updated_at          INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS markov_recommendation_log (
			id                 INTEGER NOT NULL PRIMARY KEY,
			chain_name         TEXT    NOT NULL,
			entity_id          INTEGER NOT NULL,
			prediction_json    TEXT    NOT NULL,
			entropy            REAL    NOT NULL,
			evidence_strength  REAL    NOT NULL,
			model_version      INTEGER NOT NULL,
			recommended_action TEXT    NOT NULL,
			policy_action      TEXT    NOT NULL DEFAULT '',
			outcome            TEXT    NOT NULL DEFAULT '',
			shadow_mode        INTEGER NOT NULL DEFAULT 1,
			created_at         INTEGER NOT NULL,
			resolved_at        INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS markov_forecast_evaluation (
			id             INTEGER NOT NULL PRIMARY KEY,
			chain_name     TEXT    NOT NULL,
			entity_id      INTEGER NOT NULL,
			horizon_steps  INTEGER NOT NULL,
			forecast_json  TEXT    NOT NULL,
			outcome_state  INTEGER NOT NULL DEFAULT -1,
			brier_score    REAL    NOT NULL DEFAULT 0,
			log_loss       REAL    NOT NULL DEFAULT 0,
			created_at     INTEGER NOT NULL,
			evaluated_at   INTEGER NOT NULL DEFAULT 0
		)`,

		// Seeder recruitment: precision freeleech targeting
		`CREATE TABLE IF NOT EXISTS markov_seeder_assignments (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id       INTEGER NOT NULL,
			torrent_id    INTEGER NOT NULL,
			urgency_score REAL    NOT NULL,
			assigned_at   INTEGER NOT NULL,
			fulfilled_at  INTEGER NOT NULL DEFAULT 0,
			expires_at    INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_seeder_asgn_user
			ON markov_seeder_assignments(user_id, fulfilled_at, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_seeder_asgn_torrent
			ON markov_seeder_assignments(torrent_id, fulfilled_at)`,
	}
	for _, s := range stmts {
		if _, err := d.pool.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s[:40], err)
		}
	}
	// Schema migration: add deployment_stage column to existing installations (UMM-06).
	// SQLite returns "duplicate column name" error if the column already exists; ignore it.
	_, _ = d.pool.Exec(`ALTER TABLE markov_model_metadata ADD COLUMN deployment_stage TEXT NOT NULL DEFAULT 'SHADOW'`)
	return nil
}
