package tracker

import (
	"database/sql"
	"errors"
	"time"
)

// EnsureBackupQuotaTable creates the backup_quotas table if absent.
func EnsureBackupQuotaTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS backup_quotas (
		user_id       INTEGER PRIMARY KEY,
		bytes_used    INTEGER NOT NULL DEFAULT 0,
		limit_bytes   INTEGER NOT NULL DEFAULT 0,
		period_start  INTEGER NOT NULL
	)`)
	return err
}

// checkBackupQuota returns an error if the user has exceeded their backup quota.
// delta is the bytes uploaded in this announce.
func (w *Worker) checkBackupQuota(db *sql.DB, userID UserID, delta int64) error {
	if db == nil || delta <= 0 {
		return nil
	}
	var bytesUsed, limitBytes int64
	err := db.QueryRow(
		`SELECT bytes_used, limit_bytes FROM backup_quotas WHERE user_id=?`, userID,
	).Scan(&bytesUsed, &limitBytes)
	if err == sql.ErrNoRows {
		return nil // no quota row = unlimited
	}
	if err != nil {
		return nil // quota check failure is non-fatal
	}
	if limitBytes > 0 && bytesUsed+delta > limitBytes {
		return errors.New("backup quota exceeded")
	}
	// Atomically add delta.
	db.Exec(
		`UPDATE backup_quotas SET bytes_used=bytes_used+? WHERE user_id=?`,
		delta, userID,
	)
	return nil
}

// resetDailyBackupQuotas zeroes bytes_used for periods older than 24 hours.
// Called by Scheduler on each tick.
func ResetDailyBackupQuotas(db *sql.DB) {
	if db == nil {
		return
	}
	cutoff := time.Now().Unix() - 86400
	db.Exec(
		`UPDATE backup_quotas SET bytes_used=0, period_start=?
		 WHERE period_start < ?`,
		time.Now().Unix(), cutoff,
	)
}
