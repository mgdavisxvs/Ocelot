package storage

import (
	"context"
	"errors"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// VolumeHandle is opaque driver-specific data identifying a provisioned volume.
// Stored as JSON in vs.db; never interpreted by the reconciler.
type VolumeHandle = map[string]string

// MountRequest contains all information needed to mount a volume onto a node.
type MountRequest struct {
	VolumeID   string
	NodeID     string
	Handle     VolumeHandle
	TargetPath string
	ReadOnly   bool
}

// MountPoint describes where a volume has been mounted on the host filesystem.
type MountPoint struct {
	HostPath string
	ReadOnly bool
}

// VolumeStat describes the current utilization of a provisioned volume.
type VolumeStat struct {
	CapacityMiB int64
	UsedMiB     int64
	Available   bool
}

// ErrNodeMismatch is returned by LocalDriver.Mount when the requested node does not
// match the volume's BoundNodeID, enforcing the HC-08 local-volume affinity constraint.
var ErrNodeMismatch = errors.New("local volume is bound to a different node")

// StorageDriver is the interface every storage backend must implement.
// All methods accept context.Context for cancellation and deadline propagation.
// No method may initiate database writes — state changes are the caller's responsibility.
type StorageDriver interface {
	// Name returns the unique driver identifier matching the VolumeClass.Driver field.
	Name() string

	// Create provisions storage for a new volume. Returns an opaque handle for future ops.
	Create(ctx context.Context, namespace, name string, spec domain.VolumeSpec) (VolumeHandle, error)

	// Delete permanently removes the storage backing a volume.
	Delete(ctx context.Context, handle VolumeHandle) error

	// Mount makes the volume accessible on the given node at the target path.
	// Returns the resolved MountPoint the adapter uses to bind-mount into the runtime.
	Mount(ctx context.Context, req MountRequest) (MountPoint, error)

	// Unmount removes the volume from the node without deleting backing data.
	Unmount(ctx context.Context, point MountPoint) error

	// Stat returns current usage statistics for a volume.
	Stat(ctx context.Context, handle VolumeHandle) (VolumeStat, error)

	// Snapshot creates a crash-consistent point-in-time copy of the volume.
	// Returns a driver-specific reference string for the snapshot.
	// NOTE: crash-consistent only. Application-consistent snapshots require
	// BackendAdapter.Quiesce/Resume (FUTURE tier).
	Snapshot(ctx context.Context, handle VolumeHandle, label string) (string, error)

	// RestoreFrom provisions a new volume whose initial contents are a copy of the
	// snapshot identified by snapshotRef (the string returned by Snapshot).
	// The new volume lives at namespace/name inside the driver's storage domain.
	// Returns a VolumeHandle identical in shape to what Create returns.
	RestoreFrom(ctx context.Context, snapshotRef, namespace, name string, spec domain.VolumeSpec) (VolumeHandle, error)
}
