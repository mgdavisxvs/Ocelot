package agent

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/compute/node"
)

// ── Credential storage ────────────────────────────────────────────────────────

func TestSaveLoadCredentials(t *testing.T) {
	dir := t.TempDir()
	c := &credentials{NodeID: "node-001", NodeSecret: "aabbccdd"}
	if err := saveCredentials(dir, c); err != nil {
		t.Fatal(err)
	}
	got, err := loadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected credentials, got nil")
	}
	if got.NodeID != c.NodeID || got.NodeSecret != c.NodeSecret {
		t.Fatalf("mismatch: got %+v", got)
	}
}

func TestLoadCredentialsAbsent(t *testing.T) {
	dir := t.TempDir()
	got, err := loadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil credentials on fresh dir")
	}
}

func TestCredentialsFileMode(t *testing.T) {
	dir := t.TempDir()
	if err := saveCredentials(dir, &credentials{NodeID: "x", NodeSecret: "y"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, credentialsFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credentials file mode %o, want 0600", info.Mode().Perm())
	}
}

// ── HMAC signing ──────────────────────────────────────────────────────────────

func TestClientSignature(t *testing.T) {
	secretHex := hex.EncodeToString(make([]byte, 32)) // 64 zero hex chars
	nodeID := "test-node-id"

	cli, err := newClient("http://localhost", nodeID, secretHex, false)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := hex.DecodeString(secretHex)

	body := []byte(`{"ts":1234567890000}`)
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/v1/test", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	cli.sign(req, body)

	// Verify headers are present.
	if req.Header.Get("X-Ocelot-Node-ID") != nodeID {
		t.Fatal("missing or wrong X-Ocelot-Node-ID")
	}
	tsStr := req.Header.Get("X-Ocelot-Timestamp")
	if tsStr == "" {
		t.Fatal("missing X-Ocelot-Timestamp")
	}
	sig := req.Header.Get("X-Ocelot-Signature")
	if sig == "" {
		t.Fatal("missing X-Ocelot-Signature")
	}

	// Recompute and verify signature.
	bodyHash := sha256.Sum256(body)
	bodyHashHex := hex.EncodeToString(bodyHash[:])
	message := nodeID + ":" + tsStr + ":" + bodyHashHex
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(message))
	expected := hex.EncodeToString(mac.Sum(nil))

	if sig != expected {
		t.Fatalf("signature mismatch\ngot:  %s\nwant: %s", sig, expected)
	}
}

func TestClientSignatureEmptyBody(t *testing.T) {
	secretHex := hex.EncodeToString(make([]byte, 32))
	cli, err := newClient("http://localhost", "nid", secretHex, false)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://localhost/v1/desired", nil)
	cli.sign(req, nil) // no body

	// Signature over empty body hash should not panic.
	if req.Header.Get("X-Ocelot-Signature") == "" {
		t.Fatal("signature absent for GET with no body")
	}
}

// ── Health buffer ─────────────────────────────────────────────────────────────

func TestHealthBufferDrain(t *testing.T) {
	hb := newHealthBuffer()
	ec := 0
	for i := 0; i < 5; i++ {
		hb.push(node.HealthEvent{WorkloadID: strconv.Itoa(i), Kind: node.EventStarted, ExitCode: &ec})
	}
	events, overflow := hb.drain()
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	if overflow {
		t.Fatal("unexpected overflow")
	}
	// Buffer is now empty.
	events2, _ := hb.drain()
	if len(events2) != 0 {
		t.Fatal("expected empty after drain")
	}
}

func TestHealthBufferOverflow(t *testing.T) {
	hb := newHealthBuffer()
	ec := 0
	for i := 0; i < maxBufferedEvents+10; i++ {
		hb.push(node.HealthEvent{WorkloadID: strconv.Itoa(i), ExitCode: &ec})
	}
	events, overflow := hb.drain()
	if !overflow {
		t.Fatal("expected overflow flag")
	}
	if len(events) != maxBufferedEvents {
		t.Fatalf("expected %d events, got %d", maxBufferedEvents, len(events))
	}
}

