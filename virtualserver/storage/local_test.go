package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newDriver(t *testing.T) *LocalDriver {
	t.Helper()
	d, err := NewLocalDriver(t.TempDir(), "node-1")
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	return d
}

func TestLocalDriver_Name(t *testing.T) {
	d := newDriver(t)
	if d.Name() != "local" {
		t.Fatalf("expected local, got %s", d.Name())
	}
}

func TestLocalDriver_CreateAndDelete(t *testing.T) {
	ctx := context.Background()
	d := newDriver(t)

	handle, err := d.Create(ctx, "vol-1", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(string(handle)); err != nil {
		t.Fatalf("volume dir not created: %v", err)
	}

	if err := d.Delete(ctx, handle); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(string(handle)); !os.IsNotExist(err) {
		t.Fatal("volume dir should have been removed")
	}
}

func TestLocalDriver_DeleteWhileMounted(t *testing.T) {
	ctx := context.Background()
	d := newDriver(t)

	handle, _ := d.Create(ctx, "vol-2", 0)
	d.Mount(ctx, handle, "inst-1")

	if err := d.Delete(ctx, handle); err == nil {
		t.Fatal("expected error deleting mounted volume")
	}
}

func TestLocalDriver_Mount(t *testing.T) {
	ctx := context.Background()
	d := newDriver(t)

	handle, _ := d.Create(ctx, "vol-3", 0)
	mp, err := d.Mount(ctx, handle, "inst-1")
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if mp.HostPath != string(handle) {
		t.Fatalf("HostPath=%q want=%q", mp.HostPath, string(handle))
	}

	// Mount again with a second instance is allowed.
	_, err = d.Mount(ctx, handle, "inst-2")
	if err != nil {
		t.Fatalf("second Mount: %v", err)
	}

	// Delete while two instances mounted must fail.
	if err := d.Delete(ctx, handle); err == nil {
		t.Fatal("expected error deleting double-mounted volume")
	}
}

func TestLocalDriver_Unmount(t *testing.T) {
	ctx := context.Background()
	d := newDriver(t)

	handle, _ := d.Create(ctx, "vol-4", 0)
	d.Mount(ctx, handle, "inst-1")
	d.Mount(ctx, handle, "inst-2")

	d.Unmount(ctx, handle, "inst-1")
	// still mounted by inst-2
	if err := d.Delete(ctx, handle); err == nil {
		t.Fatal("expected error: inst-2 still mounted")
	}

	d.Unmount(ctx, handle, "inst-2")
	// now clean
	if err := d.Delete(ctx, handle); err != nil {
		t.Fatalf("Delete after full unmount: %v", err)
	}
}

func TestLocalDriver_MountMissingVolume(t *testing.T) {
	ctx := context.Background()
	d := newDriver(t)
	_, err := d.Mount(ctx, VolumeHandle("/nonexistent/path"), "inst-x")
	if err == nil {
		t.Fatal("expected error mounting missing volume")
	}
}

func TestLocalDriver_Stat(t *testing.T) {
	ctx := context.Background()
	d := newDriver(t)

	handle, _ := d.Create(ctx, "vol-5", 0)
	// Write a small file to the volume.
	os.WriteFile(filepath.Join(string(handle), "data.bin"), []byte("hello"), 0o644)

	stats, err := d.Stat(ctx, handle)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stats.NodeID != "node-1" {
		t.Fatalf("NodeID=%q want node-1", stats.NodeID)
	}
	if stats.UsedBytes != 5 {
		t.Fatalf("UsedBytes=%d want 5", stats.UsedBytes)
	}
}

func TestLocalDriver_Snapshot(t *testing.T) {
	ctx := context.Background()
	d := newDriver(t)

	handle, _ := d.Create(ctx, "vol-6", 0)
	os.WriteFile(filepath.Join(string(handle), "file.txt"), []byte("snapshot content"), 0o644)

	snapHandle, err := d.Snapshot(ctx, handle)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := os.Stat(string(snapHandle)); err != nil {
		t.Fatalf("snapshot dir not found: %v", err)
	}
	// Verify file was copied.
	data, err := os.ReadFile(filepath.Join(string(snapHandle), "file.txt"))
	if err != nil || string(data) != "snapshot content" {
		t.Fatalf("snapshot file mismatch: %v %s", err, data)
	}
}

func TestLocalDriver_RestoreFrom(t *testing.T) {
	ctx := context.Background()
	d := newDriver(t)

	handle, _ := d.Create(ctx, "vol-7", 0)
	os.WriteFile(filepath.Join(string(handle), "restore.txt"), []byte("restore me"), 0o644)

	snapHandle, _ := d.Snapshot(ctx, handle)

	newHandle, err := d.RestoreFrom(ctx, snapHandle)
	if err != nil {
		t.Fatalf("RestoreFrom: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(string(newHandle), "restore.txt"))
	if err != nil || string(data) != "restore me" {
		t.Fatalf("restored file mismatch: %v %s", err, data)
	}
}
