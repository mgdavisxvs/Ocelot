// Package schema provides the compute.db DDL and database initialisation.
//
// compute.db is deliberately separate from tracker.db.
// GUC ruling 2026-09-22: two independent databases, zero cross-domain FKs.
package schema

import (
	"database/sql"
	_ "embed"
	_ "modernc.org/sqlite"
	"fmt"
)

//go:embed schema.sql
var ddl string

// Open opens (or creates) compute.db at the given path, applies WAL mode,
// and runs the DDL idempotently. Returns an error if any step fails.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_synchronous=NORMAL", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("compute/schema: open %s: %w", path, err)
	}

	db.SetMaxOpenConns(1) // SQLite: single writer

	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("compute/schema: apply DDL: %w", err)
	}

	return db, nil
}
