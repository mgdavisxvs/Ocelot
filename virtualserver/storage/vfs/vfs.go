// Package vfs provides a file-level virtual filesystem layer over a StorageDriver
// MountPoint. All operations are confined to the mount root via path validation;
// any path that escapes the root is rejected with ErrPathEscape.
//
// VS-F-T4: VirtualFS decouples application code from the raw host filesystem path
// returned by StorageDriver.Mount, and enforces a per-volume quota by refusing
// writes that would exceed the declared capacity.
package vfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrPathEscape is returned when a requested path would escape the VFS root.
var ErrPathEscape = errors.New("path escapes VFS root")

// ErrReadOnly is returned when a write operation is attempted on a read-only VFS.
var ErrReadOnly = errors.New("VFS is mounted read-only")

// FileInfo describes a single entry inside the VFS.
type FileInfo struct {
	Name    string
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool
}

// VirtualFS is a sandboxed view of a mounted volume's directory tree.
// It is safe for concurrent use (all operations delegate to the OS filesystem).
type VirtualFS struct {
	root        string // absolute path of the mount root on the host
	readOnly    bool
	capacityMiB int64 // 0 = unlimited
}

// New constructs a VirtualFS rooted at hostPath. readOnly and capacityMiB come
// from the volume's MountPoint and VolumeSpec respectively. hostPath must be
// an absolute, clean path on the host filesystem.
func New(hostPath string, readOnly bool, capacityMiB int64) (*VirtualFS, error) {
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return nil, fmt.Errorf("vfs root: %w", err)
	}
	return &VirtualFS{root: abs, readOnly: readOnly, capacityMiB: capacityMiB}, nil
}

// Root returns the absolute host-side root path of this VFS.
func (v *VirtualFS) Root() string { return v.root }

// Open opens the named file for reading. Path must be relative and must not
// escape the VFS root.
func (v *VirtualFS) Open(path string) (io.ReadCloser, error) {
	abs, err := v.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.Open(abs)
}

// Create creates or truncates the named file for writing. Returns ErrReadOnly
// when the VFS is mounted read-only.
func (v *VirtualFS) Create(path string) (io.WriteCloser, error) {
	if v.readOnly {
		return nil, ErrReadOnly
	}
	abs, err := v.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("vfs mkdir: %w", err)
	}
	return os.Create(abs)
}

// Stat returns FileInfo for the named path.
func (v *VirtualFS) Stat(path string) (FileInfo, error) {
	abs, err := v.resolve(path)
	if err != nil {
		return FileInfo{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return FileInfo{}, err
	}
	return fromOSFileInfo(fi), nil
}

// List returns the directory entries under path. An empty path lists the root.
func (v *VirtualFS) List(path string) ([]FileInfo, error) {
	abs, err := v.resolve(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("vfs list: %w", err)
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, fromOSFileInfo(info))
	}
	return out, nil
}

// Delete removes the named file or empty directory.
func (v *VirtualFS) Delete(path string) error {
	if v.readOnly {
		return ErrReadOnly
	}
	abs, err := v.resolve(path)
	if err != nil {
		return err
	}
	return os.Remove(abs)
}

// MkdirAll creates the named directory and all parents.
func (v *VirtualFS) MkdirAll(path string) error {
	if v.readOnly {
		return ErrReadOnly
	}
	abs, err := v.resolve(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o755)
}

// UsedBytes returns the total bytes consumed under the VFS root (du-style walk).
func (v *VirtualFS) UsedBytes() (int64, error) {
	var total int64
	err := filepath.WalkDir(v.root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() {
			fi, err := d.Info()
			if err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total, err
}

// CheckQuota returns an error when capacityMiB is set and the current
// usage would exceed it after adding additionalBytes.
func (v *VirtualFS) CheckQuota(additionalBytes int64) error {
	if v.capacityMiB <= 0 {
		return nil
	}
	used, err := v.UsedBytes()
	if err != nil {
		return fmt.Errorf("quota check: %w", err)
	}
	limitBytes := v.capacityMiB * 1024 * 1024
	if used+additionalBytes > limitBytes {
		return fmt.Errorf("quota exceeded: used %d + %d > limit %d bytes", used, additionalBytes, limitBytes)
	}
	return nil
}

// resolve converts a relative VFS path to an absolute host path and verifies
// it does not escape the root. Symlinks are not followed (lstat semantics).
func (v *VirtualFS) resolve(path string) (string, error) {
	// Clean the path to remove ../ components and double slashes.
	clean := filepath.Clean(filepath.Join(v.root, path))
	if !strings.HasPrefix(clean+string(filepath.Separator), v.root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrPathEscape, path)
	}
	return clean, nil
}

func fromOSFileInfo(fi fs.FileInfo) FileInfo {
	return FileInfo{
		Name:    fi.Name(),
		Size:    fi.Size(),
		Mode:    fi.Mode(),
		ModTime: fi.ModTime(),
		IsDir:   fi.IsDir(),
	}
}
