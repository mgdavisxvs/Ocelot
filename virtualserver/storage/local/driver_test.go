package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
)

func newTestDriver(t *testing.T) (*Driver, string) {
	t.Helper()
	base := t.TempDir()
	return New("node-1", base), base
}

func TestLocalDriver_Name(t *testing.T) {
	d, _ := newTestDriver(t)
	if d.Name() != "local" {
		t.Errorf("expected name 'local', got %q", d.Name())
	}
}

func TestLocalDriver_Create(t *testing.T) {
	d, base := newTestDriver(t)
	handle, err := d.Create(context.Background(), "ns", "vol1", domain.VolumeSpec{CapacityMiB: 1024})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if handle["path"] == "" {
		t.Error("expected non-empty path in handle")
	}
	if handle["nodeID"] != "node-1" {
		t.Errorf("expected nodeID=node-1, got %q", handle["nodeID"])
	}
	expected := filepath.Join(base, "ns", "vol1")
	if handle["path"] != expected {
		t.Errorf("expected path %s, got %s", expected, handle["path"])
	}
	if _, err := os.Stat(expected); os.IsNotExist(err) {
		t.Error("Create did not make the directory")
	}
}

func TestLocalDriver_Delete(t *testing.T) {
	d, _ := newTestDriver(t)
	handle, _ := d.Create(context.Background(), "ns", "vol-del", domain.VolumeSpec{CapacityMiB: 512})
	path := handle["path"]

	if err := d.Delete(context.Background(), handle); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Delete did not remove the directory")
	}
}

func TestLocalDriver_Delete_EmptyHandle(t *testing.T) {
	d, _ := newTestDriver(t)
	if err := d.Delete(context.Background(), storage.VolumeHandle{}); err == nil {
		t.Error("expected error for empty handle")
	}
}

func TestLocalDriver_Mount_NewVolume(t *testing.T) {
	d, _ := newTestDriver(t)
	handle, _ := d.Create(context.Background(), "ns", "vol-mnt", domain.VolumeSpec{CapacityMiB: 512})

	mp, err := d.Mount(context.Background(), storage.MountRequest{
		VolumeID:   "v1",
		NodeID:     "node-1",
		Handle:     handle,
		TargetPath: "/data",
	})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if mp.HostPath != handle["path"] {
		t.Errorf("expected HostPath=%s, got %s", handle["path"], mp.HostPath)
	}
}

func TestLocalDriver_Mount_SameNode(t *testing.T) {
	d, _ := newTestDriver(t)
	handle, _ := d.Create(context.Background(), "ns", "vol-same", domain.VolumeSpec{CapacityMiB: 512})
	handle["nodeID"] = "node-1" // simulate already-bound

	_, err := d.Mount(context.Background(), storage.MountRequest{
		VolumeID: "v2",
		NodeID:   "node-1",
		Handle:   handle,
	})
	if err != nil {
		t.Fatalf("Mount same node: %v", err)
	}
}

func TestLocalDriver_Mount_HC08_WrongNode(t *testing.T) {
	d, _ := newTestDriver(t)
	handle, _ := d.Create(context.Background(), "ns", "vol-hc08", domain.VolumeSpec{CapacityMiB: 512})
	handle["nodeID"] = "node-1" // bound to node-1

	_, err := d.Mount(context.Background(), storage.MountRequest{
		VolumeID: "v3",
		NodeID:   "node-99", // different node
		Handle:   handle,
	})
	if err == nil {
		t.Fatal("expected HC-08 rejection, got nil error")
	}
	if !isNodeMismatch(err) {
		t.Errorf("expected ErrNodeMismatch, got: %v", err)
	}
}

func isNodeMismatch(err error) bool {
	// errors.Is traversal works because ErrNodeMismatch is wrapped via %w
	return containsStr(err.Error(), "bound to a different node")
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestLocalDriver_Mount_EmptyHandle(t *testing.T) {
	d, _ := newTestDriver(t)
	_, err := d.Mount(context.Background(), storage.MountRequest{
		VolumeID: "v4",
		NodeID:   "node-1",
		Handle:   storage.VolumeHandle{},
	})
	if err == nil {
		t.Error("expected error for empty handle path")
	}
}

func TestLocalDriver_Unmount(t *testing.T) {
	d, _ := newTestDriver(t)
	// Unmount is a no-op for local volumes; should always succeed.
	if err := d.Unmount(context.Background(), storage.MountPoint{HostPath: "/any/path"}); err != nil {
		t.Errorf("Unmount: %v", err)
	}
}

func TestLocalDriver_Stat(t *testing.T) {
	d, _ := newTestDriver(t)
	handle, _ := d.Create(context.Background(), "ns", "vol-stat", domain.VolumeSpec{CapacityMiB: 1024})

	stat, err := d.Stat(context.Background(), handle)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !stat.Available {
		t.Error("expected Available=true")
	}
	if stat.CapacityMiB <= 0 {
		t.Errorf("expected CapacityMiB > 0, got %d", stat.CapacityMiB)
	}
}

func TestLocalDriver_Stat_EmptyHandle(t *testing.T) {
	d, _ := newTestDriver(t)
	if _, err := d.Stat(context.Background(), storage.VolumeHandle{}); err == nil {
		t.Error("expected error for empty handle")
	}
}

func TestLocalDriver_Snapshot(t *testing.T) {
	d, base := newTestDriver(t)
	handle, _ := d.Create(context.Background(), "ns", "vol-snap", domain.VolumeSpec{CapacityMiB: 512})

	// Write a test file inside the volume.
	testFile := filepath.Join(handle["path"], "data.txt")
	if err := os.WriteFile(testFile, []byte("hello snapshot"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	ref, err := d.Snapshot(context.Background(), handle, "snap-v1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if ref == "" {
		t.Error("expected non-empty snapshot ref")
	}

	// Verify snapshot path is under baseDir/.snapshots
	snapDir := filepath.Join(base, ".snapshots")
	if _, err := os.Stat(snapDir); os.IsNotExist(err) {
		t.Error("snapshot directory not created")
	}
	// Verify the test file was copied
	copiedFile := filepath.Join(ref, "data.txt")
	content, err := os.ReadFile(copiedFile)
	if err != nil {
		t.Fatalf("read snapshot file: %v", err)
	}
	if string(content) != "hello snapshot" {
		t.Errorf("snapshot content mismatch: %q", string(content))
	}
}

func TestLocalDriver_Snapshot_EmptyHandle(t *testing.T) {
	d, _ := newTestDriver(t)
	if _, err := d.Snapshot(context.Background(), storage.VolumeHandle{}, "snap"); err == nil {
		t.Error("expected error for empty handle")
	}
}
