package virtualserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
)

// Reconciler drives instance state transitions on a fixed interval loop.
type Reconciler struct {
	db       *db
	adapters *AdapterRegistry
	storage  *storage.Registry
	catalog  CatalogProvider
	metrics  *VSMetrics
	interval time.Duration
	log      *slog.Logger
}

func newReconciler(
	d *db,
	adapters *AdapterRegistry,
	stor *storage.Registry,
	catalog CatalogProvider,
	metrics *VSMetrics,
	interval time.Duration,
	log *slog.Logger,
) *Reconciler {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Reconciler{
		db: d, adapters: adapters, storage: stor,
		catalog: catalog, metrics: metrics,
		interval: interval, log: log,
	}
}

// Run is the main reconciler loop; returns when ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.metrics.ReconcilerLoops.Inc()
			t0 := time.Now()
			r.reconcile(ctx)
			r.metrics.ReconcilerDuration.Observe(time.Since(t0).Seconds())
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) {
	r.reconcileScheduled(ctx)
	r.reconcileProvisioning(ctx)
	r.reconcileStarting(ctx)
	r.reconcileRunning(ctx)
	r.reconcileStopping(ctx)
}

// ── scheduled → provisioning ─────────────────────────────────────────────────

func (r *Reconciler) reconcileScheduled(ctx context.Context) {
	instances, err := r.db.getInstancesByState(InstanceStateScheduled)
	if err != nil {
		r.log.Error("reconciler: list scheduled", "err", err)
		return
	}
	sched := newScheduler(r.db)
	for _, inst := range instances {
		svc, err := r.db.getService(inst.ServiceID)
		if err != nil {
			r.markFailed(inst, fmt.Sprintf("service not found: %v", err))
			continue
		}

		// Gate on artifact swarm availability before allocating resources.
		if svc.ArtifactHash != "" && r.catalog != nil {
			if ClassifyArtifact(r.catalog, svc.ArtifactHash) == ArtifactUnavailable {
				r.log.Info("reconciler: artifact unavailable, deferring", "instance", inst.ID, "hash", svc.ArtifactHash)
				continue
			}
		}

		decision, err := sched.Schedule(svc)
		if err != nil {
			r.log.Warn("reconciler: scheduling failed", "instance", inst.ID, "err", err)
			r.metrics.PlacementDecisions.WithLabelValues("unschedulable").Inc()
			continue
		}
		decision.InstanceID = inst.ID
		r.db.recordPlacement(decision)
		r.metrics.PlacementDecisions.WithLabelValues("placed").Inc()
		r.metrics.PlacementScore.Observe(decision.Score)

		node, err := r.db.getNode(decision.NodeID)
		if err != nil {
			r.markFailed(inst, "placed node vanished")
			continue
		}

		gpuIDs, err := r.pickGPUs(node, svc.GPUs)
		if err != nil {
			r.markFailed(inst, fmt.Sprintf("gpu pick: %v", err))
			continue
		}

		alloc := &Allocation{
			ID:            uuid.NewString(),
			InstanceID:    inst.ID,
			NodeID:        node.ID,
			CPUMillicores: svc.CPUMillicores,
			RAMBytes:      svc.RAMBytes,
			GPUDeviceIDs:  gpuIDs,
			CreatedAt:     time.Now().UTC(),
		}
		if err := r.db.createAllocation(alloc); err != nil {
			r.markFailed(inst, fmt.Sprintf("create allocation: %v", err))
			continue
		}

		inst.NodeID = node.ID
		inst.State = InstanceStateProvisioning
		inst.Message = fmt.Sprintf("placed on %s (score=%.3f)", node.Name, decision.Score)
		inst.UpdatedAt = time.Now().UTC()
		if err := r.db.upsertInstance(inst); err != nil {
			r.log.Error("reconciler: upsert scheduled→provisioning", "instance", inst.ID, "err", err)
		}
	}
}

