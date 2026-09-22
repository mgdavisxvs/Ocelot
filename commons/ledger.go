package commons

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// LedgerEntry is one immutable line in the CommonsLedger.
// Charges are positive (CC deducted); credits are negative (CC added).
type LedgerEntry struct {
	ID            int64
	TxnID         string        // UUID — UNIQUE constraint ensures idempotency
	AccountID     uint32        // user being charged/credited
	TorrentID     uint32        // 0 = system-level entry
	ResourceType  ResourceType
	Quantity      int64         // bytes transferred, or byte-seconds
	UnitPrice     ComputeCredit // CC per GB (or per GB-hour)
	Charge        ComputeCredit // positive = cost, negative = credit earned
	BalanceBefore ComputeCredit
	BalanceAfter  ComputeCredit
	AllocReason   string
	CreatedAt     int64 // unix timestamp
}

// AllocReason constants provide structured explanations for ledger entries.
const (
	ReasonDownload         = "download_charge"
	ReasonUploadCredit     = "upload_credit"
	ReasonSeedingCredit    = "seeding_credit"
	ReasonStorageCharge    = "storage_charge"
	ReasonNetworkCharge    = "network_charge"
	ReasonBudgetInitial    = "initial_allocation"
	ReasonBudgetTopup      = "admin_topup"
	ReasonPenaltyHoarding  = "reservation_hoarding"
	ReasonPenaltyAbandoned = "abandoned_reservation"
	ReasonPenaltyOverReq   = "chronic_over_requesting"
	ReasonReputationBonus  = "reputation_bonus"
)

// newTxnID generates a random 16-byte hex string for idempotency keys.
func newTxnID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use timestamp-based ID (should never happen).
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// SettleResult summarises the outcome of a ledger settlement.
type SettleResult struct {
	TxnID         string
	BalanceBefore ComputeCredit
	BalanceAfter  ComputeCredit
	Charge        ComputeCredit
	WasIdempotent bool // true if this TxnID was already settled
}

// ErrNegativeCharge is returned when a settlement would produce a negative
// monetary charge (i.e., a charge on a seeder for seeder activity). Credits
// must be passed with explicit credit semantics, not as negative charges.
var ErrNegativeCharge = errors.New("commons: charge must be >= 0; use credit functions for earned CC")

