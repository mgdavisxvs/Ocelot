// Package agent implements the Ocelot node agent.
// It registers with the control plane, reports inventory, polls desired state,
// reconciles running workloads, and streams telemetry.
// See AGENT_PROTOCOL.md for the complete wire protocol specification.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/mgdavisxvs/Ocelot/compute/node"
)

const agentVersion = "1.0.0"

// defaultIntervals are overridden by the values returned from the control plane.
const (
	defaultHeartbeatSec  = 30
	defaultInventorySec  = 300
	defaultDesiredSec    = 15
	defaultMetricsSec    = 60
	healthEventChanSize  = 256
)

// Agent is the root struct for the Ocelot node agent.
type Agent struct {
	cfg       *Config
	creds     *credentials
	client    *client
	executor  ExecutionAdapter
	healthBuf *healthBuffer
	eventsCh  chan node.HealthEvent

	// Intervals received from control plane at registration.
	heartbeatInterval time.Duration
	inventoryInterval time.Duration
	desiredInterval   time.Duration
}

// New creates and initialises an Agent. It runs the registration flow
// (§7 startup sequence) before returning.
func New(cfg *Config) (*Agent, error) {
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("agent: mkdir data dir: %w", err)
	}

	eventsCh := make(chan node.HealthEvent, healthEventChanSize)

	a := &Agent{
		cfg:               cfg,
		eventsCh:          eventsCh,
		healthBuf:         newHealthBuffer(),
		executor:          NewProcessAdapter(eventsCh),
		heartbeatInterval: defaultHeartbeatSec * time.Second,
		inventoryInterval: defaultInventorySec * time.Second,
		desiredInterval:   defaultDesiredSec * time.Second,
	}

	// ── Step 1: load or acquire credentials (§7 step 1) ─────────────────────
	if err := a.ensureRegistered(context.Background()); err != nil {
		return nil, fmt.Errorf("agent: registration: %w", err)
	}

	return a, nil
}

// Run executes the agent main loop (§7 steps 2-4). It blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) {
	slog.Info("agent starting", "node_id", a.creds.NodeID, "version", agentVersion,
		"os", runtime.GOOS, "arch", runtime.GOARCH)

	// ── Step 2: initial inventory report ─────────────────────────────────────
	a.sendInventory(ctx)

	// ── Step 3: initial desired state poll + reconcile ────────────────────────
	if desired := a.pollDesired(ctx); desired != nil {
		a.reconcile(desired)
	}

	// ── Step 4: main loop ─────────────────────────────────────────────────────
	heartbeatTick := time.NewTicker(a.heartbeatInterval)
	inventoryTick := time.NewTicker(a.inventoryInterval)
	desiredTick   := time.NewTicker(a.desiredInterval)
	metricsTick   := time.NewTicker(defaultMetricsSec * time.Second)

	defer heartbeatTick.Stop()
	defer inventoryTick.Stop()
	defer desiredTick.Stop()
	defer metricsTick.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("agent: context cancelled, shutting down")
			a.sendHeartbeat(context.Background(), node.StatusStopping)
			return

		case ev := <-a.eventsCh:
			// Executor emits events directly to this channel; buffer them.
			a.healthBuf.push(ev)
			// Flush health events immediately on state changes (do not wait for tick).
			a.flushHealth(ctx)

		case <-heartbeatTick.C:
			a.sendHeartbeat(ctx, a.currentStatus())

		case <-inventoryTick.C:
			a.sendInventory(ctx)

		case <-desiredTick.C:
			if desired := a.pollDesired(ctx); desired != nil {
				a.reconcile(desired)
			}

		case <-metricsTick.C:
			a.sendMetrics(ctx)
		}
	}
}

// ensureRegistered loads stored credentials or runs the registration flow.
func (a *Agent) ensureRegistered(ctx context.Context) error {
	creds, err := loadCredentials(a.cfg.DataDir)
	if err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}

	if creds != nil {
		slog.Info("agent: credentials loaded from storage", "node_id", creds.NodeID)
		a.creds = creds
		cli, err := newClient(a.cfg.ControlPlaneURL, creds.NodeID, creds.NodeSecret, a.cfg.TLSSkipVerify)
		if err != nil {
			return err
		}
		a.client = cli
		return nil
	}

	// No stored credentials: run first-time registration.
	slog.Info("agent: no credentials found, registering with control plane")
	return a.register(ctx)
}