// ── provisioning → starting ──────────────────────────────────────────────────

func (r *Reconciler) reconcileProvisioning(ctx context.Context) {
	instances, _ := r.db.getInstancesByState(InstanceStateProvisioning)
	for _, inst := range instances {
		svc, err := r.db.getService(inst.ServiceID)
		if err != nil {
			r.markFailed(inst, fmt.Sprintf("service not found: %v", err))
			continue
		}

		adapter, err := r.adapters.Default()
		if err != nil {
			r.log.Error("reconciler: no adapter", "err", err)
			continue
		}

		mounts, err := r.resolveMounts(ctx, svc, inst.ID, inst.NodeID)
		if err != nil {
			r.markFailed(inst, fmt.Sprintf("resolve mounts: %v", err))
			continue
		}

		// Find the GPU IDs for this instance from its allocation record.
		var gpuIDs []string
		if allocs, _ := r.db.allocationsForNode(inst.NodeID); allocs != nil {
			for _, a := range allocs {
				if a.InstanceID == inst.ID {
					gpuIDs = a.GPUDeviceIDs
					break
				}
			}
		}

		req := &ProvisionRequest{
			InstanceID:    inst.ID,
			ServiceID:     svc.ID,
			Image:         svc.Image,
			CPUMillicores: svc.CPUMillicores,
			RAMBytes:      svc.RAMBytes,
			GPUDevices:    gpuIDs,
			Mounts:        mounts,
			Env:           svc.Env,
		}

		t0 := time.Now()
		res, err := adapter.Provision(ctx, req)
		dur := time.Since(t0).Seconds()
		if err != nil {
			r.metrics.AdapterOps.WithLabelValues(adapter.Name(), "provision", "error").Inc()
			r.metrics.AdapterDuration.WithLabelValues(adapter.Name(), "provision").Observe(dur)
			r.teardownMounts(ctx, inst.ID, svc)
			r.markFailed(inst, fmt.Sprintf("provision: %v", err))
			continue
		}
		r.metrics.AdapterOps.WithLabelValues(adapter.Name(), "provision", "ok").Inc()
		r.metrics.AdapterDuration.WithLabelValues(adapter.Name(), "provision").Observe(dur)

		inst.RuntimeID = res.RuntimeID
		inst.State = InstanceStateStarting
		inst.UpdatedAt = time.Now().UTC()
		r.db.upsertInstance(inst)
	}
}

// ── starting → running ───────────────────────────────────────────────────────

func (r *Reconciler) reconcileStarting(ctx context.Context) {
	instances, _ := r.db.getInstancesByState(InstanceStateStarting)
	for _, inst := range instances {
		adapter, err := r.adapters.Default()
		if err != nil {
			continue
		}
		t0 := time.Now()
		err = adapter.Start(ctx, inst.ID, inst.RuntimeID)
		dur := time.Since(t0).Seconds()
		if err != nil {
			r.metrics.AdapterOps.WithLabelValues(adapter.Name(), "start", "error").Inc()
			r.metrics.AdapterDuration.WithLabelValues(adapter.Name(), "start").Observe(dur)
			if svc, _ := r.db.getService(inst.ServiceID); svc != nil {
				r.teardownMounts(ctx, inst.ID, svc)
			}
			r.markFailed(inst, fmt.Sprintf("start: %v", err))
			continue
		}
		r.metrics.AdapterOps.WithLabelValues(adapter.Name(), "start", "ok").Inc()
		r.metrics.AdapterDuration.WithLabelValues(adapter.Name(), "start").Observe(dur)

		inst.State = InstanceStateRunning
		inst.UpdatedAt = time.Now().UTC()
		r.db.upsertInstance(inst)
	}
}

// ── running: health polling ───────────────────────────────────────────────────

