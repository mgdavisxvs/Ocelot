package domain

import "time"

// ArtifactAvailability represents how available an artifact is in the swarm.
type ArtifactAvailability string

const (
	ArtifactAvailable   ArtifactAvailability = "available"
	ArtifactDegraded    ArtifactAvailability = "degraded"
	ArtifactUnavailable ArtifactAvailability = "unavailable"
	ArtifactUnknown     ArtifactAvailability = "unknown"
)

// ArtifactStatus describes the current swarm health of an artifact.
type ArtifactStatus struct {
	InfoHash     string
	Exists       bool
	Available    bool // true if Availability is available or degraded
	SeederCount  int
	Availability ArtifactAvailability
	LastChecked  time.Time
}

// ArtifactSource is a recommended torrent source node for artifact acquisition.
type ArtifactSource struct {
	NodeID   string
	NodeName string
	InfoHash string
	Priority int
}
