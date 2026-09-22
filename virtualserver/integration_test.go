package virtualserver_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/adapter"
	vsconfig "github.com/mgdavisxvs/Ocelot/virtualserver/config"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/reconciler"
	"github.com/mgdavisxvs/Ocelot/virtualserver/scheduler"
	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
	localstorage "github.com/mgdavisxvs/Ocelot/virtualserver/storage/local"
	"github.com/mgdavisxvs/Ocelot/virtualserver/store"

	virtualserver "github.com/mgdavisxvs/Ocelot/virtualserver"
)

// openTestStore opens a fresh VS store backed by a temp-dir SQLite file.
func openTestStore(t *testing.T) *store.VSStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vs_integration.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestIntegration_DeclaredToRunning_E2E verifies the full lifecycle:
// register 3 nodes → declare service → reconcile passes → instance reaches running.
func TestIntegration_DeclaredToRunning_E2E(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// ── Register 3 ready nodes ────────────────────────────────────────────────
	nodeNames := []string{"r730-v100-01", "r730-v100-02", "r730-v100-03"}
	for _, name := range nodeNames {
		id, err := s.CreateNode(ctx, domain.Node{
			Name:        name,
			Arch:        "x86_64",
			State:       domain.NodeReady,
			TotalRAMMiB: 65536,
			AvailRAMMiB: 65536,
			CPUThreads:  32,
		})
		if err != nil {
			t.Fatalf("create node %s: %v", name, err)
		}
		t.Logf("registered node %s id=%s", name, id)
	}

	// ── Create namespace ──────────────────────────────────────────────────────
	if _, err := s.CreateNamespace(ctx, "integration", "integration test ns"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// ── Declare service ───────────────────────────────────────────────────────
	manifest := domain.ServiceManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Service",
		Metadata:   domain.ServiceMetadata{Namespace: "integration", Name: "e2e-svc"},
		Spec: domain.ServiceSpec{
			Artifact:  domain.ArtifactReference{Type: domain.ArtifactTypeOcelot, InfoHash: "0123456789abcdef0123456789abcdef01234567"},
			Runtime:   "mock",
			Instances: 1,
			Resources: domain.ResourceRequest{RAMMiB: 2048, CPUThreads: 4},
			Restart:   domain.RestartPolicy{Policy: "on-failure", MaximumAttempts: 3},
		},
	}
	svcID, err := s.CreateService(ctx, manifest)
	if err != nil {
		t.Fatalf("create service: %v", err)
	}

	// Ensure at least one declared instance exists.
	declared, err := s.ListInstances(ctx, string(domain.InstanceDeclared))
	if err != nil {
		t.Fatalf("list declared: %v", err)
	}
	if len(declared) == 0 {
		vsPath := domain.VSPath{Namespace: "integration", Service: "e2e-svc", Instance: "0"}
		instID, err := s.CreateInstance(ctx, svcID, vsPath)
		if err != nil {
			t.Fatalf("create instance: %v", err)
		}
		t.Logf("manually created instance %s", instID)
	}

	// ── Build and run reconciler ──────────────────────────────────────────────
	mockAd := adapter.NewMockAdapter()
	rec := reconciler.New(reconciler.Config{
		Store: s,
		Adapters: map[string]adapter.BackendAdapter{
			"mock": mockAd,
		},
		Sched:    scheduler.New(scheduler.DefaultWeights()),
		Interval: 100 * time.Millisecond,
	})

	// Run up to 10 passes (declared→scheduled→provisioning→starting→running = 4 state transitions).
	for pass := 0; pass < 10; pass++ {
		if err := rec.Reconcile(ctx); err != nil {
			t.Logf("reconcile pass %d error (non-fatal): %v", pass, err)
		}
		all, _ := s.ListInstances(ctx, "")
		if len(all) > 0 && all[0].State == domain.InstanceRunning {
			break
		}
	}

	// ── Assert final state ────────────────────────────────────────────────────
	all, err := s.ListInstances(ctx, "")
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no instances found after reconcile")
	}
	finalInst := all[0]
	if finalInst.State != domain.InstanceRunning {
		t.Errorf("expected running, got %q", finalInst.State)
		return
	}

	// ── Verify allocation ─────────────────────────────────────────────────────
	alloc, err := s.GetActiveAllocation(ctx, finalInst.ID)
	if err != nil {
		t.Errorf("expected active allocation, got: %v", err)
	} else {
		if alloc.RAMMiB != 2048 {
			t.Errorf("expected RAMMiB=2048, got %d", alloc.RAMMiB)
		}
		t.Logf("allocation: node=%s cpu=%d ram=%dMiB", alloc.NodeID, alloc.CPUThreads, alloc.RAMMiB)
	}

	// ── Verify instance queryable by ID ───────────────────────────────────────
	inst, err := s.GetInstance(ctx, finalInst.ID)
	if err != nil {
		t.Errorf("GetInstance: %v", err)
	} else if inst.State != domain.InstanceRunning {
		t.Errorf("GetInstance state: expected running, got %q", inst.State)
	}

	// ── Verify service queryable ──────────────────────────────────────────────
	svc, err := s.GetService(ctx, svcID)
	if err != nil {
		t.Errorf("GetService: %v", err)
	} else if svc.Manifest.Metadata.Name != "e2e-svc" {
		t.Errorf("unexpected service name %q", svc.Manifest.Metadata.Name)
	}

	t.Logf("E2E complete: instance %s running on node %s", finalInst.ID, finalInst.NodeID)
}

