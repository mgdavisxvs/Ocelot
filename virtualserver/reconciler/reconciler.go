package reconciler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/adapter"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	vsmetrics "github.com/mgdavisxvs/Ocelot/virtualserver/metrics"
	"github.com/mgdavisxvs/Ocelot/virtualserver/scheduler"
	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
	"github.com/mgdavisxvs/Ocelot/virtualserver/store"
)

const (
	defaultInterval = 15 * time.Second
	retryBaseDelay  = 2 * time.Second
)

// StoreInterface is the subset of VSStore the reconciler requires.
type StoreInterface interface {
	ListInstances(ctx context.Context, stateFilter string) ([]domain.ServiceInstance, error)
	ListNodes(ctx context.Context, stateFilter string) ([]domain.Node, error)
	GetInstance(ctx context.Context, id string) (*domain.ServiceInstance, error)
	GetService(ctx context.Context, id int64) (*domain.Service, error)
	UpdateInstanceState(ctx context.Context, id string, to domain.InstanceState) error
	AssignNode(ctx context.Context, instanceID, nodeID string) error
	AllocateResources(ctx context.Context, instanceID, nodeID string, cpuThreads int, ramMiB int64, gpuDeviceIndex *int) error
	GetActiveAllocation(ctx context.Context, instanceID string) (*store.Allocation, error)
	ReleaseAllocation(ctx context.Context, instanceID string) error
	IncrementRetryCount(ctx context.Context, instanceID string) error
	UpdateRuntimeHandle(ctx context.Context, instanceID string, handle map[string]string) error
	WriteAuditLog(ctx context.Context, action, resourceType, resourceID, ipAddr string, success bool, errMsg string) error

	// Volume methods
	ListVolumesByState(ctx context.Context, state domain.VolumeState) ([]domain.Volume, error)
	GetVolumeByName(ctx context.Context, namespace, name string) (*domain.Volume, error)
	UpdateVolumeState(ctx context.Context, id string, to domain.VolumeState) error
	UpdateVolumeHandle(ctx context.Context, id string, handle map[string]string) error
	UpdateVolumeBoundNode(ctx context.Context, id, nodeID string) error
	UpdateVolumeFailure(ctx context.Context, id, reason string) error
	BindMount(ctx context.Context, m domain.VolumeMount) (int64, error)
	UpdateMountState(ctx context.Context, id int64, to domain.VolumeMountState) error
	ListMountsByInstance(ctx context.Context, instanceID string) ([]domain.VolumeMount, error)
}

// ArtifactCatalog is the narrow interface bridging the reconciler to artifact availability.
type ArtifactCatalog interface {
	Lookup(ctx context.Context, infoHash string) (domain.ArtifactStatus, error)
}

// VSReconciler drives instances from declared → running and handles failure recovery.
// Only one reconciler loop runs at a time (overlap guard via runningMu).
type VSReconciler struct {
	store        StoreInterface
	adapters     map[string]adapter.BackendAdapter
	classDrivers map[string]storage.StorageDriver // class name → driver
	sched        *scheduler.Scheduler
	catalog      ArtifactCatalog
	interval     time.Duration

	runningMu sync.Mutex

	stop chan struct{}
	done chan struct{}
}

// Config holds VSReconciler construction parameters.
type Config struct {
	Store        StoreInterface
	Adapters     map[string]adapter.BackendAdapter
	ClassDrivers map[string]storage.StorageDriver
	Sched        *scheduler.Scheduler
	Catalog      ArtifactCatalog
	Interval     time.Duration
}

// New creates a VSReconciler. Interval defaults to 15 s if zero.
func New(cfg Config) *VSReconciler {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	drivers := cfg.ClassDrivers
	if drivers == nil {
		drivers = map[string]storage.StorageDriver{}
	}
	return &VSReconciler{
		store:        cfg.Store,
		adapters:     cfg.Adapters,
		classDrivers: drivers,
		sched:        cfg.Sched,
		catalog:      cfg.Catalog,
		interval:     interval,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
}

// Start begins the reconciliation loop in a background goroutine.
func (r *VSReconciler) Start() {
	go r.loop()
}

// Stop signals the reconciler to halt and waits for it to exit.
func (r *VSReconciler) Stop(ctx context.Context) error {
	close(r.stop)
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *VSReconciler) loop() {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.tryRun()
		}
	}
}

