package db

import (
	"context"
	"fmt"
	"strings"
)

// SeederAssignment is a directed seeding recommendation for one user on one torrent.
// The model NEVER forces users to seed — these are advisory signals. Tracker policy
// governs any incentive or enforcement layer built on top of this data.
type SeederAssignment struct {
	ID           int64
	UserID       int64
	TorrentID    int64
	UrgencyScore float64
	AssignedAt   int64
	FulfilledAt  int64 // 0 = pending; non-zero = fulfillment Unix timestamp
	ExpiresAt    int64
}

// AssignPair uniquely identifies an active (user, torrent) assignment for de-duplication.
type AssignPair struct {
	UserID    int64
	TorrentID int64
}

// BulkInsertSeederAssignments inserts new seeder assignments in a single transaction.
func (d *DB) BulkInsertSeederAssignments(ctx context.Context, rows []SeederAssignment) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := d.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO markov_seeder_assignments
			(user_id, torrent_id, urgency_score, assigned_at, fulfilled_at, expires_at)
		VALUES (?,?,?,?,0,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, a := range rows {
		if _, err := stmt.ExecContext(ctx,
			a.UserID, a.TorrentID, a.UrgencyScore, a.AssignedAt, a.ExpiresAt); err != nil {
			return fmt.Errorf("insert assignment uid=%d tid=%d: %w", a.UserID, a.TorrentID, err)
		}
	}
	return tx.Commit()
}

// LoadActiveAssignments returns all unfulfilled, unexpired assignments.
// Callers use this to build de-duplication sets, per-user count limits, and
// the fulfillment intersection.
func (d *DB) LoadActiveAssignments(ctx context.Context, now int64) ([]SeederAssignment, error) {
	rows, err := d.pool.QueryContext(ctx, `
		SELECT id, user_id, torrent_id, urgency_score, assigned_at, fulfilled_at, expires_at
		FROM markov_seeder_assignments
		WHERE fulfilled_at=0 AND expires_at>?`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeederAssignment
	for rows.Next() {
		var a SeederAssignment
		if err := rows.Scan(&a.ID, &a.UserID, &a.TorrentID, &a.UrgencyScore,
			&a.AssignedAt, &a.FulfilledAt, &a.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FulfillSeederAssignments marks pending assignments for the given (torrentID, uids)
// as fulfilled at now. Called when a user is observed actively seeding their assigned torrent.
func (d *DB) FulfillSeederAssignments(ctx context.Context, torrentID int64, uids []int64, now int64) error {
	if len(uids) == 0 {
		return nil
	}
	placeholders := make([]string, len(uids))
	args := []any{now, torrentID}
	for i, uid := range uids {
		placeholders[i] = "?"
		args = append(args, uid)
	}
	_, err := d.pool.ExecContext(ctx,
		fmt.Sprintf(`UPDATE markov_seeder_assignments
		 SET fulfilled_at=?
		 WHERE torrent_id=? AND fulfilled_at=0 AND user_id IN (%s)`,
			strings.Join(placeholders, ",")),
		args...)
	return err
}

// LoadUserSeederAssignments returns active (unfulfilled, unexpired) assignments for
// a user, ordered by urgency descending. Used by the /user/{id}/recommendations API.
func (d *DB) LoadUserSeederAssignments(ctx context.Context, userID, now int64) ([]SeederAssignment, error) {
	rows, err := d.pool.QueryContext(ctx, `
		SELECT id, user_id, torrent_id, urgency_score, assigned_at, fulfilled_at, expires_at
		FROM markov_seeder_assignments
		WHERE user_id=? AND fulfilled_at=0 AND expires_at>?
		ORDER BY urgency_score DESC`,
		userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeederAssignment
	for rows.Next() {
		var a SeederAssignment
		if err := rows.Scan(&a.ID, &a.UserID, &a.TorrentID, &a.UrgencyScore,
			&a.AssignedAt, &a.FulfilledAt, &a.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ExpireSeederAssignments deletes fulfilled and expired assignments older than cutoff.
// Called once per persist cycle to bound table growth.
func (d *DB) ExpireSeederAssignments(ctx context.Context, cutoff int64) error {
	_, err := d.pool.ExecContext(ctx,
		`DELETE FROM markov_seeder_assignments
		 WHERE expires_at<=? OR (fulfilled_at>0 AND fulfilled_at<=?)`,
		cutoff, cutoff)
	return err
}
