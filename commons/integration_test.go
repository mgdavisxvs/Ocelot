package commons

import (
	"testing"
	"time"
)

// TestEndToEndAnnounceLifecycle exercises the full lifecycle:
// declare workload → evaluate budget → determine eligible nodes →
// calculate prices → rank placement → reserve resources (via Settle reserve) →
// meter consumption → settle ledger → verify account balance + audit trail.
func TestEndToEndAnnounceLifecycle(t *testing.T) {
	db := newTestDB(t)

	// ── 1. Declare workload economics ─────────────────────────────────────────
	const (
		leecherID = uint32(100)
		seederID  = uint32(200)
		torrentID = uint32(999)
	)
	we := &WorkloadEconomics{
		AccountUserID: leecherID,
		PriorityClass: P2Standard,
		Preemptible:   true,
		Optimization:  OptMinimizeCost,
		MaxTotalCredits: FromCC(500),
	}
	if err := UpsertWorkloadEconomics(db, torrentID, we); err != nil {
		t.Fatalf("upsert workload economics: %v", err)
	}

	loaded, err := LoadWorkloadEconomics(db, torrentID)
	if err != nil {
		t.Fatalf("load workload economics: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected workload economics, got nil")
	}
	if loaded.PriorityClass != P2Standard {
		t.Errorf("expected P2Standard, got %s", loaded.PriorityClass)
	}

	// ── 2. Evaluate budget feasibility ────────────────────────────────────────
	leecherBal, err := CheckBalance(db, leecherID)
	if err != nil {
		t.Fatalf("check balance: %v", err)
	}
	// Account not yet settled — should return 0 (auto-create on first settle).
	if leecherBal != 0 {
		t.Logf("leecher balance before first settle: %s", leecherBal)
	}

	// ── 3. Determine eligible nodes + calculate prices ────────────────────────
	es := newTestScheduler()
	req := &AllocationRequest{
		LeecherUserID: leecherID,
		TorrentID:     torrentID,
		Economics:     we,
		Seeders:       3,
		Leechers:      2,
		Candidates: []*SeederCandidate{
			{UserID: seederID, ArtifactLocal: true, UptimeSec: 7200, ReputationScore: 3000},
			{UserID: 201, ArtifactLocal: false, UptimeSec: 1800, ReputationScore: 1000},
		},
	}

	dec := es.Evaluate(req, MaxCredit, MaxCredit)
	if !dec.Accepted {
		t.Fatalf("allocation rejected: %s", dec.RejectionReason)
	}
	if len(dec.RankedSeeders) == 0 {
		t.Fatal("expected at least one ranked seeder")
	}
	// Seeder with ArtifactLocal=true must rank first.
	if dec.RankedSeeders[0].UserID != seederID {
		t.Errorf("local seeder (%d) should rank first, got %d", seederID, dec.RankedSeeders[0].UserID)
	}
	if !dec.EstimatedCost.IsPositive() {
		t.Errorf("estimated cost must be positive, got %s", dec.EstimatedCost)
	}

	// ── 4. Meter consumption via AnnounceStats ────────────────────────────────
	const downloadBytes = int64(ResourceGB) * 2 // 2 GB downloaded

	pt := DefaultPriceTable()
	charge := pt.ChargeForDownload(downloadBytes, req.Seeders, req.Leechers)
	if !charge.IsPositive() {
		t.Fatalf("expected positive download charge, got %s", charge)
	}

	uploadCredit := pt.CreditForUpload(downloadBytes/2, req.Seeders, req.Leechers)
	if !uploadCredit.IsPositive() {
		t.Fatalf("expected positive upload credit, got %s", uploadCredit)
	}

	// ── 5. Settle ledger for leecher (download charge) ────────────────────────
	unitPrice := pt.Effective(ResourceDownload, req.Seeders, req.Leechers)
	txnDL := "e2e:dl:100:999:1"
	dlResult, err := Settle(db, leecherID, torrentID, ResourceDownload,
		downloadBytes, unitPrice, charge, ReasonDownload, txnDL)
	if err != nil {
		t.Fatalf("settle download: %v", err)
	}
	if dlResult.WasIdempotent {
		t.Fatal("first settlement should not be idempotent")
	}
	// Account auto-created with DefaultStartingBalance, then charged.
	expectedAfter := DefaultStartingBalance - charge
	if dlResult.BalanceAfter != expectedAfter {
		t.Errorf("expected balance_after %s, got %s", expectedAfter, dlResult.BalanceAfter)
	}

	// ── 6. Settle ledger for seeder (upload credit) ───────────────────────────
	unitPriceUp := pt.Effective(ResourceUpload, req.Seeders, req.Leechers)
	txnUL := "e2e:ul:200:999:1"
	ulResult, err := Settle(db, seederID, torrentID, ResourceUpload,
		downloadBytes/2, unitPriceUp, uploadCredit.Neg(), ReasonUploadCredit, txnUL)
	if err != nil {
		t.Fatalf("settle upload credit: %v", err)
	}
	if ulResult.BalanceAfter <= ulResult.BalanceBefore {
		t.Errorf("seeder credit should increase balance: before=%s after=%s",
			ulResult.BalanceBefore, ulResult.BalanceAfter)
	}

	// ── 7. Idempotent re-settlement (simulate retry) ──────────────────────────
	dlResult2, err := Settle(db, leecherID, torrentID, ResourceDownload,
		downloadBytes, unitPrice, charge, ReasonDownload, txnDL)
	if err != nil {
		t.Fatalf("idempotent re-settle: %v", err)
	}
	if !dlResult2.WasIdempotent {
		t.Fatal("second settle with same txn_id must be idempotent")
	}
	if dlResult.BalanceAfter != dlResult2.BalanceAfter {
		t.Errorf("balance changed on idempotent re-settle: %s → %s",
			dlResult.BalanceAfter, dlResult2.BalanceAfter)
	}

	// ── 8. Verify account balance ─────────────────────────────────────────────
	finalBal, err := CheckBalance(db, leecherID)
	if err != nil {
		t.Fatalf("check final balance: %v", err)
	}
	if finalBal != expectedAfter {
		t.Errorf("final balance mismatch: want %s, got %s", expectedAfter, finalBal)
	}
	if finalBal >= DefaultStartingBalance {
		t.Error("leecher balance should have decreased after download")
	}

	// ── 9. Verify audit trail ─────────────────────────────────────────────────
	entries, err := QueryLedger(db, leecherID, 20)
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	// Exactly 1 entry (second was idempotent, not inserted).
	if len(entries) != 1 {
		t.Errorf("expected 1 ledger entry for leecher, got %d", len(entries))
	}
	if entries[0].TxnID != txnDL {
		t.Errorf("ledger txn_id mismatch: want %q, got %q", txnDL, entries[0].TxnID)
	}
	if entries[0].Charge != charge {
		t.Errorf("ledger charge mismatch: want %s, got %s", charge, entries[0].Charge)
	}
	if entries[0].ResourceType != ResourceDownload {
		t.Errorf("expected ResourceDownload, got %s", entries[0].ResourceType)
	}

	// ── 10. WhyDidWorkloadRunHere explanation ─────────────────────────────────
	cc := &ComputeCommons{prices: pt, metrics: DefaultMetrics}
	why := cc.WhyDidWorkloadRunHere(leecherID, seederID, torrentID, dec)
	if why == "" {
		t.Fatal("why explanation must not be empty")
	}
}