// TestIntegration_Volume_DeclaredToReady verifies the volume provisioning path:
// declare → reconcile → provisioning → ready, with driver handle persisted.
func TestIntegration_Volume_DeclaredToReady(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateNamespace(ctx, "vol-test", ""); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	drv := localstorage.New("node-1", t.TempDir())

	vol := domain.Volume{
		ID: "vol-int-1",
		Manifest: domain.VolumeManifest{
			APIVersion: "virtualserver/v1",
			Kind:       "Volume",
			Metadata:   domain.VolumeMetadata{Namespace: "vol-test", Name: "data"},
			Spec:       domain.VolumeSpec{Class: "local", CapacityMiB: 1024, AccessMode: domain.VolumeAccessRWO},
		},
		State: domain.VolumeDeclared,
	}
	if _, err := s.CreateVolume(ctx, vol); err != nil {
		t.Fatalf("create volume: %v", err)
	}

	rec := reconciler.New(reconciler.Config{
		Store:        s,
		Adapters:     map[string]adapter.BackendAdapter{},
		ClassDrivers: map[string]storage.StorageDriver{"local": drv},
		Sched:        scheduler.New(scheduler.DefaultWeights()),
		Interval:     100 * time.Millisecond,
	})

	for pass := 0; pass < 3; pass++ {
		if err := rec.Reconcile(ctx); err != nil {
			t.Logf("reconcile pass %d: %v", pass, err)
		}
	}

	final, err := s.GetVolume(ctx, "vol-int-1")
	if err != nil {
		t.Fatalf("get volume: %v", err)
	}
	if final.State != domain.VolumeReady {
		t.Errorf("expected ready, got %q", final.State)
	}
	if final.DriverHandle["path"] == "" {
		t.Error("expected driver handle path to be set")
	}
	t.Logf("volume ready at path: %s", final.DriverHandle["path"])
}

// TestIntegration_Volume_ReleasingToReleased verifies the teardown path:
// provision → set releasing → reconcile → driver.Delete → released state.
func TestIntegration_Volume_ReleasingToReleased(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateNamespace(ctx, "rel-test", ""); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	drv := localstorage.New("node-1", t.TempDir())

	vol := domain.Volume{
		ID: "vol-rel-1",
		Manifest: domain.VolumeManifest{
			APIVersion: "virtualserver/v1",
			Kind:       "Volume",
			Metadata:   domain.VolumeMetadata{Namespace: "rel-test", Name: "ephemeral"},
			Spec:       domain.VolumeSpec{Class: "local", CapacityMiB: 512, AccessMode: domain.VolumeAccessRWO},
		},
		State: domain.VolumeDeclared,
	}
	if _, err := s.CreateVolume(ctx, vol); err != nil {
		t.Fatalf("create volume: %v", err)
	}

	rec := reconciler.New(reconciler.Config{
		Store:        s,
		Adapters:     map[string]adapter.BackendAdapter{},
		ClassDrivers: map[string]storage.StorageDriver{"local": drv},
		Sched:        scheduler.New(scheduler.DefaultWeights()),
		Interval:     100 * time.Millisecond,
	})

	// Provision: declared → ready
	for pass := 0; pass < 3; pass++ {
		rec.Reconcile(ctx) //nolint:errcheck
	}
	provisioned, err := s.GetVolume(ctx, "vol-rel-1")
	if err != nil {
		t.Fatalf("get provisioned volume: %v", err)
	}
	if provisioned.State != domain.VolumeReady {
		t.Fatalf("expected ready after provisioning, got %q", provisioned.State)
	}
	diskPath := provisioned.DriverHandle["path"]

	// Transition to releasing (simulates API DELETE).
	if err := s.UpdateVolumeState(ctx, "vol-rel-1", domain.VolumeReleasing); err != nil {
		t.Fatalf("set releasing: %v", err)
	}

	// Reconcile: releasing → driver.Delete → released
	for pass := 0; pass < 2; pass++ {
		rec.Reconcile(ctx) //nolint:errcheck
	}

	final, err := s.GetVolume(ctx, "vol-rel-1")
	if err != nil {
		t.Fatalf("get released volume: %v", err)
	}
	if final.State != domain.VolumeReleased {
		t.Errorf("expected released, got %q", final.State)
	}
	// Verify disk cleanup: directory must be gone after release.
	if _, statErr := os.Stat(diskPath); !os.IsNotExist(statErr) {
		t.Errorf("expected directory %s to be removed after release", diskPath)
	}
	t.Logf("volume released and disk cleaned: %s", diskPath)
}

// TestIntegration_Config_Disabled verifies that New returns nil when disabled.
func TestIntegration_Config_Disabled(t *testing.T) {
	cfg := vsconfig.VSConfig{Enabled: false}
	vs, err := virtualserver.New(cfg, nil)
	if err != nil {
		t.Fatalf("expected nil error when disabled, got %v", err)
	}
	if vs != nil {
		t.Error("expected nil VirtualServer when disabled")
	}
}
