package commons

import "fmt"

// Account holds the economic state for one tracker user.
//
// Three distinct credit dimensions prevent conflating allocation authority with
// budget:
//   - Balance:    spendable CC (deducted on consume, credited on contribute)
//   - Allocated:  lifetime CC granted by the system (allocation authority)
//   - Reserved:   CC currently held against pending operations (held but not
//     yet settled)
//
// The invariant Balance >= 0 must hold for non-protected accounts.
type Account struct {
	UserID        uint32
	Balance       ComputeCredit // current spendable CC
	Allocated     ComputeCredit // lifetime total CC allocated
	Reserved      ComputeCredit // CC held for pending operations
	Reputation    int64         // 0–10000; higher = better ratio behaviour
	PriorityClass PriorityClass
	CreatedAt     int64 // unix timestamp
	UpdatedAt     int64 // unix timestamp
}

// Available returns the CC available for new spending (Balance - Reserved).
func (a *Account) Available() ComputeCredit {
	avail := a.Balance - a.Reserved
	if avail < 0 {
		return 0
	}
	return avail
}

// CanAfford returns true if the account can cover cost from available CC,
// or if the account has a protected priority class that bypasses budget limits.
func (a *Account) CanAfford(cost ComputeCredit) bool {
	if a.PriorityClass.IsProtected() {
		return true
	}
	return a.Available() >= cost
}

// Budget represents the spending limit for a user on a specific torrent,
// or globally (TorrentID == nil).
type Budget struct {
	ID         int64
	UserID     uint32
	TorrentID  *uint32       // nil = global budget
	MaxCredits ComputeCredit // hard spending ceiling
	Consumed   ComputeCredit // CC charged so far
	Deadline   *int64        // optional unix expiry timestamp
}

// Remaining returns the CC still available in this budget.
func (b *Budget) Remaining() ComputeCredit {
	remaining := b.MaxCredits - b.Consumed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// IsExhausted returns true when no more CC can be charged against this budget.
func (b *Budget) IsExhausted() bool {
	return b.Consumed >= b.MaxCredits
}

// CanAccept returns true if the budget can absorb an additional charge of cost.
func (b *Budget) CanAccept(cost ComputeCredit) bool {
	return b.Consumed+cost <= b.MaxCredits
}

// ErrBudgetExhausted is returned when a charge would exceed the budget ceiling.
type ErrBudgetExhausted struct {
	UserID    uint32
	TorrentID *uint32
	Requested ComputeCredit
	Remaining ComputeCredit
}

func (e *ErrBudgetExhausted) Error() string {
	scope := "global"
	if e.TorrentID != nil {
		scope = fmt.Sprintf("torrent %d", *e.TorrentID)
	}
	return fmt.Sprintf("commons: budget exhausted for user %d (%s): requested %s, remaining %s",
		e.UserID, scope, e.Requested, e.Remaining)
}

// ErrInsufficientBalance is returned when an account cannot cover a charge.
type ErrInsufficientBalance struct {
	UserID    uint32
	Required  ComputeCredit
	Available ComputeCredit
}

func (e *ErrInsufficientBalance) Error() string {
	return fmt.Sprintf("commons: insufficient balance for user %d: required %s, available %s",
		e.UserID, e.Required, e.Available)
}

// Reputation constants.
const (
	ReputationDefault   int64 = 5000  // fresh account (midpoint: neutral pricing)
	ReputationMax       int64 = 10000
	ReputationMin       int64 = 0
	ReputationBonusStep int64 = 50  // earned per announce interval of good behaviour
	ReputationPenalty   int64 = 200 // lost per violation
)

// ReputationDiscount returns a pricing multiplier (millis) based on reputation.
// Linear mapping: 0 → 1200 (20% surcharge), 5000 → 1000 (neutral), 10000 → 800 (20% discount).
func ReputationDiscount(reputation int64) int64 {
	if reputation < 0 {
		reputation = 0
	}
	if reputation > ReputationMax {
		reputation = ReputationMax
	}
	// 1200 - rep * 400 / 10000
	return 1200 - reputation*400/ReputationMax
}
