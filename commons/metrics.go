package commons

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// CommonsMetrics holds all Prometheus metrics for the Compute Commons layer.
// One shared instance is created at package init; tests may substitute their own.
type CommonsMetrics struct {
	// ResourcePrice tracks the current effective price per GB for each resource type.
	ResourcePrice *prometheus.GaugeVec

	// ResourceUtilization tracks the swarm utilisation ratio (leechers/total) per torrent.
	ResourceUtilization *prometheus.GaugeVec

	// CreditsConsumed counts total CC charged across all settlements.
	CreditsConsumed prometheus.Counter

	// BudgetRemaining is a gauge of the remaining CC in accounts (approximation).
	BudgetRemaining *prometheus.GaugeVec

	// AllocationTotal counts accepted scheduling decisions.
	AllocationTotal *prometheus.CounterVec

	// AllocationRejectedTotal counts rejected scheduling decisions with reason.
	AllocationRejectedTotal *prometheus.CounterVec

	// EstimatedVsActualCost tracks the ratio of estimated to actual CC cost.
	EstimatedVsActualCost prometheus.Histogram

	// LedgerSettlements counts ledger settlement calls (idempotent re-submissions included).
	LedgerSettlements *prometheus.CounterVec

	// PriorityEnforcements counts how often P0/P1 protection blocked a budget-bypass attempt.
	PriorityEnforcements prometheus.Counter

	// ReservationExpiries counts expired credit holds released.
	ReservationExpiries prometheus.Counter
}

// DefaultMetrics is the package-level singleton used by ComputeCommons.
var DefaultMetrics = newCommonsMetrics()

func newCommonsMetrics() *CommonsMetrics {
	return &CommonsMetrics{
		ResourcePrice: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "commons_resource_price",
				Help: "Effective CC price per GB for each resource type (base units / ResourceGB)",
			},
			[]string{"resource_type"},
		),
		ResourceUtilization: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "commons_resource_utilization",
				Help: "Swarm utilisation ratio (leechers / total peers) per torrent",
			},
			[]string{"torrent_id"},
		),
		CreditsConsumed: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "commons_credits_consumed_total",
				Help: "Total CC base units consumed across all ledger settlements",
			},
		),
		BudgetRemaining: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "commons_budget_remaining",
				Help: "Remaining CC budget for a user (base units)",
			},
			[]string{"user_id"},
		),
		AllocationTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "commons_allocation_total",
				Help: "Total number of peer allocation decisions made",
			},
			[]string{"priority_class", "result"},
		),
		AllocationRejectedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "commons_allocation_rejected_total",
				Help: "Total number of peer allocations rejected, by reason",
			},
			[]string{"reason"},
		),
		EstimatedVsActualCost: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "commons_estimated_vs_actual_cost_ratio",
				Help:    "Ratio of estimated CC cost to actual CC settled (1.0 = perfect estimate)",
				Buckets: []float64{0.1, 0.25, 0.5, 0.75, 1.0, 1.25, 1.5, 2.0, 5.0},
			},
		),
		LedgerSettlements: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "commons_ledger_settlements_total",
				Help: "Total ledger settlement calls",
			},
			[]string{"resource_type", "idempotent"},
		),
		PriorityEnforcements: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "commons_priority_enforcements_total",
				Help: "Number of times P0/P1 priority protection prevented a budget bypass",
			},
		),
		ReservationExpiries: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "commons_reservation_expiries_total",
				Help: "Number of expired reservations that had their credit holds released",
			},
		),
	}
}

// ObservePrice updates the ResourcePrice gauge for rt.
func (m *CommonsMetrics) ObservePrice(rt ResourceType, price ComputeCredit) {
	// Convert from base units to CC per GB for human-readable gauge.
	pricePerGB := float64(price.Raw()) / float64(CreditScale)
	m.ResourcePrice.WithLabelValues(string(rt)).Set(pricePerGB)
}

// ObserveSettlement records a ledger settlement in metrics.
func (m *CommonsMetrics) ObserveSettlement(rt ResourceType, charge ComputeCredit, idempotent bool) {
	idem := "false"
	if idempotent {
		idem = "true"
	}
	m.LedgerSettlements.WithLabelValues(string(rt), idem).Inc()
	if !idempotent && charge > 0 {
		m.CreditsConsumed.Add(float64(charge.Raw()))
	}
}

// ObserveAllocation records an allocation decision.
func (m *CommonsMetrics) ObserveAllocation(pc PriorityClass, accepted bool) {
	result := "accepted"
	if !accepted {
		result = "rejected"
	}
	m.AllocationTotal.WithLabelValues(pc.String(), result).Inc()
}

// ObserveRejection records a rejected allocation with a structured reason.
func (m *CommonsMetrics) ObserveRejection(reason string) {
	m.AllocationRejectedTotal.WithLabelValues(reason).Inc()
}
