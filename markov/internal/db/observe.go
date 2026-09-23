package db

import (
	"context"
	"fmt"
)

// PeerRow is one row from the tracker peers table.
type PeerRow struct {
	UID       int64
	FID       int64 // torrent ID
	Active    bool
	Remaining int64
	Mtime     int64 // Unix timestamp of last announce
}

// TorrentRow is one row from the tracker torrents table.
type TorrentRow struct {
	ID       int64
	Seeders  int64
	Leechers int64
}

// UserRow is ratio-relevant columns from the tracker users table.
type UserRow struct {
	ID         int64
	Uploaded   int64
	Downloaded int64
	CanLeech   bool
}

// FreeleechUID is a set of user IDs with active freeleech tokens.
type FreeleechUID = map[int64]struct{}

// SnatchRow is one row from the tracker snatches table.
type SnatchRow struct {
	UID    int64
	FID    int64
	Tstamp int64
}

// StoredPeerState is one row from markov_peer_states.
type StoredPeerState struct {
	TorrentID  int64
	UID        int64
	State      int
	ObservedAt int64
}

// StoredTorrentState is one row from markov_torrent_states.
type StoredTorrentState struct {
	TorrentID  int64
	State      int
	ObservedAt int64
}

// StoredUserState is one row from markov_user_states.
type StoredUserState struct {
	UID        int64
	State      int
	PathJSON   string
	ObservedAt int64
}

// LoadPeers fetches active peer rows from the tracker shard.
func (d *DB) LoadPeers(ctx context.Context) ([]PeerRow, error) {
	rows, err := d.trackerDB.QueryContext(ctx,
		`SELECT user_id, torrent_id, active, remaining, last_announce FROM peers`)
	if err != nil {
		return nil, fmt.Errorf("LoadPeers: %w", err)
	}
	defer rows.Close()
	var out []PeerRow
	for rows.Next() {
		var r PeerRow
		var active int64
		if err := rows.Scan(&r.UID, &r.FID, &active, &r.Remaining, &r.Mtime); err != nil {
			return nil, err
		}
		r.Active = active != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadTorrents fetches seeder/leecher counts from the tracker shard.
func (d *DB) LoadTorrents(ctx context.Context) ([]TorrentRow, error) {
	rows, err := d.trackerDB.QueryContext(ctx,
		`SELECT id, seeders, leechers FROM torrents`)
	if err != nil {
		return nil, fmt.Errorf("LoadTorrents: %w", err)
	}
	defer rows.Close()
	var out []TorrentRow
	for rows.Next() {
		var r TorrentRow
		if err := rows.Scan(&r.ID, &r.Seeders, &r.Leechers); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadUsers fetches ratio-relevant columns from users + user_passkeys in the tracker shard.
func (d *DB) LoadUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := d.trackerDB.QueryContext(ctx,
		`SELECT u.id, u.uploaded, u.downloaded, COALESCE(up.can_leech, 1)
		 FROM users u
		 LEFT JOIN user_passkeys up ON u.id = up.user_id`)
	if err != nil {
		return nil, fmt.Errorf("LoadUsers: %w", err)
	}
	defer rows.Close()
	var out []UserRow
	for rows.Next() {
		var r UserRow
		var canLeech int64
		if err := rows.Scan(&r.ID, &r.Uploaded, &r.Downloaded, &canLeech); err != nil {
			return nil, err
		}
		r.CanLeech = canLeech != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadFreeleechUIDs returns the set of user IDs with active tokens.
func (d *DB) LoadFreeleechUIDs(ctx context.Context) (FreeleechUID, error) {
	rows, err := d.trackerDB.QueryContext(ctx,
		`SELECT DISTINCT user_id FROM tokens`)
	if err != nil {
		return nil, fmt.Errorf("LoadFreeleechUIDs: %w", err)
	}
	defer rows.Close()
	out := make(FreeleechUID)
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out[uid] = struct{}{}
	}
	return out, rows.Err()
}

// LoadSnatches returns snatch events after the given Unix timestamp watermark.
func (d *DB) LoadSnatches(ctx context.Context, afterTstamp int64) ([]SnatchRow, error) {
	rows, err := d.trackerDB.QueryContext(ctx,
		`SELECT user_id, torrent_id, snatched_time FROM snatches
		 WHERE snatched_time > ? ORDER BY snatched_time ASC`,
		afterTstamp)
	if err != nil {
		return nil, fmt.Errorf("LoadSnatches: %w", err)
	}
	defer rows.Close()
	var out []SnatchRow
	for rows.Next() {
		var r SnatchRow
		if err := rows.Scan(&r.UID, &r.FID, &r.Tstamp); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadStoredPeerStates loads all saved peer states from a previous run.
func (d *DB) LoadStoredPeerStates(ctx context.Context) ([]StoredPeerState, error) {
	rows, err := d.markovDB.QueryContext(ctx,
		`SELECT torrent_id, uid, state, observed_at FROM markov_peer_states`)
	if err != nil {
		return nil, fmt.Errorf("LoadStoredPeerStates: %w", err)
	}
	defer rows.Close()
	var out []StoredPeerState
	for rows.Next() {
		var r StoredPeerState
		if err := rows.Scan(&r.TorrentID, &r.UID, &r.State, &r.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadStoredTorrentStates loads all saved torrent health states.
func (d *DB) LoadStoredTorrentStates(ctx context.Context) ([]StoredTorrentState, error) {
	rows, err := d.markovDB.QueryContext(ctx,
		`SELECT torrent_id, state, observed_at FROM markov_torrent_states`)
	if err != nil {
		return nil, fmt.Errorf("LoadStoredTorrentStates: %w", err)
	}
	defer rows.Close()
	var out []StoredTorrentState
	for rows.Next() {
		var r StoredTorrentState
		if err := rows.Scan(&r.TorrentID, &r.State, &r.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadStoredUserStates loads all saved user ratio states.
func (d *DB) LoadStoredUserStates(ctx context.Context) ([]StoredUserState, error) {
	rows, err := d.markovDB.QueryContext(ctx,
		`SELECT uid, state, COALESCE(path_json,''), observed_at FROM markov_user_states`)
	if err != nil {
		return nil, fmt.Errorf("LoadStoredUserStates: %w", err)
	}
	defer rows.Close()
	var out []StoredUserState
	for rows.Next() {
		var r StoredUserState
		if err := rows.Scan(&r.UID, &r.State, &r.PathJSON, &r.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ChainCountRow holds one (from, to, count) triple for a named chain.
type ChainCountRow struct {
	From  int
	To    int
	Count float64
}

// LoadChainCounts loads persisted Markov counts for the named chain.
func (d *DB) LoadChainCounts(ctx context.Context, chainName string) ([]ChainCountRow, error) {
	rows, err := d.markovDB.QueryContext(ctx,
		`SELECT from_state, to_state, count FROM markov_chain_counts WHERE chain_name=?`,
		chainName)
	if err != nil {
		return nil, fmt.Errorf("LoadChainCounts(%s): %w", chainName, err)
	}
	defer rows.Close()
	var out []ChainCountRow
	for rows.Next() {
		var r ChainCountRow
		if err := rows.Scan(&r.From, &r.To, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
