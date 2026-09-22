package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// ── Volume CRUD ────────────────────────────────────────────────────────────────

// CreateVolume inserts a new volume record in the declared state.
func (s *VSStore) CreateVolume(ctx context.Context, v domain.Volume) (string, error) {
	raw, err := json.Marshal(v.Manifest)
	if err != nil {
		return "", fmt.Errorf("marshal volume manifest: %w", err)
	}
	handle, err := json.Marshal(v.DriverHandle)
	if err != nil {
		return "", fmt.Errorf("marshal driver handle: %w", err)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO virtualserver_volumes
			(id, namespace, name, manifest_json, state, bound_node_id, driver_handle, failure_reason, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID,
		v.Manifest.Metadata.Namespace,
		v.Manifest.Metadata.Name,
		string(raw),
		string(domain.VolumeDeclared),
		"",
		string(handle),
		"",
		now,
		now,
	)
	if err != nil {
		if isUniqueErr(err) {
			return "", ErrConflict
		}
		return "", fmt.Errorf("insert volume: %w", err)
	}
	return v.ID, nil
}

// GetVolume retrieves a volume by its UUID.
func (s *VSStore) GetVolume(ctx context.Context, id string) (*domain.Volume, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, manifest_json, state, bound_node_id, driver_handle, failure_reason, created_at, updated_at
		 FROM virtualserver_volumes WHERE id = ?`, id)
	return scanVolume(row)
}

// GetVolumeByName retrieves a volume by namespace+name.
func (s *VSStore) GetVolumeByName(ctx context.Context, namespace, name string) (*domain.Volume, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, manifest_json, state, bound_node_id, driver_handle, failure_reason, created_at, updated_at
		 FROM virtualserver_volumes WHERE namespace = ? AND name = ?`, namespace, name)
	return scanVolume(row)
}

// ListVolumes returns all volumes optionally filtered by namespace.
func (s *VSStore) ListVolumes(ctx context.Context, namespace string) ([]domain.Volume, error) {
	var rows *sql.Rows
	var err error
	if namespace == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, manifest_json, state, bound_node_id, driver_handle, failure_reason, created_at, updated_at
			 FROM virtualserver_volumes ORDER BY created_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, manifest_json, state, bound_node_id, driver_handle, failure_reason, created_at, updated_at
			 FROM virtualserver_volumes WHERE namespace = ? ORDER BY created_at DESC`, namespace)
	}
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	defer rows.Close()
	return scanVolumes(rows)
}

// ListVolumesByState returns all volumes in a given state.
func (s *VSStore) ListVolumesByState(ctx context.Context, state domain.VolumeState) ([]domain.Volume, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, manifest_json, state, bound_node_id, driver_handle, failure_reason, created_at, updated_at
		 FROM virtualserver_volumes WHERE state = ? ORDER BY created_at ASC`, string(state))
	if err != nil {
		return nil, fmt.Errorf("list volumes by state: %w", err)
	}
	defer rows.Close()
	return scanVolumes(rows)
}

// UpdateVolumeState transitions a volume to the given state, validating the transition first.
func (s *VSStore) UpdateVolumeState(ctx context.Context, id string, to domain.VolumeState) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var current string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM virtualserver_volumes WHERE id = ?`, id).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("get volume state: %w", err)
	}
	if err := domain.ValidateVolumeTransition(domain.VolumeState(current), to); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE virtualserver_volumes SET state = ?, updated_at = ? WHERE id = ?`,
		string(to), time.Now().Unix(), id)
	return err
}

// UpdateVolumeHandle stores the opaque driver handle after provisioning.
func (s *VSStore) UpdateVolumeHandle(ctx context.Context, id string, handle map[string]string) error {
	raw, err := json.Marshal(handle)
	if err != nil {
		return fmt.Errorf("marshal handle: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.db.ExecContext(ctx,
		`UPDATE virtualserver_volumes SET driver_handle = ?, updated_at = ? WHERE id = ?`,
		string(raw), time.Now().Unix(), id)
	return err
}

// UpdateVolumeBoundNode sets the BoundNodeID (HC-08: local volumes are pinned to one node).
func (s *VSStore) UpdateVolumeBoundNode(ctx context.Context, id, nodeID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE virtualserver_volumes SET bound_node_id = ?, updated_at = ? WHERE id = ?`,
		nodeID, time.Now().Unix(), id)
	return err
}

// UpdateVolumeFailure records a failure reason.
func (s *VSStore) UpdateVolumeFailure(ctx context.Context, id, reason string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE virtualserver_volumes SET state = ?, failure_reason = ?, updated_at = ? WHERE id = ?`,
		string(domain.VolumeFailed), reason, time.Now().Unix(), id)
	return err
}

// DeleteVolume removes a volume that is in released state.
// Returns ErrConflict if the volume has active mounts.
func (s *VSStore) DeleteVolume(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM virtualserver_volumes WHERE id = ?`, id).Scan(&state); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	if domain.VolumeState(state) != domain.VolumeReleased {
		return ErrConflict
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM virtualserver_volumes WHERE id = ?`, id)
	return err
}

