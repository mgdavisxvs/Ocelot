package tracker

import (
	"sync"
	"time"
)

// ArtifactType classifies what kind of artifact a torrent represents.
type ArtifactType string

const (
	ArtifactModel    ArtifactType = "model"
	ArtifactDataset  ArtifactType = "dataset"
	ArtifactBuild    ArtifactType = "build"
	ArtifactFirmware ArtifactType = "firmware"
)

// ReplicationHealth is the four-state health model for artifact replica coverage.
//
//	HEALTHY        — replicas >= desired
//	UNDER_REPLICATED — min_replicas <= replicas < desired
//	DEGRADED       — 0 < replicas < min_replicas
//	UNAVAILABLE    — replicas == 0
type ReplicationHealth uint8

const (
	HealthHealthy         ReplicationHealth = iota
	HealthUnderReplicated                   // alert: insufficient but above minimum
	HealthDegraded                          // page: below minimum threshold
	HealthUnavailable                       // critical: zero replicas
)

func (h ReplicationHealth) String() string {
	switch h {
	case HealthHealthy:
		return "HEALTHY"
	case HealthUnderReplicated:
		return "UNDER_REPLICATED"
	case HealthDegraded:
		return "DEGRADED"
	case HealthUnavailable:
		return "UNAVAILABLE"
	default:
		return "UNKNOWN"
	}
}

// EvalHealth maps a live replica count against policy thresholds.
func EvalHealth(replicaCount, minReplicas, desiredReplicas int) ReplicationHealth {
	switch {
	case replicaCount >= desiredReplicas:
		return HealthHealthy
	case replicaCount >= minReplicas:
		return HealthUnderReplicated
	case replicaCount > 0:
		return HealthDegraded
	default:
		return HealthUnavailable
	}
}

// Artifact wraps a torrent with identity, policy, and verification state.
// It is the core abstraction of Ocelot Fabric Layer 3.
type Artifact struct {
	mu sync.RWMutex

	ID              uint64
	InfoHash        string
	Type            ArtifactType
	SHA256          string
	SizeBytes       int64
	MinReplicas     int
	DesiredReplicas int
	MaxReplicas     int

	VerifyState  VerifyState
	RetryCount   int
	LastVerified time.Time
	CreatedAt    time.Time

	// Access tracking for eviction policy.
	// Must be populated before the eviction engine is built.
	AccessCount    int64
	LastAccessedAt time.Time

	// S-E6: demand heatmap for proximity-aware placement.
	Heatmap *DemandHeatmap
}

// Health returns the current replication health given a live replica count.
func (a *Artifact) Health(replicaCount int) ReplicationHealth {
	a.mu.RLock()
	min := a.MinReplicas
	desired := a.DesiredReplicas
	a.mu.RUnlock()
	return EvalHealth(replicaCount, min, desired)
}

// RecordAccess updates access tracking fields atomically.
func (a *Artifact) RecordAccess() {
	a.mu.Lock()
	a.AccessCount++
	a.LastAccessedAt = time.Now()
	a.mu.Unlock()
}

// NewArtifact creates an Artifact with defaults including an initialized heatmap.
func NewArtifact(infoHash string) *Artifact {
	return &Artifact{
		InfoHash:  infoHash,
		CreatedAt: time.Now(),
		Heatmap:   NewDemandHeatmap(1000),
	}
}

// ArtifactList is a concurrent-safe registry of artifacts keyed by info_hash.
type ArtifactList struct {
	mu        sync.RWMutex
	artifacts map[string]*Artifact
}

func NewArtifactList() *ArtifactList {
	return &ArtifactList{artifacts: make(map[string]*Artifact)}
}

func (al *ArtifactList) Get(infoHash string) (*Artifact, bool) {
	al.mu.RLock()
	defer al.mu.RUnlock()
	a, ok := al.artifacts[infoHash]
	return a, ok
}

func (al *ArtifactList) Set(infoHash string, a *Artifact) {
	al.mu.Lock()
	defer al.mu.Unlock()
	if a.Heatmap == nil {
		a.Heatmap = NewDemandHeatmap(1000)
	}
	al.artifacts[infoHash] = a
}

func (al *ArtifactList) Delete(infoHash string) {
	al.mu.Lock()
	defer al.mu.Unlock()
	delete(al.artifacts, infoHash)
}

func (al *ArtifactList) Size() int {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return len(al.artifacts)
}

func (al *ArtifactList) ForEach(fn func(hash string, a *Artifact) bool) {
	al.mu.RLock()
	defer al.mu.RUnlock()
	for h, a := range al.artifacts {
		if !fn(h, a) {
			break
		}
	}
}
