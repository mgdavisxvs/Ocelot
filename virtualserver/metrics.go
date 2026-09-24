package virtualserver

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const ns = "ocelot_vs"

// VSMetrics holds all Prometheus instrumentation for the virtual server.
type VSMetrics struct {
	ReconcilerLoops    prometheus.Counter
	ReconcilerDuration prometheus.Histogram

	AdapterOps      *prometheus.CounterVec   // labels: adapter, operation, result
	AdapterDuration *prometheus.HistogramVec // labels: adapter, operation

	PlacementDecisions *prometheus.CounterVec // labels: result (placed, unschedulable)
	PlacementScore     prometheus.Histogram

	VolumeOps      *prometheus.CounterVec   // labels: driver, operation, result
	VolumeDuration *prometheus.HistogramVec // labels: driver, operation

	APIRequests  *prometheus.CounterVec   // labels: method, path, status
	APIDuration  *prometheus.HistogramVec // labels: method, path
}

// NewVSMetrics registers and returns all VS metrics using promauto (auto-register).
func NewVSMetrics(reg prometheus.Registerer) *VSMetrics {
	factory := promauto.With(reg)

	return &VSMetrics{
		ReconcilerLoops: factory.NewCounter(prometheus.CounterOpts{
			Namespace: ns,
			Name:      "reconciler_loops_total",
			Help:      "Total number of reconciler loop iterations.",
		}),
		ReconcilerDuration: factory.NewHistogram(prometheus.HistogramOpts{
			Namespace: ns,
			Name:      "reconciler_loop_duration_seconds",
			Help:      "Duration of each reconciler loop iteration.",
			Buckets:   prometheus.DefBuckets,
		}),

		AdapterOps: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns,
			Name:      "adapter_operations_total",
			Help:      "Total backend adapter operations.",
		}, []string{"adapter", "operation", "result"}),
		AdapterDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: ns,
			Name:      "adapter_operation_duration_seconds",
			Help:      "Duration of backend adapter operations.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"adapter", "operation"}),

		PlacementDecisions: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns,
			Name:      "placement_decisions_total",
			Help:      "Total placement decisions made by the scheduler.",
		}, []string{"result"}),
		PlacementScore: factory.NewHistogram(prometheus.HistogramOpts{
			Namespace: ns,
			Name:      "placement_score",
			Help:      "Scheduler score of the winning node placement.",
			Buckets:   []float64{0.1, 0.2, 0.3, 0.5, 0.7, 0.9, 1.0},
		}),

		VolumeOps: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns,
			Name:      "volume_operations_total",
			Help:      "Total storage driver operations.",
		}, []string{"driver", "operation", "result"}),
		VolumeDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: ns,
			Name:      "volume_operation_duration_seconds",
			Help:      "Duration of storage driver operations.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"driver", "operation"}),

		APIRequests: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns,
			Name:      "api_requests_total",
			Help:      "Total HTTP API requests.",
		}, []string{"method", "path", "status"}),
		APIDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: ns,
			Name:      "api_request_duration_seconds",
			Help:      "Duration of HTTP API requests.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "path"}),
	}
}
