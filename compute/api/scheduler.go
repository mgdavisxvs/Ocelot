package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/mgdavisxvs/Ocelot/compute/node"
)

const schedulerBatchSize = 16

// Scheduler picks pending workloads and dispatches them to capable nodes.
// It runs best-fit bin packing: widest-fit on RAM to pack nodes tightly and
// leave larger gaps free for bigger workloads.
type Scheduler struct {
	db       *sql.DB
	interval time.Duration
}

// NewScheduler returns a Scheduler that runs every interval.
func NewScheduler(db *sql.DB, interval time.Duration) *Scheduler {
	return &Scheduler{db: db, interval: interval}
}

// Run starts the scheduling loop and blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scheduleBatch(ctx)
		}
	}
}

type pendingWork struct {
	id       string
	orgID    string
	manifest node.WorkloadManifest
	priority int
}

// scheduleBatch selects up to schedulerBatchSize pending workloads and
// attempts to dispatch each in priority-then-FIFO order.
func (s *Scheduler) scheduleBatch(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, org_id, manifest, priority
		FROM workloads
		WHERE status IN ('submitted','queued')
		ORDER BY priority DESC, COALESCE(queued_at, submitted_at) ASC
		LIMIT ?`, schedulerBatchSize)
	if err != nil {
		slog.Error("scheduler: query pending workloads", "err", err)
		return
	}
	defer rows.Close()

	var pending []pendingWork
	for rows.Next() {
		var pw pendingWork
		var manifestJSON string
		if err := rows.Scan(&pw.id, &pw.orgID, &manifestJSON, &pw.priority); err != nil {
			slog.Warn("scheduler: scan row", "err", err)
			continue
		}
		if err := json.Unmarshal([]byte(manifestJSON), &pw.manifest); err != nil {
			slog.Warn("scheduler: unmarshal manifest", "workload_id", pw.id, "err", err)
			continue
		}
		pending = append(pending, pw)
	}
	if err := rows.Err(); err != nil {
		slog.Error("scheduler: scan pending workloads", "err", err)
		return
	}

	for _, pw := range pending {
		ok, err := s.scheduleOne(ctx, pw)
		if err != nil {
			slog.Error("scheduler: schedule workload", "workload_id", pw.id, "err", err)
			continue
		}
		if ok {
			slog.Info("scheduler: workload dispatched",
				"workload_id", pw.id, "priority", pw.priority)
		}
	}
}

// scheduleOne attempts to dispatch a single workload within a transaction.
// Returns (true, nil) when dispatched, (false, nil) when no node is available.
//
// SetMaxOpenConns(1) serializes all concurrent DB operations through the single
// connection, preventing double-booking without needing BEGIN IMMEDIATE.
func (s *Scheduler) scheduleOne(ctx context.Context, pw pendingWork) (bool, error) {
	res := &pw.manifest.Resources

	if res.GPUCount > 1 {
		// Multi-GPU scheduling (> 1) is a Phase 2 feature.
		slog.Warn("scheduler: multi-GPU workloads not yet supported; skipping",
			"workload_id", pw.id, "gpu_count", res.GPUCount)
		return false, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Find a capable node inside the transaction.
	nodeID, err := s.findNode(ctx, tx, res)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// Optionally claim a GPU.
	var gpuID sql.NullString
	var gpuVRAMMb int
	if res.GPUCount == 1 {
		gpuID.String, gpuVRAMMb, err = s.findFreeGPU(ctx, tx, nodeID, res.GPUVRAMMb)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		gpuID.Valid = true
	}

	now := nowMs()
	reservationID := uuid.New().String()

	var gpuIDArg any
	if gpuID.Valid {
		gpuIDArg = gpuID.String
	}

	if _, err = tx.ExecContext(ctx, `
		INSERT INTO reservations
			(id, node_id, workload_id, cpu_mcores, ram_mb, gpu_id, gpu_vram_mb, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'reserved', ?)`,
		reservationID, nodeID, pw.id,
		res.CPUMillicores, res.RAMMb,
		gpuIDArg, gpuVRAMMb,
		now); err != nil {
		return false, err
	}

	if gpuID.Valid {
		if _, err = tx.ExecContext(ctx,
			"UPDATE node_gpus SET workload_id = ?, vram_reserved = ? WHERE id = ?",
			pw.id, gpuVRAMMb, gpuID.String); err != nil {
			return false, err
		}
	}

	// Advance workload to dispatched; set queued_at if not already set.
	res2, err := tx.ExecContext(ctx, `
		UPDATE workloads
		SET status       = 'dispatched',
		    node_id      = ?,
		    scheduled_at = ?,
		    queued_at    = COALESCE(queued_at, ?)
		WHERE id = ? AND status IN ('submitted','queued')`,
		nodeID, now, now, pw.id)
	if err != nil {
		return false, err
	}
	if n, _ := res2.RowsAffected(); n == 0 {
		// Another goroutine already dispatched this workload; silently skip.
		return false, nil
	}

	_ = insertEvent(ctx, tx, "system", "workload", pw.id, "workload.scheduled",
		map[string]any{
			"node_id":         nodeID,
			"cpu_millicores":  res.CPUMillicores,
			"ram_mb":          res.RAMMb,
			"gpu_id":          gpuID.String,
			"reservation_id":  reservationID,
		})

	return true, tx.Commit()
}

// findNode returns the ID of a ready node that has sufficient free CPU and RAM.
// Best-fit selection: prefer nodes where the allocation leaves the smallest
// remaining RAM gap, packing tightly and reserving larger nodes for bigger jobs.
func (s *Scheduler) findNode(ctx context.Context, tx *sql.Tx, res *node.ResourceSpec) (string, error) {
	var nodeID string
	var err error
	if res.GPUCount == 0 {
		err = tx.QueryRowContext(ctx, `
			SELECT n.id
			FROM nodes n
			LEFT JOIN (
				SELECT node_id,
				       SUM(cpu_mcores) AS cu,
				       SUM(ram_mb)     AS ru
				FROM reservations
				WHERE state IN ('reserved','allocated','running')
				GROUP BY node_id
			) r ON r.node_id = n.id
			WHERE n.status IN ('ready','busy')
			  AND (n.cpu_cores * 1000 - COALESCE(r.cu, 0)) >= ?
			  AND (n.ram_mb           - COALESCE(r.ru, 0)) >= ?
			ORDER BY (n.ram_mb - COALESCE(r.ru, 0)) ASC
			LIMIT 1`,
			res.CPUMillicores, res.RAMMb).Scan(&nodeID)
	} else {
		err = tx.QueryRowContext(ctx, `
			SELECT n.id
			FROM nodes n
			LEFT JOIN (
				SELECT node_id,
				       SUM(cpu_mcores) AS cu,
				       SUM(ram_mb)     AS ru
				FROM reservations
				WHERE state IN ('reserved','allocated','running')
				GROUP BY node_id
			) r ON r.node_id = n.id
			WHERE n.status IN ('ready','busy')
			  AND (n.cpu_cores * 1000 - COALESCE(r.cu, 0)) >= ?
			  AND (n.ram_mb           - COALESCE(r.ru, 0)) >= ?
			  AND EXISTS (
			      SELECT 1 FROM node_gpus g
			      WHERE g.node_id = n.id
			        AND g.workload_id IS NULL
			        AND g.health      = 'ready'
			        AND g.vram_mb    >= ?
			  )
			ORDER BY (n.ram_mb - COALESCE(r.ru, 0)) ASC
			LIMIT 1`,
			res.CPUMillicores, res.RAMMb, res.GPUVRAMMb).Scan(&nodeID)
	}
	return nodeID, err
}

// findFreeGPU returns the id and vram_mb of the free GPU on nodeID with the
// smallest VRAM that still meets the minimum (best-fit for VRAM).
func (s *Scheduler) findFreeGPU(ctx context.Context, tx *sql.Tx, nodeID string, minVRAMMb int) (id string, vramMb int, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT id, vram_mb FROM node_gpus
		WHERE node_id     = ?
		  AND workload_id IS NULL
		  AND health      = 'ready'
		  AND vram_mb    >= ?
		ORDER BY vram_mb ASC
		LIMIT 1`,
		nodeID, minVRAMMb).Scan(&id, &vramMb)
	return
}

