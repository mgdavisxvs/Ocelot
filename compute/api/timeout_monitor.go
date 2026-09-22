package api

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// WorkloadTimeoutMonitor marks running workloads timed_out when they exceed
// their deadline. Deadlines are set at submission time from manifest.timeout_sec.
type WorkloadTimeoutMonitor struct {
	db            *sql.DB
	checkInterval time.Duration
}

func NewWorkloadTimeoutMonitor(db *sql.DB, checkInterval time.Duration) *WorkloadTimeoutMonitor {
	return &WorkloadTimeoutMonitor{db: db, checkInterval: checkInterval}
}

func (m *WorkloadTimeoutMonitor) Run(ctx context.Context) {
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

func (m *WorkloadTimeoutMonitor) check(ctx context.Context) {
	now := nowMs()

	rows, err := m.db.QueryContext(ctx, `
		SELECT id FROM workloads
		WHERE status IN ('running','checkpointing')
		  AND deadline IS NOT NULL
		  AND deadline < ?`, now)
	if err != nil {
		slog.Error("timeout_monitor: query expired workloads", "err", err)
		return
	}
	defer rows.Close()

	var expired []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			expired = append(expired, id)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("timeout_monitor: scan expired workloads", "err", err)
		return
	}

	for _, id := range expired {
		res, err := m.db.ExecContext(ctx, `
			UPDATE workloads
			SET status = 'timed_out', finished_at = ?
			WHERE id = ? AND status IN ('running','checkpointing')`,
			now, id)
		if err != nil {
			slog.Error("timeout_monitor: mark timed_out", "workload_id", id, "err", err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			slog.Warn("timeout_monitor: workload exceeded deadline",
				"workload_id", id)
			releaseWorkloadResources(ctx, m.db, id, now)
			_ = insertEvent(ctx, m.db, "system", "workload", id, "workload.timed_out",
				map[string]any{"reason": "deadline_exceeded"})
		}
	}
}
