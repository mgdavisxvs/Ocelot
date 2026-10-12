// Package api implements the Ocelot control-plane HTTP API for node agents.
// All routes follow AGENT_PROTOCOL.md (§4).
package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mgdavisxvs/Ocelot/compute/node"
)

const (
	replayWindowMs = 60_000      // §2.3 replay window
	maxBodyBytes   = 512 * 1024  // 512 KiB hard cap on all request bodies
)

// AgentHandler handles all /v1/nodes/* requests from ocelot-agent processes.
type AgentHandler struct {
	db             *sql.DB
	bootstrapToken string
}

// New returns a new AgentHandler backed by db.
func New(db *sql.DB, bootstrapToken string) *AgentHandler {
	return &AgentHandler{db: db, bootstrapToken: bootstrapToken}
}

// Mount registers all agent API routes on mux.
// Uses Go 1.22 method+pattern routing: "METHOD /path/{wildcard}".
func (h *AgentHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/nodes/register", h.handleRegister)
	mux.HandleFunc("POST /v1/nodes/{id}/reregister", h.handleReregister)
	mux.HandleFunc("POST /v1/nodes/{id}/heartbeat", h.handleHeartbeat)
	mux.HandleFunc("POST /v1/nodes/{id}/inventory", h.handleInventory)
	mux.HandleFunc("GET /v1/nodes/{id}/desired", h.handleDesired)
	mux.HandleFunc("POST /v1/nodes/{id}/health", h.handleHealth)
	mux.HandleFunc("POST /v1/nodes/{id}/metrics", h.handleMetrics)
}

// ── §4.1 Registration ─────────────────────────────────────────────────────────

func (h *AgentHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !h.verifyBootstrap(r) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing bootstrap token", false)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), false)
		return
	}
	var req node.RegisterRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON: "+err.Error(), false)
		return
	}
	if err := validateRegisterRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), false)
		return
	}

	// Hostname uniqueness check.
	var existingID string
	switch qErr := h.db.QueryRowContext(r.Context(),
		"SELECT id FROM nodes WHERE hostname = ?", req.Hostname).Scan(&existingID); {
	case qErr == nil:
		writeError(w, http.StatusConflict, "HOSTNAME_CONFLICT",
			fmt.Sprintf("hostname %s already registered as node %s", req.Hostname, existingID), false)
		return
	case qErr != sql.ErrNoRows:
		slog.Error("register: hostname lookup", "err", qErr)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}

	nodeID := uuid.New().String()
	secretHex, err := newSecretHex()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "secret generation failed", true)
		return
	}
	now := nowMs()
	labelsJSON, _ := json.Marshal(req.Labels)

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "tx begin failed", true)
		return
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err = tx.ExecContext(r.Context(), `
		INSERT INTO nodes
			(id, hostname, display_name, arch, cpu_cores, ram_mb, storage_gb,
			 labels, status, last_heartbeat, agent_version, node_secret_hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'joining', ?, ?, ?, ?)`,
		nodeID, req.Hostname, req.DisplayName, req.Arch,
		req.CPUCores, req.RAMMb, req.StorageGb,
		string(labelsJSON), now, req.AgentVersion, secretHex, now); err != nil {
		slog.Error("register: insert node", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}

	for _, g := range req.GPUs {
		if _, err = tx.ExecContext(r.Context(), `
			INSERT INTO node_gpus (id, node_id, device_index, model, vram_mb, cuda_cap, health)
			VALUES (?, ?, ?, ?, ?, ?, 'ready')`,
			uuid.New().String(), nodeID, g.DeviceIndex, g.Model, g.VRAMMb, g.CUDACap); err != nil {
			slog.Error("register: insert gpu", "device_index", g.DeviceIndex, "err", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
			return
		}
	}

	if _, err = tx.ExecContext(r.Context(),
		"INSERT INTO node_economics (node_id) VALUES (?)", nodeID); err != nil {
		slog.Warn("register: insert economics", "err", err) // non-fatal
	}

	_ = insertEvent(r.Context(), tx, nodeID, "node", nodeID, "node.joined",
		map[string]any{"hostname": req.Hostname, "arch": req.Arch, "agent_version": req.AgentVersion})

	if err = tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "tx commit failed", true)
		return
	}

	slog.Info("node registered", "node_id", nodeID, "hostname", req.Hostname, "arch", req.Arch)

	writeJSON(w, http.StatusCreated, node.RegisterResponse{
		NodeID:                 nodeID,
		NodeSecret:             secretHex,
		HeartbeatIntervalSec:   30,
		InventoryIntervalSec:   300,
		DesiredPollIntervalSec: 15,
	})
}

