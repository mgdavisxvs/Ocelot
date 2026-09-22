package commons

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ComputeCommons is the economic control plane for Ocelot. It sits between
// the tracker's peer-selection logic and the raw database, orchestrating:
//   - Resource metering (from announce deltas)
//   - Ledger settlement (idempotent CC charges and credits)
//   - Budget enforcement (P2/P3 only; P0/P1 are policy-protected)
//   - Economic peer ranking (via EconomicScheduler)
//   - Reservation management and expiry
//
// The Commons must not become a second scheduler. It provides economic
// signals to the tracker scheduler; the tracker's own feasibility logic
// (whitelist, IP validation, peer state) is never overridden here.
type ComputeCommons struct {
	db        *sql.DB
	prices    *PriceTable
	scheduler *EconomicScheduler
	metrics   *CommonsMetrics
	logger    *slog.Logger
	mu        sync.RWMutex // protects prices (rarely written)
}

// New creates and initialises a ComputeCommons backed by the given *sql.DB.
// Call InitSchema(db) before New to ensure tables exist.
func New(db *sql.DB, logger *slog.Logger) (*ComputeCommons, error) {
	if logger == nil {
		logger = slog.Default()
	}

	pt, err := LoadPriceTable(db)
	if err != nil {
		return nil, fmt.Errorf("commons: load price table: %w", err)
	}

	cc := &ComputeCommons{
		db:      db,
		prices:  pt,
		metrics: DefaultMetrics,
		logger:  logger,
	}
	cc.scheduler = NewEconomicScheduler(pt, cc.metrics)
	return cc, nil
}

// SettleAnnounce is called on each announce where bandwidth stats changed.
// It records CC charges (downloads) and credits (uploads) in the ledger.
//
// stats.EffectiveUploaded > 0 → seeder earns CC (negative charge = credit).
// stats.EffectiveDownloaded > 0 → leecher pays CC (positive charge).
//
// Settlement is idempotent: if the same (user, torrent, timestamps) combination
// is settled twice, the second call is a no-op via UNIQUE(txn_id).
func (c *ComputeCommons) SettleAnnounce(stats *AnnounceStats) error {
	c.mu.RLock()
	pt := c.prices
	c.mu.RUnlock()

	// ── Upload credit (seeder earns CC) ───────────────────────────────────────
	if stats.EffectiveUploaded > 0 {
		credit := pt.CreditForUpload(stats.EffectiveUploaded, stats.Seeders, stats.Leechers)
		if credit > 0 {
			txnID := announceTxnID(stats.UserID, stats.TorrentID, stats.Timestamp.UnixNano(), "up")
			res, err := Settle(c.db, stats.UserID, stats.TorrentID,
				ResourceUpload, stats.EffectiveUploaded,
				pt.EffectiveForUpload(stats.Seeders, stats.Leechers),
				credit.Neg(), // negative charge = credit earned
				ReasonUploadCredit, txnID)
			if err != nil {
				return fmt.Errorf("commons: settle upload credit: %w", err)
			}
			c.metrics.ObserveSettlement(ResourceUpload, credit.Neg(), res.WasIdempotent)
			c.logger.Debug("upload credit settled",
				"user_id", stats.UserID, "torrent_id", stats.TorrentID,
				"bytes", stats.EffectiveUploaded, "credit", credit)
		}
	}

	// ── Download charge (leecher pays CC) ─────────────────────────────────────
	if stats.EffectiveDownloaded > 0 {
		charge := pt.ChargeForDownload(stats.EffectiveDownloaded, stats.Seeders, stats.Leechers)
		if charge > 0 {
			txnID := announceTxnID(stats.UserID, stats.TorrentID, stats.Timestamp.UnixNano(), "dl")
			res, err := Settle(c.db, stats.UserID, stats.TorrentID,
				ResourceDownload, stats.EffectiveDownloaded,
				pt.EffectiveForDownload(stats.Seeders, stats.Leechers),
				charge,
				ReasonDownload, txnID)
			if err != nil {
				return fmt.Errorf("commons: settle download charge: %w", err)
			}
			c.metrics.ObserveSettlement(ResourceDownload, charge, res.WasIdempotent)
			c.logger.Debug("download charge settled",
				"user_id", stats.UserID, "torrent_id", stats.TorrentID,
				"bytes", stats.EffectiveDownloaded, "charge", charge)
		}
	}

	return nil
}

