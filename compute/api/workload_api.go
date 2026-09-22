package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/mgdavisxvs/Ocelot/compute/node"
)

// ── Request / response types ──────────────────────────────────────────────────

type SubmitWorkloadRequest struct {
	Name       string                `json:"name"`
	Priority   int                   `json:"priority"`
	MaxRetries int                   `json:"max_retries"`
	Deadline   *int64                `json:"deadline,omitempty"` // unix epoch ms
	ProjectID  string                `json:"project_id,omitempty"`
	Manifest   node.WorkloadManifest `json:"manifest"`
}

type WorkloadSummary struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id"`
	ProjectID   string `json:"project_id,omitempty"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Priority    int    `json:"priority"`
	NodeID      string `json:"node_id,omitempty"`
	SubmittedAt int64  `json:"submitted_at"`
	QueuedAt    *int64 `json:"queued_at,omitempty"`
	ScheduledAt *int64 `json:"scheduled_at,omitempty"`
	StartedAt   *int64 `json:"started_at,omitempty"`
	FinishedAt  *int64 `json:"finished_at,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	FailureMsg  string `json:"failure_msg,omitempty"`
	RetryCount  int    `json:"retry_count"`
	MaxRetries  int    `json:"max_retries"`
	Deadline    *int64 `json:"deadline,omitempty"`
}

type WorkloadDetail struct {
	WorkloadSummary
	Manifest node.WorkloadManifest `json:"manifest"`
}

// ── WorkloadAPI ───────────────────────────────────────────────────────────────

type WorkloadAPI struct {
	db   *sql.DB
	auth *AuthHandler
}

func NewWorkloadAPI(db *sql.DB, auth *AuthHandler) *WorkloadAPI {
	return &WorkloadAPI{db: db, auth: auth}
}

func (w *WorkloadAPI) Mount(mux *http.ServeMux) {
	all := []string{"operator", "project", "user", "service", "auditor"}
	rw := []string{"operator", "project", "user", "service"}

	mux.HandleFunc("POST /v1/workloads", w.auth.protect(w.handleSubmit, rw...))
	mux.HandleFunc("GET /v1/workloads", w.auth.protect(w.handleList, all...))
	mux.HandleFunc("GET /v1/workloads/{id}", w.auth.protect(w.handleGet, all...))
	mux.HandleFunc("DELETE /v1/workloads/{id}", w.auth.protect(w.handleCancel, rw...))
	mux.HandleFunc("POST /v1/workloads/{id}/stop", w.auth.protect(w.handleStop, rw...))
}

// ── POST /v1/workloads ────────────────────────────────────────────────────────

