package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

func testNode() domain.Node {
	return domain.Node{ID: "n1", Name: "test-node", Arch: "x86_64", State: domain.NodeReady}
}

func testReq(id string) ProvisionRequest {
	return ProvisionRequest{
		InstanceID: id,
		NodeID:     "n1",
		Manifest: domain.ServiceManifest{
			APIVersion: "virtualserver/v1",
			Kind:       "Service",
			Metadata:   domain.ServiceMetadata{Namespace: "test", Name: "svc"},
		},
	}
}

func TestMockAdapter_HappyPath(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()
	n := testNode()

	if err := m.Probe(ctx, n); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	handle, err := m.Provision(ctx, testReq("inst-1"))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := m.Start(ctx, handle); err != nil {
		t.Fatalf("Start: %v", err)
	}
	status, err := m.Inspect(ctx, handle)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !status.Running {
		t.Error("expected running=true after Start")
	}
	hr, err := m.Health(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	if !hr.Healthy {
		t.Error("expected healthy=true")
	}
	if err := m.Stop(ctx, handle, StopGraceful); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(ctx, handle); err != nil {
		t.Fatal(err)
	}

	if m.ProvisionCalls != 1 || m.StartCalls != 1 || m.StopCalls != 1 || m.DestroyCalls != 1 {
		t.Errorf("call counts: provision=%d start=%d stop=%d destroy=%d",
			m.ProvisionCalls, m.StartCalls, m.StopCalls, m.DestroyCalls)
	}
}

func TestMockAdapter_FailProvision(t *testing.T) {
	m := NewMockAdapter()
	m.FailMode = FailProvision
	_, err := m.Provision(context.Background(), testReq("x"))
	if err == nil {
		t.Error("expected provision failure")
	}
}

func TestMockAdapter_FailStart(t *testing.T) {
	m := NewMockAdapter()
	handle, _ := m.Provision(context.Background(), testReq("x"))
	m.FailMode = FailStart
	if err := m.Start(context.Background(), handle); err == nil {
		t.Error("expected start failure")
	}
}

func TestMockAdapter_ContextCancel_Provision(t *testing.T) {
	m := NewMockAdapter()
	m.ProvisionDelay = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := m.Provision(ctx, testReq("x"))
	if err == nil {
		t.Error("expected context cancellation")
	}
}

func TestMockAdapter_Reset(t *testing.T) {
	m := NewMockAdapter()
	m.Provision(context.Background(), testReq("x"))
	m.Reset()
	if m.ProvisionCalls != 0 {
		t.Error("expected ProvisionCalls reset to 0")
	}
	if m.IsProvisioned("x") {
		t.Error("expected provisioned state cleared")
	}
}

func TestMockAdapter_InspectAfterStop(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()
	handle, _ := m.Provision(ctx, testReq("i1"))
	m.Start(ctx, handle)
	m.Stop(ctx, handle, StopGraceful)
	status, _ := m.Inspect(ctx, handle)
	if status.Running {
		t.Error("expected running=false after stop")
	}
}