// CheckAllocation validates that the leecher can afford to download from the
// swarm right now. Returns nil if allowed; returns ErrBudgetExhausted or
// ErrInsufficientBalance if blocked.
//
// P0/P1 accounts are never blocked — they bypass budget checks.
func (c *ComputeCommons) CheckAllocation(userID, torrentID uint32, pc PriorityClass) error {
	if pc.IsProtected() {
		return nil // P0/P1 always allowed
	}

	balance, err := CheckBalance(c.db, userID)
	if err != nil {
		// DB error — fail open (don't block the announce) but log it.
		c.logger.Warn("commons: check balance error (fail-open)", "user_id", userID, "err", err)
		return nil
	}

	if balance <= 0 && pc == P3Opportunistic {
		c.metrics.ObserveRejection("insufficient_balance")
		return &ErrInsufficientBalance{UserID: userID, Required: FromCC(1), Available: balance}
	}

	budget, err := CheckBudgetAvailable(c.db, userID, torrentID)
	if err != nil {
		c.logger.Warn("commons: check budget error (fail-open)", "user_id", userID, "err", err)
		return nil
	}
	if budget <= 0 {
		c.metrics.ObserveRejection("budget_exhausted")
		return &ErrBudgetExhausted{UserID: userID, TorrentID: &torrentID,
			Requested: FromCC(1), Remaining: 0}
	}

	return nil
}

// EvaluatePeers performs economic ranking of seeder candidates for a leecher.
// Always returns a valid decision; the caller decides whether to apply it.
func (c *ComputeCommons) EvaluatePeers(req *AllocationRequest) *AllocationDecision {
	balance, err := CheckBalance(c.db, req.LeecherUserID)
	if err != nil {
		c.logger.Warn("commons: evaluate peers balance error", "err", err)
		balance = MaxCredit // fail-open
	}

	budget, err := CheckBudgetAvailable(c.db, req.LeecherUserID, req.TorrentID)
	if err != nil {
		c.logger.Warn("commons: evaluate peers budget error", "err", err)
		budget = MaxCredit // fail-open
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.scheduler.Evaluate(req, balance, budget)
}

// ReloadPrices refreshes the price table from the database.
func (c *ComputeCommons) ReloadPrices() error {
	pt, err := LoadPriceTable(c.db)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.prices = pt
	c.scheduler = NewEconomicScheduler(pt, c.metrics)
	c.mu.Unlock()
	return nil
}

// ExpireOldReservations runs the reservation expiry pass and returns the count.
// Intended to be called periodically (e.g., every announce interval) from
// the tracker's existing Scheduler.
func (c *ComputeCommons) ExpireOldReservations() (int64, error) {
	n, err := ExpireReservations(c.db)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		c.metrics.ReservationExpiries.Add(float64(n))
		c.logger.Info("commons: expired reservations", "count", n)
	}
	return n, nil
}

// AccountSummary returns a human-readable summary for a user account.
func (c *ComputeCommons) AccountSummary(userID uint32) (string, error) {
	a, err := LoadAccount(c.db, userID)
	if err != nil {
		return "", err
	}
	if a == nil {
		return fmt.Sprintf("user %d: no account (will be auto-created on first announce)", userID), nil
	}
	entries, err := QueryLedger(c.db, userID, 5)
	if err != nil {
		return "", err
	}
	s := fmt.Sprintf(
		"user %d | balance %s | priority %s | reputation %d | recent txns: %d",
		userID, a.Balance, a.PriorityClass, a.Reputation, len(entries),
	)
	return s, nil
}

// announceTxnID generates a deterministic (user, torrent, nanos, direction) key
// for idempotent settlement within one announce epoch.
func announceTxnID(userID, torrentID uint32, nanos int64, direction string) string {
	return fmt.Sprintf("announce:%d:%d:%d:%s", userID, torrentID, nanos/int64(time.Second), direction)
}

// WhyDidWorkloadRunHere returns a structured explanation for an allocation
// decision. This answers the observability requirement:
// "Why did workload X run on node Y, and what did it cost?"
func (c *ComputeCommons) WhyDidWorkloadRunHere(
	leecherUserID, seederUserID, torrentID uint32,
	decision *AllocationDecision,
) string {
	if decision == nil {
		return fmt.Sprintf("no allocation decision recorded for user %d on torrent %d",
			leecherUserID, torrentID)
	}
	accepted := "REJECTED"
	if decision.Accepted {
		accepted = "ACCEPTED"
	}
	return fmt.Sprintf(
		"allocation %s | leecher %d → seeder %d | torrent %d | "+
			"reason: %s | estimated_cost: %s | effective_price: %s/GB | rejection: %s",
		accepted, leecherUserID, seederUserID, torrentID,
		decision.AllocationReason,
		decision.EstimatedCost,
		decision.EffectivePrice,
		decision.RejectionReason,
	)
}
