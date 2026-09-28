package tracker

import (
	"database/sql"
	"errors"
	"time"
)

// EnsureDataQuotaTable creates the data_quotas table if absent.
func EnsureDataQuotaTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS data_quotas (
        user_id      INTEGER PRIMARY KEY,
        bytes_used   INTEGER NOT NULL DEFAULT 0,
        limit_bytes  INTEGER NOT NULL DEFAULT 0,
        period_start INTEGER NOT NULL
    )`)
	return err
}

// applyDataQuota checks and records a download delta against the user's quota.
// Returns an error if the quota would be exceeded.
func applyDataQuota(db *sql.DB, userID UserID, downloadDelta int64) error {
	if db == nil || downloadDelta <= 0 {
		return nil
	}
	var bytesUsed, limitBytes int64
	err := db.QueryRow(
		`SELECT bytes_used, limit_bytes FROM data_quotas WHERE user_id=?`, userID,
	).Scan(&bytesUsed, &limitBytes)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return nil // quota check failure is non-fatal
	}
	if limitBytes > 0 && bytesUsed+downloadDelta > limitBytes {
		return errors.New("data quota exceeded")
	}
	db.Exec(
		`UPDATE data_quotas SET bytes_used=bytes_used+? WHERE user_id=?`,
		downloadDelta, userID,
	)
	return nil
}

// resetDailyDataQuotas zeroes bytes_used for quotas whose period is older than 24h.
// Called by Scheduler on each tick.
func ResetDailyDataQuotas(db *sql.DB) {
	if db == nil {
		return
	}
	cutoff := time.Now().Unix() - 86400
	db.Exec(
		`UPDATE data_quotas SET bytes_used=0, period_start=?
         WHERE period_start < ?`,
		time.Now().Unix(), cutoff,
	)
}

// SetDataQuota sets a per-user download quota limit (called from admin API).
func setDataQuota(db *sql.DB, userID UserID, limitBytes int64) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(
		`INSERT OR REPLACE INTO data_quotas (user_id, bytes_used, limit_bytes, period_start)
         VALUES (?, 0, ?, ?)`,
		userID, limitBytes, time.Now().Unix(),
	)
	return err
}
