package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// ── Response types ────────────────────────────────────────────────────────────

type NodeSummary struct {
	ID            string            `json:"id"`
	Hostname      string            `json:"hostname"`
	DisplayName   string            `json:"display_name,omitempty"`
	Arch          string            `json:"arch"`
	CPUCores      int               `json:"cpu_cores"`
	RAMMb         int               `json:"ram_mb"`
	StorageGb     int               `json:"storage_gb"`
	Labels        map[string]string `json:"labels"`
	Status        string            `json:"status"`
	LastHeartbeat *int64            `json:"last_heartbeat,omitempty"`
	AgentVersion  string            `json:"agent_version,omitempty"`
	CreatedAt     int64             `json:"created_at"`
	DrainAt       *int64            `json:"drain_at,omitempty"`
}

type GPUSummary struct {
	ID           string `json:"id"`
	DeviceIndex  int    `json:"device_index"`
	Model        string `json:"model"`
	VRAMMb       int    `json:"vram_mb"`
	CUDACap      string `json:"cuda_cap,omitempty"`
	Health       string `json:"health"`
	VRAMReserved int    `json:"vram_reserved"`
	WorkloadID   string `json:"workload_id,omitempty"`
}

type NodeDetail struct {
	NodeSummary
	GPUs          []GPUSummary `json:"gpus"`
	FreeCPUMcores int          `json:"free_cpu_mcores"`
	FreeRAMMb     int          `json:"free_ram_mb"`
}

// ── NodeAPI ───────────────────────────────────────────────────────────────────

type NodeAPI struct {
	db   *sql.DB
	auth *AuthHandler
}

func NewNodeAPI(db *sql.DB, auth *AuthHandler) *NodeAPI {
	return &NodeAPI{db: db, auth: auth}
}

func (n *NodeAPI) Mount(mux *http.ServeMux) {
	ro := []string{"operator", "auditor"}
	op := []string{"operator"}

	mux.HandleFunc("GET /v1/nodes", n.auth.protect(n.handleList, ro...))
	mux.HandleFunc("GET /v1/nodes/{id}", n.auth.protect(n.handleGet, ro...))
	mux.HandleFunc("POST /v1/nodes/{id}/drain", n.auth.protect(n.handleDrain, op...))
	mux.HandleFunc("DELETE /v1/nodes/{id}", n.auth.protect(n.handleSetDead, op...))
}

// ── GET /v1/nodes ─────────────────────────────────────────────────────────────