// tryRun executes one reconciliation pass if no other pass is running.
func (r *VSReconciler) tryRun() {
	if !r.runningMu.TryLock() {
		return // previous loop still in progress; skip this tick
	}
	defer r.runningMu.Unlock()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), r.interval)
	defer cancel()

	err := r.reconcile(ctx)
	vsmetrics.ReconcilerDuration.Observe(time.Since(start).Seconds())
	if err != nil {
		vsmetrics.ReconcilerLoops.WithLabelValues("error").Inc()
	} else {
		vsmetrics.ReconcilerLoops.WithLabelValues("ok").Inc()
	}
}

// Reconcile runs a single full reconciliation pass across all instance states.
// It is exported for use in integration tests and external tooling.
func (r *VSReconciler) Reconcile(ctx context.Context) error {
	return r.reconcile(ctx)
}

// reconcile is the internal single-pass implementation.
func (r *VSReconciler) reconcile(ctx context.Context) error {
	if err := r.reconcileVolumes(ctx); err != nil {
		// Non-fatal: log and continue with instance reconciliation.
		r.store.WriteAuditLog(ctx, "volume_reconcile_error", "volume", "", "", false, err.Error()) //nolint:errcheck
	}

	nodes, err := r.store.ListNodes(ctx, "")
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	declared, err := r.store.ListInstances(ctx, string(domain.InstanceDeclared))
	if err != nil {
		return fmt.Errorf("list declared: %w", err)
	}
	for i := range declared {
		if err := r.scheduleInstance(ctx, &declared[i], nodes); err != nil {
			r.store.WriteAuditLog(ctx, "schedule_failed", "instance", declared[i].ID, "", false, err.Error()) //nolint:errcheck
		}
	}

	scheduled, err := r.store.ListInstances(ctx, string(domain.InstanceScheduled))
	if err != nil {
		return fmt.Errorf("list scheduled: %w", err)
	}
	for i := range scheduled {
		if err := r.provisionInstance(ctx, &scheduled[i]); err != nil {
			r.store.WriteAuditLog(ctx, "provision_failed", "instance", scheduled[i].ID, "", false, err.Error()) //nolint:errcheck
		}
	}

	provisioning, err := r.store.ListInstances(ctx, string(domain.InstanceProvisioning))
	if err != nil {
		return fmt.Errorf("list provisioning: %w", err)
	}
	for i := range provisioning {
		if err := r.startInstance(ctx, &provisioning[i]); err != nil {
			r.store.WriteAuditLog(ctx, "start_failed", "instance", provisioning[i].ID, "", false, err.Error()) //nolint:errcheck
		}
	}

	failed, err := r.store.ListInstances(ctx, string(domain.InstanceFailed))
	if err != nil {
		return fmt.Errorf("list failed: %w", err)
	}
	for i := range failed {
		r.handleFailedInstance(ctx, &failed[i])
	}

	return nil
}

func (r *VSReconciler) scheduleInstance(ctx context.Context, inst *domain.ServiceInstance, nodes []domain.Node) error {
	svc, err := r.store.GetService(ctx, inst.ServiceID)
	if err != nil {
		return fmt.Errorf("get service %d: %w", inst.ServiceID, err)
	}

	var eligible []domain.Node
	for _, n := range nodes {
		if n.State == domain.NodeReady {
			eligible = append(eligible, n)
		}
	}

	// HC-08: if any local-class volume declares a BoundNodeID, constrain eligible nodes to that one.
	if pinnedNodeID := r.localVolumePinnedNode(ctx, svc.Manifest); pinnedNodeID != "" {
		var pinned []domain.Node
		for _, n := range eligible {
			if n.ID == pinnedNodeID {
				pinned = append(pinned, n)
				break
			}
		}
		eligible = pinned
	}

	decision, err := r.sched.Schedule(ctx, svc.Manifest, eligible, r.catalog)
	if err != nil {
		return fmt.Errorf("schedule instance %s: %w", inst.ID, err)
	}

	if err := r.store.AssignNode(ctx, inst.ID, decision.SelectedNodeID); err != nil {
		return fmt.Errorf("assign node: %w", err)
	}

	spec := svc.Manifest.Spec

	// Find the first eligible GPU device on the selected node (if GPU required).
	var gpuIdx *int
	if spec.Resources.GPU.Required {
		for _, n := range eligible {
			if n.ID == decision.SelectedNodeID {
				eligible := n.EligibleGPUs(spec.Resources.GPU.MinVRAMMiB)
				if len(eligible) > 0 {
					idx := eligible[0].Index
					gpuIdx = &idx
				}
				break
			}
		}
	}

	if err := r.store.AllocateResources(ctx, inst.ID, decision.SelectedNodeID,
		spec.Resources.CPUThreads, spec.Resources.RAMMiB, gpuIdx); err != nil {
		r.store.UpdateInstanceState(ctx, inst.ID, domain.InstanceDeclared) //nolint:errcheck
		return fmt.Errorf("allocate resources: %w", err)
	}

	vsmetrics.PlacementDecisions.WithLabelValues("scheduled").Inc()
	r.store.WriteAuditLog(ctx, "scheduled", "instance", inst.ID, "", true, //nolint:errcheck
		fmt.Sprintf("node=%s score=%.3f", decision.SelectedNodeID, decision.Score))
	return nil
}

