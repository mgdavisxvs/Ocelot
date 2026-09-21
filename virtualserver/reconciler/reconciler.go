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
}

// ArtifactCatalog is the narrow interface bridging the reconciler to artifact availability.
type ArtifactCatalog interface {
	Lookup(ctx context.Context, infoHash string) (domain.ArtifactStatus, error)
}

// VSReconciler drives instances from declared → running and handles failure recovery.
// Only one reconciler loop runs at a time (overlap guard via runningMu).
type VSReconciler struct {
	store    StoreInterface
	adapters map[string]adapter.BackendAdapter
	sched    *scheduler.Scheduler
	catalog  ArtifactCatalog
	interval time.Duration

	runningMu sync.Mutex

	stop chan struct{}
	done chan struct{}
}

// Config holds VSReconciler construction parameters.
type Config struct {
	Store    StoreInterface
	Adapters map[string]adapter.BackendAdapter
	Sched    *scheduler.Scheduler
	Catalog  ArtifactCatalog
	Interval time.Duration
}

// New creates a VSReconciler. Interval defaults to 15 s if zero.
func New(cfg Config) *VSReconciler {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	return &VSReconciler{
		store:    cfg.Store,
		adapters: cfg.Adapters,
		sched:    cfg.Sched,
		catalog:  cfg.Catalog,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
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

	r.store.UpdateRuntimeHandle(ctx, inst.ID, handle.Data) //nolint:errcheck
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

	r.store.ReleaseAllocation(ctx, inst.ID) //nolint:errcheck
	r.store.IncrementRetryCount(ctx, inst.ID) //nolint:errcheck
	r.store.WriteAuditLog(ctx, "retry_scheduled", "instance", inst.ID, "", true, //nolint:errcheck
		fmt.Sprintf("attempt=%d", inst.RetryCount+1))
}