func (n *NodeAPI) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 500 {
			limit = v
		}
	}

	where := []string{"1=1"}
	args := []any{}

	if st := q.Get("status"); st != "" {
		where = append(where, "status = ?")
		args = append(args, st)
	}
	if arch := q.Get("arch"); arch != "" {
		where = append(where, "arch = ?")
		args = append(args, arch)
	}

	query := fmt.Sprintf(`
		SELECT id, hostname, display_name, arch, cpu_cores, ram_mb, storage_gb,
		       labels, status, last_heartbeat, agent_version, created_at, drain_at
		FROM nodes WHERE %s
		ORDER BY created_at DESC LIMIT ?`, strings.Join(where, " AND "))
	args = append(args, limit)

	rows, err := n.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		slog.Error("list nodes", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	defer rows.Close()

	var nodes []NodeSummary
	for rows.Next() {
		ns, err := scanNodeSummaryRow(rows)
		if err != nil {
			continue
		}
		nodes = append(nodes, ns)
	}
	if nodes == nil {
		nodes = []NodeSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// ── GET /v1/nodes/{id} ────────────────────────────────────────────────────────

func (n *NodeAPI) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var ns NodeSummary
	var displayName, agentVersion, labelsJSON sql.NullString
	var lastHeartbeat, drainAt sql.NullInt64
	err := n.db.QueryRowContext(r.Context(), `
		SELECT id, hostname, display_name, arch, cpu_cores, ram_mb, storage_gb,
		       labels, status, last_heartbeat, agent_version, created_at, drain_at
		FROM nodes WHERE id = ?`, id).Scan(
		&ns.ID, &ns.Hostname, &displayName, &ns.Arch,
		&ns.CPUCores, &ns.RAMMb, &ns.StorageGb,
		&labelsJSON, &ns.Status, &lastHeartbeat, &agentVersion, &ns.CreatedAt, &drainAt)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "node not found", false)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	ns.DisplayName = displayName.String
	ns.AgentVersion = agentVersion.String
	if lastHeartbeat.Valid {
		ns.LastHeartbeat = &lastHeartbeat.Int64
	}
	if drainAt.Valid {
		ns.DrainAt = &drainAt.Int64
	}
	ns.Labels = parseLabels(labelsJSON.String)

	nd := NodeDetail{NodeSummary: ns, GPUs: []GPUSummary{}}

	gpuRows, err := n.db.QueryContext(r.Context(), `
		SELECT id, device_index, model, vram_mb, cuda_cap, health, vram_reserved, workload_id
		FROM node_gpus WHERE node_id = ? ORDER BY device_index`, id)
	if err == nil {
		defer gpuRows.Close()
		for gpuRows.Next() {
			var g GPUSummary
			var cudaCap, workloadID sql.NullString
			if gpuRows.Scan(&g.ID, &g.DeviceIndex, &g.Model, &g.VRAMMb,
				&cudaCap, &g.Health, &g.VRAMReserved, &workloadID) == nil {
				g.CUDACap = cudaCap.String
				g.WorkloadID = workloadID.String
				nd.GPUs = append(nd.GPUs, g)
			}
		}
	}

	var usedCPU, usedRAM sql.NullInt64
	n.db.QueryRowContext(r.Context(), `
		SELECT SUM(cpu_mcores), SUM(ram_mb)
		FROM reservations WHERE node_id = ? AND state IN ('reserved','allocated','running')`,
		id).Scan(&usedCPU, &usedRAM)
	nd.FreeCPUMcores = nd.CPUCores*1000 - int(usedCPU.Int64)
	nd.FreeRAMMb = nd.RAMMb - int(usedRAM.Int64)

	writeJSON(w, http.StatusOK, nd)
}

// ── POST /v1/nodes/{id}/drain ─────────────────────────────────────────────────

func (n *NodeAPI) handleDrain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	now := nowMs()

	res, err := n.db.ExecContext(r.Context(), `
		UPDATE nodes SET status='draining', drain_at=?
		WHERE id=? AND status NOT IN ('dead','lost')`, now, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "node not found or already dead/lost", false)
		return
	}
	_ = insertEvent(r.Context(), n.db, "system", "node", id, "node.drained",
		map[string]any{"drain_at": now})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "draining", "drain_at": now})
}

// ── DELETE /v1/nodes/{id} — mark dead ─────────────────────────────────────────

func (n *NodeAPI) handleSetDead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	now := nowMs()

	res, err := n.db.ExecContext(r.Context(),
		"UPDATE nodes SET status='dead' WHERE id=? AND status != 'dead'", id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "node not found or already dead", false)
		return
	}
	_ = insertEvent(r.Context(), n.db, "system", "node", id, "node.drained",
		map[string]any{"reason": "marked_dead", "ts": now})
	w.WriteHeader(http.StatusNoContent)
}

// ── DB / JSON scan helpers ────────────────────────────────────────────────────

func scanNodeSummaryRow(rows *sql.Rows) (NodeSummary, error) {
	var ns NodeSummary
	var displayName, agentVersion, labelsJSON sql.NullString
	var lastHeartbeat, drainAt sql.NullInt64
	err := rows.Scan(&ns.ID, &ns.Hostname, &displayName, &ns.Arch,
		&ns.CPUCores, &ns.RAMMb, &ns.StorageGb,
		&labelsJSON, &ns.Status, &lastHeartbeat, &agentVersion, &ns.CreatedAt, &drainAt)
	if err != nil {
		return NodeSummary{}, err
	}
	ns.DisplayName = displayName.String
	ns.AgentVersion = agentVersion.String
	if lastHeartbeat.Valid {
		ns.LastHeartbeat = &lastHeartbeat.Int64
	}
	if drainAt.Valid {
		ns.DrainAt = &drainAt.Int64
	}
	ns.Labels = parseLabels(labelsJSON.String)
	return ns, nil
}

func parseLabels(s string) map[string]string {
	if s == "" || s == "{}" {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]string{}
	}
	return m
}
