package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	_ "modernc.org/sqlite"
)

// inMemDB creates an in-memory SQLite DB with the minimal tracker schema.
func inMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE torrents (
			id      INTEGER PRIMARY KEY,
			seeders INTEGER DEFAULT 0
		);
		CREATE TABLE torrent_hashes (
			torrent_id INTEGER NOT NULL REFERENCES torrents(id),
			info_hash  TEXT NOT NULL UNIQUE
		);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type staticDBProvider struct{ db *sql.DB }

func (p *staticDBProvider) CurrentDB() *sql.DB { return p.db }

func insertTorrent(t *testing.T, db *sql.DB, infoHash string, seeders int) {
	t.Helper()
	res, err := db.Exec(`INSERT INTO torrents (seeders) VALUES (?)`, seeders)
	if err != nil {
		t.Fatalf("insert torrent: %v", err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO torrent_hashes (torrent_id, info_hash) VALUES (?, ?)`, id, infoHash); err != nil {
		t.Fatalf("insert hash: %v", err)
	}
}

const testHash = "aabbccdd00112233aabbccdd00112233aabbccdd"

func TestLookup_NotFound(t *testing.T) {
	db := inMemDB(t)
	c := New(&staticDBProvider{db: db})
	status, err := c.Lookup(context.Background(), testHash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Exists {
		t.Error("expected Exists=false for missing torrent")
	}
	if status.Available {
		t.Error("expected Available=false for missing torrent")
	}
	if status.Availability != domain.ArtifactUnavailable {
		t.Errorf("expected ArtifactUnavailable, got %q", status.Availability)
	}
}

func TestLookup_ZeroSeeders_Unavailable(t *testing.T) {
	db := inMemDB(t)
	insertTorrent(t, db, testHash, 0)
	c := New(&staticDBProvider{db: db})
	status, _ := c.Lookup(context.Background(), testHash)
	if status.Availability != domain.ArtifactUnavailable {
		t.Errorf("0 seeders should be unavailable, got %q", status.Availability)
	}
	if status.Available {
		t.Error("0 seeders should not be available")
	}
}

func TestLookup_OneTwoSeeders_Degraded(t *testing.T) {
	for _, n := range []int{1, 2} {
		db := inMemDB(t)
		insertTorrent(t, db, testHash, n)
		c := New(&staticDBProvider{db: db})
		status, _ := c.Lookup(context.Background(), testHash)
		if status.Availability != domain.ArtifactDegraded {
			t.Errorf("%d seeders: expected degraded, got %q", n, status.Availability)
		}
		if !status.Available {
			t.Errorf("%d seeders: expected Available=true for degraded", n)
		}
	}
}

func TestLookup_ThreePlusSeeders_Available(t *testing.T) {
	for _, n := range []int{3, 10, 100} {
		db := inMemDB(t)
		insertTorrent(t, db, testHash, n)
		c := New(&staticDBProvider{db: db})
		status, _ := c.Lookup(context.Background(), testHash)
		if status.Availability != domain.ArtifactAvailable {
			t.Errorf("%d seeders: expected available, got %q", n, status.Availability)
		}
		if !status.Available {
			t.Errorf("%d seeders: expected Available=true", n)
		}
		if status.SeederCount != n {
			t.Errorf("expected SeederCount=%d, got %d", n, status.SeederCount)
		}
	}
}

func TestLookup_NilDB(t *testing.T) {
	c := New(&staticDBProvider{db: nil})
	_, err := c.Lookup(context.Background(), testHash)
	if err == nil {
		t.Error("expected error when DB is nil")
	}
}
