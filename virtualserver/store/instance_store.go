package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// CreateInstance creates a new service instance record in declared state.
func (s *VSStore) CreateInstance(ctx context.Context, serviceID int64, vsPath domain.VSPath) (string, error) {
	id := uuid.New().String()
	now := time.Now().Unix()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO virtualserver_instances
		(id,service_id,vs_path,state,retry_count,runtime_handle,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		id, serviceID, vsPath.String(), string(domain.InstanceDeclared), 0, "{}", now, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert instance: %w", err)
	}
	return id, nil
}

// GetInstance retrieves an instance by ID.
func (s *VSStore) GetInstance(ctx context.Context, id string) (*domain.ServiceInstance, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,service_id,COALESCE(node_id,''),vs_path,state,retry_count,runtime_handle,created_at,updated_at
		FROM virtualserver_instances WHERE id=?`, id)
	inst, err := scanInstance(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return inst, err
}

// ListInstancesByService returns all instances for a given service ID.
func (s *VSStore) ListInstancesByService(ctx context.Context, serviceID int64) ([]domain.ServiceInstance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,service_id,COALESCE(node_id,''),vs_path,state,retry_count,runtime_handle,created_at,updated_at
		FROM virtualserver_instances WHERE service_id=? ORDER BY created_at`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListInstances returns all instances, optionally filtered by state.
func (s *VSStore) ListInstances(ctx context.Context, stateFilter string) ([]domain.ServiceInstance, error) {
	var rows *sql.Rows
	var err error
	if stateFilter != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id,service_id,COALESCE(node_id,''),vs_path,state,retry_count,runtime_handle,created_at,updated_at
			FROM virtualserver_instances WHERE state=? ORDER BY created_at`, stateFilter)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id,service_id,COALESCE(node_id,''),vs_path,state,retry_count,runtime_handle,created_at,updated_at
			FROM virtualserver_instances ORDER BY created_at`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// UpdateInstanceState transitions an instance to a new state (with validation).
func (s *VSStore) UpdateInstanceState(ctx context.Context, id string, to domain.InstanceState) error {
	inst, err := s.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	if err := domain.ValidateInstanceTransition(inst.State, to); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.db.ExecContext(ctx, `
		UPDATE virtualserver_instances SET state=?, updated_at=? WHERE id=?`,
		string(to), time.Now().Unix(), id,
	)
	return err
}

// AssignNode sets the node_id and transitions to scheduled state.
func (s *VSStore) AssignNode(ctx context.Context, instanceID, nodeID string) error {
	inst, err := s.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	if err := domain.ValidateInstanceTransition(inst.State, domain.InstanceScheduled); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
		UPDATE virtualserver_instances
		SET node_id=?, state=?, updated_at=?
		WHERE id=?`,
		nodeID, string(domain.InstanceScheduled), now, instanceID,
	)
	return err
}

// UpdateRuntimeHandle stores adapter-specific handle data on an instance.
func (s *VSStore) UpdateRuntimeHandle(ctx context.Context, instanceID string, handle map[string]string) error {
	data, _ := json.Marshal(handle)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE virtualserver_instances SET runtime_handle=?, updated_at=? WHERE id=?`,
		string(data), time.Now().Unix(), instanceID,
	)
	return err
}

// IncrementRetryCount increments the retry counter and resets state to declared.
func (s *VSStore) IncrementRetryCount(ctx context.Context, instanceID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE virtualserver_instances
		SET retry_count=retry_count+1, state=?, updated_at=?
		WHERE id=?`,
		string(domain.InstanceDeclared), time.Now().Unix(), instanceID,
	)
	return err
}

// ── scanners ──────────────────────────────────────────────────────────────────

type instanceScanner interface {
	Scan(dest ...interface{}) error
}

func scanInstance(r instanceScanner) (*domain.ServiceInstance, error) {
	return scanInstanceFields(r)
}

func scanInstances(rows *sql.Rows) ([]domain.ServiceInstance, error) {
	var out []domain.ServiceInstance
	for rows.Next() {
		inst, err := scanInstanceFields(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inst)
	}
	return out, rows.Err()
}

func scanInstanceFields(r instanceScanner) (*domain.ServiceInstance, error) {
	var inst domain.ServiceInstance
	var state, vsPathStr, handleJSON string
	var createdAt, updatedAt int64
	if err := r.Scan(
		&inst.ID, &inst.ServiceID, &inst.NodeID,
		&vsPathStr, &state, &inst.RetryCount,
		&handleJSON, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	inst.State = domain.InstanceState(state)
	inst.CreatedAt = time.Unix(createdAt, 0)
	inst.UpdatedAt = time.Unix(updatedAt, 0)
	inst.RuntimeHandle = make(map[string]string)
	json.Unmarshal([]byte(handleJSON), &inst.RuntimeHandle)
	if vsPathStr != "" {
		p, err := domain.Parse(vsPathStr)
		if err == nil {
			inst.VSPath = p
		}
	}
	return &inst, nil
}
