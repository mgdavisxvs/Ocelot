package commons

import "fmt"

// PriorityClass defines the allocation priority for a workload or account.
// Policy outranks price: a high budget MUST NEVER allow P2/P3 workloads to
// displace P0/P1 workloads. The hierarchy is constitutional.
type PriorityClass int

const (
	// P0Critical protects essential system-level torrents (OS images, security
	// updates, infrastructure data). Cannot be preempted or budget-blocked.
	P0Critical PriorityClass = 0

	// P1Guaranteed provides SLA-level delivery for premium/staff accounts.
	// Bandwidth is reserved even under scarcity.
	P1Guaranteed PriorityClass = 1

	// P2Standard is the default for regular authenticated users.
	P2Standard PriorityClass = 2

	// P3Opportunistic serves free/trial accounts using only excess capacity.
	// Will be throttled first under scarcity. Downloads may be denied if budget
	// is exhausted.
	P3Opportunistic PriorityClass = 3
)

// String returns the human-readable name of the priority class.
func (pc PriorityClass) String() string {
	switch pc {
	case P0Critical:
		return "P0_CRITICAL"
	case P1Guaranteed:
		return "P1_GUARANTEED"
	case P2Standard:
		return "P2_STANDARD"
	case P3Opportunistic:
		return "P3_OPPORTUNISTIC"
	default:
		return fmt.Sprintf("P?(%d)", int(pc))
	}
}

// IsProtected returns true for P0 and P1 classes that cannot be budget-preempted.
func (pc PriorityClass) IsProtected() bool {
	return pc == P0Critical || pc == P1Guaranteed
}

// IsValid returns true if the priority class is within the defined range.
func (pc PriorityClass) IsValid() bool {
	return pc >= P0Critical && pc <= P3Opportunistic
}

// Outranks returns true if pc has strictly higher priority than other
// (lower numeric value = higher priority).
func (pc PriorityClass) Outranks(other PriorityClass) bool {
	return int(pc) < int(other)
}