// ── §4.2 Re-registration (secret rotation) ───────────────────────────────────

func (h *AgentHandler) handleReregister(w http.ResponseWriter, r *http.Request) {
	if !h.verifyBootstrap(r) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing bootstrap token", false)
		return
	}
	nodeID := r.PathValue("id")
	if aerr := h.checkNodeExists(r.Context(), nodeID); aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	secretHex, err := newSecretHex()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "secret generation failed", true)
		return
	}
	if _, err = h.db.ExecContext(r.Context(),
		"UPDATE nodes SET node_secret_hash = ? WHERE id = ?", secretHex, nodeID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	_ = insertEvent(r.Context(), h.db, nodeID, "node", nodeID, "node.reregistered", nil)
	slog.Info("node re-registered (secret rotated)", "node_id", nodeID)
	writeJSON(w, http.StatusOK, map[string]string{"node_secret": secretHex})
}

// ── §4.3 Heartbeat ────────────────────────────────────────────────────────────

func (h *AgentHandler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	body, aerr := h.readSignedBody(r, nodeID)
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	var req node.HeartbeatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON", false)
		return
	}
	now := nowMs()
	res, err := h.db.ExecContext(r.Context(),
		"UPDATE nodes SET status = ?, last_heartbeat = ? WHERE id = ?",
		string(req.Status), now, nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusGone, "NODE_GONE", "node not registered", false)
		return
	}
	writeJSON(w, http.StatusOK, node.HeartbeatResponse{OK: true, ServerTimeMs: now})
}

// ── §4.4 Inventory ────────────────────────────────────────────────────────────

