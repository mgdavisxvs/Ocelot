package db

import (
	"context"
	"database/sql"
)

// PeerQualityRecord holds Beta-Binomial parameters for one (user, torrent) pair.
type PeerQualityRecord struct {
	UID       int64
	TorrentID int64
	Alpha     float64
	Beta      float64
	ObsCount  int
	UpdatedAt int64
}

func (d *DB) UpsertPeerQuality(ctx context.Context, recs []PeerQualityRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := d.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO peer_quality (uid, torrent_id, alpha, beta, obs_count, updated_at)
		 VALUES (?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range recs {
		if _, err := stmt.ExecContext(ctx, r.UID, r.TorrentID, r.Alpha, r.Beta, r.ObsCount, r.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) LoadPeerQuality(ctx context.Context, uid, torrentID int64) (*PeerQualityRecord, error) {
	r := &PeerQualityRecord{UID: uid, TorrentID: torrentID}
	err := d.pool.QueryRowContext(ctx,
		`SELECT alpha, beta, obs_count, updated_at FROM peer_quality WHERE uid=? AND torrent_id=?`,
		uid, torrentID).Scan(&r.Alpha, &r.Beta, &r.ObsCount, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// LoadAllPeerQuality returns every row from peer_quality for startup state restore.
func (d *DB) LoadAllPeerQuality(ctx context.Context) ([]PeerQualityRecord, error) {
	rows, err := d.pool.QueryContext(ctx,
		`SELECT uid, torrent_id, alpha, beta, obs_count, updated_at FROM peer_quality`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PeerQualityRecord
	for rows.Next() {
		var r PeerQualityRecord
		if err := rows.Scan(&r.UID, &r.TorrentID, &r.Alpha, &r.Beta, &r.ObsCount, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadUserGlobalReliability computes Σα / Σ(α+β) and total obs for a user directly from DB.
func (d *DB) LoadUserGlobalReliability(ctx context.Context, uid int64) (globalP float64, obsCount int, err error) {
	var sumAlpha, sumTotal float64
	rows, err := d.pool.QueryContext(ctx,
		`SELECT alpha, beta, obs_count FROM peer_quality WHERE uid=?`, uid)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var alpha, beta float64
		var obs int
		if err := rows.Scan(&alpha, &beta, &obs); err != nil {
			return 0, 0, err
		}
		sumAlpha += alpha
		sumTotal += alpha + beta
		obsCount += obs
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if sumTotal == 0 {
		return 0.5, 0, nil
	}
	return sumAlpha / sumTotal, obsCount, nil
}
