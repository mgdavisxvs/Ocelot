package commons

// Optimization defines the scheduling objective for a workload.
type Optimization int

const (
	// OptMinimizeCost prefers peers that minimise CC expenditure (e.g., seeders
	// with low scarcity multiplier, local peers with cheaper transfer costs).
	OptMinimizeCost Optimization = 0

	// OptMinimizeCompletionTime prefers peers that maximise transfer speed
	// regardless of CC cost, subject to budget limits.
	OptMinimizeCompletionTime Optimization = 1
)

// WorkloadEconomics attaches economic policy to a torrent workload. These
// fields extend the existing Torrent model without replacing it.
//
// YAML equivalent:
//
//	economics:
//	  account: <user_id>
//	  max_total_credits: <CC * 1_000_000>
//	  priority_class: P2_STANDARD
//	  deadline: <unix timestamp>
//	  preemptible: true
//	  max_resource_price:
//	    download: <CC per GB>
//	  optimization: minimize_cost
type WorkloadEconomics struct {
	// AccountUserID is the user account responsible for CC charges on this torrent.
	AccountUserID uint32

	// MaxTotalCredits is the hard CC spending ceiling for this torrent.
	// Zero means no limit (subject only to account balance).
	MaxTotalCredits ComputeCredit

	// PriorityClass determines preemption protection and scheduler preference.
	// Policy always outranks price.
	PriorityClass PriorityClass

	// Deadline is an optional unix timestamp after which the workload is
	// considered expired. Zero = no deadline.
	Deadline int64

	// Preemptible indicates this torrent's seeding can be gracefully stopped
	// to make room for higher-priority workloads. P0/P1 are never preemptible
	// regardless of this field.
	Preemptible bool

	// MaxResourcePrice caps the per-unit CC price the workload will accept.
	// If the effective price exceeds this cap, the workload either degrades
	// (OptMinimizeCost) or accepts the cap violation (OptMinimizeCompletionTime).
	MaxResourcePrice map[ResourceType]ComputeCredit

	// Optimization specifies the scheduling objective.
	Optimization Optimization
}

// DefaultWorkloadEconomics returns conservative defaults for a P2 standard user.
func DefaultWorkloadEconomics(accountUserID uint32) WorkloadEconomics {
	return WorkloadEconomics{
		AccountUserID:   accountUserID,
		MaxTotalCredits: 0, // no explicit limit
		PriorityClass:   P2Standard,
		Preemptible:     true,
		Optimization:    OptMinimizeCost,
		MaxResourcePrice: map[ResourceType]ComputeCredit{
			ResourceDownload: FromCC(200), // won't pay > 200 CC/GB
		},
	}
}

// EffectivePriority returns the actual priority to enforce, ensuring P0/P1
// assets always hold their priority regardless of other settings.
func (we *WorkloadEconomics) EffectivePriority() PriorityClass {
	if !we.PriorityClass.IsValid() {
		return P2Standard
	}
	return we.PriorityClass
}

// WouldAcceptPrice returns true if the effective price for rt is within this
// workload's max resource price cap (or no cap is set).
func (we *WorkloadEconomics) WouldAcceptPrice(rt ResourceType, price ComputeCredit) bool {
	cap, ok := we.MaxResourcePrice[rt]
	if !ok || cap <= 0 {
		return true
	}
	return price <= cap
}
