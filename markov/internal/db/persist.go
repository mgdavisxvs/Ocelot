package db

import (
	"context"
	"database/sql"
	"fmt"
)

// UpsertChainCounts persists all counts for the named chain in a single
// REPLACE INTO batch (max 1000 rows per call to bound query size).
func (d *DB) UpsertChainCounts(ctx context.Context, chainName string, counts [][]float64) error {
	tx, err := d.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`REPLACE INTO markov_chain_counts (chain_name, from_state, to_state, count) VALUES (?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for i, row := range counts {
		for j, v := range row {
			if _, err := stmt.ExecContext(ctx, chainName, i, j, v); err != nil {
				return fmt.Errorf("exec [%d][%d]: %w", i, j, err)
			}
		}
	}
	return tx.Commit()
}

// UpsertPeerStates bulk-upserts the current peer state snapshot.
type PeerStateRecord struct {
	TorrentID  int64
	UID        int64
	State      int
	ObservedAt int64
}

func (d *DB) UpsertPeerStates(ctx context.Context, recs []PeerStateRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := d.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`REPLACE INTO markov_peer_states (torrent_id, uid, state, observed_at) VALUES (?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range recs {
		if _, err := stmt.ExecContext(ctx, r.TorrentID, r.UID, r.State, r.ObservedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeletePeerStates removes stale peer state rows for (torrent, uid) pairs
// that are no longer present in xbt_files_users.
func (d *DB) DeletePeerStates(ctx context.Context, torrentID, uid int64) error {
	_, err := d.pool.ExecContext(ctx,
		`DELETE FROM markov_peer_states WHERE torrent_id=? AND uid=?`, torrentID, uid)
	return err
}

// UpsertTorrentStates bulk-upserts torrent health states.
type TorrentStateRecord struct {
	TorrentID  int64
	State      int
	ObservedAt int64
}

func (d *DB) UpsertTorrentStates(ctx context.Context, recs []TorrentStateRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := d.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`REPLACE INTO markov_torrent_states (torrent_id, state, observed_at) VALUES (?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range recs {
		if _, err := stmt.ExecContext(ctx, r.TorrentID, r.State, r.ObservedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertUserStates bulk-upserts user ratio states with path JSON.
type UserStateRecord struct {
	UID        int64
	State      int
	PathJSON   string
	ObservedAt int64
}

func (d *DB) UpsertUserStates(ctx context.Context, recs []UserStateRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := d.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`REPLACE INTO markov_user_states (uid, state, path_json, observed_at) VALUES (?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range recs {
		if _, err := stmt.ExecContext(ctx, r.UID, r.State, r.PathJSON, r.ObservedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TorrentPredictionRecord is a full prediction row.
type TorrentPredictionRecord struct {
	TorrentID          int64
	HealthState        int
	PiJSON             string
	Pi24hJSON          string
	Pi72hJSON          string
	DeadProb24h        float64
	DeadProb72h        float64
	ExpectedDeadHours  float64
	Entropy            float64
	RecommendedInterval int
	UpdatedAt          int64
}

func (d *DB) UpsertPredictions(ctx context.Context, recs []TorrentPredictionRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := d.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx, `
		REPLACE INTO markov_predictions
			(torrent_id, health_state, pi_json, pi_24h_json, pi_72h_json,
			 dead_prob_24h, dead_prob_72h, expected_dead_hours,
			 entropy, recommended_interval, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range recs {
		if _, err := stmt.ExecContext(ctx,
			r.TorrentID, r.HealthState, r.PiJSON, r.Pi24hJSON, r.Pi72hJSON,
			r.DeadProb24h, r.DeadProb72h, r.ExpectedDeadHours,
			r.Entropy, r.RecommendedInterval, r.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UserAnomalyRecord holds fraud detection output for one user.
type UserAnomalyRecord struct {
	UID               int64
	AnomalyScore      float64
	PathLogLikelihood float64
	Flagged           bool
	UpdatedAt         int64
}

func (d *DB) UpsertUserAnomalies(ctx context.Context, recs []UserAnomalyRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := d.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx, `
		REPLACE INTO markov_user_anomaly
			(uid, anomaly_score, path_log_likelihood, flagged, updated_at)
		VALUES (?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range recs {
		flagged := 0
		if r.Flagged {
			flagged = 1
		}
		if _, err := stmt.ExecContext(ctx, r.UID, r.AnomalyScore, r.PathLogLikelihood, flagged, r.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FreeleechCandidateRecord holds one recommended freeleech torrent.
type FreeleechCandidateRecord struct {
	TorrentID     int64
	PriorityScore float64
	DeadProb72h   float64
	Recommended   bool
	UpdatedAt     int64
}

func (d *DB) UpsertFreeleechCandidates(ctx context.Context, recs []FreeleechCandidateRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := d.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Clear stale candidates first.
	if _, err := tx.ExecContext(ctx, `UPDATE markov_freeleech_candidates SET recommended=0`); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
		REPLACE INTO markov_freeleech_candidates
			(torrent_id, priority_score, dead_prob_72h, recommended, updated_at)
		VALUES (?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range recs {
		rec := 0
		if r.Recommended {
			rec = 1
		}
		if _, err := stmt.ExecContext(ctx, r.TorrentID, r.PriorityScore, r.DeadProb72h, rec, r.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadPrediction fetches a single torrent prediction row.
func (d *DB) LoadPrediction(ctx context.Context, torrentID int64) (*TorrentPredictionRecord, error) {
	r := &TorrentPredictionRecord{TorrentID: torrentID}
	err := d.pool.QueryRowContext(ctx, `
		SELECT health_state, pi_json, pi_24h_json, pi_72h_json,
		       dead_prob_24h, dead_prob_72h, expected_dead_hours,
		       entropy, recommended_interval, updated_at
		FROM markov_predictions WHERE torrent_id=?`, torrentID).Scan(
		&r.HealthState, &r.PiJSON, &r.Pi24hJSON, &r.Pi72hJSON,
		&r.DeadProb24h, &r.DeadProb72h, &r.ExpectedDeadHours,
		&r.Entropy, &r.RecommendedInterval, &r.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// LoadUserAnomaly fetches a single user anomaly row.
func (d *DB) LoadUserAnomaly(ctx context.Context, uid int64) (*UserAnomalyRecord, error) {
	r := &UserAnomalyRecord{UID: uid}
	var flagged int8
	err := d.pool.QueryRowContext(ctx, `
		SELECT anomaly_score, path_log_likelihood, flagged, updated_at
		FROM markov_user_anomaly WHERE uid=?`, uid).Scan(
		&r.AnomalyScore, &r.PathLogLikelihood, &flagged, &r.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Flagged = flagged != 0
	return r, nil
}

// LoadFreeleechCandidates returns the current top recommended torrents.
func (d *DB) LoadFreeleechCandidates(ctx context.Context, limit int) ([]FreeleechCandidateRecord, error) {
	rows, err := d.pool.QueryContext(ctx, `
		SELECT torrent_id, priority_score, dead_prob_72h, recommended, updated_at
		FROM markov_freeleech_candidates
		WHERE recommended=1
		ORDER BY priority_score DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FreeleechCandidateRecord
	for rows.Next() {
		var r FreeleechCandidateRecord
		var rec int8
		if err := rows.Scan(&r.TorrentID, &r.PriorityScore, &r.DeadProb72h, &rec, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Recommended = rec != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
