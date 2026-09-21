package virtualserver_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/adapter"
	vsconfig "github.com/mgdavisxvs/Ocelot/virtualserver/config"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/reconciler"
	"github.com/mgdavisxvs/Ocelot/virtualserver/scheduler"
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
