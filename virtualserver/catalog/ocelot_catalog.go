package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	vsmetrics "github.com/mgdavisxvs/Ocelot/virtualserver/metrics"
)

const (
	seederThresholdDegraded  = 1
	seederThresholdAvailable = 3
)

// DBProvider is the narrow interface the catalog needs from the tracker shard manager.
// Matches SQLiteShardManager.CurrentDB() signature.
type DBProvider interface {
	CurrentDB() *sql.DB
}

// OcelotCatalogAdapter queries the tracker SQLite shard for artifact availability.
// It is read-only and never writes to the tracker DB.
type OcelotCatalogAdapter struct {
	provider DBProvider
}

// New creates an OcelotCatalogAdapter backed by the given tracker shard provider.
func New(provider DBProvider) *OcelotCatalogAdapter {
	return &OcelotCatalogAdapter{provider: provider}
}

// Lookup returns the availability status for the given info_hash.
// SeederCount < seederThresholdAvailable produces ArtifactDegraded.
// A missing torrent returns ArtifactStatus{Exists: false}.
func (c *OcelotCatalogAdapter) Lookup(ctx context.Context, infoHash string) (domain.ArtifactStatus, error) {
	db := c.provider.CurrentDB()
	if db == nil {
		vsmetrics.ArtifactLookups.WithLabelValues("error").Inc()
		return domain.ArtifactStatus{InfoHash: infoHash}, fmt.Errorf("catalog: tracker DB not available")
	}

	var seeders int
	err := db.QueryRowContext(ctx, `
		SELECT t.seeders
		FROM torrents t
		JOIN torrent_hashes h ON h.torrent_id = t.id
		WHERE h.info_hash = ?
	`, infoHash).Scan(&seeders)

	if errors.Is(err, sql.ErrNoRows) {
		vsmetrics.ArtifactLookups.WithLabelValues("unavailable").Inc()
		return domain.ArtifactStatus{
			InfoHash:     infoHash,
			Exists:       false,
			Available:    false,
			Availability: domain.ArtifactUnavailable,
		}, nil
	}
	if err != nil {
		vsmetrics.ArtifactLookups.WithLabelValues("error").Inc()
		return domain.ArtifactStatus{InfoHash: infoHash}, fmt.Errorf("catalog: query failed: %w", err)
	}

	status := domain.ArtifactStatus{
		InfoHash:    infoHash,
		Exists:      true,
		SeederCount: seeders,
	}

	switch {
	case seeders >= seederThresholdAvailable:
		status.Available = true
		status.Availability = domain.ArtifactAvailable
		vsmetrics.ArtifactLookups.WithLabelValues("available").Inc()
	case seeders >= seederThresholdDegraded:
		status.Available = true
		status.Availability = domain.ArtifactDegraded
		vsmetrics.ArtifactLookups.WithLabelValues("degraded").Inc()
	default:
		status.Available = false
		status.Availability = domain.ArtifactUnavailable
		vsmetrics.ArtifactLookups.WithLabelValues("unavailable").Inc()
	}

	return status, nil
}
