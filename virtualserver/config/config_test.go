package config

import (
	"testing"
	"time"
)

func TestDefaultVSConfig_Values(t *testing.T) {
	cfg := DefaultVSConfig()

	if cfg.Enabled {
		t.Error("expected Enabled=false by default")
	}
	if cfg.Port != ":9091" {
		t.Errorf("expected Port=:9091, got %q", cfg.Port)
	}
	if cfg.AdminKey != "" {
		t.Errorf("expected empty AdminKey, got %q", cfg.AdminKey)
	}
	if cfg.DBPath != "data/db/vs.db" {
		t.Errorf("expected DBPath=data/db/vs.db, got %q", cfg.DBPath)
	}
	if cfg.ReconcileEvery != 15*time.Second {
		t.Errorf("expected ReconcileEvery=15s, got %v", cfg.ReconcileEvery)
	}
	if cfg.MaxBodyBytes != 1<<20 {
		t.Errorf("expected MaxBodyBytes=1MiB, got %d", cfg.MaxBodyBytes)
	}
	if cfg.RequestTimeout != 30*time.Second {
		t.Errorf("expected RequestTimeout=30s, got %v", cfg.RequestTimeout)
	}
}

func TestDefaultVSConfig_StorageClassesNil(t *testing.T) {
	cfg := DefaultVSConfig()
	if cfg.StorageClasses != nil {
		t.Errorf("expected nil StorageClasses, got %v", cfg.StorageClasses)
	}
}

func TestVolumeClass_Fields(t *testing.T) {
	vc := VolumeClass{
		Name:   "fast-ssd",
		Driver: "nvme",
		Params: map[string]string{"iops": "10000"},
	}
	if vc.Name != "fast-ssd" {
		t.Errorf("unexpected Name: %q", vc.Name)
	}
	if vc.Driver != "nvme" {
		t.Errorf("unexpected Driver: %q", vc.Driver)
	}
	if vc.Params["iops"] != "10000" {
		t.Errorf("unexpected iops param: %q", vc.Params["iops"])
	}
}

func TestVSConfig_Override(t *testing.T) {
	cfg := DefaultVSConfig()
	cfg.Enabled = true
	cfg.AdminKey = "secret"
	cfg.Port = ":8080"

	if !cfg.Enabled {
		t.Error("expected Enabled=true after override")
	}
	if cfg.AdminKey != "secret" {
		t.Errorf("expected AdminKey=secret, got %q", cfg.AdminKey)
	}
	if cfg.Port != ":8080" {
		t.Errorf("expected Port=:8080, got %q", cfg.Port)
	}
	// Other fields unchanged from default.
	if cfg.DBPath != "data/db/vs.db" {
		t.Errorf("unrelated field changed: DBPath=%q", cfg.DBPath)
	}
}