// TestEndToEndBudgetEnforcement verifies that a P3 workload with a tight budget
// is rejected when the estimated price exceeds the per-resource price cap.
func TestEndToEndBudgetEnforcement(t *testing.T) {
	es := newTestScheduler()
	we := &WorkloadEconomics{
		AccountUserID: 300,
		PriorityClass: P3Opportunistic,
		Preemptible:   true,
		Optimization:  OptMinimizeCost,
		// Price cap: 1 CC per download — will be exceeded under scarcity
		MaxResourcePrice: map[ResourceType]ComputeCredit{
			ResourceDownload: FromCC(1),
		},
	}
	req := &AllocationRequest{
		LeecherUserID: 300,
		TorrentID:     888,
		Economics:     we,
		Seeders:       0,  // zero seeders: maximum scarcity
		Leechers:      20, // high contention
		Candidates: []*SeederCandidate{
			{UserID: 400, ArtifactLocal: false, UptimeSec: 3600, ReputationScore: 2000},
		},
	}

	dec := es.Evaluate(req, MaxCredit, MaxCredit)
	if dec.Accepted {
		t.Fatalf("P3 with tight price cap and high scarcity should be rejected; effective price=%s", dec.EffectivePrice)
	}
}

// TestEndToEndP0Constitutional verifies the constitutional guarantee:
// P0 is always accepted regardless of zero balance and zero budget.
func TestEndToEndP0Constitutional(t *testing.T) {
	db := newTestDB(t)
	es := newTestScheduler()

	const (
		criticalUserID = uint32(500)
		criticalTorrent = uint32(777)
	)

	we := &WorkloadEconomics{
		AccountUserID: criticalUserID,
		PriorityClass: P0Critical,
		Preemptible:   false,
		Optimization:  OptMinimizeCompletionTime,
	}
	req := &AllocationRequest{
		LeecherUserID: criticalUserID,
		TorrentID:     criticalTorrent,
		Economics:     we,
		Seeders:       1,
		Leechers:      1,
		Candidates: []*SeederCandidate{
			{UserID: 600, ArtifactLocal: true, UptimeSec: 86400, ReputationScore: 9000, PriorityClass: P1Guaranteed},
		},
	}

	// Zero balance, zero budget — P0 must still be accepted.
	dec := es.Evaluate(req, 0, 0)
	if !dec.Accepted {
		t.Fatalf("P0 CRITICAL must always be accepted; rejection: %s", dec.RejectionReason)
	}

	// Settle with zero charge to auto-create the account.
	_, err := Settle(db, criticalUserID, criticalTorrent, ResourceDownload,
		0, 0, 0, ReasonDownload, "e2e:p0:500:777:1")
	if err != nil {
		t.Fatalf("settle zero: %v", err)
	}

	// Account should exist now.
	acc, err := LoadAccount(db, criticalUserID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if acc == nil {
		t.Fatal("account should have been auto-created")
	}
}

// TestEndToEndReservationExpiry verifies that expired reservations are cleaned up.
func TestEndToEndReservationExpiry(t *testing.T) {
	db := newTestDB(t)

	now := time.Now().Unix()

	// Insert a reservation that expired 10 minutes ago.
	expiredAt := time.Now().Add(-10 * time.Minute).Unix()
	_, err := db.Exec(`
		INSERT INTO commons_reservations
		(id, user_id, torrent_id, resource_type, quantity, credit_hold, expires_at, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
		"res-expired-1", 700, 1, string(ResourceDownload), int64(ResourceGB), int64(FromCC(50)), expiredAt, now,
	)
	if err != nil {
		t.Fatalf("insert reservation: %v", err)
	}

	n, err := ExpireReservations(db)
	if err != nil {
		t.Fatalf("expire reservations: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 expired reservation deleted, got %d", n)
	}

	// Insert a future reservation — should not be deleted.
	futureAt := time.Now().Add(10 * time.Minute).Unix()
	_, err = db.Exec(`
		INSERT INTO commons_reservations
		(id, user_id, torrent_id, resource_type, quantity, credit_hold, expires_at, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
		"res-future-1", 701, 1, string(ResourceDownload), int64(ResourceGB), int64(FromCC(50)), futureAt, now,
	)
	if err != nil {
		t.Fatalf("insert future reservation: %v", err)
	}

	n2, err := ExpireReservations(db)
	if err != nil {
		t.Fatalf("expire reservations 2: %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 deleted for future reservation, got %d", n2)
	}
}

// TestEndToEndConcurrentSettle verifies that concurrent Settle calls
// for the same txn_id produce exactly one ledger entry.
func TestEndToEndConcurrentSettle(t *testing.T) {
	db := newTestDB(t)
	const txnID = "concurrent-idempotent-test"
	const userID = uint32(800)
	const tor = uint32(1)

	charge := FromCC(10)
	unitPrice := FromCC(50)

	results := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() {
			_, err := Settle(db, userID, tor, ResourceDownload,
				int64(ResourceGB), unitPrice, charge, ReasonDownload, txnID)
			results <- err
		}()
	}

	for i := 0; i < 5; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent settle error: %v", err)
		}
	}

	entries, err := QueryLedger(db, userID, 20)
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected exactly 1 ledger entry after 5 concurrent settles with same txn_id, got %d", len(entries))
	}
}
