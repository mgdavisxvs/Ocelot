package commons

import (
	"testing"
)

// ── SetUserPriority ───────────────────────────────────────────────────────────

func TestSetUserPriority_CreatesAccount(t *testing.T) {
	db := newTestDB(t)
	cc, err := New(db, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := cc.SetUserPriority(99, P1Guaranteed); err != nil {
		t.Fatalf("SetUserPriority: %v", err)
	}

	acct, err := LoadAccount(db, 99)
	if err != nil {
		t.Fatalf("LoadAccount: %v", err)
	}
	if acct == nil {
		t.Fatal("account not created")
	}
	if acct.PriorityClass != P1Guaranteed {
		t.Errorf("PriorityClass = %d, want P1Guaranteed (%d)", acct.PriorityClass, P1Guaranteed)
	}
	if acct.Balance != DefaultStartingBalance {
		t.Errorf("Balance = %v, want DefaultStartingBalance (%v)", acct.Balance, DefaultStartingBalance)
	}
	if acct.Reputation != ReputationDefault {
		t.Errorf("Reputation = %d, want %d", acct.Reputation, ReputationDefault)
	}
}

func TestSetUserPriority_UpdatesExisting(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.SetUserPriority(10, P2Standard); err != nil {
		t.Fatalf("initial: %v", err)
	}
	if err := cc.SetUserPriority(10, P1Guaranteed); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	acct, _ := LoadAccount(db, 10)
	if acct.PriorityClass != P1Guaranteed {
		t.Errorf("PriorityClass = %d, want P1Guaranteed", acct.PriorityClass)
	}
}

func TestSetUserPriority_P0Constitutional(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.SetUserPriority(1, P0Critical); err != nil {
		t.Fatalf("SetUserPriority P0: %v", err)
	}

	acct, _ := LoadAccount(db, 1)
	if acct == nil {
		t.Fatal("account nil")
	}
	if !acct.PriorityClass.IsProtected() {
		t.Error("P0 should be protected")
	}
}

func TestSetUserPriority_InvalidClass(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.SetUserPriority(1, PriorityClass(99)); err == nil {
		t.Error("expected error for invalid priority class")
	}
}

func TestSetUserPriority_P3Opportunistic(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.SetUserPriority(5, P3Opportunistic); err != nil {
		t.Fatalf("SetUserPriority P3: %v", err)
	}
	acct, _ := LoadAccount(db, 5)
	if acct.PriorityClass != P3Opportunistic {
		t.Errorf("PriorityClass = %d, want P3Opportunistic", acct.PriorityClass)
	}
}

// ── SetUserBudget ─────────────────────────────────────────────────────────────

func TestSetUserBudget_PerTorrent_Available(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.SetUserBudget(7, 42, 500); err != nil {
		t.Fatalf("SetUserBudget: %v", err)
	}

	avail, err := CheckBudgetAvailable(db, 7, 42)
	if err != nil {
		t.Fatalf("CheckBudgetAvailable: %v", err)
	}
	if avail <= 0 {
		t.Errorf("budget should be positive, got %v", avail)
	}
}

func TestSetUserBudget_ZeroCredits_Exhausted(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.SetUserBudget(3, 1, 0); err != nil {
		t.Fatalf("SetUserBudget zero: %v", err)
	}

	avail, err := CheckBudgetAvailable(db, 3, 1)
	if err != nil {
		t.Fatalf("CheckBudgetAvailable: %v", err)
	}
	if avail > 0 {
		t.Errorf("zero-credit budget should report 0 available, got %v", avail)
	}
}

func TestSetUserBudget_Replaces(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.SetUserBudget(4, 2, 1000); err != nil {
		t.Fatalf("initial budget: %v", err)
	}

	// Replace with smaller cap
	if err := cc.SetUserBudget(4, 2, 100); err != nil {
		t.Fatalf("replace budget: %v", err)
	}

	avail, err := CheckBudgetAvailable(db, 4, 2)
	if err != nil {
		t.Fatalf("CheckBudgetAvailable: %v", err)
	}
	// Should be 100 CC worth of raw units, not 1000
	want := FromCC(100)
	if avail != want {
		t.Errorf("avail = %v, want %v (100 CC)", avail, want)
	}
}

func TestSetUserBudget_GlobalBudget(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	// torrentID=0 → global budget (NULL in DB)
	if err := cc.SetUserBudget(6, 0, 200); err != nil {
		t.Fatalf("global budget: %v", err)
	}
	// Re-set is idempotent
	if err := cc.SetUserBudget(6, 0, 300); err != nil {
		t.Fatalf("replace global budget: %v", err)
	}

	// Global budget applies to all torrents for this user
	avail, err := CheckBudgetAvailable(db, 6, 999)
	if err != nil {
		t.Fatalf("CheckBudgetAvailable: %v", err)
	}
	want := FromCC(300)
	if avail != want {
		t.Errorf("global avail = %v, want %v (300 CC)", avail, want)
	}
}
