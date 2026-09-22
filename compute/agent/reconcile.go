package agent

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/mgdavisxvs/Ocelot/compute/node"
)

const stopGrace = 30 * time.Second

// reconcile applies desired state to the actual running workload set.
// It implements the algorithm from AGENT_PROTOCOL.md §8.
func (a *Agent) reconcile(desired *node.DesiredStateResponse) {
	if desired == nil {
		return
	}

	// Build desired set: workload IDs with action == "run".
	desiredRun := make(map[string]node.DesiredWorkload, len(desired.Workloads))
	for _, wl := range desired.Workloads {
		if wl.Action == node.ActionRun {
			desiredRun[wl.ID] = wl
		}
	}

	// Build running set.
	runningIDs := a.executor.RunningIDs()
	runningSet := make(map[string]struct{}, len(runningIDs))
	for _, id := range runningIDs {
		runningSet[id] = struct{}{}
	}

	// Stop workloads no longer in desired state.
	for id := range runningSet {
		if _, wanted := desiredRun[id]; !wanted {
			slog.Info("reconcile: stopping removed workload", "workload_id", id)
			if err := a.executor.Stop(id, stopGrace); err != nil {
				slog.Error("reconcile: stop failed", "workload_id", id, "err", err)
			}
			ec := 0
			a.healthBuf.push(node.HealthEvent{
				WorkloadID: id,
				Kind:       node.EventStopped,
				Ts:         nowMs(),
				ExitCode:   &ec,
			})
		}
	}

	// Handle explicit stop/checkpoint actions.
	for _, wl := range desired.Workloads {
		switch wl.Action {
		case node.ActionStop:
			if _, running := runningSet[wl.ID]; running {
				slog.Info("reconcile: explicit stop", "workload_id", wl.ID)
				if err := a.executor.Stop(wl.ID, stopGrace); err != nil {
					slog.Error("reconcile: explicit stop failed", "workload_id", wl.ID, "err", err)
				}
			}
		case node.ActionCheckpoint:
			// Phase 3: trigger checkpoint snapshot. For now, log and skip.
			slog.Info("reconcile: checkpoint requested (Phase 3, not yet implemented)", "workload_id", wl.ID)
		}
	}

	// Start workloads not yet running.
	for id, wl := range desiredRun {
		if _, running := runningSet[id]; running {
			continue // already up, idempotent
		}

		slog.Info("reconcile: starting workload", "workload_id", id, "name", wl.Manifest.Name)

		// Ensure volumes are available before starting (LocalDriver only for Phase 2).
		if err := a.mountVolumes(wl.Manifest.Volumes, desired.Volumes); err != nil {
			slog.Error("reconcile: volume mount failed", "workload_id", id, "err", err)
			msg := "volume mount failed: " + err.Error()
			ec := 1
			a.healthBuf.push(node.HealthEvent{
				WorkloadID: id,
				Kind:       node.EventFailed,
				Ts:         nowMs(),
				ExitCode:   &ec,
				Message:    msg,
			})
			continue
		}

		if err := a.executor.Start(wl, a.cfg.DataDir); err != nil {
			slog.Error("reconcile: start failed", "workload_id", id, "err", err)
			msg := err.Error()
			ec := 1
			a.healthBuf.push(node.HealthEvent{
				WorkloadID: id,
				Kind:       node.EventFailed,
				Ts:         nowMs(),
				ExitCode:   &ec,
				Message:    msg,
			})
			continue
		}

		pid := 0
		a.healthBuf.push(node.HealthEvent{
			WorkloadID: id,
			Kind:       node.EventStarted,
			Ts:         nowMs(),
			PID:        pid,
			ExitCode:   nil,
		})
	}

	// Initiate background prefetch (non-blocking, best-effort).
	for _, pf := range desired.Prefetch {
		go a.prefetch(pf.Infohash, pf.Priority)
	}
}

// mountVolumes ensures all volumes required by a workload are accessible.
// Phase 2: LocalDriver only — verifies the source path exists.
func (a *Agent) mountVolumes(mounts []node.VolumeMount, specs []node.VolumeSpec) error {
	specMap := make(map[string]node.VolumeSpec, len(specs))
	for _, s := range specs {
		specMap[s.VolumeID] = s
	}

	for _, m := range mounts {
		spec, ok := specMap[m.VolumeID]
		if !ok {
			return fmt.Errorf("volume %s: no spec provided", m.VolumeID)
		}
		switch spec.Driver {
		case "local":
			if spec.SourcePath == "" {
				return fmt.Errorf("volume %s: local driver requires source_path", m.VolumeID)
			}
			if _, err := statPath(spec.SourcePath); err != nil {
				return fmt.Errorf("volume %s: source path %s: %w", m.VolumeID, spec.SourcePath, err)
			}
			// For read-only mounts we just verify the path; actual bind mount
			// is the entrypoint script's responsibility for Phase 2.
			slog.Debug("volume ready", "volume_id", m.VolumeID, "source", spec.SourcePath)
		case "artifact":
			// Phase 3: resolve infohash → local extract directory.
			slog.Info("volume driver 'artifact' not yet implemented, skipping", "volume_id", m.VolumeID)
		default:
			return fmt.Errorf("volume %s: unsupported driver %q", m.VolumeID, spec.Driver)
		}
	}
	return nil
}

// prefetch logs the request. Full BitTorrent acquisition is Phase 3.
func (a *Agent) prefetch(infohash string, priority int) {
	slog.Info("prefetch requested (Phase 3 implementation pending)",
		"infohash", infohash,
		"priority", priority,
	)
}