func (r *Reconciler) reconcileRunning(ctx context.Context) {
	instances, _ := r.db.getInstancesByState(InstanceStateRunning)
	for _, inst := range instances {
		adapter, err := r.adapters.Default()
		if err != nil {
			continue
		}
		state, err := adapter.Status(ctx, inst.ID, inst.RuntimeID)
		if err != nil {
			r.log.Warn("reconciler: status poll error", "instance", inst.ID, "err", err)
			continue
		}
		if state == InstanceStateFailed || state == InstanceStateStopped {
			svc, _ := r.db.getService(inst.ServiceID)
			r.handleExit(ctx, inst, state, svc)
		}
	}
}

// handleExit applies the service's restart policy after an instance exits.
func (r *Reconciler) handleExit(ctx context.Context, inst *Instance, exitState InstanceState, svc *Service) {
	if svc != nil {
		r.teardownMounts(ctx, inst.ID, svc)
	}

	shouldRestart := false
	if svc != nil {
		switch svc.RestartPolicy {
		case RestartAlways:
			shouldRestart = true
		case RestartOnFailure:
			shouldRestart = exitState == InstanceStateFailed
		}
	}

	if shouldRestart {
		inst.Restarts++
		inst.State = InstanceStateScheduled
		inst.NodeID = ""
		inst.RuntimeID = ""
		inst.Message = "restarting"
		inst.UpdatedAt = time.Now().UTC()
		r.db.upsertInstance(inst)
		return
	}

	inst.State = exitState
	inst.UpdatedAt = time.Now().UTC()
	r.db.upsertInstance(inst)
}

// ── stopping → stopped ───────────────────────────────────────────────────────

func (r *Reconciler) reconcileStopping(ctx context.Context) {
	instances, _ := r.db.getInstancesByState(InstanceStateStopping)
	for _, inst := range instances {
		adapter, err := r.adapters.Default()
		if err != nil {
			continue
		}
		t0 := time.Now()
		_ = adapter.Stop(ctx, inst.ID, inst.RuntimeID)
		dur := time.Since(t0).Seconds()
		r.metrics.AdapterOps.WithLabelValues(adapter.Name(), "stop", "ok").Inc()
		r.metrics.AdapterDuration.WithLabelValues(adapter.Name(), "stop").Observe(dur)

		t0 = time.Now()
		_ = adapter.Destroy(ctx, inst.ID, inst.RuntimeID)
		r.metrics.AdapterOps.WithLabelValues(adapter.Name(), "destroy", "ok").Inc()
		r.metrics.AdapterDuration.WithLabelValues(adapter.Name(), "destroy").Observe(time.Since(t0).Seconds())

		if svc, _ := r.db.getService(inst.ServiceID); svc != nil {
			r.teardownMounts(ctx, inst.ID, svc)
		}
		inst.State = InstanceStateStopped
		inst.UpdatedAt = time.Now().UTC()
		r.db.upsertInstance(inst)
	}
}

// ── mount helpers ─────────────────────────────────────────────────────────────

// resolveMounts calls StorageDriver.Mount for each VolumeMount in svc,
// records VolumeBinding rows, and returns fully resolved ResolvedMount values.
func (r *Reconciler) resolveMounts(ctx context.Context, svc *Service, instanceID, nodeID string) ([]ResolvedMount, error) {
	var out []ResolvedMount
	for _, vm := range svc.VolumeMounts {
		vol, err := r.db.getVolume(vm.VolumeID)
		if err != nil {
			return nil, fmt.Errorf("volume %q: %w", vm.VolumeID, err)
		}
		drv, err := r.storage.Get(vol.DriverName)
		if err != nil {
			return nil, fmt.Errorf("driver %q: %w", vol.DriverName, err)
		}

		t0 := time.Now()
		mp, err := drv.Mount(ctx, storage.VolumeHandle(vol.Handle), instanceID)
		dur := time.Since(t0).Seconds()
		if err != nil {
			r.metrics.VolumeOps.WithLabelValues(vol.DriverName, "mount", "error").Inc()
			r.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "mount").Observe(dur)
			return nil, fmt.Errorf("mount volume %q: %w", vm.VolumeID, err)
		}
		r.metrics.VolumeOps.WithLabelValues(vol.DriverName, "mount", "ok").Inc()
		r.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "mount").Observe(dur)

		if vol.NodeAffinity == "" {
			r.db.setVolumeAffinity(vm.VolumeID, nodeID)
		}

		r.db.createBinding(&VolumeBinding{
			ID:         uuid.NewString(),
			InstanceID: instanceID,
			VolumeID:   vm.VolumeID,
			MountPath:  vm.MountPath,
			ReadOnly:   vm.ReadOnly,
			HostPath:   mp.HostPath,
			CreatedAt:  time.Now().UTC(),
		})

		out = append(out, ResolvedMount{
			VolumeID:  vm.VolumeID,
			MountPath: vm.MountPath,
			HostPath:  mp.HostPath,
			ReadOnly:  vm.ReadOnly,
		})
	}
	return out, nil
}