// register performs the initial registration handshake (§4.1).
func (a *Agent) register(ctx context.Context) error {
	if a.cfg.BootstrapToken == "" {
		return fmt.Errorf("bootstrap token is required for first-time registration")
	}

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("get hostname: %w", err)
	}

	inv := collectInventory(agentVersion)
	gpuSpecs := make([]node.GPUSpec, len(inv.GPUs))
	for i, g := range inv.GPUs {
		gpuSpecs[i] = node.GPUSpec{
			DeviceIndex: g.DeviceIndex,
			Model:       g.Model,
			VRAMMb:      g.VRAMMb,
			CUDACap:     g.CUDACap,
		}
	}

	req := node.RegisterRequest{
		Hostname:     hostname,
		DisplayName:  a.cfg.DisplayName,
		Arch:         runtime.GOARCH,
		AgentVersion: agentVersion,
		CPUCores:     inv.CPUCores,
		RAMMb:        inv.RAMMb,
		StorageGb:    inv.StorageGb,
		GPUs:         gpuSpecs,
		Labels:       a.cfg.Labels,
	}

	bc := newBootstrapClient(a.cfg.ControlPlaneURL, a.cfg.BootstrapToken, a.cfg.TLSSkipVerify)
	var resp node.RegisterResponse
	if err := bc.post(ctx, "/v1/nodes/register", &req, &resp); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	creds := &credentials{
		NodeID:     resp.NodeID,
		NodeSecret: resp.NodeSecret,
	}
	if err := saveCredentials(a.cfg.DataDir, creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	a.creds = creds

	cli, err := newClient(a.cfg.ControlPlaneURL, creds.NodeID, creds.NodeSecret, a.cfg.TLSSkipVerify)
	if err != nil {
		return err
	}
	a.client = cli

	// Apply intervals from control plane response.
	if resp.HeartbeatIntervalSec > 0 {
		a.heartbeatInterval = time.Duration(resp.HeartbeatIntervalSec) * time.Second
	}
	if resp.InventoryIntervalSec > 0 {
		a.inventoryInterval = time.Duration(resp.InventoryIntervalSec) * time.Second
	}
	if resp.DesiredPollIntervalSec > 0 {
		a.desiredInterval = time.Duration(resp.DesiredPollIntervalSec) * time.Second
	}

	slog.Info("agent: registered successfully",
		"node_id", creds.NodeID,
		"heartbeat_interval", a.heartbeatInterval,
		"desired_interval", a.desiredInterval,
	)
	return nil
}

func (a *Agent) sendHeartbeat(ctx context.Context, status node.Status) {
	req := node.HeartbeatRequest{
		Ts:         nowMs(),
		Status:     status,
		LoadAvg1m:  readLoadAvg1m(),
		CPUUsedPct: 100 - readCPUIdlePct(),
		RAMUsedMb:  readTotalRAMMb() - readFreeRAMMb(),
		UptimeSec:  readUptimeSec(),
	}
	var resp node.HeartbeatResponse
	path := fmt.Sprintf("/v1/nodes/%s/heartbeat", a.creds.NodeID)
	if err := a.client.post(ctx, path, &req, &resp); err != nil {
		slog.Warn("heartbeat failed", "err", err)
		a.handleAPIError(ctx, err)
		return
	}
	// Check for clock skew > 30s.
	skew := abs64(nowMs() - resp.ServerTimeMs)
	if skew > 30_000 {
		slog.Warn("clock skew detected", "skew_ms", skew)
	}
}

func (a *Agent) sendInventory(ctx context.Context) {
	inv := collectInventory(agentVersion)
	path := fmt.Sprintf("/v1/nodes/%s/inventory", a.creds.NodeID)
	var resp struct{ OK bool `json:"ok"` }
	if err := a.client.post(ctx, path, &inv, &resp); err != nil {
		slog.Warn("inventory push failed", "err", err)
	}
}

func (a *Agent) pollDesired(ctx context.Context) *node.DesiredStateResponse {
	path := fmt.Sprintf("/v1/nodes/%s/desired", a.creds.NodeID)
	var resp node.DesiredStateResponse
	if err := a.client.get(ctx, path, &resp); err != nil {
		slog.Warn("desired state poll failed", "err", err)
		a.handleAPIError(ctx, err)
		return nil
	}
	return &resp
}

func (a *Agent) flushHealth(ctx context.Context) {
	events, overflow := a.healthBuf.drain()
	if len(events) == 0 {
		return
	}
	req := node.HealthRequest{
		Ts:             nowMs(),
		Events:         events,
		BufferOverflow: overflow,
	}
	path := fmt.Sprintf("/v1/nodes/%s/health", a.creds.NodeID)
	var resp struct{ OK bool `json:"ok"` }
	if err := a.client.post(ctx, path, &req, &resp); err != nil {
		slog.Warn("health push failed", "err", err)
		// Re-buffer events on failure so they are not lost.
		for _, ev := range events {
			a.healthBuf.push(ev)
		}
	}
}

func (a *Agent) sendMetrics(ctx context.Context) {
	runningIDs := a.executor.RunningIDs()
	wlMetrics := make([]node.WorkloadMetrics, 0, len(runningIDs))
	for _, id := range runningIDs {
		// Phase 3: read actual cgroup accounting. For now: zero counters.
		wlMetrics = append(wlMetrics, node.WorkloadMetrics{WorkloadID: id})
	}
	req := node.MetricsRequest{
		Ts:          nowMs(),
		IntervalSec: defaultMetricsSec,
		Workloads:   wlMetrics,
		Node:        node.NodeMetrics{},
	}
	path := fmt.Sprintf("/v1/nodes/%s/metrics", a.creds.NodeID)
	var resp struct{ OK bool `json:"ok"` }
	if err := a.client.post(ctx, path, &req, &resp); err != nil {
		slog.Warn("metrics push failed", "err", err)
	}
}

// handleAPIError reacts to structured API errors per §5 error code table.
func (a *Agent) handleAPIError(ctx context.Context, err error) {
	aerr, ok := err.(*apiError)
	if !ok {
		return
	}
	if aerr.IsGone() || aerr.IsUnauth() {
		slog.Warn("agent: control plane says re-register", "code", aerr.code)
		if regErr := a.register(ctx); regErr != nil {
			slog.Error("agent: re-registration failed", "err", regErr)
		}
	}
}

func (a *Agent) currentStatus() node.Status {
	// Phase 3: derive from CPU/RAM/GPU pressure.
	return node.StatusReady
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func nowMs() int64 {
	return time.Now().UnixMilli()
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func readLoadAvg1m() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	var v float64
	fmt.Sscanf(string(data), "%f", &v)
	return v
}

func readUptimeSec() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	var v float64
	fmt.Sscanf(string(data), "%f", &v)
	return int64(v)
}

func statPath(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
