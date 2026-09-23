package commons

import (
	"database/sql"
	"fmt"
	"time"
)

// InitSchema creates all commons tables in the given *sql.DB.
// Safe to call multiple times (CREATE TABLE IF NOT EXISTS).
// Schema follows the same WITHOUT ROWID / covering-index pattern used by
// the tracker's existing schema.
func InitSchema(db *sql.DB) error {
	_, err := db.Exec(`
-- Account balances, budgets, and reputation per user.
CREATE TABLE IF NOT EXISTS commons_accounts (
	user_id       INTEGER PRIMARY KEY,
	balance       INTEGER NOT NULL DEFAULT 0,
	allocated     INTEGER NOT NULL DEFAULT 0,
	reserved      INTEGER NOT NULL DEFAULT 0,
	reputation    INTEGER NOT NULL DEFAULT 1000,
	priority_class INTEGER NOT NULL DEFAULT 2,
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
) WITHOUT ROWID;

-- Per-workload (torrent) spending limits. torrent_id NULL = global limit.
CREATE TABLE IF NOT EXISTS commons_budgets (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id     INTEGER NOT NULL,
	torrent_id  INTEGER,
	max_credits INTEGER NOT NULL,
	consumed    INTEGER NOT NULL DEFAULT 0,
	deadline    INTEGER
);
CREATE INDEX IF NOT EXISTS idx_commons_budgets_user
	ON commons_budgets(user_id, torrent_id);

-- Configurable resource prices with scarcity parameters.
CREATE TABLE IF NOT EXISTS commons_prices (
	resource_type  TEXT PRIMARY KEY,
	base_price     INTEGER NOT NULL,
	min_price      INTEGER NOT NULL,
	max_price      INTEGER NOT NULL,
	scarcity_factor   INTEGER NOT NULL DEFAULT 2000,
	max_scarcity_mult INTEGER NOT NULL DEFAULT 3000,
	updated_at     INTEGER NOT NULL
) WITHOUT ROWID;

-- Metered resource usage per user per torrent.
CREATE TABLE IF NOT EXISTS commons_usage (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id      INTEGER NOT NULL,
	torrent_id   INTEGER,
	resource_type TEXT NOT NULL,
	requested    INTEGER NOT NULL DEFAULT 0,
	reserved     INTEGER NOT NULL DEFAULT 0,
	measured     INTEGER NOT NULL DEFAULT 0,
	start_time   INTEGER NOT NULL,
	end_time     INTEGER,
	settled      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_commons_usage_user
	ON commons_usage(user_id, settled);

-- Immutable ledger of every CC charge and credit. NEVER DELETE ROWS.
CREATE TABLE IF NOT EXISTS commons_ledger (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	txn_id        TEXT    NOT NULL UNIQUE,
	account_id    INTEGER NOT NULL,
	torrent_id    INTEGER NOT NULL DEFAULT 0,
	resource_type TEXT    NOT NULL,
	quantity      INTEGER NOT NULL,
	unit_price    INTEGER NOT NULL,
	charge        INTEGER NOT NULL,
	balance_before INTEGER NOT NULL,
	balance_after  INTEGER NOT NULL,
	alloc_reason  TEXT    NOT NULL,
	created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_commons_ledger_account
	ON commons_ledger(account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_commons_ledger_torrent
	ON commons_ledger(torrent_id, created_at DESC)
	WHERE torrent_id > 0;

-- Active resource reservations (credit holds before settlement).
CREATE TABLE IF NOT EXISTS commons_reservations (
	id            TEXT    PRIMARY KEY,
	user_id       INTEGER NOT NULL,
	torrent_id    INTEGER NOT NULL DEFAULT 0,
	resource_type TEXT    NOT NULL,
	quantity      INTEGER NOT NULL,
	credit_hold   INTEGER NOT NULL,
	expires_at    INTEGER NOT NULL,
	status        TEXT    NOT NULL DEFAULT 'active',
	created_at    INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_commons_reservations_user
	ON commons_reservations(user_id, status);
CREATE INDEX IF NOT EXISTS idx_commons_reservations_expires
	ON commons_reservations(expires_at, status);

-- Economic policy fields per torrent.
CREATE TABLE IF NOT EXISTS commons_workload_economics (
	torrent_id        INTEGER PRIMARY KEY,
	account_user_id   INTEGER NOT NULL,
	max_total_credits INTEGER NOT NULL DEFAULT 0,
	priority_class    INTEGER NOT NULL DEFAULT 2,
	deadline          INTEGER,
	preemptible       INTEGER NOT NULL DEFAULT 1,
	optimization      INTEGER NOT NULL DEFAULT 0,
	created_at        INTEGER NOT NULL,
	updated_at        INTEGER NOT NULL
) WITHOUT ROWID;
`)
	if err != nil {
		return fmt.Errorf("commons: init schema: %w", err)
	}
	return nil
}