// ── Mount CRUD ─────────────────────────────────────────────────────────────────

// BindMount creates a new mount record in pending state.
func (s *VSStore) BindMount(ctx context.Context, m domain.VolumeMount) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO virtualserver_volume_mounts (volume_id, instance_id, target_path, read_only, state)
		 VALUES (?, ?, ?, ?, ?)`,
		m.VolumeID, m.InstanceID, m.TargetPath, boolToInt(m.ReadOnly), string(domain.MountPending))
	if err != nil {
		return 0, fmt.Errorf("bind mount: %w", err)
	}
	return res.LastInsertId()
}

// GetMount retrieves a single mount by ID.
func (s *VSStore) GetMount(ctx context.Context, id int64) (*domain.VolumeMount, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, volume_id, instance_id, target_path, read_only, state, mounted_at, unmounted_at
		 FROM virtualserver_volume_mounts WHERE id = ?`, id)
	return scanMount(row)
}

// ListMountsByInstance returns all mounts for a given instance.
func (s *VSStore) ListMountsByInstance(ctx context.Context, instanceID string) ([]domain.VolumeMount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, volume_id, instance_id, target_path, read_only, state, mounted_at, unmounted_at
		 FROM virtualserver_volume_mounts WHERE instance_id = ? ORDER BY id`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list mounts by instance: %w", err)
	}
	defer rows.Close()
	return scanMounts(rows)
}

// ListActiveMountsByVolume returns mounts for a volume that are not released.
func (s *VSStore) ListActiveMountsByVolume(ctx context.Context, volumeID string) ([]domain.VolumeMount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, volume_id, instance_id, target_path, read_only, state, mounted_at, unmounted_at
		 FROM virtualserver_volume_mounts
		 WHERE volume_id = ? AND state NOT IN ('released') ORDER BY id`, volumeID)
	if err != nil {
		return nil, fmt.Errorf("list active mounts by volume: %w", err)
	}
	defer rows.Close()
	return scanMounts(rows)
}

// UpdateMountState transitions a mount to the given state.
func (s *VSStore) UpdateMountState(ctx context.Context, id int64, to domain.VolumeMountState) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now().Unix()
	var err error
	switch to {
	case domain.MountActive:
		_, err = s.db.ExecContext(ctx,
			`UPDATE virtualserver_volume_mounts SET state = ?, mounted_at = ? WHERE id = ?`,
			string(to), now, id)
	case domain.MountReleased:
		_, err = s.db.ExecContext(ctx,
			`UPDATE virtualserver_volume_mounts SET state = ?, unmounted_at = ? WHERE id = ?`,
			string(to), now, id)
	default:
		_, err = s.db.ExecContext(ctx,
			`UPDATE virtualserver_volume_mounts SET state = ? WHERE id = ?`,
			string(to), id)
	}
	return err
}

// ── Snapshot CRUD ──────────────────────────────────────────────────────────────

// CreateSnapshot inserts a new snapshot record in pending state.
func (s *VSStore) CreateSnapshot(ctx context.Context, snap domain.VolumeSnapshot) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO virtualserver_volume_snapshots (id, volume_id, label, state, driver_ref, size_mib, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		snap.ID, snap.VolumeID, snap.Label,
		string(domain.SnapshotPending), "", 0,
		time.Now().Unix())
	if err != nil {
		if isUniqueErr(err) {
			return ErrConflict
		}
		return fmt.Errorf("create snapshot: %w", err)
	}
	return nil
}

// GetSnapshot retrieves a snapshot by ID.
func (s *VSStore) GetSnapshot(ctx context.Context, id string) (*domain.VolumeSnapshot, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, volume_id, label, state, driver_ref, size_mib, created_at, completed_at
		 FROM virtualserver_volume_snapshots WHERE id = ?`, id)
	return scanSnapshot(row)
}

// ListSnapshots returns all snapshots for a volume.
func (s *VSStore) ListSnapshots(ctx context.Context, volumeID string) ([]domain.VolumeSnapshot, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, volume_id, label, state, driver_ref, size_mib, created_at, completed_at
		 FROM virtualserver_volume_snapshots WHERE volume_id = ? ORDER BY created_at DESC`, volumeID)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()
	return scanSnapshots(rows)
}

