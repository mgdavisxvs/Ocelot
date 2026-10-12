package api

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// NodeLossMonitor periodically marks nodes lost when they exceed the heartbeat
// silence threshold (AGENT_PROTOCOL.md §4.3: 3 × heartbeat_interval_sec).
type NodeLossMonitor struct {
	db            *sql.DB
	checkInterval time.Duration // how often to scan; typically == heartbeatInterval
	lossAfter     time.Duration // silence threshold before marking lost
}

// NewNodeLossMonitor returns a monitor configured for the given heartbeat interval.
// It scans every heartbeatInterval and marks nodes lost after 3 × that interval.
func NewNodeLossMonitor(db *sql.DB, heartbeatInterval time.Duration) *NodeLossMonitor {
	return &NodeLossMonitor{
		db:            db,
		checkInterval: heartbeatInterval,
		lossAfter:     3 * heartbeatInterval,
	}
}

// Run starts the monitor loop. It blocks until ctx is cancelled.
func (m *NodeLossMonitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check(ctx)
		}
	}
}

// check performs one scan and marks stale nodes lost.
func (m *NodeLossMonitor) check(ctx context.Context) {
	threshold := time.Now().Add(-m.lossAfter).UnixMilli()

	rows, err := m.db.QueryContext(ctx, `
		SELECT id FROM nodes
		WHERE last_heartbeat IS NOT NULL
		  AND last_heartbeat < ?
		  AND status NOT IN ('lost', 'dead')`,
		threshold)
	if err != nil {
		slog.Error("monitor: query stale nodes", "err", err)
		return
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("monitor: scan stale nodes", "err", err)
		return
	}

	for _, id := range stale {
		res, err := m.db.ExecContext(ctx,
			"UPDATE nodes SET status = 'lost' WHERE id = ? AND status NOT IN ('lost','dead')", id)
		if err != nil {
			slog.Error("monitor: mark lost", "node_id", id, "err", err)
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		slog.Warn("monitor: node marked lost (heartbeat timeout)",
			"node_id", id, "threshold_sec", int(m.lossAfter.Seconds()))
		_ = insertEvent(ctx, m.db, "system", "node", id, "node.lost",
			map[string]any{"reason": "heartbeat_timeout", "threshold_sec": int(m.lossAfter.Seconds())})
		m.recoverNodeWorkloads(ctx, id)
	}
}

// recoverNodeWorkloads requeues or fails all active workloads on a lost node.
func (m *NodeLossMonitor) recoverNodeWorkloads(ctx context.Context, nodeID string) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id FROM workloads
		WHERE node_id = ?
		  AND status IN ('dispatched','running','checkpointing','stopping')`, nodeID)
	if err != nil {
		slog.Error("monitor: query node workloads", "node_id", nodeID, "err", err)
		return
	}
	defer rows.Close()

	var wIDs []string
	for rows.Next() {
		var wid string
		if err := rows.Scan(&wid); err == nil {
			wIDs = append(wIDs, wid)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("monitor: scan node workloads", "node_id", nodeID, "err", err)
		return
	}

	now := nowMs()
	for _, wid := range wIDs {
		// First mark the workload failed so tryRequeueIfEligible can pick it up.
		if _, err := m.db.ExecContext(ctx, `
			UPDATE workloads SET status = 'failed', finished_at = ?, failure_msg = 'node lost'
			WHERE id = ? AND status NOT IN ('completed','failed','timed_out','cancelled')`,
			now, wid); err != nil {
			slog.Warn("monitor: mark workload failed", "workload_id", wid, "err", err)
			continue
		}
		releaseWorkloadResources(ctx, m.db, wid, now)
		if !tryRequeueIfEligible(ctx, m.db, wid, "node_lost") {
			_ = insertEvent(ctx, m.db, "system", "workload", wid, "workload.failed",
				map[string]any{"reason": "node_lost", "node_id": nodeID})
			slog.Warn("monitor: workload failed (node lost, no retries)", "workload_id", wid, "node_id", nodeID)
		}
	}
}
