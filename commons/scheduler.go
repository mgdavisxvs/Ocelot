package commons

import "fmt"

// AllocationRequest describes a peer requesting to download from the swarm.
// The EconomicScheduler validates it and ranks eligible seeders.
type AllocationRequest struct {
	// LeeherUserID is the user requesting data.
	LeecherUserID uint32

	// TorrentID identifies the torrent.
	TorrentID uint32

	// WorkloadEconomics carries the policy fields for this torrent.
	Economics *WorkloadEconomics

	// Candidates is the list of candidate seeders with their scoring inputs.
	Candidates []*SeederCandidate

	// Seeders / Leechers is the current swarm state for scarcity pricing.
	Seeders  int
	Leechers int
}

// SeederCandidate represents one potential seeder for ranking.
type SeederCandidate struct {
	UserID uint32
	// ArtifactLocal is true if the seeder is on the same subnet/region,
	// avoiding cross-network transfer costs.
	ArtifactLocal bool
	// StorageLocal is true if the seeder shares storage locality with the leecher.
	StorageLocal bool
	// UptimeSec is how long this peer has been seeding (proxy for reliability).
	UptimeSec int64
	// ReputationScore is the seeder's Reputation value (higher = more reliable).
	ReputationScore int64
	// ConnectedPeers is how many peers this seeder is already serving (contention proxy).
	ConnectedPeers int
	// PriorityClass is the seeder's account priority (P1 seeders serve P0 leechers first).
	PriorityClass PriorityClass
}

// AllocationDecision is the output of the EconomicScheduler.
type AllocationDecision struct {
	// Accepted is true if the allocation was approved.
	Accepted bool

	// RejectionReason describes why the allocation was refused, if Accepted==false.
	RejectionReason string

	// RankedSeeders contains the Candidates in recommended order, most preferred first.
	RankedSeeders []*SeederCandidate

	// EstimatedCost is the projected CC charge for one announce interval of downloading.
	EstimatedCost ComputeCredit

	// EffectivePrice is the scarcity-adjusted price per GB that will be applied.
	EffectivePrice ComputeCredit

	// AllocationReason is a structured log entry explaining the decision.
	AllocationReason string
}

// EconomicScheduler integrates economic signals into peer selection while
// preserving hard feasibility and policy constraints.
//
// Decision hierarchy (strictly ordered — later stages never override earlier ones):
//  1. Priority policy — P0/P1 cannot be displaced by budget pressure.
//  2. Budget check — P2/P3 leechers must have sufficient CC.
//  3. Economic ranking — sort valid candidates by combined economic score.
type EconomicScheduler struct {
	prices  *PriceTable
	metrics *CommonsMetrics
}

// NewEconomicScheduler creates a scheduler backed by the given price table.
func NewEconomicScheduler(prices *PriceTable, metrics *CommonsMetrics) *EconomicScheduler {
	if metrics == nil {
		metrics = DefaultMetrics
	}
	return &EconomicScheduler{prices: prices, metrics: metrics}
}