// UpdateSnapshotState finalizes a snapshot with its driver reference and size.
func (s *VSStore) UpdateSnapshotState(ctx context.Context, id string, state domain.SnapshotState, driverRef string, sizeMiB int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now().Unix()
	if state == domain.SnapshotReady {
		_, err := s.db.ExecContext(ctx,
			`UPDATE virtualserver_volume_snapshots SET state = ?, driver_ref = ?, size_mib = ?, completed_at = ? WHERE id = ?`,
			string(state), driverRef, sizeMiB, now, id)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE virtualserver_volume_snapshots SET state = ? WHERE id = ?`,
		string(state), id)
	return err
}

// ── Scanner helpers ────────────────────────────────────────────────────────────

func scanVolume(row *sql.Row) (*domain.Volume, error) {
	var v domain.Volume
	var manifestJSON, handleJSON string
	var createdAt, updatedAt int64

	err := row.Scan(&v.ID, &manifestJSON, &v.State, &v.BoundNodeID, &handleJSON, &v.FailureReason, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan volume: %w", err)
	}
	if err := json.Unmarshal([]byte(manifestJSON), &v.Manifest); err != nil {
		return nil, fmt.Errorf("unmarshal volume manifest: %w", err)
	}
	if handleJSON != "" && handleJSON != "{}" {
		if err := json.Unmarshal([]byte(handleJSON), &v.DriverHandle); err != nil {
			return nil, fmt.Errorf("unmarshal driver handle: %w", err)
		}
	}
	v.CreatedAt = time.Unix(createdAt, 0)
	v.UpdatedAt = time.Unix(updatedAt, 0)
	return &v, nil
}

func scanVolumes(rows *sql.Rows) ([]domain.Volume, error) {
	var out []domain.Volume
	for rows.Next() {
		var v domain.Volume
		var manifestJSON, handleJSON string
		var createdAt, updatedAt int64

		if err := rows.Scan(&v.ID, &manifestJSON, &v.State, &v.BoundNodeID, &handleJSON, &v.FailureReason, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan volume row: %w", err)
		}
		if err := json.Unmarshal([]byte(manifestJSON), &v.Manifest); err != nil {
			return nil, fmt.Errorf("unmarshal volume manifest: %w", err)
		}
		if handleJSON != "" && handleJSON != "{}" {
			if err := json.Unmarshal([]byte(handleJSON), &v.DriverHandle); err != nil {
				return nil, fmt.Errorf("unmarshal driver handle: %w", err)
			}
		}
		v.CreatedAt = time.Unix(createdAt, 0)
		v.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanMount(row *sql.Row) (*domain.VolumeMount, error) {
	var m domain.VolumeMount
	var readOnly int
	var mountedAt, unmountedAt sql.NullInt64
	if err := row.Scan(&m.ID, &m.VolumeID, &m.InstanceID, &m.TargetPath, &readOnly, &m.State, &mountedAt, &unmountedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan mount: %w", err)
	}
	m.ReadOnly = readOnly != 0
	if mountedAt.Valid {
		t := time.Unix(mountedAt.Int64, 0)
		m.MountedAt = &t
	}
	if unmountedAt.Valid {
		t := time.Unix(unmountedAt.Int64, 0)
		m.UnmountedAt = &t
	}
	return &m, nil
}

func scanMounts(rows *sql.Rows) ([]domain.VolumeMount, error) {
	var out []domain.VolumeMount
	for rows.Next() {
		var m domain.VolumeMount
		var readOnly int
		var mountedAt, unmountedAt sql.NullInt64
		if err := rows.Scan(&m.ID, &m.VolumeID, &m.InstanceID, &m.TargetPath, &readOnly, &m.State, &mountedAt, &unmountedAt); err != nil {
			return nil, fmt.Errorf("scan mount row: %w", err)
		}
		m.ReadOnly = readOnly != 0
		if mountedAt.Valid {
			t := time.Unix(mountedAt.Int64, 0)
			m.MountedAt = &t
		}
		if unmountedAt.Valid {
			t := time.Unix(unmountedAt.Int64, 0)
			m.UnmountedAt = &t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanSnapshot(row *sql.Row) (*domain.VolumeSnapshot, error) {
	var snap domain.VolumeSnapshot
	var createdAt int64
	var completedAt sql.NullInt64
	if err := row.Scan(&snap.ID, &snap.VolumeID, &snap.Label, &snap.State, &snap.DriverRef, &snap.SizeMiB, &createdAt, &completedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan snapshot: %w", err)
	}
	snap.CreatedAt = time.Unix(createdAt, 0)
	if completedAt.Valid {
		t := time.Unix(completedAt.Int64, 0)
		snap.CompletedAt = &t
	}
	return &snap, nil
}

func scanSnapshots(rows *sql.Rows) ([]domain.VolumeSnapshot, error) {
	var out []domain.VolumeSnapshot
	for rows.Next() {
		var snap domain.VolumeSnapshot
		var createdAt int64
		var completedAt sql.NullInt64
		if err := rows.Scan(&snap.ID, &snap.VolumeID, &snap.Label, &snap.State, &snap.DriverRef, &snap.SizeMiB, &createdAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan snapshot row: %w", err)
		}
		snap.CreatedAt = time.Unix(createdAt, 0)
		if completedAt.Valid {
			t := time.Unix(completedAt.Int64, 0)
			snap.CompletedAt = &t
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

