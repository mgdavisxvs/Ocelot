package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// All VS metrics are registered at package init. Labels are bounded to prevent cardinality explosion.

var (
	// InstancesTotal is a gauge tracking live instance counts by state.
	InstancesTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "ocelot_vs",
		Name:      "instances_total",
		Help:      "Number of service instances currently in each state.",
	}, []string{"state"})

	// InstanceStateTransitions counts state transitions.
	InstanceStateTransitions = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ocelot_vs",
		Name:      "instance_state_transitions_total",
		Help:      "Total number of instance state transitions.",
	}, []string{"from", "to"})

	// PlacementDecisions counts scheduling outcomes.
	PlacementDecisions = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ocelot_vs",
		Name:      "placement_decisions_total",
		Help:      "Total scheduling decisions, labeled by outcome.",
	}, []string{"outcome"}) // outcome: scheduled, rejected

	// PlacementFailures counts scheduling failures by reason.
	PlacementFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ocelot_vs",
		Name:      "placement_failures_total",
		Help:      "Total placement failures by reason.",
	}, []string{"reason"}) // reason: no_candidates, resource_exhausted

	// ReconcilerLoops counts reconciler loop iterations.
	ReconcilerLoops = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ocelot_vs",
		Name:      "reconciler_loops_total",
		Help:      "Total reconciler loop iterations.",
	}, []string{"outcome"}) // outcome: ok, error

	// ReconcilerDuration observes reconciler loop wall time.
	ReconcilerDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "ocelot_vs",
		Name:      "reconciler_duration_seconds",
		Help:      "Wall-clock time per reconciler loop iteration.",
		Buckets:   prometheus.DefBuckets,
	})

	// AdapterOperations counts adapter method calls.
	AdapterOperations = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ocelot_vs",
		Name:      "adapter_operations_total",
		Help:      "Total adapter operations by method and outcome.",
	}, []string{"adapter", "method", "outcome"}) // outcome: ok, error

	// AdapterDuration observes adapter operation latency.
	AdapterDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "ocelot_vs",
		Name:      "adapter_operation_duration_seconds",
		Help:      "Latency of adapter operations.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"adapter", "method"})

	// APIRequests counts HTTP API requests.
	APIRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ocelot_vs",
		Name:      "api_requests_total",
		Help:      "Total VS HTTP API requests.",
	}, []string{"method", "path", "status"})

	// APIRequestDuration observes HTTP handler latency.
	APIRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "ocelot_vs",
		Name:      "api_request_duration_seconds",
		Help:      "Latency of VS HTTP API handlers.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "path"})

	// NodeStates is a gauge tracking node count by state.
	NodeStates = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "ocelot_vs",
		Name:      "node_states_total",
		Help:      "Number of nodes in each state.",
	}, []string{"state"})

	// ArtifactLookups counts artifact catalog lookups.
	ArtifactLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ocelot_vs",
		Name:      "artifact_lookups_total",
		Help:      "Total artifact catalog lookups.",
	}, []string{"outcome"}) // outcome: available, degraded, unavailable, error
)