// tryRequeueIfEligible requeues a failed/timed-out workload when it still has
// retries remaining. Returns true and emits workload.requeued when requeued.
func tryRequeueIfEligible(ctx context.Context, db *sql.DB, workloadID string, reason string) bool {
	now := nowMs()
	res, err := db.ExecContext(ctx, `
		UPDATE workloads
		SET status = 'queued', queued_at = ?, node_id = NULL,
		    retry_count = retry_count + 1, failure_msg = ?
		WHERE id = ? AND retry_count < max_retries
		  AND status IN ('failed','timed_out')`,
		now, reason, workloadID)
	if err != nil {
		slog.Warn("tryRequeue: update", "workload_id", workloadID, "err", err)
		return false
	}
	if n, _ := res.RowsAffected(); n > 0 {
		slog.Info("workload requeued for retry", "workload_id", workloadID, "reason", reason)
		_ = insertEvent(ctx, db, "system", "workload", workloadID, "workload.requeued",
			map[string]any{"reason": reason})
		return true
	}
	return false
}

// releaseWorkloadResources marks the workload's reservation released and frees
// any GPU it held. Safe to call multiple times (idempotent via state guard).
func releaseWorkloadResources(ctx context.Context, db execer, workloadID string, finishedAt int64) {
	if _, err := db.ExecContext(ctx, `
		UPDATE reservations
		SET state = 'released', released_at = ?
		WHERE workload_id = ? AND state != 'released'`,
		finishedAt, workloadID); err != nil {
		slog.Warn("scheduler: release reservation", "workload_id", workloadID, "err", err)
	}
	if _, err := db.ExecContext(ctx,
		"UPDATE node_gpus SET workload_id = NULL, vram_reserved = 0 WHERE workload_id = ?",
		workloadID); err != nil {
		slog.Warn("scheduler: release gpu", "workload_id", workloadID, "err", err)
	}
}