// UpsertDefaultPrices inserts the factory-default prices if no price rows exist.
func UpsertDefaultPrices(db *sql.DB) error {
	defaults := DefaultPriceTable()
	now := time.Now().Unix()
	for rt, rp := range defaults.Prices {
		_, err := db.Exec(
			`INSERT INTO commons_prices
			 (resource_type, base_price, min_price, max_price, scarcity_factor, max_scarcity_mult, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(resource_type) DO NOTHING`,
			string(rt), rp.Base.Raw(), rp.Min.Raw(), rp.Max.Raw(),
			defaults.ScarcityFactor, defaults.MaxScarcityMultiplier, now,
		)
		if err != nil {
			return fmt.Errorf("commons: upsert default price %s: %w", rt, err)
		}
	}
	return nil
}

// LoadPriceTable reads all price rows from the database into a PriceTable.
func LoadPriceTable(db *sql.DB) (*PriceTable, error) {
	rows, err := db.Query(
		`SELECT resource_type, base_price, min_price, max_price,
		        scarcity_factor, max_scarcity_mult
		 FROM commons_prices`,
	)
	if err != nil {
		return nil, fmt.Errorf("commons: load price table: %w", err)
	}
	defer rows.Close()

	pt := &PriceTable{
		Prices:                make(map[ResourceType]*ResourcePrice),
		ScarcityFactor:        2000,
		MaxScarcityMultiplier: 3000,
	}
	for rows.Next() {
		var rtStr string
		var base, min, max, sf, msm int64
		if err := rows.Scan(&rtStr, &base, &min, &max, &sf, &msm); err != nil {
			return nil, fmt.Errorf("commons: scan price row: %w", err)
		}
		pt.Prices[ResourceType(rtStr)] = &ResourcePrice{
			Base: ComputeCredit(base),
			Min:  ComputeCredit(min),
			Max:  ComputeCredit(max),
		}
		// Use last row's scarcity params (they're global, not per-resource).
		pt.ScarcityFactor = sf
		pt.MaxScarcityMultiplier = msm
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fill any missing resources with defaults.
	defaults := DefaultPriceTable()
	for rt, rp := range defaults.Prices {
		if _, ok := pt.Prices[rt]; !ok {
			pt.Prices[rt] = rp
		}
	}
	return pt, nil
}

// UpsertAccount creates or updates an account row.
func UpsertAccount(db *sql.DB, a *Account) error {
	_, err := db.Exec(
		`INSERT INTO commons_accounts
		 (user_id, balance, allocated, reserved, reputation, priority_class, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   balance        = excluded.balance,
		   allocated      = excluded.allocated,
		   reserved       = excluded.reserved,
		   reputation     = excluded.reputation,
		   priority_class = excluded.priority_class,
		   updated_at     = excluded.updated_at`,
		a.UserID, a.Balance.Raw(), a.Allocated.Raw(), a.Reserved.Raw(),
		a.Reputation, int(a.PriorityClass), a.CreatedAt, a.UpdatedAt,
	)
	return err
}

// LoadAccount fetches the account for userID. Returns nil if not found.
func LoadAccount(db *sql.DB, userID uint32) (*Account, error) {
	a := &Account{UserID: userID}
	var balance, allocated, reserved int64
	var pc int
	err := db.QueryRow(
		`SELECT balance, allocated, reserved, reputation, priority_class, created_at, updated_at
		 FROM commons_accounts WHERE user_id = ?`, userID,
	).Scan(&balance, &allocated, &reserved, &a.Reputation, &pc, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("commons: load account: %w", err)
	}
	a.Balance = ComputeCredit(balance)
	a.Allocated = ComputeCredit(allocated)
	a.Reserved = ComputeCredit(reserved)
	a.PriorityClass = PriorityClass(pc)
	return a, nil
}

// UpsertBudget creates or updates a budget row.
func UpsertBudget(db *sql.DB, b *Budget) error {
	var deadlineVal interface{}
	if b.Deadline != nil {
		deadlineVal = *b.Deadline
	}
	var torrentIDVal interface{}
	if b.TorrentID != nil {
		torrentIDVal = *b.TorrentID
	}
	_, err := db.Exec(
		`INSERT INTO commons_budgets (user_id, torrent_id, max_credits, consumed, deadline)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   max_credits = excluded.max_credits,
		   consumed    = excluded.consumed,
		   deadline    = excluded.deadline`,
		b.UserID, torrentIDVal, b.MaxCredits.Raw(), b.Consumed.Raw(), deadlineVal,
	)
	return err
}

// UpsertWorkloadEconomics persists WorkloadEconomics for a torrent.
func UpsertWorkloadEconomics(db *sql.DB, torrentID uint32, we *WorkloadEconomics) error {
	now := time.Now().Unix()
	var deadlineVal interface{}
	if we.Deadline > 0 {
		deadlineVal = we.Deadline
	}
	_, err := db.Exec(
		`INSERT INTO commons_workload_economics
		 (torrent_id, account_user_id, max_total_credits, priority_class, deadline, preemptible, optimization, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(torrent_id) DO UPDATE SET
		   account_user_id   = excluded.account_user_id,
		   max_total_credits = excluded.max_total_credits,
		   priority_class    = excluded.priority_class,
		   deadline          = excluded.deadline,
		   preemptible       = excluded.preemptible,
		   optimization      = excluded.optimization,
		   updated_at        = excluded.updated_at`,
		torrentID, we.AccountUserID, we.MaxTotalCredits.Raw(), int(we.PriorityClass),
		deadlineVal, we.Preemptible, int(we.Optimization), now, now,
	)
	return err
}

// LoadWorkloadEconomics fetches WorkloadEconomics for torrentID. Returns nil if absent.
func LoadWorkloadEconomics(db *sql.DB, torrentID uint32) (*WorkloadEconomics, error) {
	we := &WorkloadEconomics{MaxResourcePrice: make(map[ResourceType]ComputeCredit)}
	var maxCredits int64
	var pc, opt int
	var preemptible bool
	var deadline sql.NullInt64
	err := db.QueryRow(
		`SELECT account_user_id, max_total_credits, priority_class, deadline, preemptible, optimization
		 FROM commons_workload_economics WHERE torrent_id = ?`, torrentID,
	).Scan(&we.AccountUserID, &maxCredits, &pc, &deadline, &preemptible, &opt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("commons: load workload economics: %w", err)
	}
	we.MaxTotalCredits = ComputeCredit(maxCredits)
	we.PriorityClass = PriorityClass(pc)
	if deadline.Valid {
		we.Deadline = deadline.Int64
	}
	we.Preemptible = preemptible
	we.Optimization = Optimization(opt)
	return we, nil
}

// ExpireReservations marks expired reservations as 'expired' and releases
// any credit holds back to the account balance.
func ExpireReservations(db *sql.DB) (int64, error) {
	now := time.Now().Unix()

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("commons: expire reservations begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Find all active expired reservations.
	rows, err := tx.Query(
		`SELECT id, user_id, credit_hold FROM commons_reservations
		 WHERE status = 'active' AND expires_at <= ?`, now,
	)
	if err != nil {
		return 0, fmt.Errorf("commons: expire query: %w", err)
	}

	type expiredRow struct {
		id         string
		userID     uint32
		creditHold int64
	}
	var expired []expiredRow
	for rows.Next() {
		var r expiredRow
		if err := rows.Scan(&r.id, &r.userID, &r.creditHold); err != nil {
			rows.Close()
			return 0, fmt.Errorf("commons: expire scan: %w", err)
		}
		expired = append(expired, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, r := range expired {
		_, err := tx.Exec(
			`UPDATE commons_reservations SET status = 'expired' WHERE id = ?`, r.id)
		if err != nil {
			return 0, fmt.Errorf("commons: expire update reservation: %w", err)
		}
		// Release the credit hold back to the balance.
		_, err = tx.Exec(
			`UPDATE commons_accounts
			 SET reserved = MAX(0, reserved - ?), updated_at = ?
			 WHERE user_id = ?`,
			r.creditHold, now, r.userID,
		)
		if err != nil {
			return 0, fmt.Errorf("commons: expire release hold: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commons: expire commit: %w", err)
	}
	return int64(len(expired)), nil
}
