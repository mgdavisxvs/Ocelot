package storage

import "context"

// VolumeHandle is an opaque token that identifies a provisioned volume.
// Drivers interpret the string themselves; callers treat it as a cookie.
type VolumeHandle string

// MountPoint carries the host-side path a BackendAdapter passes into the
// container or VM runtime.
type MountPoint struct {
	HostPath string
	ReadOnly bool
}

// VolumeStats reports capacity and locality for a volume.
type VolumeStats struct {
	SizeBytes int64
	UsedBytes int64
	NodeID    string
}

// StorageDriver is the plugin interface for persistent storage backends.
// Implementations must be safe for concurrent use.
type StorageDriver interface {
	// Name returns the registered driver name (e.g. "local", "nfs").
	Name() string

	// Create allocates a new volume and returns its opaque handle.
	Create(ctx context.Context, volumeID string, sizeBytes int64) (VolumeHandle, error)

	// Delete releases all resources associated with handle.
	Delete(ctx context.Context, handle VolumeHandle) error

	// Mount prepares handle for access by instanceID and returns the host
	// path that should be bind-mounted into the instance.
	Mount(ctx context.Context, handle VolumeHandle, instanceID string) (*MountPoint, error)

	// Unmount releases the binding between handle and instanceID.
	Unmount(ctx context.Context, handle VolumeHandle, instanceID string) error

	// Stat returns current capacity and locality information.
	Stat(ctx context.Context, handle VolumeHandle) (*VolumeStats, error)

	// Snapshot creates a point-in-time copy of handle and returns the
	// snapshot's own opaque handle.
	Snapshot(ctx context.Context, handle VolumeHandle) (VolumeHandle, error)

	// RestoreFrom allocates a new volume pre-populated with the data from
	// snapshotHandle and returns the new volume's handle.
	RestoreFrom(ctx context.Context, snapshotHandle VolumeHandle) (VolumeHandle, error)
}