func (r *VSReconciler) provisionInstance(ctx context.Context, inst *domain.ServiceInstance) error {
	svc, err := r.store.GetService(ctx, inst.ServiceID)
	if err != nil {
		return err
	}

	ad, ok := r.adapters[svc.Manifest.Spec.Runtime]
	if !ok {
		return fmt.Errorf("no adapter registered for runtime %q", svc.Manifest.Spec.Runtime)
	}

	if err := r.store.UpdateInstanceState(ctx, inst.ID, domain.InstanceProvisioning); err != nil {
		return err
	}

	alloc, _ := r.store.GetActiveAllocation(ctx, inst.ID)
	req := adapter.ProvisionRequest{
		InstanceID: inst.ID,
		NodeID:     inst.NodeID,
		Manifest:   svc.Manifest,
	}
	if alloc != nil {
		req.Allocation = adapter.Allocation{
			CPUThreads:     alloc.CPUThreads,
			RAMMiB:         alloc.RAMMiB,
			GPUDeviceIndex: alloc.GPUDeviceIndex,
		}
	}

	// Resolve and mount volumes declared in the service manifest.
	resolvedMounts, mountIDs, err := r.resolveMounts(ctx, svc.Manifest, inst.ID, inst.NodeID)
	if err != nil {
		r.store.UpdateInstanceState(ctx, inst.ID, domain.InstanceFailed) //nolint:errcheck
		return fmt.Errorf("resolve mounts: %w", err)
	}
	req.Mounts = resolvedMounts
	_ = mountIDs // mount IDs tracked in DB; not needed by the adapter

	t0 := time.Now()
	handle, err := ad.Provision(ctx, req)
	vsmetrics.AdapterDuration.WithLabelValues(ad.Name(), "provision").Observe(time.Since(t0).Seconds())
	if err != nil {
		vsmetrics.AdapterOperations.WithLabelValues(ad.Name(), "provision", "error").Inc()
		r.store.UpdateInstanceState(ctx, inst.ID, domain.InstanceFailed) //nolint:errcheck
		vsmetrics.InstancesTotal.WithLabelValues(string(domain.InstanceFailed)).Inc()
		return fmt.Errorf("provision: %w", err)
	}
	vsmetrics.AdapterOperations.WithLabelValues(ad.Name(), "provision", "ok").Inc()

	r.store.UpdateRuntimeHandle(ctx, inst.ID, handle.Data)                                                         //nolint:errcheck
	r.store.WriteAuditLog(ctx, "provisioned", "instance", inst.ID, "", true, fmt.Sprintf("adapter=%s", ad.Name())) //nolint:errcheck
	return nil
}

