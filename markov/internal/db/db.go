package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgdavisxvs/ocelot/markov/internal/config"

	_ "modernc.org/sqlite"
)

// DB holds two SQLite connections: one read-only handle on the latest tracker
// shard, and one read-write handle on the dedicated Markov state file.
type DB struct {
	trackerDB *sql.DB // read-only: ocelot-YYYY-MM.db shard
	markovDB  *sql.DB // read-write: markov.db
	cfg       *config.Config
}

func Open(cfg *config.Config) (*DB, error) {
	shardPath, err := latestShard(cfg.TrackerDBDir)
	if err != nil {
		return nil, fmt.Errorf("locate tracker shard: %w", err)
	}

	trackerDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?cache=shared&mode=ro", shardPath))
	if err != nil {
		return nil, fmt.Errorf("open tracker db: %w", err)
	}
	trackerDB.SetMaxOpenConns(4)
	trackerDB.SetMaxIdleConns(2)
	if err := trackerDB.Ping(); err != nil {
		trackerDB.Close()
		return nil, fmt.Errorf("ping tracker db: %w", err)
	}

	markovDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?cache=shared&mode=rwc", cfg.MarkovDBPath))
	if err != nil {
		trackerDB.Close()
		return nil, fmt.Errorf("open markov db: %w", err)
	}
	markovDB.SetMaxOpenConns(4)
	markovDB.SetMaxIdleConns(2)
	if err := markovDB.Ping(); err != nil {
		trackerDB.Close()
		markovDB.Close()
		return nil, fmt.Errorf("ping markov db: %w", err)
	}

	d := &DB{trackerDB: trackerDB, markovDB: markovDB, cfg: cfg}
	if err := d.applyPragmas(); err != nil {
		d.Close()
		return nil, fmt.Errorf("pragmas: %w", err)
	}
	if err := d.createSchema(); err != nil {
		d.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	slog.Info("databases opened",
		"tracker_shard", shardPath,
		"markov_db", cfg.MarkovDBPath)
	return d, nil
}

func (d *DB) Close() error {
	var errs []string
	if d.trackerDB != nil {
		if err := d.trackerDB.Close(); err != nil {
			errs = append(errs, "tracker: "+err.Error())
		}
	}
	if d.markovDB != nil {
		if err := d.markovDB.Close(); err != nil {
			errs = append(errs, "markov: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (d *DB) applyPragmas() error {
	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA cache_size=-65536`,
		`PRAGMA mmap_size=268435456`,
		`PRAGMA foreign_keys=ON`,
	}
	for _, p := range pragmas {
		if _, err := d.markovDB.Exec(p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
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
	}
	for _, s := range stmts {
		if _, err := d.markovDB.Exec(s); err != nil {
			return fmt.Errorf("exec schema: %w", err)
		}
	}
	return nil
}

// latestShard returns the path to the most recent ocelot-YYYY-MM.db file in dir.
func latestShard(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("readdir %s: %w", dir, err)
	}
	var shards []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "ocelot-") && strings.HasSuffix(name, ".db") {
			shards = append(shards, name)
		}
	}
	if len(shards) == 0 {
		return "", fmt.Errorf("no ocelot-*.db shards found in %s", dir)
	}
	sort.Strings(shards)
	return filepath.Join(dir, shards[len(shards)-1]), nil
}
