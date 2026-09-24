package virtualserver

import (
	"context"
	"testing"
)

// ── AdapterRegistry ───────────────────────────────────────────────────────────

func TestAdapterRegistry_RegisterGet(t *testing.T) {
	r := NewAdapterRegistry()
	r.Register(NoOpAdapter{})

	a, err := r.Get("noop")
	if err != nil {
		t.Fatalf("Get noop: %v", err)
	}
	if a.Name() != "noop" {
		t.Fatalf("expected noop, got %s", a.Name())
	}
}

func TestAdapterRegistry_GetMissing(t *testing.T) {
	r := NewAdapterRegistry()
	if _, err := r.Get("docker"); err == nil {
		t.Fatal("expected error for unregistered adapter")
	}
}

func TestAdapterRegistry_DuplicatePanics(t *testing.T) {
	r := NewAdapterRegistry()
	r.Register(NoOpAdapter{})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r.Register(NoOpAdapter{})
}

func TestAdapterRegistry_Default(t *testing.T) {
	r := NewAdapterRegistry()
	if _, err := r.Default(); err == nil {
		t.Fatal("expected error from empty registry")
	}
	r.Register(NoOpAdapter{})
	a, err := r.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if a.Name() != "noop" {
		t.Fatalf("unexpected default: %s", a.Name())
	}
}

// ── NoOpAdapter ───────────────────────────────────────────────────────────────

func TestNoOpAdapter(t *testing.T) {
	ctx := context.Background()
	a := NoOpAdapter{}

	if a.Name() != "noop" {
		t.Fatalf("Name: %s", a.Name())
	}

	res, err := a.Provision(ctx, &ProvisionRequest{InstanceID: "i1"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if res.RuntimeID != "noop-i1" {
		t.Fatalf("RuntimeID: %s", res.RuntimeID)
	}

	if err := a.Start(ctx, "i1", "noop-i1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Stop(ctx, "i1", "noop-i1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := a.Destroy(ctx, "i1", "noop-i1"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	state, err := a.Status(ctx, "i1", "noop-i1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if state != InstanceStateRunning {
		t.Fatalf("Status: expected Running, got %s", state)
	}
}
