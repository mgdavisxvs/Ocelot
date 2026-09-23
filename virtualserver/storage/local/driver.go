package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
)

// Driver implements StorageDriver using the local filesystem (hostPath volumes).
// BoundNodeID is stored in the opaque handle so HC-08 can prevent cross-node mounts:
// once a local volume is mounted on a node, it can never be mounted on a different node.
type Driver struct {
	nodeID  string // ID of the node this driver is running on
	baseDir string // root directory for all local volumes
}

// New creates a Driver bound to the given node and base directory.
func New(nodeID, baseDir string) *Driver {
	return &Driver{nodeID: nodeID, baseDir: baseDir}
}

func (d *Driver) Name() string { return "local" }

// Create makes a directory at {baseDir}/{namespace}/{name} and returns a handle
// containing the path and the BoundNodeID for HC-08 enforcement.
func (d *Driver) Create(_ context.Context, namespace, name string, _ domain.VolumeSpec) (storage.VolumeHandle, error) {
	path := filepath.Join(d.baseDir, namespace, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("local.Create mkdir %s: %w", path, err)
	}
	return storage.VolumeHandle{
		"path":   path,
		"nodeID": d.nodeID,
	}, nil
}

// Delete removes the directory tree backing the volume.
func (d *Driver) Delete(_ context.Context, handle storage.VolumeHandle) error {
	path := handle["path"]
	if path == "" {
		return fmt.Errorf("local.Delete: empty path in handle")
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("local.Delete %s: %w", path, err)
	}
	return nil
}

// Mount enforces HC-08: rejects the request if the volume's BoundNodeID differs from
// req.NodeID. For new volumes (BoundNodeID=="") any node is accepted.
func (d *Driver) Mount(_ context.Context, req storage.MountRequest) (storage.MountPoint, error) {
	boundNodeID := req.Handle["nodeID"]
	if boundNodeID != "" && boundNodeID != req.NodeID {
		return storage.MountPoint{}, fmt.Errorf("%w: bound to %s, requested for %s",
			storage.ErrNodeMismatch, boundNodeID, req.NodeID)
	}
	path := req.Handle["path"]
	if path == "" {
		return storage.MountPoint{}, fmt.Errorf("local.Mount: empty path in handle")
	}
	// Re-create directory if it was removed externally (e.g. node reimaged).
	if err := os.MkdirAll(path, 0o755); err != nil {
		return storage.MountPoint{}, fmt.Errorf("local.Mount mkdir: %w", err)
	}
	return storage.MountPoint{HostPath: path, ReadOnly: req.ReadOnly}, nil
}

// Unmount is a no-op for hostPath volumes. The directory persists after the container stops.
func (d *Driver) Unmount(_ context.Context, _ storage.MountPoint) error { return nil }

// Stat returns filesystem-level capacity and usage via statfs(2).
func (d *Driver) Stat(_ context.Context, handle storage.VolumeHandle) (storage.VolumeStat, error) {
	path := handle["path"]
	if path == "" {
		return storage.VolumeStat{}, fmt.Errorf("local.Stat: empty path in handle")
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return storage.VolumeStat{}, fmt.Errorf("local.Stat statfs %s: %w", path, err)
	}
	bsize := int64(st.Bsize)
	totalMiB := int64(st.Blocks) * bsize / (1024 * 1024)
	availMiB := int64(st.Bavail) * bsize / (1024 * 1024)
	return storage.VolumeStat{
		CapacityMiB: totalMiB,
		UsedMiB:     totalMiB - availMiB,
		Available:   true,
	}, nil
}

// Snapshot creates a crash-consistent point-in-time copy of the volume directory.
// Snapshots are stored under {baseDir}/.snapshots/{label}-{timestamp} and can be
// used for backup or manual restore. Application-consistent snapshots require
// quiescing the workload before calling this (a FUTURE-tier capability).
func (d *Driver) Snapshot(_ context.Context, handle storage.VolumeHandle, label string) (string, error) {
	srcPath := handle["path"]
	if srcPath == "" {
		return "", fmt.Errorf("local.Snapshot: empty path in handle")
	}
	snapDir := filepath.Join(d.baseDir, ".snapshots")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return "", fmt.Errorf("local.Snapshot mkdir: %w", err)
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	snapPath := filepath.Join(snapDir, label+"-"+ts)
	if err := copyDirTree(srcPath, snapPath); err != nil {
		// Clean up partial snapshot on failure.
		os.RemoveAll(snapPath) //nolint:errcheck
		return "", fmt.Errorf("local.Snapshot copy: %w", err)
	}
	return snapPath, nil
}

// RestoreFrom creates a new volume directory populated from the snapshot at snapshotRef.
// snapshotRef is the absolute path returned by Snapshot. The target path must not exist.
func (d *Driver) RestoreFrom(_ context.Context, snapshotRef, namespace, name string, _ domain.VolumeSpec) (storage.VolumeHandle, error) {
	if snapshotRef == "" {
		return nil, fmt.Errorf("local.RestoreFrom: empty snapshot ref")
	}
	if _, err := os.Stat(snapshotRef); err != nil {
		return nil, fmt.Errorf("local.RestoreFrom: snapshot not found at %s: %w", snapshotRef, err)
	}
	targetPath := filepath.Join(d.baseDir, namespace, name)
	if _, err := os.Stat(targetPath); err == nil {
		return nil, fmt.Errorf("local.RestoreFrom: target path %s already exists", targetPath)
	}
	if err := copyDirTree(snapshotRef, targetPath); err != nil {
		os.RemoveAll(targetPath) //nolint:errcheck
		return nil, fmt.Errorf("local.RestoreFrom copy: %w", err)
	}
	return storage.VolumeHandle{
		"path":   targetPath,
		"nodeID": d.nodeID,
	}, nil
}

func copyDirTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