func (r *VSReconciler) startInstance(ctx context.Context, inst *domain.ServiceInstance) error {
	svc, err := r.store.GetService(ctx, inst.ServiceID)
	if err != nil {
		return err
	}

	ad, ok := r.adapters[svc.Manifest.Spec.Runtime]
	if !ok {
		return fmt.Errorf("no adapter registered for runtime %q", svc.Manifest.Spec.Runtime)
	}

	if err := r.store.UpdateInstanceState(ctx, inst.ID, domain.InstanceStarting); err != nil {
		return err
	}

	handle := adapter.RuntimeHandle{
		AdapterName: ad.Name(),
		InstanceID:  inst.ID,
		Data:        inst.RuntimeHandle,
	}

	t0 := time.Now()
	err = ad.Start(ctx, handle)
	vsmetrics.AdapterDuration.WithLabelValues(ad.Name(), "start").Observe(time.Since(t0).Seconds())
	if err != nil {
		vsmetrics.AdapterOperations.WithLabelValues(ad.Name(), "start", "error").Inc()
		r.store.UpdateInstanceState(ctx, inst.ID, domain.InstanceFailed) //nolint:errcheck
		vsmetrics.InstancesTotal.WithLabelValues(string(domain.InstanceFailed)).Inc()
		return fmt.Errorf("start: %w", err)
	}
	vsmetrics.AdapterOperations.WithLabelValues(ad.Name(), "start", "ok").Inc()

	r.store.UpdateInstanceState(ctx, inst.ID, domain.InstanceRunning) //nolint:errcheck
	vsmetrics.InstancesTotal.WithLabelValues(string(domain.InstanceRunning)).Inc()
	r.store.WriteAuditLog(ctx, "started", "instance", inst.ID, "", true, "") //nolint:errcheck
	return nil
}

// localVolumePinnedNode returns the BoundNodeID if any local-class volume in the manifest
// has been bound to a specific node (HC-08 enforcement). Returns "" if unconstrained.
func (r *VSReconciler) localVolumePinnedNode(ctx context.Context, manifest domain.ServiceManifest) string {
	for _, m := range manifest.Spec.Mounts {
		vol, err := r.store.GetVolumeByName(ctx, manifest.Metadata.Namespace, m.VolumeName)
		if err != nil || vol == nil {
			continue
		}
		drv, ok := r.classDrivers[vol.Manifest.Spec.Class]
		if !ok || drv.Name() != "local" {
			continue
		}
		if vol.BoundNodeID != "" {
			return vol.BoundNodeID
		}
	}
	return ""
}