// teardownMounts calls StorageDriver.Unmount for all active bindings of instanceID
// and removes the binding records.
func (r *Reconciler) teardownMounts(ctx context.Context, instanceID string, svc *Service) {
	bindings, err := r.db.bindingsForInstance(instanceID)
	if err != nil {
		r.log.Warn("teardownMounts: list bindings", "instance", instanceID, "err", err)
		return
	}
	for _, b := range bindings {
		vol, err := r.db.getVolume(b.VolumeID)
		if err != nil {
			continue
		}
		drv, err := r.storage.Get(vol.DriverName)
		if err != nil {
			continue
		}
		t0 := time.Now()
		err = drv.Unmount(ctx, storage.VolumeHandle(vol.Handle), instanceID)
		dur := time.Since(t0).Seconds()
		result := "ok"
		if err != nil {
			result = "error"
		}
		r.metrics.VolumeOps.WithLabelValues(vol.DriverName, "unmount", result).Inc()
		r.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "unmount").Observe(dur)
	}
	r.db.deleteBindingsForInstance(instanceID)
}

// ── GPU selection ─────────────────────────────────────────────────────────────

// pickGPUs selects the minimal set of free GPU device IDs from node
// that satisfies all GPURequirement entries in reqs.
func (r *Reconciler) pickGPUs(node *Node, reqs []GPURequirement) ([]string, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	usedIDs, err := r.db.nodeAllocatedGPUs(node.ID)
	if err != nil {
		return nil, err
	}
	free := make([]GPU, 0, len(node.GPUs))
	for _, g := range node.GPUs {
		if _, used := usedIDs[g.DeviceID]; !used {
			free = append(free, g)
		}
	}
	var picked []string
	for _, req := range reqs {
		var matched []string
		for _, g := range free {
			if req.VRAMPerDevice > 0 && g.VRAMBytes < req.VRAMPerDevice {
				continue
			}
			if req.ModelFilter != "" && !strings.Contains(g.Model, req.ModelFilter) {
				continue
			}
			matched = append(matched, g.DeviceID)
			if len(matched) == req.Count {
				break
			}
		}
		if len(matched) < req.Count {
			return nil, fmt.Errorf("cannot satisfy GPU requirement: need %d got %d", req.Count, len(matched))
		}
		pickedSet := make(map[string]struct{})
		for _, id := range matched {
			pickedSet[id] = struct{}{}
		}
		newFree := free[:0]
		for _, g := range free {
			if _, p := pickedSet[g.DeviceID]; !p {
				newFree = append(newFree, g)
			}
		}
		free = newFree
		picked = append(picked, matched...)
	}
	return picked, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (r *Reconciler) markFailed(inst *Instance, msg string) {
	r.log.Warn("reconciler: marking instance failed", "instance", inst.ID, "msg", msg)
	inst.State = InstanceStateFailed
	inst.Message = msg
	inst.UpdatedAt = time.Now().UTC()
	r.db.upsertInstance(inst)
}
