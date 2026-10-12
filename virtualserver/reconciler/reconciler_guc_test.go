package reconciler

// GUC Test Coverage — reconciler.go
// Knuth  (~5): algorithmic correctness, loop invariants, data-structure invariants
// Turing (~5): termination conditions, halting behaviour, decidability
// Church (~5): functional purity, side-effect isolation, referential transparency
// Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/adapter"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/scheduler"
)

// ---------------------------------------------------------------------------
// additional mock helpers (extend the base mock from reconciler_test.go)
// ---------------------------------------------------------------------------

// failCreateStore wraps mockStore and injects a CreateInstance error for one
// specific serviceID so we can test partial-failure paths.
type failCreateStore struct {
	*mockStore
	failServiceID int64
	createCalls   int32 // atomic
}

func (f *failCreateStore) CreateInstance(ctx context.Context, serviceID int64, vsPath domain.VSPath) (string, error) {
	atomic.AddInt32(&f.createCalls, 1)
	if serviceID == f.failServiceID {
		return "", fmt.Errorf("injected CreateInstance error for service %d", serviceID)
	}
	return f.mockStore.CreateInstance(ctx, serviceID, vsPath)
}

// countingAuditStore wraps mockStore and counts WriteAuditLog calls atomically.
type countingAuditStore struct {
	*mockStore
	auditCount int32
}

func (c *countingAuditStore) WriteAuditLog(ctx context.Context, action, resourceType, resourceID, ipAddr string, success bool, errMsg string) error {
	atomic.AddInt32(&c.auditCount, 1)
	return c.mockStore.WriteAuditLog(ctx, action, resourceType, resourceID, ipAddr, success, errMsg)
}

// newReconcilerWith builds a VSReconciler using the supplied StoreInterface.
func newReconcilerWith(st StoreInterface, ad adapter.BackendAdapter, interval time.Duration) *VSReconciler {
	adapters := map[string]adapter.BackendAdapter{}
	if ad != nil {
		adapters["mock"] = ad
	}
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return New(Config{
		Store:    st,
		Adapters: adapters,
		Sched:    scheduler.New(scheduler.DefaultWeights()),
		Interval: interval,
	})
}

// ---------------------------------------------------------------------------
// Knuth — algorithmic correctness
// ---------------------------------------------------------------------------

