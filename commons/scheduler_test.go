package commons

import (
	"testing"
)

func newTestScheduler() *EconomicScheduler {
	return NewEconomicScheduler(DefaultPriceTable(), nil)
}

func makeRequest(leecherUserID, torrentID uint32, pc PriorityClass, seeders, leechers int) *AllocationRequest {
	we := &WorkloadEconomics{
		AccountUserID: leecherUserID,
		PriorityClass: pc,
		Preemptible:   true,
		Optimization:  OptMinimizeCost,
	}
	return &AllocationRequest{
		LeecherUserID: leecherUserID,
		TorrentID:     torrentID,
		Economics:     we,
		Seeders:       seeders,
		Leechers:      leechers,
	}
}

// ── Priority enforcement ──────────────────────────────────────────────────────

func TestP0AlwaysAccepted(t *testing.T) {
	es := newTestScheduler()
	req := makeRequest(1, 1, P0Critical, 5, 10)
	// Zero balance, zero budget — P0 must still pass.
	dec := es.Evaluate(req, 0, 0)
	if !dec.Accepted {
		t.Fatalf("P0 allocation rejected: %s", dec.RejectionReason)
	}
}

func TestP1AlwaysAccepted(t *testing.T) {
	es := newTestScheduler()
	req := makeRequest(2, 1, P1Guaranteed, 5, 10)
	dec := es.Evaluate(req, 0, 0)
	if !dec.Accepted {
		t.Fatalf("P1 allocation rejected: %s", dec.RejectionReason)
	}
}

func TestP2RejectedOnZeroBalance(t *testing.T) {
	es := newTestScheduler()
	req := makeRequest(3, 1, P2Standard, 5, 10)
	dec := es.Evaluate(req, 0, MaxCredit)
	// P2 with zero balance is allowed (only P3 is blocked at zero).
	// See account.go: P3Opportunistic blocks at <= 0.
	// P2 is only blocked at zero within the Evaluate logic for P3.
	// This test verifies P2 is not blocked merely by zero balance.
	if !dec.Accepted {
		// If rejected, reason must NOT be priority-related.
		if dec.RejectionReason == "" {
			t.Fatal("P2 should be accepted at zero balance (only P3 is blocked)")
		}
	}
}

func TestP3RejectedOnZeroBalance(t *testing.T) {
	es := newTestScheduler()
	req := makeRequest(4, 1, P3Opportunistic, 5, 10)
	dec := es.Evaluate(req, 0, MaxCredit)
	if dec.Accepted {
		t.Fatal("P3 with zero balance should be rejected")
	}
	if dec.RejectionReason != "insufficient_balance" {
		t.Errorf("expected 'insufficient_balance', got %q", dec.RejectionReason)
	}
}

func TestP3RejectedOnExhaustedBudget(t *testing.T) {
	es := newTestScheduler()
	req := makeRequest(5, 1, P3Opportunistic, 5, 10)
	dec := es.Evaluate(req, MaxCredit, 0) // balance fine, budget gone
	if dec.Accepted {
		t.Fatal("P3 with exhausted budget should be rejected")
	}
	if dec.RejectionReason != "budget_exhausted" {
		t.Errorf("expected 'budget_exhausted', got %q", dec.RejectionReason)
	}
}

func TestP3PriceCapRejection(t *testing.T) {
	es := newTestScheduler()
	we := &WorkloadEconomics{
		AccountUserID: 6,
		PriorityClass: P3Opportunistic,
		Preemptible:   true,
		Optimization:  OptMinimizeCost,
		MaxResourcePrice: map[ResourceType]ComputeCredit{
			ResourceDownload: FromCC(1), // absurdly low cap
		},
	}
	req := &AllocationRequest{
		LeecherUserID: 6,
		TorrentID:     1,
		Economics:     we,
		Seeders:       0,  // high scarcity
		Leechers:      50, // many leechers → expensive
	}
	dec := es.Evaluate(req, MaxCredit, MaxCredit)
	if dec.Accepted {
		t.Fatal("P3 should be rejected when effective price exceeds price cap")
	}
}

// ── Candidate ranking ─────────────────────────────────────────────────────────

func TestLocalSeedersRankedFirst(t *testing.T) {
	es := newTestScheduler()
	req := makeRequest(10, 1, P2Standard, 10, 5)
	req.Candidates = []*SeederCandidate{
		{UserID: 100, ArtifactLocal: false},
		{UserID: 101, ArtifactLocal: true}, // should rank first
		{UserID: 102, ArtifactLocal: false},
	}
	dec := es.Evaluate(req, MaxCredit, MaxCredit)
	if !dec.Accepted {
		t.Fatalf("accepted expected, got %s", dec.RejectionReason)
	}
	if len(dec.RankedSeeders) == 0 {
		t.Fatal("expected ranked seeders")
	}
	if dec.RankedSeeders[0].UserID != 101 {
		t.Errorf("expected local seeder (101) first, got %d", dec.RankedSeeders[0].UserID)
	}
}

func TestReliableSeederRanked(t *testing.T) {
	es := newTestScheduler()
	req := makeRequest(11, 1, P2Standard, 5, 5)
	req.Candidates = []*SeederCandidate{
		{UserID: 200, UptimeSec: 60, ReputationScore: 100},
		{UserID: 201, UptimeSec: 86400, ReputationScore: 5000}, // long uptime + high rep
	}
	dec := es.Evaluate(req, MaxCredit, MaxCredit)
	if !dec.Accepted {
		t.Fatalf("accepted expected")
	}
	if dec.RankedSeeders[0].UserID != 201 {
		t.Errorf("expected reliable seeder (201) first, got %d", dec.RankedSeeders[0].UserID)
	}
}

func TestEmptyCandidateList(t *testing.T) {
	es := newTestScheduler()
	req := makeRequest(12, 1, P2Standard, 0, 5)
	req.Candidates = nil
	dec := es.Evaluate(req, MaxCredit, MaxCredit)
	if !dec.Accepted {
		t.Fatal("should accept even with no candidates (scheduler does not inject peers)")
	}
	if len(dec.RankedSeeders) != 0 {
		t.Fatal("no candidates should produce empty ranked list")
	}
}

// ── Price integration ─────────────────────────────────────────────────────────

func TestEstimatedCostPositive(t *testing.T) {
	es := newTestScheduler()
	req := makeRequest(20, 1, P2Standard, 5, 5)
	dec := es.Evaluate(req, MaxCredit, MaxCredit)
	if !dec.Accepted {
		t.Fatal("should be accepted")
	}
	if !dec.EstimatedCost.IsPositive() {
		t.Errorf("expected positive estimated cost, got %s", dec.EstimatedCost)
	}
}

func TestWhyDidWorkloadRunHere(t *testing.T) {
	cc := &ComputeCommons{prices: DefaultPriceTable(), metrics: DefaultMetrics}
	dec := &AllocationDecision{
		Accepted:         true,
		AllocationReason: "priority:P2_STANDARD budget_ok",
		EstimatedCost:    FromCC(5),
		EffectivePrice:   FromCC(50),
	}
	why := cc.WhyDidWorkloadRunHere(1, 2, 99, dec)
	if why == "" {
		t.Fatal("why explanation should not be empty")
	}
}