func (w *WorkloadAPI) handleSubmit(resp http.ResponseWriter, r *http.Request) {
	claims, _ := authFromContext(r.Context())

	body, err := readBody(r)
	if err != nil {
		writeError(resp, http.StatusBadRequest, "BAD_REQUEST", err.Error(), false)
		return
	}
	var req SubmitWorkloadRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(resp, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON: "+err.Error(), false)
		return
	}
	if err := validateSubmitRequest(&req); err != nil {
		writeError(resp, http.StatusBadRequest, "BAD_REQUEST", err.Error(), false)
		return
	}
	if req.Priority == 0 {
		req.Priority = 50
	}
	req.Manifest.Name = req.Name

	manifestJSON, _ := json.Marshal(req.Manifest)
	workloadID := uuid.New().String()
	now := nowMs()

	var deadline *int64
	if req.Deadline != nil {
		deadline = req.Deadline
	} else if req.Manifest.TimeoutSec > 0 {
		d := now + int64(req.Manifest.TimeoutSec)*1000
		deadline = &d
	}

	var projectIDArg any
	if req.ProjectID != "" {
		projectIDArg = req.ProjectID
	}
	var deadlineArg any
	if deadline != nil {
		deadlineArg = *deadline
	}

	_, err = w.db.ExecContext(r.Context(), `
		INSERT INTO workloads
			(id, org_id, project_id, account_id, name, status, priority, manifest,
			 submitted_at, deadline, max_retries)
		VALUES (?, ?, ?, ?, ?, 'submitted', ?, ?, ?, ?, ?)`,
		workloadID, claims.OrgID, projectIDArg, claims.AccountID,
		req.Name, req.Priority, string(manifestJSON),
		now, deadlineArg, req.MaxRetries)
	if err != nil {
		slog.Error("submit workload", "err", err)
		writeError(resp, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}

	_ = insertEvent(r.Context(), w.db, claims.AccountID, "workload", workloadID, "workload.queued",
		map[string]any{"name": req.Name, "priority": req.Priority})

	slog.Info("workload submitted", "workload_id", workloadID, "name", req.Name, "org_id", claims.OrgID)
	writeJSON(resp, http.StatusCreated, map[string]any{
		"id":           workloadID,
		"status":       "submitted",
		"submitted_at": now,
	})
}

// ── GET /v1/workloads ─────────────────────────────────────────────────────────

func (w *WorkloadAPI) handleList(resp http.ResponseWriter, r *http.Request) {
	claims, _ := authFromContext(r.Context())
	q := r.URL.Query()

	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	where := []string{"org_id = ?"}
	args := []any{claims.OrgID}

	if st := q.Get("status"); st != "" {
		where = append(where, "status = ?")
		args = append(args, st)
	}
	if nid := q.Get("node_id"); nid != "" {
		where = append(where, "node_id = ?")
		args = append(args, nid)
	}
	if pid := q.Get("project_id"); pid != "" {
		where = append(where, "project_id = ?")
		args = append(args, pid)
	}
	if cursor := q.Get("cursor"); cursor != "" {
		where = append(where, "submitted_at < (SELECT submitted_at FROM workloads WHERE id = ?)")
		args = append(args, cursor)
	}

	query := fmt.Sprintf(`
		SELECT id, org_id, project_id, name, status, priority, node_id,
		       submitted_at, queued_at, scheduled_at, started_at, finished_at,
		       exit_code, failure_msg, retry_count, max_retries, deadline
		FROM workloads
		WHERE %s
		ORDER BY submitted_at DESC
		LIMIT ?`, strings.Join(where, " AND "))
	args = append(args, limit+1)

	rows, err := w.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		slog.Error("list workloads", "err", err)
		writeError(resp, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	defer rows.Close()

	var workloads []WorkloadSummary
	for rows.Next() {
		ws, err := scanWorkloadSummary(rows)
		if err != nil {
			slog.Warn("list workloads: scan row", "err", err)
			continue
		}
		workloads = append(workloads, ws)
	}

	var nextCursor string
	if len(workloads) > limit {
		nextCursor = workloads[limit-1].ID
		workloads = workloads[:limit]
	}
	if workloads == nil {
		workloads = []WorkloadSummary{}
	}

	out := map[string]any{"workloads": workloads}
	if nextCursor != "" {
		out["next_cursor"] = nextCursor
	}
	writeJSON(resp, http.StatusOK, out)
}

// ── GET /v1/workloads/{id} ────────────────────────────────────────────────────

func (w *WorkloadAPI) handleGet(resp http.ResponseWriter, r *http.Request) {
	claims, _ := authFromContext(r.Context())
	id := r.PathValue("id")

	var manifestJSON string
	var ws WorkloadSummary
	var projectID, nodeID, failureMsg sql.NullString
	var queuedAt, scheduledAt, startedAt, finishedAt, deadline, exitCode sql.NullInt64

	err := w.db.QueryRowContext(r.Context(), `
		SELECT id, org_id, project_id, name, status, priority, node_id,
		       submitted_at, queued_at, scheduled_at, started_at, finished_at,
		       exit_code, failure_msg, retry_count, max_retries, deadline, manifest
		FROM workloads WHERE id = ? AND org_id = ?`, id, claims.OrgID).Scan(
		&ws.ID, &ws.OrgID, &projectID, &ws.Name,
		&ws.Status, &ws.Priority, &nodeID,
		&ws.SubmittedAt, &queuedAt, &scheduledAt, &startedAt, &finishedAt,
		&exitCode, &failureMsg,
		&ws.RetryCount, &ws.MaxRetries, &deadline, &manifestJSON)
	if err == sql.ErrNoRows {
		writeError(resp, http.StatusNotFound, "NOT_FOUND", "workload not found", false)
		return
	}
	if err != nil {
		writeError(resp, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}

	ws.ProjectID = projectID.String
	ws.NodeID = nodeID.String
	ws.FailureMsg = failureMsg.String
	if queuedAt.Valid {
		ws.QueuedAt = &queuedAt.Int64
	}
	if scheduledAt.Valid {
		ws.ScheduledAt = &scheduledAt.Int64
	}
	if startedAt.Valid {
		ws.StartedAt = &startedAt.Int64
	}
	if finishedAt.Valid {
		ws.FinishedAt = &finishedAt.Int64
	}
	if deadline.Valid {
		ws.Deadline = &deadline.Int64
	}
	if exitCode.Valid {
		v := int(exitCode.Int64)
		ws.ExitCode = &v
	}

	var manifest node.WorkloadManifest
	_ = json.Unmarshal([]byte(manifestJSON), &manifest)
	writeJSON(resp, http.StatusOK, WorkloadDetail{WorkloadSummary: ws, Manifest: manifest})
}

// ── DELETE /v1/workloads/{id} — cancel ────────────────────────────────────────

func (w *WorkloadAPI) handleCancel(resp http.ResponseWriter, r *http.Request) {
	claims, _ := authFromContext(r.Context())
	id := r.PathValue("id")
	now := nowMs()

	res, err := w.db.ExecContext(r.Context(), `
		UPDATE workloads
		SET status     = CASE
		                   WHEN status IN ('running','checkpointing') THEN 'stopping'
		                   ELSE 'cancelled'
		                 END,
		    finished_at = CASE
		                   WHEN status IN ('running','checkpointing') THEN NULL
		                   ELSE ?
		                 END
		WHERE id = ? AND org_id = ?
		  AND status NOT IN ('completed','failed','timed_out','cancelled','stopping')`,
		now, id, claims.OrgID)
	if err != nil {
		writeError(resp, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		w.db.QueryRowContext(r.Context(),
			"SELECT COUNT(*) FROM workloads WHERE id=? AND org_id=?", id, claims.OrgID).Scan(&exists)
		if exists == 0 {
			writeError(resp, http.StatusNotFound, "NOT_FOUND", "workload not found", false)
		} else {
			writeError(resp, http.StatusConflict, "ALREADY_TERMINAL",
				"workload is already in a terminal or stopping state", false)
		}
		return
	}

	var newStatus string
	w.db.QueryRowContext(r.Context(), "SELECT status FROM workloads WHERE id=?", id).Scan(&newStatus)
	_ = insertEvent(r.Context(), w.db, claims.AccountID, "workload", id, "workload.cancelled",
		map[string]any{"new_status": newStatus})

	writeJSON(resp, http.StatusOK, map[string]string{"id": id, "status": newStatus})
}

// ── POST /v1/workloads/{id}/stop ──────────────────────────────────────────────

func (w *WorkloadAPI) handleStop(resp http.ResponseWriter, r *http.Request) {
	claims, _ := authFromContext(r.Context())
	id := r.PathValue("id")

	res, err := w.db.ExecContext(r.Context(), `
		UPDATE workloads SET status = 'stopping'
		WHERE id = ? AND org_id = ? AND status IN ('running','checkpointing')`,
		id, claims.OrgID)
	if err != nil {
		writeError(resp, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var status string
		w.db.QueryRowContext(r.Context(),
			"SELECT status FROM workloads WHERE id=? AND org_id=?", id, claims.OrgID).Scan(&status)
		if status == "" {
			writeError(resp, http.StatusNotFound, "NOT_FOUND", "workload not found", false)
		} else {
			writeError(resp, http.StatusConflict, "NOT_RUNNING",
				fmt.Sprintf("workload is not running (status=%s)", status), false)
		}
		return
	}
	writeJSON(resp, http.StatusOK, map[string]string{"id": id, "status": "stopping"})
}

// ── Validation ────────────────────────────────────────────────────────────────

func validateSubmitRequest(req *SubmitWorkloadRequest) error {
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 256 {
		return fmt.Errorf("name: required, max 256 chars")
	}
	validTypes := map[string]bool{
		"binary": true, "container": true, "model": true, "dataset": true,
		"archive": true, "vm_image": true, "script": true,
	}
	if !validTypes[req.Manifest.Type] {
		return fmt.Errorf("manifest.type: must be one of binary, container, model, dataset, archive, vm_image, script")
	}
	res := req.Manifest.Resources
	if res.CPUMillicores < 1 {
		return fmt.Errorf("manifest.resources.cpu_millicores: must be >= 1")
	}
	if res.RAMMb < 1 {
		return fmt.Errorf("manifest.resources.ram_mb: must be >= 1")
	}
	if res.GPUCount < 0 || res.GPUCount > 16 {
		return fmt.Errorf("manifest.resources.gpu_count: must be 0–16")
	}
	if res.GPUCount > 0 && res.GPUVRAMMb < 1 {
		return fmt.Errorf("manifest.resources.gpu_vram_mb: required when gpu_count > 0")
	}
	if req.MaxRetries < 0 || req.MaxRetries > 10 {
		return fmt.Errorf("max_retries: must be 0–10")
	}
	return nil
}

// ── DB scan helpers ───────────────────────────────────────────────────────────

func scanWorkloadSummary(rows *sql.Rows) (WorkloadSummary, error) {
	var ws WorkloadSummary
	var projectID, nodeID, failureMsg sql.NullString
	var queuedAt, scheduledAt, startedAt, finishedAt, deadline, exitCode sql.NullInt64
	err := rows.Scan(
		&ws.ID, &ws.OrgID, &projectID, &ws.Name,
		&ws.Status, &ws.Priority, &nodeID,
		&ws.SubmittedAt, &queuedAt, &scheduledAt, &startedAt, &finishedAt,
		&exitCode, &failureMsg,
		&ws.RetryCount, &ws.MaxRetries, &deadline,
	)
	if err != nil {
		return WorkloadSummary{}, err
	}
	ws.ProjectID = projectID.String
	ws.NodeID = nodeID.String
	ws.FailureMsg = failureMsg.String
	if queuedAt.Valid {
		ws.QueuedAt = &queuedAt.Int64
	}
	if scheduledAt.Valid {
		ws.ScheduledAt = &scheduledAt.Int64
	}
	if startedAt.Valid {
		ws.StartedAt = &startedAt.Int64
	}
	if finishedAt.Valid {
		ws.FinishedAt = &finishedAt.Int64
	}
	if deadline.Valid {
		ws.Deadline = &deadline.Int64
	}
	if exitCode.Valid {
		v := int(exitCode.Int64)
		ws.ExitCode = &v
	}
	return ws, nil
}
