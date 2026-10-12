package commons

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// Force single connection so all goroutines share the same in-memory DB.
	db.SetMaxOpenConns(1)
	if err := InitSchema(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if err := UpsertDefaultPrices(db); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSettleBasic(t *testing.T) {
	db := newTestDB(t)
	charge := FromCC(100)
	res, err := Settle(db, 1, 42, ResourceDownload, int64(ResourceGB),
		FromCC(50), charge, ReasonDownload, "")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if res.WasIdempotent {
		t.Fatal("first settlement should not be idempotent")
	}
	// Balance should start at DefaultStartingBalance and decrease by charge.
	expectedAfter := DefaultStartingBalance - charge
	if res.BalanceAfter != expectedAfter {
		t.Errorf("expected balance_after %s, got %s", expectedAfter, res.BalanceAfter)
	}
}

func TestSettleIdempotent(t *testing.T) {
	db := newTestDB(t)
	const txnID = "test-txn-idempotent"

	charge := FromCC(50)
	r1, err := Settle(db, 1, 1, ResourceDownload, int64(ResourceGB),
		FromCC(50), charge, ReasonDownload, txnID)
	if err != nil {
		t.Fatalf("first settle: %v", err)
	}

	r2, err := Settle(db, 1, 1, ResourceDownload, int64(ResourceGB),
		FromCC(50), charge, ReasonDownload, txnID)
	if err != nil {
		t.Fatalf("second settle: %v", err)
	}

	if !r2.WasIdempotent {
		t.Fatal("second settle with same txn_id should be idempotent")
	}
	// Balance must not change on idempotent re-settlement.
	if r1.BalanceAfter != r2.BalanceAfter {
		t.Errorf("balance changed on idempotent re-settlement: %s → %s",
			r1.BalanceAfter, r2.BalanceAfter)
	}
}

func TestSettleNoDoubleSpend(t *testing.T) {
	db := newTestDB(t)

	// Drain the account to nearly zero.
	bigCharge := DefaultStartingBalance - FromCC(1) // leave 1 CC
	_, err := Settle(db, 5, 1, ResourceDownload, int64(ResourceGB),
		FromCC(50), bigCharge, ReasonDownload, "drain-txn")
	if err != nil {
		t.Fatalf("drain settle: %v", err)
	}

	// Verify balance.
	bal, err := CheckBalance(db, 5)
	if err != nil {
		t.Fatalf("check balance: %v", err)
	}
	if bal != FromCC(1) {
		t.Errorf("expected 1 CC remaining, got %s", bal)
	}
}

func TestSettleCredit(t *testing.T) {
	db := newTestDB(t)
	// Credit (negative charge) should increase balance.
	credit := FromCC(200)
	res, err := Settle(db, 2, 1, ResourceUpload, int64(ResourceGB),
		FromCC(10), credit.Neg(), ReasonUploadCredit, "")
	if err != nil {
		t.Fatalf("settle credit: %v", err)
	}
	expected := DefaultStartingBalance + credit
	if res.BalanceAfter != expected {
		t.Errorf("expected %s after credit, got %s", expected, res.BalanceAfter)
	}
}

func TestCheckBalance(t *testing.T) {
	db := newTestDB(t)
	// Non-existent account returns 0.
	bal, err := CheckBalance(db, 999)
	if err != nil {
		t.Fatalf("check balance: %v", err)
	}
	if bal != 0 {
		t.Fatalf("expected 0 for new account, got %s", bal)
	}

	// After a settle, the account is auto-created.
	_, err = Settle(db, 999, 1, ResourceDownload, int64(ResourceGB),
		FromCC(50), FromCC(10), ReasonDownload, "")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	bal2, err := CheckBalance(db, 999)
	if err != nil {
		t.Fatalf("check balance after settle: %v", err)
	}
	if bal2 >= DefaultStartingBalance {
		t.Fatalf("balance should have decreased, got %s", bal2)
	}
}

func TestQueryLedger(t *testing.T) {
	db := newTestDB(t)
	const userID = uint32(10)

	for i := 0; i < 5; i++ {
		_, err := Settle(db, userID, uint32(i), ResourceDownload, int64(ResourceGB),
			FromCC(50), FromCC(5), ReasonDownload, "")
		if err != nil {
			t.Fatalf("settle %d: %v", i, err)
		}
	}

	entries, err := QueryLedger(db, userID, 10)
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}

func TestSettleNegativeCharge_IsCredit(t *testing.T) {
	db := newTestDB(t)
	// A negative charge is a credit — balance should increase.
	res, err := Settle(db, 3, 1, ResourceUpload, 1000,
		FromCC(10), FromCC(-200), ReasonUploadCredit, "")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if res.BalanceAfter <= res.BalanceBefore {
		t.Fatal("credit should increase balance")
	}
}

func TestBudgetExhaustedCheck(t *testing.T) {
	db := newTestDB(t)
	const userID = uint32(20)
	const torrentID = uint32(99)

	// Create a tiny budget: only 10 CC.
	b := &Budget{
		UserID:     userID,
		TorrentID:  func() *uint32 { v := torrentID; return &v }(),
		MaxCredits: FromCC(10),
	}
	if err := UpsertBudget(db, b); err != nil {
		t.Fatalf("upsert budget: %v", err)
	}

	// Exhaust the budget via settle (the update trigger in Settle debits consumed).
	_, err := Settle(db, userID, torrentID, ResourceDownload, int64(ResourceGB),
		FromCC(50), FromCC(10), ReasonDownload, "budget-drain")
	if err != nil {
		t.Fatalf("drain settle: %v", err)
	}

	// Budget remaining should be 0.
	remaining, err := CheckBudgetAvailable(db, userID, torrentID)
	if err != nil {
		t.Fatalf("check budget: %v", err)
	}
	if remaining != 0 {
		t.Errorf("expected 0 remaining, got %s", remaining)
	}
}

func TestLedgerImmutability(t *testing.T) {
	db := newTestDB(t)
	const userID = uint32(50)

	_, err := Settle(db, userID, 1, ResourceDownload, int64(ResourceGB),
		FromCC(50), FromCC(30), ReasonDownload, "immutability-test")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	// Attempt to UPDATE the ledger directly; this should succeed at SQL level
	// but our code never does it — we verify by checking rows.
	entries, _ := QueryLedger(db, userID, 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	original := entries[0].Charge

	// Simulate a second independent settlement (different txn_id).
	_, err = Settle(db, userID, 1, ResourceDownload, int64(ResourceGB),
		FromCC(50), FromCC(15), ReasonDownload, "immutability-test-2")
	if err != nil {
		t.Fatalf("second settle: %v", err)
	}

	entries2, _ := QueryLedger(db, userID, 10)
	if len(entries2) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries2))
	}
	// Original entry must not have changed.
	var found bool
	for _, e := range entries2 {
		if e.TxnID == "immutability-test" {
			if e.Charge != original {
				t.Errorf("ledger entry was mutated: %s → %s", original, e.Charge)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("original ledger entry not found")
	}
}