// Evaluate performs the full allocation decision for req.
//
// It does NOT access the database directly; balance/budget checks are performed
// by the caller (ComputeCommons.CheckAllocation) which passes results in via req.
func (es *EconomicScheduler) Evaluate(
	req *AllocationRequest,
	leecherBalance, leecherBudget ComputeCredit,
) *AllocationDecision {

	if req.Economics == nil {
		req.Economics = &WorkloadEconomics{
			AccountUserID: req.LeecherUserID,
			PriorityClass: P2Standard,
			Preemptible:   true,
			Optimization:  OptMinimizeCost,
		}
	}

	pc := req.Economics.EffectivePriority()
	effectivePrice := es.prices.EffectiveForDownload(req.Seeders, req.Leechers)

	// Update price metric.
	es.metrics.ObservePrice(ResourceDownload, effectivePrice)

	// ── Stage 1: Priority policy ──────────────────────────────────────────────
	// P0/P1 leechers always proceed; P0/P1 policy on the torrent itself also
	// protects the allocation. Budget can never override this.
	if pc.IsProtected() {
		es.metrics.PriorityEnforcements.Add(0) // just logging
		ranked := es.rankCandidates(req, effectivePrice)
		return &AllocationDecision{
			Accepted:         true,
			RankedSeeders:    ranked,
			EstimatedCost:    0, // protected classes not charged
			EffectivePrice:   effectivePrice,
			AllocationReason: fmt.Sprintf("priority:%s protected allocation", pc),
		}
	}

	// ── Stage 2: Budget enforcement ───────────────────────────────────────────
	// Estimate cost for one announce interval at typical 1 GB/hour transfer.
	// This is a quick check; exact settlement happens in Settle().
	estimatedCost := es.estimateCost(effectivePrice, req.Economics)

	if leecherBalance <= 0 && !pc.IsProtected() {
		es.metrics.ObserveAllocation(pc, false)
		es.metrics.ObserveRejection("insufficient_balance")
		return &AllocationDecision{
			Accepted:         false,
			RejectionReason:  "insufficient_balance",
			EffectivePrice:   effectivePrice,
			AllocationReason: fmt.Sprintf("user %d has zero CC balance", req.LeecherUserID),
		}
	}

	if leecherBudget <= 0 && !pc.IsProtected() {
		es.metrics.ObserveAllocation(pc, false)
		es.metrics.ObserveRejection("budget_exhausted")
		return &AllocationDecision{
			Accepted:         false,
			RejectionReason:  "budget_exhausted",
			EffectivePrice:   effectivePrice,
			AllocationReason: fmt.Sprintf("user %d torrent budget exhausted", req.LeecherUserID),
		}
	}

	// Price cap check: if the leecher has a max_resource_price and the
	// effective price exceeds it, throttle P3 or degrade for P2.
	if req.Economics.MaxResourcePrice != nil {
		cap, hasCap := req.Economics.MaxResourcePrice[ResourceDownload]
		if hasCap && cap > 0 && effectivePrice > cap {
			if pc == P3Opportunistic {
				es.metrics.ObserveAllocation(pc, false)
				es.metrics.ObserveRejection("price_cap_exceeded")
				return &AllocationDecision{
					Accepted:         false,
					RejectionReason:  "price_cap_exceeded",
					EffectivePrice:   effectivePrice,
					AllocationReason: fmt.Sprintf("P3 price cap %s exceeded by %s", cap, effectivePrice),
				}
			}
			// P2: accept but note in reason.
		}
	}

	// ── Stage 3: Economic ranking ─────────────────────────────────────────────
	ranked := es.rankCandidates(req, effectivePrice)

	es.metrics.ObserveAllocation(pc, true)
	return &AllocationDecision{
		Accepted:         true,
		RankedSeeders:    ranked,
		EstimatedCost:    estimatedCost,
		EffectivePrice:   effectivePrice,
		AllocationReason: fmt.Sprintf("priority:%s budget_ok estimated_cost:%s", pc, estimatedCost),
	}
}

// rankCandidates scores and sorts seeders by economic fitness.
//
// Scoring dimensions (all in millis, higher = better):
//  1. Artifact locality: +500 millis if same subnet/region → lower transfer cost
//  2. Storage locality:  +300 millis if co-located storage
//  3. Reliability:       uptime / (uptime + 3600) scaled 0–400 millis
//  4. Reputation:        seeder reputation 0–200 millis
//  5. Contention:        -50 millis per 10 active peers the seeder already serves
//  6. Priority class:    P1 seeders get +200 millis bonus (serve protected leechers first)
func (es *EconomicScheduler) rankCandidates(req *AllocationRequest, _ ComputeCredit) []*SeederCandidate {
	type scored struct {
		c     *SeederCandidate
		score int64
	}
	items := make([]scored, len(req.Candidates))
	for i, c := range req.Candidates {
		s := int64(0)

		// 1. Artifact locality.
		if c.ArtifactLocal {
			s += 500
		}

		// 2. Storage locality.
		if c.StorageLocal {
			s += 300
		}

		// 3. Reliability (uptime).
		uptimeFactor := c.UptimeSec * 400 / (c.UptimeSec + 3600 + 1)
		s += uptimeFactor

		// 4. Reputation (0–200 millis).
		repFactor := c.ReputationScore * 200 / (ReputationMax + 1)
		s += repFactor

		// 5. Contention penalty.
		s -= int64(c.ConnectedPeers/10) * 50

		// 6. Priority bonus for P1 seeders.
		if c.PriorityClass == P1Guaranteed {
			s += 200
		}

		items[i] = scored{c, s}
	}

	// Simple insertion sort — candidate lists are typically small (≤50 peers).
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].score > items[j-1].score; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}

	ranked := make([]*SeederCandidate, len(items))
	for i, it := range items {
		ranked[i] = it.c
	}
	return ranked
}

// estimateCost projects the CC charge for one announce interval.
// Assumes a nominal transfer rate of 10 MB/interval for estimation purposes.
// Actual charge is settled from measured bytes.
func (es *EconomicScheduler) estimateCost(pricePerGB ComputeCredit, we *WorkloadEconomics) ComputeCredit {
	const nominalBytesPerInterval int64 = 10 * 1024 * 1024 // 10 MB
	cost := pricePerGB.MulFrac(nominalBytesPerInterval, ResourceGB)
	if we.MaxTotalCredits > 0 && cost > we.MaxTotalCredits {
		return we.MaxTotalCredits
	}
	return cost
}
