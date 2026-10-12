package commons

import (
	"testing"
	"time"
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

// ── SettleAnnounce ────────────────────────────────────────────────────────────

func TestSettleAnnounce_UploadCredit(t *testing.T) {
	db := newTestDB(t)
	cc, err := New(db, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Give user an account first so we can read the balance change.
	if err := cc.SetUserPriority(55, P2Standard); err != nil {
		t.Fatalf("SetUserPriority: %v", err)
	}
	before, _ := CheckBalance(db, 55)

	stats := &AnnounceStats{
		UserID:            55,
		TorrentID:         10,
		EffectiveUploaded: int64(ResourceGB), // 1 GB uploaded
		Seeders:           1,
		Leechers:          5,
		Timestamp:         time.Now(),
	}
	if err := cc.SettleAnnounce(stats); err != nil {
		t.Fatalf("SettleAnnounce upload: %v", err)
	}

	after, _ := CheckBalance(db, 55)
	if after <= before {
		t.Errorf("balance should increase after upload credit: before=%s after=%s", before, after)
	}
}

func TestSettleAnnounce_DownloadCharge(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)
	if err := cc.SetUserPriority(66, P2Standard); err != nil {
		t.Fatalf("SetUserPriority: %v", err)
	}
	before, _ := CheckBalance(db, 66)

	stats := &AnnounceStats{
		UserID:              66,
		TorrentID:           20,
		EffectiveDownloaded: int64(ResourceGB),
		Seeders:             5,
		Leechers:            1,
		Timestamp:           time.Now(),
	}
	if err := cc.SettleAnnounce(stats); err != nil {
		t.Fatalf("SettleAnnounce download: %v", err)
	}

	after, _ := CheckBalance(db, 66)
	if after >= before {
		t.Errorf("balance should decrease after download charge: before=%s after=%s", before, after)
	}
}

func TestSettleAnnounce_Idempotent(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)
	ts := time.Now()
	stats := &AnnounceStats{
		UserID:              77,
		TorrentID:           30,
		EffectiveDownloaded: int64(ResourceGB),
		Seeders:             3,
		Leechers:            2,
		Timestamp:           ts,
	}
	if err := cc.SettleAnnounce(stats); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	b1, _ := CheckBalance(db, 77)

	// Same stats, same timestamp → idempotent (no double-charge)
	if err := cc.SettleAnnounce(stats); err != nil {
		t.Fatalf("second settle: %v", err)
	}
	b2, _ := CheckBalance(db, 77)
	if b1 != b2 {
		t.Errorf("balance changed on second settle: %s → %s", b1, b2)
	}
}

// ── EvaluatePeers ─────────────────────────────────────────────────────────────

func TestEvaluatePeers_ReturnsDecision(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	// Create account with DefaultStartingBalance so balance check passes.
	if err := cc.SetUserPriority(1, P2Standard); err != nil {
		t.Fatalf("SetUserPriority: %v", err)
	}

	req := &AllocationRequest{
		LeecherUserID: 1,
		TorrentID:     5,
		Candidates: []*SeederCandidate{
			{UserID: 100, UptimeSec: 3600, ReputationScore: ReputationDefault, PriorityClass: P2Standard},
			{UserID: 101, UptimeSec: 7200, ReputationScore: ReputationDefault, PriorityClass: P1Guaranteed},
		},
		Seeders:  2,
		Leechers: 1,
	}
	decision := cc.EvaluatePeers(req)
	if decision == nil {
		t.Fatal("EvaluatePeers returned nil")
	}
	if !decision.Accepted {
		t.Error("decision should be accepted (user has balance from DefaultStartingBalance)")
	}
	if len(decision.RankedSeeders) == 0 {
		t.Error("expected ranked seeders in decision")
	}
}

func TestEvaluatePeers_ExhaustedBalance_P3(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	// Create a P3 account with zero balance by draining it.
	// Settle a huge charge to exhaust balance.
	bigCharge := DefaultStartingBalance + FromCC(1)
	_, _ = Settle(db, 200, 1, ResourceDownload, int64(ResourceGB),
		FromCC(100), bigCharge, ReasonDownload, "exhaust-test")

	req := &AllocationRequest{
		LeecherUserID: 200,
		TorrentID:     1,
		Candidates: []*SeederCandidate{
			{UserID: 300, UptimeSec: 100, ReputationScore: ReputationDefault, PriorityClass: P3Opportunistic},
		},
		Seeders:  1,
		Leechers: 1,
	}
	decision := cc.EvaluatePeers(req)
	if decision == nil {
		t.Fatal("EvaluatePeers returned nil")
	}
	// Decision is always returned (scheduler decides whether to accept).
}

// ── CheckAllocation ───────────────────────────────────────────────────────────

func TestCheckAllocation_P0NeverBlocked(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	// P0 bypasses all checks even with no account
	if err := cc.CheckAllocation(999, 1, P0Critical); err != nil {
		t.Errorf("P0 should never be blocked, got: %v", err)
	}
}

func TestCheckAllocation_P1NeverBlocked(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.CheckAllocation(998, 1, P1Guaranteed); err != nil {
		t.Errorf("P1 should never be blocked, got: %v", err)
	}
}

func TestCheckAllocation_P2_SufficientBalance(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	// Fresh account → DefaultStartingBalance → P2 should be allowed
	if err := cc.CheckAllocation(50, 1, P2Standard); err != nil {
		t.Errorf("P2 with default balance should be allowed, got: %v", err)
	}
}

func TestCheckAllocation_BudgetExhausted(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	// Set a zero budget for torrent 9
	if err := cc.SetUserBudget(60, 9, 0); err != nil {
		t.Fatalf("SetUserBudget: %v", err)
	}
	if err := cc.SetUserPriority(60, P2Standard); err != nil {
		t.Fatalf("SetUserPriority: %v", err)
	}

	err := cc.CheckAllocation(60, 9, P2Standard)
	if err == nil {
		t.Error("expected ErrBudgetExhausted for zero budget")
	}
}

// ── ReloadPrices ──────────────────────────────────────────────────────────────

func TestReloadPrices_Succeeds(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.ReloadPrices(); err != nil {
		t.Errorf("ReloadPrices: %v", err)
	}
}

// ── ExpireOldReservations ─────────────────────────────────────────────────────

func TestExpireOldReservations_Empty(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	n, err := cc.ExpireOldReservations()
	if err != nil {
		t.Errorf("ExpireOldReservations: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 expired on empty DB, got %d", n)
	}
}

// ── AccountSummary ────────────────────────────────────────────────────────────

func TestAccountSummary_NoAccount(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	s, err := cc.AccountSummary(12345)
	if err != nil {
		t.Fatalf("AccountSummary no-account: %v", err)
	}
	if s == "" {
		t.Error("expected non-empty summary for missing account")
	}
}

func TestAccountSummary_ExistingAccount(t *testing.T) {
	db := newTestDB(t)
	cc, _ := New(db, nil)

	if err := cc.SetUserPriority(88, P2Standard); err != nil {
		t.Fatalf("SetUserPriority: %v", err)
	}

	s, err := cc.AccountSummary(88)
	if err != nil {
		t.Fatalf("AccountSummary: %v", err)
	}
	if s == "" {
		t.Error("expected non-empty summary")
	}
}

// ── SetUserBudget_GlobalBudget (unchanged below) ──────────────────────────────

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
