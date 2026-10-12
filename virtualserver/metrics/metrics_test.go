package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// collectMetric gathers metric families from the default registry and returns
// the one matching the given fully-qualified name.
func collectMetric(t *testing.T, fqn string) *dto.MetricFamily {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == fqn {
			return mf
		}
	}
	return nil
}

func TestMetrics_InstancesTotal_Registered(t *testing.T) {
	// Touch the metric so it shows up in the gather output.
	InstancesTotal.WithLabelValues("declared").Add(0)
	mf := collectMetric(t, "ocelot_vs_instances_total")
	if mf == nil {
		t.Fatal("ocelot_vs_instances_total not found in registry")
	}
	if !strings.Contains(mf.GetHelp(), "state") {
		t.Errorf("unexpected help text: %q", mf.GetHelp())
	}
}

func TestMetrics_InstanceStateTransitions_Registered(t *testing.T) {
	InstanceStateTransitions.WithLabelValues("declared", "scheduled").Add(0)
	mf := collectMetric(t, "ocelot_vs_instance_state_transitions_total")
	if mf == nil {
		t.Fatal("ocelot_vs_instance_state_transitions_total not found in registry")
	}
}

func TestMetrics_PlacementDecisions_Registered(t *testing.T) {
	PlacementDecisions.WithLabelValues("scheduled").Add(0)
	mf := collectMetric(t, "ocelot_vs_placement_decisions_total")
	if mf == nil {
		t.Fatal("ocelot_vs_placement_decisions_total not found in registry")
	}
}

func TestMetrics_PlacementFailures_Registered(t *testing.T) {
	PlacementFailures.WithLabelValues("no_candidates").Add(0)
	mf := collectMetric(t, "ocelot_vs_placement_failures_total")
	if mf == nil {
		t.Fatal("ocelot_vs_placement_failures_total not found in registry")
	}
}

func TestMetrics_ReconcilerLoops_Registered(t *testing.T) {
	ReconcilerLoops.WithLabelValues("ok").Add(0)
	mf := collectMetric(t, "ocelot_vs_reconciler_loops_total")
	if mf == nil {
		t.Fatal("ocelot_vs_reconciler_loops_total not found in registry")
	}
}

func TestMetrics_ReconcilerDuration_Registered(t *testing.T) {
	ReconcilerDuration.Observe(0.001)
	mf := collectMetric(t, "ocelot_vs_reconciler_duration_seconds")
	if mf == nil {
		t.Fatal("ocelot_vs_reconciler_duration_seconds not found in registry")
	}
}

func TestMetrics_AdapterOperations_Registered(t *testing.T) {
	AdapterOperations.WithLabelValues("mock", "Start", "ok").Add(0)
	mf := collectMetric(t, "ocelot_vs_adapter_operations_total")
	if mf == nil {
		t.Fatal("ocelot_vs_adapter_operations_total not found in registry")
	}
}

func TestMetrics_APIRequests_Registered(t *testing.T) {
	APIRequests.WithLabelValues("GET", "/v1/nodes", "200").Add(0)
	mf := collectMetric(t, "ocelot_vs_api_requests_total")
	if mf == nil {
		t.Fatal("ocelot_vs_api_requests_total not found in registry")
	}
}

func TestMetrics_NodeStates_Registered(t *testing.T) {
	NodeStates.WithLabelValues("ready").Add(0)
	mf := collectMetric(t, "ocelot_vs_node_states_total")
	if mf == nil {
		t.Fatal("ocelot_vs_node_states_total not found in registry")
	}
}

func TestMetrics_VolumeCount_Registered(t *testing.T) {
	VolumeCount.WithLabelValues("local", "declared").Add(0)
	mf := collectMetric(t, "ocelot_vs_volumes_total")
	if mf == nil {
		t.Fatal("ocelot_vs_volumes_total not found in registry")
	}
}

func TestMetrics_VolumeCapacityMiB_Registered(t *testing.T) {
	VolumeCapacityMiB.WithLabelValues("local").Add(0)
	mf := collectMetric(t, "ocelot_vs_volume_capacity_mib")
	if mf == nil {
		t.Fatal("ocelot_vs_volume_capacity_mib not found in registry")
	}
}

func TestMetrics_VolumeUsedMiB_Registered(t *testing.T) {
	VolumeUsedMiB.WithLabelValues("local").Add(0)
	mf := collectMetric(t, "ocelot_vs_volume_used_mib")
	if mf == nil {
		t.Fatal("ocelot_vs_volume_used_mib not found in registry")
	}
}

func TestMetrics_VolumeOperations_Registered(t *testing.T) {
	VolumeOperations.WithLabelValues("local", "create", "ok").Add(0)
	mf := collectMetric(t, "ocelot_vs_volume_operations_total")
	if mf == nil {
		t.Fatal("ocelot_vs_volume_operations_total not found in registry")
	}
}

func TestMetrics_VolumeCount_IncrDecr(t *testing.T) {
	before := getGaugeValue(t, "ocelot_vs_volumes_total", map[string]string{"class": "ssd", "state": "ready"})
	VolumeCount.WithLabelValues("ssd", "ready").Inc()
	after := getGaugeValue(t, "ocelot_vs_volumes_total", map[string]string{"class": "ssd", "state": "ready"})
	if after != before+1 {
		t.Errorf("expected gauge to increment by 1: before=%f after=%f", before, after)
	}
	VolumeCount.WithLabelValues("ssd", "ready").Dec()
}

// getGaugeValue extracts a specific gauge value from the default registry by
// metric name and label set.
func getGaugeValue(t *testing.T, fqn string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != fqn {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsMatch(m.GetLabel(), labels) {
				return m.GetGauge().GetValue()
			}
		}
	}
	return 0
}

func labelsMatch(pairs []*dto.LabelPair, want map[string]string) bool {
	matched := 0
	for _, lp := range pairs {
		if v, ok := want[lp.GetName()]; ok && v == lp.GetValue() {
			matched++
		}
	}
	return matched == len(want)
}