// resolveMounts calls Mount on each volume declared in the service manifest and records
// the mount in the store. Returns adapter.ResolvedMount slices and the DB mount IDs.
func (r *VSReconciler) resolveMounts(ctx context.Context, manifest domain.ServiceManifest, instanceID, nodeID string) ([]adapter.ResolvedMount, []int64, error) {
	var resolved []adapter.ResolvedMount
	var ids []int64

	for _, decl := range manifest.Spec.Mounts {
		vol, err := r.store.GetVolumeByName(ctx, manifest.Metadata.Namespace, decl.VolumeName)
		if err != nil {
			return nil, nil, fmt.Errorf("volume %q not found: %w", decl.VolumeName, err)
		}
		if vol.State != domain.VolumeReady && vol.State != domain.VolumeBound {
			return nil, nil, fmt.Errorf("volume %q is not ready (state=%s)", decl.VolumeName, vol.State)
		}

		drv, ok := r.classDrivers[vol.Manifest.Spec.Class]
		if !ok {
			return nil, nil, fmt.Errorf("no driver registered for class %q", vol.Manifest.Spec.Class)
		}

		t0 := time.Now()
		mp, err := drv.Mount(ctx, storage.MountRequest{
			VolumeID:   vol.ID,
			NodeID:     nodeID,
			Handle:     vol.DriverHandle,
			TargetPath: decl.TargetPath,
			ReadOnly:   decl.ReadOnly,
		})
		vsmetrics.VolumeOperations.WithLabelValues(drv.Name(), "mount", outcomeStr(err)).Inc()
		_ = t0
		if err != nil {
			return nil, nil, fmt.Errorf("mount volume %q: %w", vol.ID, err)
		}

		mountID, err := r.store.BindMount(ctx, domain.VolumeMount{
			VolumeID:   vol.ID,
			InstanceID: instanceID,
			TargetPath: decl.TargetPath,
			ReadOnly:   decl.ReadOnly,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("bind mount record: %w", err)
		}
		r.store.UpdateMountState(ctx, mountID, domain.MountActive) //nolint:errcheck

		if vol.BoundNodeID == "" && drv.Name() == "local" {
			r.store.UpdateVolumeBoundNode(ctx, vol.ID, nodeID) //nolint:errcheck
		}
		if vol.State == domain.VolumeReady {
			r.store.UpdateVolumeState(ctx, vol.ID, domain.VolumeBound) //nolint:errcheck
		}
		vsmetrics.VolumeCount.WithLabelValues(vol.Manifest.Spec.Class, string(domain.VolumeBound)).Inc()

		resolved = append(resolved, adapter.ResolvedMount{
			HostPath:   mp.HostPath,
			TargetPath: decl.TargetPath,
			ReadOnly:   decl.ReadOnly,
		})
		ids = append(ids, mountID)
	}
	return resolved, ids, nil
}

// reconcileVolumes drives volumes in declared state through provisioning → ready.
func (r *VSReconciler) reconcileVolumes(ctx context.Context) error {
	declared, err := r.store.ListVolumesByState(ctx, domain.VolumeDeclared)
	if err != nil {
		return fmt.Errorf("list declared volumes: %w", err)
	}
	for i := range declared {
		if err := r.provisionVolume(ctx, &declared[i]); err != nil {
			r.store.WriteAuditLog(ctx, "volume_provision_failed", "volume", declared[i].ID, "", false, err.Error()) //nolint:errcheck
		}
	}
	return nil
}

// provisionVolume calls the storage driver Create for a declared volume.
func (r *VSReconciler) provisionVolume(ctx context.Context, vol *domain.Volume) error {
	drv, ok := r.classDrivers[vol.Manifest.Spec.Class]
	if !ok {
		return fmt.Errorf("no driver registered for class %q", vol.Manifest.Spec.Class)
	}

	if err := r.store.UpdateVolumeState(ctx, vol.ID, domain.VolumeProvisioning); err != nil {
		return fmt.Errorf("set provisioning: %w", err)
	}
	vsmetrics.VolumeCount.WithLabelValues(vol.Manifest.Spec.Class, string(domain.VolumeProvisioning)).Inc()

	handle, err := drv.Create(ctx, vol.Manifest.Metadata.Namespace, vol.Manifest.Metadata.Name, vol.Manifest.Spec)
	vsmetrics.VolumeOperations.WithLabelValues(drv.Name(), "create", outcomeStr(err)).Inc()
	if err != nil {
		r.store.UpdateVolumeFailure(ctx, vol.ID, err.Error()) //nolint:errcheck
		vsmetrics.VolumeCount.WithLabelValues(vol.Manifest.Spec.Class, string(domain.VolumeFailed)).Inc()
		return fmt.Errorf("driver create: %w", err)
	}

	r.store.UpdateVolumeHandle(ctx, vol.ID, handle)                     //nolint:errcheck
	r.store.UpdateVolumeState(ctx, vol.ID, domain.VolumeReady)          //nolint:errcheck
	vsmetrics.VolumeCount.WithLabelValues(vol.Manifest.Spec.Class, string(domain.VolumeReady)).Inc()
	vsmetrics.VolumeCapacityMiB.WithLabelValues(vol.Manifest.Spec.Class).Add(float64(vol.Manifest.Spec.CapacityMiB))
	r.store.WriteAuditLog(ctx, "volume_provisioned", "volume", vol.ID, "", true, //nolint:errcheck
		fmt.Sprintf("class=%s driver=%s", vol.Manifest.Spec.Class, drv.Name()))
	return nil
}

func outcomeStr(err error) string {
	if err == nil {
		return "ok"
	}
	return "error"
}

func (r *VSReconciler) handleFailedInstance(ctx context.Context, inst *domain.ServiceInstance) {
	svc, err := r.store.GetService(ctx, inst.ServiceID)
	if err != nil {
		return
	}

	policy := svc.Manifest.Spec.Restart
	maxAttempts := policy.MaximumAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3 // conservative default when unset
	}

	if policy.Policy != "on-failure" && policy.Policy != "always" {
		return // no restart desired
	}
	if inst.RetryCount >= maxAttempts {
		r.store.WriteAuditLog(ctx, "abandoned", "instance", inst.ID, "", false, //nolint:errcheck
			fmt.Sprintf("retries=%d max=%d policy=%s", inst.RetryCount, maxAttempts, policy.Policy))
		return
	}

	// Exponential backoff: retryBaseDelay * 2^retryCount
	delay := retryBaseDelay
	for i := 0; i < inst.RetryCount && i < 6; i++ {
		delay *= 2
	}
	if time.Since(inst.UpdatedAt) < delay {
		return // too soon; next loop will check again
	}

	r.store.ReleaseAllocation(ctx, inst.ID)                                      //nolint:errcheck
	r.store.IncrementRetryCount(ctx, inst.ID)                                    //nolint:errcheck
	r.store.WriteAuditLog(ctx, "retry_scheduled", "instance", inst.ID, "", true, //nolint:errcheck
		fmt.Sprintf("attempt=%d", inst.RetryCount+1))
}