// ── Registration flow ─────────────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	// Fake control plane that handles registration.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/nodes/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-bootstrap-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		resp := node.RegisterResponse{
			NodeID:                 "registered-node-uuid",
			NodeSecret:             hex.EncodeToString(make([]byte, 32)),
			HeartbeatIntervalSec:   30,
			InventoryIntervalSec:   300,
			DesiredPollIntervalSec: 15,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	cfg := &Config{
		ControlPlaneURL: srv.URL,
		BootstrapToken:  "test-bootstrap-token",
		DataDir:         dir,
		Labels:          map[string]string{},
		TLSSkipVerify:   false,
	}

	a := &Agent{
		cfg:               cfg,
		eventsCh:          make(chan node.HealthEvent, 8),
		healthBuf:         newHealthBuffer(),
		executor:          NewProcessAdapter(make(chan node.HealthEvent, 8)),
		heartbeatInterval: defaultHeartbeatSec * time.Second,
		inventoryInterval: defaultInventorySec * time.Second,
		desiredInterval:   defaultDesiredSec * time.Second,
	}

	if err := a.register(context.Background()); err != nil {
		t.Fatalf("register: %v", err)
	}

	if a.creds.NodeID != "registered-node-uuid" {
		t.Fatalf("wrong node_id: %s", a.creds.NodeID)
	}

	// Credentials must be persisted to disk.
	loaded, err := loadCredentials(dir)
	if err != nil || loaded == nil {
		t.Fatal("credentials not persisted")
	}
	if loaded.NodeID != "registered-node-uuid" {
		t.Fatal("persisted node_id mismatch")
	}
}

// ── Desired state reconciliation ──────────────────────────────────────────────

func TestReconcileStartsNewWorkload(t *testing.T) {
	// Write a trivial script to run as a workload.
	dir := t.TempDir()
	script := filepath.Join(dir, "hello.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho hello\n"), 0755) //nolint:errcheck

	eventsCh := make(chan node.HealthEvent, 16)
	exec_ := NewProcessAdapter(eventsCh)
	hb := newHealthBuffer()

	a := &Agent{
		cfg:       &Config{DataDir: dir},
		executor:  exec_,
		healthBuf: hb,
		eventsCh:  eventsCh,
	}

	desired := &node.DesiredStateResponse{
		SchemaVersion: 1,
		Workloads: []node.DesiredWorkload{
			{
				ID:     "wl-test-001",
				Action: node.ActionRun,
				Manifest: node.WorkloadManifest{
					Name:       "test-workload",
					Type:       "script",
					Entrypoint: script,
				},
			},
		},
	}

	a.reconcile(desired)

	// Allow process to start and exit.
	time.Sleep(300 * time.Millisecond)

	// Should have emitted at least a started event (via executor watch).
	select {
	case ev := <-eventsCh:
		if ev.WorkloadID != "wl-test-001" {
			t.Fatalf("unexpected workload_id: %s", ev.WorkloadID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no health event received after workload exit")
	}
}

func TestReconcileStopsRemovedWorkload(t *testing.T) {
	dir := t.TempDir()
	// Script that sleeps so we can stop it.
	script := filepath.Join(dir, "sleep.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0755) //nolint:errcheck

	eventsCh := make(chan node.HealthEvent, 16)
	exec_ := NewProcessAdapter(eventsCh)
	hb := newHealthBuffer()

	a := &Agent{
		cfg:       &Config{DataDir: dir},
		executor:  exec_,
		healthBuf: hb,
		eventsCh:  eventsCh,
	}

	// Start the workload.
	startDesired := &node.DesiredStateResponse{
		SchemaVersion: 1,
		Workloads: []node.DesiredWorkload{
			{ID: "wl-sleep", Action: node.ActionRun,
				Manifest: node.WorkloadManifest{Name: "sleep", Type: "script", Entrypoint: script}},
		},
	}
	a.reconcile(startDesired)
	time.Sleep(200 * time.Millisecond) // let it start

	if !exec_.IsRunning("wl-sleep") {
		t.Fatal("expected workload to be running")
	}

	// Now reconcile with empty desired set — should stop the workload.
	a.reconcile(&node.DesiredStateResponse{SchemaVersion: 1})
	time.Sleep(500 * time.Millisecond)

	if exec_.IsRunning("wl-sleep") {
		t.Fatal("expected workload to be stopped")
	}
}

// ── Inventory ─────────────────────────────────────────────────────────────────

func TestCollectInventoryBasic(t *testing.T) {
	inv := collectInventory("1.0.0-test")
	if inv.CPUCores <= 0 {
		t.Fatal("CPUCores must be > 0")
	}
	if inv.Ts <= 0 {
		t.Fatal("Ts must be positive")
	}
	if inv.AgentVersion != "1.0.0-test" {
		t.Fatal("AgentVersion mismatch")
	}
}
