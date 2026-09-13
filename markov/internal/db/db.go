package db

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/mgdavisxvs/ocelot/markov/internal/config"

	_ "github.com/go-sql-driver/mysql"
)

// DB wraps a *sql.DB with helpers for the Markov service.
type DB struct {
	pool *sql.DB
	cfg  *config.Config
}

func Open(cfg *config.Config) (*DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=false&interpolateParams=true",
		cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, cfg.DBName)
	pool, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	pool.SetMaxOpenConns(10)
	pool.SetMaxIdleConns(4)
	if err := pool.Ping(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	d := &DB{pool: pool, cfg: cfg}
	if err := d.createSchema(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	slog.Info("database connected", "host", cfg.DBHost, "db", cfg.DBName)
	return d, nil
}

func (d *DB) Close() error { return d.pool.Close() }

func (d *DB) createSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS markov_chain_counts (
			chain_name  VARCHAR(32)  NOT NULL,
			from_state  TINYINT      NOT NULL,
			to_state    TINYINT      NOT NULL,
			count       DOUBLE       NOT NULL DEFAULT 0,
			PRIMARY KEY (chain_name, from_state, to_state)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS markov_peer_states (
			torrent_id  INT    NOT NULL,
			uid         INT    NOT NULL,
			state       TINYINT NOT NULL,
			observed_at BIGINT NOT NULL,
			PRIMARY KEY (torrent_id, uid)
		) ENGINE=InnoDB`,

		`CREATE TABLE IF NOT EXISTS markov_torrent_states (
			torrent_id  INT     NOT NULL PRIMARY KEY,
			state       TINYINT NOT NULL,
			observed_at BIGINT  NOT NULL
		) ENGINE=InnoDB`,

		`CREATE TABLE IF NOT EXISTS markov_user_states (
			uid         INT     NOT NULL PRIMARY KEY,
			state       TINYINT NOT NULL,
			path_json   TEXT,
			observed_at BIGINT  NOT NULL
		) ENGINE=InnoDB`,

		`CREATE TABLE IF NOT EXISTS markov_predictions (
			torrent_id          INT     NOT NULL PRIMARY KEY,
			health_state        TINYINT NOT NULL,
			pi_json             TEXT    NOT NULL,
			pi_24h_json         TEXT    NOT NULL,
			pi_72h_json         TEXT    NOT NULL,
			dead_prob_24h       DOUBLE  NOT NULL,
			dead_prob_72h       DOUBLE  NOT NULL,
			expected_dead_hours DOUBLE  NOT NULL,
			entropy             DOUBLE  NOT NULL,
			recommended_interval INT   NOT NULL,
			updated_at          BIGINT  NOT NULL
		) ENGINE=InnoDB`,

		`CREATE TABLE IF NOT EXISTS markov_user_anomaly (
			uid                  INT     NOT NULL PRIMARY KEY,
			anomaly_score        DOUBLE  NOT NULL,
			path_log_likelihood  DOUBLE  NOT NULL,
			flagged              TINYINT(1) NOT NULL DEFAULT 0,
			updated_at           BIGINT  NOT NULL
		) ENGINE=InnoDB`,

		`CREATE TABLE IF NOT EXISTS markov_freeleech_candidates (
			torrent_id    INT     NOT NULL PRIMARY KEY,
			priority_score DOUBLE NOT NULL,
			dead_prob_72h  DOUBLE NOT NULL,
			recommended    TINYINT(1) NOT NULL DEFAULT 0,
			updated_at     BIGINT  NOT NULL
		) ENGINE=InnoDB`,
	}
	for _, s := range stmts {
		if _, err := d.pool.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s[:40], err)
		}
	}
	return nil
}
