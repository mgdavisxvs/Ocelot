package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

const (
	volSubdir  = "volumes"
	snapSubdir = "snapshots"
)

// LocalDriver maps volumes to subdirectories of baseDir.
// It enforces single-node affinity by being bound to one nodeID.
// Snapshots are directory copies; mount returns the directory path directly.
type LocalDriver struct {
	baseDir string
	nodeID  string

	mu     sync.Mutex
	mounts map[VolumeHandle]map[string]struct{} // handle → set of instanceIDs
}

// NewLocalDriver creates a LocalDriver rooted at baseDir associated with nodeID.
// The directories volumes/ and snapshots/ are created under baseDir on first use.
func NewLocalDriver(baseDir, nodeID string) (*LocalDriver, error) {
	for _, sub := range []string{volSubdir, snapSubdir} {
		if err := os.MkdirAll(filepath.Join(baseDir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("local driver init %s: %w", sub, err)
		}
	}
	return &LocalDriver{
		baseDir: baseDir,
		nodeID:  nodeID,
		mounts:  make(map[VolumeHandle]map[string]struct{}),
	}, nil
}

func (d *LocalDriver) Name() string { return "local" }

func (d *LocalDriver) Create(_ context.Context, volumeID string, _ int64) (VolumeHandle, error) {
	dir := filepath.Join(d.baseDir, volSubdir, volumeID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("local.Create %s: %w", volumeID, err)
	}
	return VolumeHandle(dir), nil
}

func (d *LocalDriver) Delete(_ context.Context, handle VolumeHandle) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.mounts[handle]) > 0 {
		return fmt.Errorf("local.Delete: volume still mounted by %d instance(s)", len(d.mounts[handle]))
	}
	delete(d.mounts, handle)
	if err := os.RemoveAll(string(handle)); err != nil {
		return fmt.Errorf("local.Delete: %w", err)
	}
	return nil
}

func (d *LocalDriver) Mount(_ context.Context, handle VolumeHandle, instanceID string) (*MountPoint, error) {
	if _, err := os.Stat(string(handle)); err != nil {
		return nil, fmt.Errorf("local.Mount: volume path missing: %w", err)
	}
	d.mu.Lock()
	if d.mounts[handle] == nil {
		d.mounts[handle] = make(map[string]struct{})
	}
	d.mounts[handle][instanceID] = struct{}{}
	d.mu.Unlock()
	return &MountPoint{HostPath: string(handle)}, nil
}

func (d *LocalDriver) Unmount(_ context.Context, handle VolumeHandle, instanceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.mounts[handle], instanceID)
	if len(d.mounts[handle]) == 0 {
		delete(d.mounts, handle)
	}
	return nil
}

func (d *LocalDriver) Stat(_ context.Context, handle VolumeHandle) (*VolumeStats, error) {
	info, err := os.Stat(string(handle))
	if err != nil {
		return nil, fmt.Errorf("local.Stat: %w", err)
	}
	used, _ := dirSize(string(handle))
	_ = info
	return &VolumeStats{NodeID: d.nodeID, UsedBytes: used}, nil
}

func (d *LocalDriver) Snapshot(_ context.Context, handle VolumeHandle) (VolumeHandle, error) {
	snapID := uuid.New().String()
	dest := filepath.Join(d.baseDir, snapSubdir, snapID)
	if err := copyDir(string(handle), dest); err != nil {
		return "", fmt.Errorf("local.Snapshot: %w", err)
	}
	return VolumeHandle(dest), nil
}

func (d *LocalDriver) RestoreFrom(_ context.Context, snapshotHandle VolumeHandle) (VolumeHandle, error) {
	newID := uuid.New().String()
	dest := filepath.Join(d.baseDir, volSubdir, newID)
	if err := copyDir(string(snapshotHandle), dest); err != nil {
		return "", fmt.Errorf("local.RestoreFrom: %w", err)
	}
	return VolumeHandle(dest), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
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
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
}