func (h *AgentHandler) handleInventory(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	body, aerr := h.readSignedBody(r, nodeID)
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	var req node.InventoryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON", false)
		return
	}
	now := nowMs()
	res, err := h.db.ExecContext(r.Context(), `
		UPDATE nodes
		SET cpu_cores = ?, ram_mb = ?, storage_gb = ?, agent_version = ?, last_heartbeat = ?
		WHERE id = ?`,
		req.CPUCores, req.RAMMb, req.StorageGb, req.AgentVersion, now, nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusGone, "NODE_GONE", "node not registered", false)
		return
	}
	for _, g := range req.GPUs {
		if _, err = h.db.ExecContext(r.Context(), `
			INSERT INTO node_gpus (id, node_id, device_index, model, vram_mb, cuda_cap, health)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(node_id, device_index) DO UPDATE SET
				model    = excluded.model,
				vram_mb  = excluded.vram_mb,
				cuda_cap = excluded.cuda_cap,
				health   = excluded.health`,
			uuid.New().String(), nodeID, g.DeviceIndex, g.Model, g.VRAMMb, g.CUDACap, string(g.Health)); err != nil {
			slog.Warn("inventory: gpu upsert", "node_id", nodeID, "device_index", g.DeviceIndex, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ── §4.5 Desired state poll ───────────────────────────────────────────────────

func (h *AgentHandler) handleDesired(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	// GET carries no body; pass nil (agent signs over empty body hash).
	if aerr := h.verifyAuth(r.Context(), r, nodeID, nil); aerr != nil {
		writeAPIError(w, aerr)
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, status, manifest
		FROM workloads
		WHERE node_id = ?
		  AND status IN ('dispatched','running','stopping','checkpointing')`,
		nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	defer rows.Close()

	workloads := make([]node.DesiredWorkload, 0)
	volumeIDs := make(map[string]struct{})

	for rows.Next() {
		var id, status, manifestJSON string
		if err := rows.Scan(&id, &status, &manifestJSON); err != nil {
			continue
		}
		var manifest node.WorkloadManifest
		if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
			slog.Warn("desired: unmarshal manifest", "workload_id", id, "err", err)
			continue
		}
		action := node.ActionRun
		switch status {
		case "stopping":
			action = node.ActionStop
		case "checkpointing":
			action = node.ActionCheckpoint
		}
		workloads = append(workloads, node.DesiredWorkload{ID: id, Action: action, Manifest: manifest})
		for _, vm := range manifest.Volumes {
			volumeIDs[vm.VolumeID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}

	volSpecs := make([]node.VolumeSpec, 0, len(volumeIDs))
	for vid := range volumeIDs {
		var driver string
		var sourcePath sql.NullString
		err := h.db.QueryRowContext(r.Context(),
			"SELECT driver, source_path FROM volumes WHERE id = ? AND deleted_at IS NULL", vid).
			Scan(&driver, &sourcePath)
		if err != nil {
			continue
		}
		volSpecs = append(volSpecs, node.VolumeSpec{
			VolumeID:   vid,
			Driver:     driver,
			SourcePath: sourcePath.String,
		})
	}

	writeJSON(w, http.StatusOK, node.DesiredStateResponse{
		SchemaVersion: 1,
		Workloads:     workloads,
		Volumes:       volSpecs,
	})
}

// ── §4.6 Health events ────────────────────────────────────────────────────────

func (h *AgentHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	body, aerr := h.readSignedBody(r, nodeID)
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	var req node.HealthRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON", false)
		return
	}
	if req.BufferOverflow {
		slog.Warn("health: agent buffer overflow, events may have been dropped", "node_id", nodeID)
	}
	for _, ev := range req.Events {
		h.applyHealthEvent(r.Context(), nodeID, ev)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *AgentHandler) applyHealthEvent(ctx context.Context, nodeID string, ev node.HealthEvent) {
	exitCode := 0
	if ev.ExitCode != nil {
		exitCode = *ev.ExitCode
	}
	var query string
	var args []any

	terminal := false
	retryable := false

	switch ev.Kind {
	case node.EventStarted:
		query = `UPDATE workloads SET status = 'running', started_at = ? WHERE id = ? AND node_id = ?`
		args = []any{ev.Ts, ev.WorkloadID, nodeID}

	case node.EventCompleted, node.EventStopped:
		query = `UPDATE workloads SET status = 'completed', finished_at = ?, exit_code = ?, failure_msg = ? WHERE id = ? AND node_id = ?`
		args = []any{ev.Ts, exitCode, ev.Message, ev.WorkloadID, nodeID}
		terminal = true

	case node.EventFailed, node.EventOOMKilled:
		query = `UPDATE workloads SET status = 'failed', finished_at = ?, exit_code = ?, failure_msg = ? WHERE id = ? AND node_id = ?`
		args = []any{ev.Ts, exitCode, ev.Message, ev.WorkloadID, nodeID}
		terminal = true
		retryable = true

	case node.EventTimedOut:
		query = `UPDATE workloads SET status = 'timed_out', finished_at = ?, exit_code = ?, failure_msg = ? WHERE id = ? AND node_id = ?`
		args = []any{ev.Ts, exitCode, ev.Message, ev.WorkloadID, nodeID}
		terminal = true
		retryable = true

	case node.EventCheckpoint:
		_ = insertEvent(ctx, h.db, nodeID, "workload", ev.WorkloadID, "workload.checkpoint",
			map[string]any{"ts": ev.Ts})
		return

	default:
		slog.Warn("health: unknown event kind", "kind", ev.Kind, "workload_id", ev.WorkloadID)
		return
	}

	if _, err := h.db.ExecContext(ctx, query, args...); err != nil {
		slog.Warn("health: update workload status", "workload_id", ev.WorkloadID, "kind", ev.Kind, "err", err)
	}
	if terminal {
		releaseWorkloadResources(ctx, h.db, ev.WorkloadID, ev.Ts)
		if retryable {
			tryRequeueIfEligible(ctx, h.db, ev.WorkloadID, ev.Message)
		}
	}
	_ = insertEvent(ctx, h.db, nodeID, "workload", ev.WorkloadID,
		"workload."+string(ev.Kind),
		map[string]any{"exit_code": exitCode, "message": ev.Message, "pid": ev.PID})
}

// ── §4.7 Metrics ──────────────────────────────────────────────────────────────

func (h *AgentHandler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	body, aerr := h.readSignedBody(r, nodeID)
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	var req node.MetricsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON", false)
		return
	}
	for _, wm := range req.Workloads {
		if _, err := h.db.ExecContext(r.Context(), `
			INSERT OR IGNORE INTO workload_metrics
				(workload_id, ts, interval_sec, cpu_millicore_sec, ram_mb_sec, gpu_vram_mb_sec,
				 bytes_read, bytes_written, net_bytes_in, net_bytes_out)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			wm.WorkloadID, req.Ts, req.IntervalSec,
			wm.CPUMillicoreSec, wm.RAMMbSec, wm.GPUVRAMMbSec,
			wm.BytesRead, wm.BytesWritten, wm.NetBytesIn, wm.NetBytesOut); err != nil {
			slog.Warn("metrics: insert workload metrics", "workload_id", wm.WorkloadID, "err", err)
		}
	}
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT OR IGNORE INTO node_metrics (node_id, ts, interval_sec, cpu_millicore_sec, ram_mb_sec, watts_sec)
		VALUES (?, ?, ?, ?, ?, ?)`,
		nodeID, req.Ts, req.IntervalSec,
		req.Node.CPUMillicoreSec, req.Node.RAMMbSec, req.Node.WattsSec); err != nil {
		slog.Warn("metrics: insert node metrics", "node_id", nodeID, "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ── Authentication ────────────────────────────────────────────────────────────

func (h *AgentHandler) verifyBootstrap(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	return strings.HasPrefix(auth, "Bearer ") &&
		strings.TrimPrefix(auth, "Bearer ") == h.bootstrapToken
}

// readSignedBody reads the body then verifies the HMAC signature.
// Combines readBody + verifyAuth for POST endpoints.
func (h *AgentHandler) readSignedBody(r *http.Request, nodeID string) ([]byte, *apiErr) {
	body, err := readBody(r)
	if err != nil {
		return nil, &apiErr{http.StatusBadRequest, "BAD_REQUEST", err.Error(), false}
	}
	if aerr := h.verifyAuth(r.Context(), r, nodeID, body); aerr != nil {
		return nil, aerr
	}
	return body, nil
}

// verifyAuth validates the X-Ocelot-* headers and HMAC signature.
// body may be nil for GET requests (treated as empty body for the hash).
func (h *AgentHandler) verifyAuth(ctx context.Context, r *http.Request, pathNodeID string, body []byte) *apiErr {
	headerNodeID := r.Header.Get("X-Ocelot-Node-ID")
	tsStr := r.Header.Get("X-Ocelot-Timestamp")
	sig := r.Header.Get("X-Ocelot-Signature")

	if headerNodeID == "" || tsStr == "" || sig == "" {
		return &apiErr{http.StatusUnauthorized, "UNAUTHORIZED", "missing auth headers", false}
	}
	if headerNodeID != pathNodeID {
		return &apiErr{http.StatusUnauthorized, "UNAUTHORIZED", "node_id header/path mismatch", false}
	}
	tsMs, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return &apiErr{http.StatusUnauthorized, "UNAUTHORIZED", "invalid timestamp format", false}
	}
	if diff := abs64(time.Now().UnixMilli() - tsMs); diff > replayWindowMs {
		return &apiErr{http.StatusUnauthorized, "UNAUTHORIZED", "request outside replay window", false}
	}

	var secretHex string
	switch qErr := h.db.QueryRowContext(ctx,
		"SELECT node_secret_hash FROM nodes WHERE id = ?", pathNodeID).Scan(&secretHex); {
	case qErr == sql.ErrNoRows:
		return &apiErr{http.StatusGone, "NODE_GONE", fmt.Sprintf("node %s not registered", pathNodeID), false}
	case qErr != nil:
		return &apiErr{http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true}
	}

	secret, err := hex.DecodeString(secretHex)
	if err != nil {
		return &apiErr{http.StatusInternalServerError, "INTERNAL_ERROR", "invalid stored secret", true}
	}

	// Compute expected HMAC per §2.2. nil body treated as empty.
	bodyBytes := body
	if bodyBytes == nil {
		bodyBytes = []byte{}
	}
	bodyHash := sha256.Sum256(bodyBytes)
	message := pathNodeID + ":" + tsStr + ":" + hex.EncodeToString(bodyHash[:])
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(message))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return &apiErr{http.StatusUnauthorized, "UNAUTHORIZED", "signature mismatch", false}
	}
	return nil
}

func (h *AgentHandler) checkNodeExists(ctx context.Context, nodeID string) *apiErr {
	var dummy string
	switch err := h.db.QueryRowContext(ctx, "SELECT id FROM nodes WHERE id = ?", nodeID).Scan(&dummy); {
	case err == nil:
		return nil
	case err == sql.ErrNoRows:
		return &apiErr{http.StatusGone, "NODE_GONE", fmt.Sprintf("node %s not registered", nodeID), false}
	default:
		return &apiErr{http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true}
	}
}

// ── Database helpers ──────────────────────────────────────────────────────────

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertEvent writes a structured event to the events journal.
// actor is typically a node_id or account_id; use "system" for internal events.
// org_id is left blank for node-initiated events (no FK on events.org_id).
func insertEvent(ctx context.Context, db execer, actor, subjectType, subjectID, kind string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, _ := json.Marshal(payload)
	_, err := db.ExecContext(ctx, `
		INSERT INTO events (id, ts, org_id, actor, kind, subject_type, subject_id, payload)
		VALUES (?, ?, '', ?, ?, ?, ?, ?)`,
		uuid.New().String(), nowMs(), actor, kind, subjectType, subjectID, string(payloadJSON))
	return err
}

// ── Validation ────────────────────────────────────────────────────────────────

func validateRegisterRequest(req *node.RegisterRequest) error {
	if req.Hostname == "" || len(req.Hostname) > 253 {
		return fmt.Errorf("hostname: required, max 253 chars")
	}
	if req.Arch != "amd64" && req.Arch != "arm64" {
		return fmt.Errorf("arch: must be amd64 or arm64")
	}
	if req.AgentVersion == "" {
		return fmt.Errorf("agent_version: required")
	}
	if req.CPUCores < 1 || req.CPUCores > 65535 {
		return fmt.Errorf("cpu_cores: must be 1–65535")
	}
	if req.RAMMb < 1 || req.RAMMb > 67108864 {
		return fmt.Errorf("ram_mb: must be 1–67108864")
	}
	if req.StorageGb < 0 || req.StorageGb > 1048576 {
		return fmt.Errorf("storage_gb: must be 0–1048576")
	}
	if len(req.GPUs) > 16 {
		return fmt.Errorf("gpus: max 16 entries")
	}
	for i, g := range req.GPUs {
		if g.DeviceIndex < 0 || g.DeviceIndex > 15 {
			return fmt.Errorf("gpus[%d].device_index: must be 0–15", i)
		}
		if g.Model == "" || len(g.Model) > 128 {
			return fmt.Errorf("gpus[%d].model: required, max 128 chars", i)
		}
		if g.VRAMMb < 1 || g.VRAMMb > 2097152 {
			return fmt.Errorf("gpus[%d].vram_mb: must be 1–2097152", i)
		}
	}
	if len(req.Labels) > 32 {
		return fmt.Errorf("labels: max 32 pairs")
	}
	return nil
}

// ── HTTP utilities ────────────────────────────────────────────────────────────

type apiErr struct {
	status  int
	code    string
	message string
	retry   bool
}

func writeAPIError(w http.ResponseWriter, e *apiErr) {
	writeError(w, e.status, e.code, e.message, e.retry)
}

func writeError(w http.ResponseWriter, status int, code, message string, retry bool) {
	writeJSON(w, status, node.ErrorResponse{
		Error: node.ErrorDetail{Code: code, Message: message, Retry: retry},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode failed", "err", err)
	}
}

func readBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
}

func nowMs() int64 { return time.Now().UnixMilli() }

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func newSecretHex() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