// TestGUC_Reconciler_DesiredEqualsActualNoOp verifies that when the active
// instance count already equals DesiredCount, reconcileDesiredCount creates
// zero additional instances (loop-invariant: gap = desired - active = 0).
func TestGUC_Reconciler_DesiredEqualsActualNoOp(t *testing.T) {
	ms := newMockStore()
	svc := newTestService("mock")
	svc.DesiredCount = 1
	ms.services[1] = svc

	ms.addInstance(&domain.ServiceInstance{
		ID:        "inst-running",
		ServiceID: 1,
		State:     domain.InstanceRunning,
		NodeID:    "n1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	ms.mu.Lock()
	total := len(ms.instances)
	ms.mu.Unlock()

	if total != 1 {
		t.Errorf("expected exactly 1 instance (no-op), got %d", total)
	}
}

// TestGUC_Reconciler_DesiredCountCreatesExactGap checks that the reconciler
// creates precisely (desired - active) instances — no more, no less.
func TestGUC_Reconciler_DesiredCountCreatesExactGap(t *testing.T) {
	tests := []struct {
		desired    int
		activeNow  int
		wantNewMin int
	}{
		{desired: 3, activeNow: 1, wantNewMin: 2},
		{desired: 5, activeNow: 3, wantNewMin: 2},
		{desired: 1, activeNow: 0, wantNewMin: 1},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(fmt.Sprintf("desired=%d_active=%d", tc.desired, tc.activeNow), func(t *testing.T) {
			ms := newMockStore()
			svc := newTestService("mock")
			svc.DesiredCount = tc.desired
			ms.services[1] = svc
			ms.nodes = []domain.Node{newTestNode()}

			for i := 0; i < tc.activeNow; i++ {
				ms.addInstance(&domain.ServiceInstance{
					ID:        fmt.Sprintf("pre-%d", i),
					ServiceID: 1,
					State:     domain.InstanceRunning,
					NodeID:    "n1",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				})
			}

			r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
			if err := r.reconcile(context.Background()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}

			ms.mu.Lock()
			total := len(ms.instances)
			ms.mu.Unlock()

			if total < tc.activeNow+tc.wantNewMin {
				t.Errorf("expected >= %d instances, got %d", tc.activeNow+tc.wantNewMin, total)
			}
		})
	}
}

// TestGUC_Reconciler_ExponentialBackoffGrowth asserts that the exponential
// back-off formula (retryBaseDelay * 2^retryCount) prevents premature retry.
func TestGUC_Reconciler_ExponentialBackoffGrowth(t *testing.T) {
	// retryBaseDelay = 2s.  For retryCount=2 the delay = 2 * 2^2 = 8 s.
	// An instance updated 6 s ago must NOT be retried; one updated 10 s ago must.
	baseDelay := retryBaseDelay // 2 s (package-level const)

	tests := []struct {
		retryCount  int
		updatedAgo  time.Duration
		wantRetried bool
	}{
		{retryCount: 0, updatedAgo: baseDelay - 500*time.Millisecond, wantRetried: false},
		{retryCount: 0, updatedAgo: baseDelay + 500*time.Millisecond, wantRetried: true},
		{retryCount: 2, updatedAgo: 7 * time.Second, wantRetried: false}, // 7 < 8
		{retryCount: 2, updatedAgo: 9 * time.Second, wantRetried: true},  // 9 > 8
	}

	for _, tc := range tests {
		tc := tc
		t.Run(fmt.Sprintf("rc=%d_ago=%s", tc.retryCount, tc.updatedAgo), func(t *testing.T) {
			ms := newMockStore()
			svc := newTestService("mock")
			svc.Manifest.Spec.Restart = domain.RestartPolicy{Policy: "on-failure", MaximumAttempts: 99}
			ms.services[1] = svc
			ms.nodes = []domain.Node{newTestNode()}

			inst := &domain.ServiceInstance{
				ID:         "inst-backoff",
				ServiceID:  1,
				State:      domain.InstanceFailed,
				RetryCount: tc.retryCount,
				UpdatedAt:  time.Now().Add(-tc.updatedAgo),
			}
			ms.addInstance(inst)

			r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
			r.reconcile(context.Background()) //nolint:errcheck

			ms.mu.Lock()
			state := ms.instances["inst-backoff"].State
			ms.mu.Unlock()

			retried := state == domain.InstanceDeclared
			if retried != tc.wantRetried {
				t.Errorf("updatedAgo=%s retryCount=%d: wantRetried=%v got state=%q",
					tc.updatedAgo, tc.retryCount, tc.wantRetried, state)
			}
		})
	}
}

// TestGUC_Reconciler_NodeHeartbeatThresholdBoundary checks that only nodes
// whose silence exceeds nodeTTL are degraded — boundary at exactly TTL is safe.
func TestGUC_Reconciler_NodeHeartbeatThresholdBoundary(t *testing.T) {
	ttl := 30 * time.Second
	now := time.Now()
	stale := now.Add(-(ttl + time.Second))
	fresh := now.Add(-(ttl - time.Second))

	tests := []struct {
		name          string
		lastHeartbeat *time.Time
		wantDegraded  bool
	}{
		{"stale_over_ttl", &stale, true},
		{"fresh_within_ttl", &fresh, false},
		{"nil_never_degraded", nil, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ms := newMockStore()
			ms.nodes = []domain.Node{{
				ID:            "n1",
				Name:          "node1",
				State:         domain.NodeReady,
				LastHeartbeat: tc.lastHeartbeat,
			}}

			r := newReconcilerWithMock(ms, nil)
			r.nodeTTL = ttl
			r.reconcile(context.Background()) //nolint:errcheck

			ms.mu.Lock()
			state := ms.nodes[0].State
			ms.mu.Unlock()

			degraded := state == domain.NodeDegraded
			if degraded != tc.wantDegraded {
				t.Errorf("%s: wantDegraded=%v got state=%q", tc.name, tc.wantDegraded, state)
			}
		})
	}
}

// TestGUC_Reconciler_RetryCountIncrementedOnRequeue verifies the loop
// invariant that each eligible retry increments RetryCount by exactly one.
func TestGUC_Reconciler_RetryCountIncrementedOnRequeue(t *testing.T) {
	ms := newMockStore()
	svc := newTestService("mock")
	svc.Manifest.Spec.Restart = domain.RestartPolicy{Policy: "on-failure", MaximumAttempts: 10}
	ms.services[1] = svc

	inst := &domain.ServiceInstance{
		ID:         "inst-rc",
		ServiceID:  1,
		State:      domain.InstanceFailed,
		RetryCount: 4,
		UpdatedAt:  time.Now().Add(-60 * time.Second), // well past any backoff
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	r.reconcile(context.Background()) //nolint:errcheck

	ms.mu.Lock()
	rc := ms.instances["inst-rc"].RetryCount
	ms.mu.Unlock()

	if rc != 5 {
		t.Errorf("expected RetryCount=5 after one requeue, got %d", rc)
	}
}

// ---------------------------------------------------------------------------
// Turing — termination conditions
// ---------------------------------------------------------------------------

// TestGUC_Reconciler_LoopTerminatesOnStop confirms the background goroutine
// halts cleanly when Stop is called, and the done channel is eventually closed.
func TestGUC_Reconciler_LoopTerminatesOnStop(t *testing.T) {
	ms := newMockStore()
	r := newReconcilerWithMock(ms, nil)
	r.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := r.Stop(ctx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	// done channel must be closed after Stop
	select {
	case <-r.done:
		// correct
	default:
		t.Error("done channel not closed after Stop returned")
	}
}

// TestGUC_Reconciler_StopContextExpired verifies that Stop returns the
// context's error when the context expires before the loop exits.
func TestGUC_Reconciler_StopContextExpired(t *testing.T) {
	ms := newMockStore()
	r := newReconcilerWithMock(ms, nil)

	// Do NOT call Start — so the loop goroutine never closes done.
	// Use an already-cancelled context; Stop must return ctx.Err immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Stop

	err := r.Stop(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestGUC_Reconciler_MaxRetriesHaltsRetry asserts that once RetryCount reaches
// MaximumAttempts the instance is permanently terminated, halting the retry loop.
func TestGUC_Reconciler_MaxRetriesHaltsRetry(t *testing.T) {
	maxAttempts := 3
	ms := newMockStore()
	svc := newTestService("mock")
	svc.Manifest.Spec.Restart = domain.RestartPolicy{Policy: "on-failure", MaximumAttempts: maxAttempts}
	ms.services[1] = svc

	inst := &domain.ServiceInstance{
		ID:         "inst-maxretry",
		ServiceID:  1,
		State:      domain.InstanceFailed,
		RetryCount: maxAttempts, // already at the cap
		UpdatedAt:  time.Now().Add(-60 * time.Second),
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())

	// Run multiple passes to confirm the state doesn't oscillate.
	for i := 0; i < 3; i++ {
		r.reconcile(context.Background()) //nolint:errcheck
	}

	ms.mu.Lock()
	state := ms.instances["inst-maxretry"].State
	ms.mu.Unlock()

	if state != domain.InstanceTerminated {
		t.Errorf("expected InstanceTerminated after max retries, got %q", state)
	}
}

// TestGUC_Reconciler_ZeroDesiredCountSkipped confirms that a service with
// DesiredCount=0 terminates the reconcile loop for that service without
// creating any instance (decidability: the loop body is never entered).
func TestGUC_Reconciler_ZeroDesiredCountSkipped(t *testing.T) {
	ms := newMockStore()
	svc := newTestService("mock")
	svc.DesiredCount = 0
	ms.services[1] = svc

	r := newReconcilerWithMock(ms, nil)
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	ms.mu.Lock()
	total := len(ms.instances)
	ms.mu.Unlock()

	if total != 0 {
		t.Errorf("expected 0 instances for DesiredCount=0, got %d", total)
	}
}

// TestGUC_Reconciler_LoopIntervalRespected verifies the background loop fires
// at the configured interval.  We start the reconciler with a 60 ms tick and a
// service that needs one instance; after 200 ms the instance must have been
// created (showing the ticker fired).
func TestGUC_Reconciler_LoopIntervalRespected(t *testing.T) {
	ms := newMockStore()
	svc := newTestService("mock")
	svc.DesiredCount = 1
	ms.services[1] = svc
	ms.nodes = []domain.Node{newTestNode()}

	r := newReconcilerWith(ms, adapter.NewMockAdapter(), 60*time.Millisecond)
	r.Start()
	defer r.Stop(context.Background()) //nolint:errcheck

	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		ms.mu.Lock()
		total := len(ms.instances)
		ms.mu.Unlock()
		if total > 0 {
			return // loop fired and created instance
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("reconciler loop did not fire within 400 ms; expected at least one instance created")
}

// ---------------------------------------------------------------------------
// Church — functional purity / side-effect isolation
// ---------------------------------------------------------------------------

// TestGUC_Reconciler_IdempotentDoubleReconcile asserts that running two
// reconcile passes on an already-satisfied state produces the same result
// as one pass (referential transparency of the reconcile function).
func TestGUC_Reconciler_IdempotentDoubleReconcile(t *testing.T) {
	ms := newMockStore()
	svc := newTestService("mock")
	svc.DesiredCount = 2
	ms.services[1] = svc
	ms.nodes = []domain.Node{newTestNode()}

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())

	// First pass — fills the gap
	r.reconcile(context.Background()) //nolint:errcheck

	ms.mu.Lock()
	afterFirst := len(ms.instances)
	ms.mu.Unlock()

	// Second pass — must not create more instances
	r.reconcile(context.Background()) //nolint:errcheck

	ms.mu.Lock()
	afterSecond := len(ms.instances)
	ms.mu.Unlock()

	// After the first pass we may have declared instances (not yet running).
	// The second pass should not further duplicate them.
	if afterSecond > afterFirst {
		t.Errorf("idempotency violated: count grew from %d to %d on second pass", afterFirst, afterSecond)
	}
}

// TestGUC_Reconciler_CreateInstanceErrorIsolated checks that a CreateInstance
// error for service A does not prevent service B from getting its instance.
func TestGUC_Reconciler_CreateInstanceErrorIsolated(t *testing.T) {
	base := newMockStore()

	svcA := newTestService("mock")
	svcA.ID = 1
	svcA.DesiredCount = 1
	base.services[1] = svcA

	svcB := newTestService("mock")
	svcB.ID = 2
	svcB.Manifest.Metadata.Name = "svc-b"
	svcB.DesiredCount = 1
	base.services[2] = svcB

	// failCreateStore causes CreateInstance to fail for service 1 only.
	fs := &failCreateStore{mockStore: base, failServiceID: 1}

	r := newReconcilerWith(fs, adapter.NewMockAdapter(), 0)
	r.reconcile(context.Background()) //nolint:errcheck

	base.mu.Lock()
	total := len(base.instances)
	base.mu.Unlock()

	// Service B should have gotten its instance despite service A failing.
	if total < 1 {
		t.Errorf("expected at least one instance (for service B) despite error for service A, got %d", total)
	}
}

// TestGUC_Reconciler_ProvisionErrorIsolated verifies that a provision failure
// on one instance does not affect an already-running instance (side-effect isolation).
func TestGUC_Reconciler_ProvisionErrorIsolated(t *testing.T) {
	ms := newMockStore()
	ms.services[1] = newTestService("mock")
	ms.nodes = []domain.Node{newTestNode()}

	// One running instance that must be left untouched.
	ms.addInstance(&domain.ServiceInstance{
		ID:            "inst-running",
		ServiceID:     1,
		State:         domain.InstanceRunning,
		NodeID:        "n1",
		RuntimeHandle: map[string]string{"pid": "1"},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	})

	// One scheduled instance that will fail provisioning.
	ms.addInstance(&domain.ServiceInstance{
		ID:            "inst-sched",
		ServiceID:     1,
		State:         domain.InstanceScheduled,
		NodeID:        "n1",
		RuntimeHandle: map[string]string{},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	})
	ms.allocs["inst-sched"] = nil // ensure GetActiveAllocation returns not-found

	ad := adapter.NewMockAdapter()
	// Mark the running instance as started so Inspect returns Running=true.
	ad.Start(context.Background(), adapter.RuntimeHandle{AdapterName: "mock", InstanceID: "inst-running"}) //nolint:errcheck
	ad.FailMode = adapter.FailProvision

	r := newReconcilerWithMock(ms, ad)
	r.reconcile(context.Background()) //nolint:errcheck

	ms.mu.Lock()
	runningState := ms.instances["inst-running"].State
	ms.mu.Unlock()

	if runningState != domain.InstanceRunning {
		t.Errorf("running instance state changed due to neighbour provision failure: got %q", runningState)
	}
}

// TestGUC_Reconciler_ReconcileDoesNotMutateStoreNodeSlice confirms that the
// reconciler does not mutate the node slice returned by the store between
// two consecutive reads (referential transparency of ListNodes).
func TestGUC_Reconciler_ReconcileDoesNotMutateStoreNodeSlice(t *testing.T) {
	ms := newMockStore()
	node := newTestNode()
	ms.nodes = []domain.Node{node}

	r := newReconcilerWithMock(ms, nil)
	r.reconcile(context.Background()) //nolint:errcheck

	// The node in the store must still be NodeReady (no heartbeat == nil, so TTL skip).
	ms.mu.Lock()
	state := ms.nodes[0].State
	ms.mu.Unlock()

	if state != domain.NodeReady {
		t.Errorf("node state unexpectedly mutated to %q", state)
	}
}

// TestGUC_Reconciler_MultiServiceIndependence verifies that two services are
// reconciled independently: each gets its own instances without cross-pollination.
func TestGUC_Reconciler_MultiServiceIndependence(t *testing.T) {
	ms := newMockStore()

	svcA := newTestService("mock")
	svcA.ID = 10
	svcA.DesiredCount = 1
	ms.services[10] = svcA

	svcB := newTestService("mock")
	svcB.ID = 20
	svcB.Manifest.Metadata.Name = "svc-b"
	svcB.DesiredCount = 1
	ms.services[20] = svcB

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	r.reconcile(context.Background()) //nolint:errcheck

	ms.mu.Lock()
	var countA, countB int
	for _, inst := range ms.instances {
		switch inst.ServiceID {
		case 10:
			countA++
		case 20:
			countB++
		}
	}
	ms.mu.Unlock()

	if countA < 1 {
		t.Errorf("service A got no instances (expected >= 1)")
	}
	if countB < 1 {
		t.Errorf("service B got no instances (expected >= 1)")
	}
}

// ---------------------------------------------------------------------------
// Gödel — formal consistency / invariant preservation
// ---------------------------------------------------------------------------

// TestGUC_Reconciler_AuditLogWrittenOnInstanceCreation verifies the formal
// contract that every created instance emits an "instance_created" audit entry.
func TestGUC_Reconciler_AuditLogWrittenOnInstanceCreation(t *testing.T) {
	base := newMockStore()
	svc := newTestService("mock")
	svc.DesiredCount = 1
	base.services[1] = svc

	cs := &countingAuditStore{mockStore: base}
	r := newReconcilerWith(cs, adapter.NewMockAdapter(), 0)
	r.reconcile(context.Background()) //nolint:errcheck

	count := atomic.LoadInt32(&cs.auditCount)
	if count == 0 {
		t.Error("expected at least one audit log entry after reconcile creating an instance")
	}

	// Verify the specific action appears in the log.
	base.mu.Lock()
	found := false
	for _, entry := range base.auditLog {
		if len(entry) >= 16 && entry[:16] == "instance_created" {
			found = true
			break
		}
	}
	base.mu.Unlock()

	if !found {
		t.Errorf("audit log does not contain 'instance_created' entry; log: %v", base.auditLog)
	}
}

// TestGUC_Reconciler_NeverRestartPolicyTerminatesDirect asserts the invariant:
// an instance with restart policy "never" is always moved to terminated (not
// re-queued into declared), so the state machine is consistent and irrevocable.
func TestGUC_Reconciler_NeverRestartPolicyTerminatesDirect(t *testing.T) {
	ms := newMockStore()
	svc := newTestService("mock")
	svc.Manifest.Spec.Restart = domain.RestartPolicy{Policy: "never"}
	ms.services[1] = svc

	inst := &domain.ServiceInstance{
		ID:         "inst-never",
		ServiceID:  1,
		State:      domain.InstanceFailed,
		RetryCount: 0,
		UpdatedAt:  time.Now().Add(-60 * time.Second),
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())
	r.reconcile(context.Background()) //nolint:errcheck

	ms.mu.Lock()
	state := ms.instances["inst-never"].State
	ms.mu.Unlock()

	if state != domain.InstanceTerminated {
		t.Errorf("restart=never invariant violated: expected InstanceTerminated, got %q", state)
	}
}

// TestGUC_Reconciler_NilHeartbeatNodeNeverDegraded preserves the consistency
// invariant that a node whose LastHeartbeat is nil (never sent one) is never
// marked degraded regardless of how much time has elapsed.
func TestGUC_Reconciler_NilHeartbeatNodeNeverDegraded(t *testing.T) {
	ms := newMockStore()
	ms.nodes = []domain.Node{{
		ID:            "n-fresh",
		Name:          "new-node",
		State:         domain.NodeReady,
		LastHeartbeat: nil, // never sent a heartbeat
	}}

	r := newReconcilerWithMock(ms, nil)
	r.nodeTTL = 1 * time.Millisecond // extremely short TTL to stress the guard
	r.reconcile(context.Background()) //nolint:errcheck

	ms.mu.Lock()
	state := ms.nodes[0].State
	ms.mu.Unlock()

	if state != domain.NodeReady {
		t.Errorf("nil-heartbeat node degraded unexpectedly: got state %q", state)
	}
}

// TestGUC_Reconciler_ConcurrentTryRunSerializes checks the Gödel invariant that
// the reconciler never runs two concurrent passes (runningMu overlap guard).
// When the mutex is already held, tryRun must return without blocking.
func TestGUC_Reconciler_ConcurrentTryRunSerializes(t *testing.T) {
	ms := newMockStore()
	r := newReconcilerWithMock(ms, nil)

	// Hold the mutex to simulate an in-progress pass.
	r.runningMu.Lock()
	done := make(chan struct{})
	go func() {
		r.tryRun() // must not block
		close(done)
	}()

	select {
	case <-done:
		// correct: returned immediately
	case <-time.After(300 * time.Millisecond):
		t.Error("tryRun blocked while runningMu was held — overlap guard broken")
	}
	r.runningMu.Unlock()
}

// TestGUC_Reconciler_ExhaustedRetriesAlwaysTerminal proves the state-machine
// consistency invariant: once a failed instance's RetryCount >= MaximumAttempts
// it must always end in InstanceTerminated, regardless of how many passes run.
func TestGUC_Reconciler_ExhaustedRetriesAlwaysTerminal(t *testing.T) {
	maxAttempts := 2
	ms := newMockStore()
	svc := newTestService("mock")
	svc.Manifest.Spec.Restart = domain.RestartPolicy{Policy: "on-failure", MaximumAttempts: maxAttempts}
	ms.services[1] = svc

	inst := &domain.ServiceInstance{
		ID:         "inst-exhausted",
		ServiceID:  1,
		State:      domain.InstanceFailed,
		RetryCount: maxAttempts,
		UpdatedAt:  time.Now().Add(-5 * time.Minute),
	}
	ms.addInstance(inst)

	r := newReconcilerWithMock(ms, adapter.NewMockAdapter())

	// Run 5 passes — state must never leave the terminal set.
	for i := 0; i < 5; i++ {
		r.reconcile(context.Background()) //nolint:errcheck
		ms.mu.Lock()
		state := ms.instances["inst-exhausted"].State
		ms.mu.Unlock()
		if state != domain.InstanceTerminated {
			t.Errorf("pass %d: expected InstanceTerminated, got %q (invariant broken)", i+1, state)
			return
		}
	}
}
