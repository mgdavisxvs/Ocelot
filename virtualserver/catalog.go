package virtualserver

// ArtifactStatus classifies the reachability of a swarm artifact.
type ArtifactStatus string

const (
	// ArtifactAvailable means ≥ 3 seeders are active — safe to provision.
	ArtifactAvailable ArtifactStatus = "available"
	// ArtifactDegraded means ≥ 1 seeder is active — provisioning may be slow.
	ArtifactDegraded ArtifactStatus = "degraded"
	// ArtifactUnavailable means 0 seeders — provisioning must be gated.
	ArtifactUnavailable ArtifactStatus = "unavailable"
)

// CatalogProvider abstracts access to the live torrent swarm.
// The tracker package satisfies this interface via OcelotCatalogAdapter.
type CatalogProvider interface {
	// SeederCount returns the number of active seeders for infoHash.
	// Returns 0 if the torrent is unknown.
	SeederCount(infoHash string) int
}

// ClassifyArtifact returns the ArtifactStatus for infoHash based on live seeder
// counts from the catalog.
func ClassifyArtifact(catalog CatalogProvider, infoHash string) ArtifactStatus {
	n := catalog.SeederCount(infoHash)
	switch {
	case n >= 3:
		return ArtifactAvailable
	case n >= 1:
		return ArtifactDegraded
	default:
		return ArtifactUnavailable
	}
}

// OcelotCatalogAdapter bridges a CatalogProvider to the tracker's TorrentList
// via a simple accessor function, avoiding import cycles.
type OcelotCatalogAdapter struct {
	seederCount func(infoHash string) int
}

// NewOcelotCatalogAdapter wraps seederCountFn as a CatalogProvider.
// Callers pass a closure over tracker.TorrentList.Get:
//
//	adapter := NewOcelotCatalogAdapter(func(h string) int {
//	    t, ok := torrents.Get(h)
//	    if !ok { return 0 }
//	    return t.Seeders.Size()
//	})
func NewOcelotCatalogAdapter(seederCountFn func(infoHash string) int) *OcelotCatalogAdapter {
	return &OcelotCatalogAdapter{seederCount: seederCountFn}
}

func (a *OcelotCatalogAdapter) SeederCount(infoHash string) int {
	if a.seederCount == nil {
		return 0
	}
	return a.seederCount(infoHash)
}