// Settle records a CC charge (cost > 0) or credit (cost < 0) against accountID
// atomically within db. If txnID already exists in the ledger the operation
// returns successfully with WasIdempotent=true (idempotency guarantee).
//
// Safety invariants enforced here:
//  1. Ledger is append-only; existing entries are never modified.
//  2. The balance update and ledger insert happen in a single transaction.
//  3. The UNIQUE(txn_id) constraint prevents double-settlement at the DB level.
func Settle(db *sql.DB, accountID, torrentID uint32, rt ResourceType,
	quantity int64, unitPrice, charge ComputeCredit, reason, txnID string) (*SettleResult, error) {

	if txnID == "" {
		txnID = newTxnID()
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("commons: settle begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Fetch current balance (create account row if absent).
	var balanceBefore int64
	err = tx.QueryRow(
		`SELECT balance FROM commons_accounts WHERE user_id = ?`, accountID,
	).Scan(&balanceBefore)
	if errors.Is(err, sql.ErrNoRows) {
		// Auto-create account with default starting balance.
		now := time.Now().Unix()
		_, err = tx.Exec(
			`INSERT INTO commons_accounts (user_id, balance, allocated, reserved, reputation, priority_class, created_at, updated_at)
			 VALUES (?, ?, ?, 0, ?, ?, ?, ?)`,
			accountID, DefaultStartingBalance.Raw(), DefaultStartingBalance.Raw(),
			ReputationDefault, int(P2Standard), now, now,
		)
		if err != nil {
			return nil, fmt.Errorf("commons: settle create account: %w", err)
		}
		balanceBefore = DefaultStartingBalance.Raw()
	} else if err != nil {
		return nil, fmt.Errorf("commons: settle fetch balance: %w", err)
	}

	balanceAfter := balanceBefore - int64(charge)

	// Insert ledger entry — ON CONFLICT IGNORE for idempotency.
	res, err := tx.Exec(
		`INSERT OR IGNORE INTO commons_ledger
		 (txn_id, account_id, torrent_id, resource_type, quantity, unit_price, charge,
		  balance_before, balance_after, alloc_reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		txnID, accountID, torrentID, string(rt), quantity,
		int64(unitPrice), int64(charge), balanceBefore, balanceAfter,
		reason, time.Now().Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("commons: settle insert ledger: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("commons: settle rows affected: %w", err)
	}
	if rows == 0 {
		// txn_id already existed — idempotent return.
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commons: settle commit (idempotent): %w", err)
		}
		return &SettleResult{
			TxnID:         txnID,
			BalanceBefore: ComputeCredit(balanceBefore),
			BalanceAfter:  ComputeCredit(balanceBefore), // unchanged
			Charge:        charge,
			WasIdempotent: true,
		}, nil
	}

	// Update balance.
	_, err = tx.Exec(
		`UPDATE commons_accounts SET balance = ?, updated_at = ? WHERE user_id = ?`,
		balanceAfter, time.Now().Unix(), accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("commons: settle update balance: %w", err)
	}

	// Update budget consumed if a budget record exists.
	if int64(charge) > 0 && torrentID > 0 {
		_, _ = tx.Exec(
			`UPDATE commons_budgets SET consumed = consumed + ?
			 WHERE user_id = ? AND torrent_id = ? AND (max_credits - consumed) >= ?`,
			int64(charge), accountID, torrentID, int64(charge),
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commons: settle commit: %w", err)
	}

	return &SettleResult{
		TxnID:         txnID,
		BalanceBefore: ComputeCredit(balanceBefore),
		BalanceAfter:  ComputeCredit(balanceAfter),
		Charge:        charge,
		WasIdempotent: false,
	}, nil
}

// CheckBalance returns the current CC balance for accountID.
// Returns 0 and no error if the account does not exist yet.
func CheckBalance(db *sql.DB, accountID uint32) (ComputeCredit, error) {
	var balance int64
	err := db.QueryRow(
		`SELECT balance FROM commons_accounts WHERE user_id = ?`, accountID,
	).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("commons: check balance: %w", err)
	}
	return ComputeCredit(balance), nil
}

// CheckBudgetAvailable returns the remaining CC in the most restrictive budget
// that applies to (accountID, torrentID). Returns MaxCredit if no budget exists.
func CheckBudgetAvailable(db *sql.DB, accountID, torrentID uint32) (ComputeCredit, error) {
	// Check torrent-specific budget first, then global.
	var remaining sql.NullInt64
	err := db.QueryRow(
		`SELECT MIN(max_credits - consumed) FROM commons_budgets
		 WHERE user_id = ? AND (torrent_id = ? OR torrent_id IS NULL)
		 AND (deadline IS NULL OR deadline > ?)`,
		accountID, torrentID, time.Now().Unix(),
	).Scan(&remaining)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MaxCredit, fmt.Errorf("commons: check budget: %w", err)
	}
	if !remaining.Valid {
		return MaxCredit, nil // no budget record = unlimited
	}
	if remaining.Int64 < 0 {
		return 0, nil
	}
	return ComputeCredit(remaining.Int64), nil
}

// QueryLedger returns recent ledger entries for an account in reverse
// chronological order.
func QueryLedger(db *sql.DB, accountID uint32, limit int) ([]*LedgerEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(
		`SELECT id, txn_id, account_id, torrent_id, resource_type, quantity,
		        unit_price, charge, balance_before, balance_after, alloc_reason, created_at
		 FROM commons_ledger WHERE account_id = ?
		 ORDER BY created_at DESC, id DESC LIMIT ?`,
		accountID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("commons: query ledger: %w", err)
	}
	defer rows.Close()

	var entries []*LedgerEntry
	for rows.Next() {
		e := &LedgerEntry{}
		var rt string
		if err := rows.Scan(
			&e.ID, &e.TxnID, &e.AccountID, &e.TorrentID, &rt,
			&e.Quantity, (*int64)(&e.UnitPrice), (*int64)(&e.Charge),
			(*int64)(&e.BalanceBefore), (*int64)(&e.BalanceAfter),
			&e.AllocReason, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("commons: scan ledger row: %w", err)
		}
		e.ResourceType = ResourceType(rt)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
