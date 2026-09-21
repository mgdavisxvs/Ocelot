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

// CreateOperation inserts a new operation in pending state.
func (s *VSStore) CreateOperation(ctx context.Context, instanceID string, opType domain.OperationType, adapter string) (string, error) {
	id := uuid.New().String()
	now := time.Now().Unix()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO virtualserver_operations(id,instance_id,op_type,state,adapter,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?)`,
		id, instanceID, string(opType), string(domain.OperationPending), adapter, now, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert operation: %w", err)
	}
	return id, nil
}

// UpdateOperationState transitions an operation to a new state.
func (s *VSStore) UpdateOperationState(ctx context.Context, id string, to domain.OperationState) error {
	op, err := s.GetOperation(ctx, id)
	if err != nil {
		return err
	}
	if err := domain.ValidateOperationTransition(op.State, to); err != nil {
		return err
	}
	now := time.Now().Unix()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var completedAt interface{}
	if to == domain.OperationSucceeded || to == domain.OperationFailed || to == domain.OperationCancelled {
		completedAt = now
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE virtualserver_operations SET state=?, updated_at=?, completed_at=? WHERE id=?`,
		string(to), now, completedAt, id,
	)
	return err
}

// AppendOperationEvent adds a timestamped event to an operation.
func (s *VSStore) AppendOperationEvent(ctx context.Context, operationID, eventType, message string, payload map[string]interface{}) error {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payloadJSON, _ := json.Marshal(payload)
	now := time.Now().Unix()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO virtualserver_operation_events(operation_id,event_type,message,payload,created_at)
		VALUES (?,?,?,?,?)`,
		operationID, eventType, message, string(payloadJSON), now,
	)
	return err
}

// GetOperation retrieves an operation by ID including its events.
func (s *VSStore) GetOperation(ctx context.Context, id string) (*domain.Operation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,instance_id,op_type,state,adapter,created_at,updated_at,completed_at
		FROM virtualserver_operations WHERE id=?`, id)
	op, err := scanOperation(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	events, err := s.listOperationEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	op.Events = events
	return op, nil
}

func (s *VSStore) listOperationEvents(ctx context.Context, operationID string) ([]domain.OperationEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,operation_id,event_type,message,payload,created_at
		FROM virtualserver_operation_events WHERE operation_id=? ORDER BY id`, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.OperationEvent
	for rows.Next() {
		var ev domain.OperationEvent
		var payloadJSON string
		var createdAt int64
		if err := rows.Scan(&ev.ID, &ev.OperationID, &ev.EventType, &ev.Message, &payloadJSON, &createdAt); err != nil {
			return nil, err
		}
		ev.Payload = make(map[string]interface{})
		json.Unmarshal([]byte(payloadJSON), &ev.Payload)
		ev.CreatedAt = time.Unix(createdAt, 0)
		events = append(events, ev)
	}
	return events, rows.Err()
}

// WriteAuditLog records an administrative action.
func (s *VSStore) WriteAuditLog(ctx context.Context, action, resourceType, resourceID, ipAddr string, success bool, errMsg string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log(timestamp,action,resource_type,resource_id,ip_address,success,error_message)
		VALUES (?,?,?,?,?,?,?)`,
		time.Now().Unix(), action, resourceType, resourceID, ipAddr, boolToInt(success), errMsg,
	)
	return err
}

// ListAuditEvents returns audit log entries for a given resource type and ID.
func (s *VSStore) ListAuditEvents(ctx context.Context, resourceType, resourceID string) ([]map[string]interface{}, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,timestamp,action,resource_type,resource_id,ip_address,success,error_message
		FROM audit_log WHERE resource_type=? AND resource_id=? ORDER BY timestamp DESC LIMIT 100`,
		resourceType, resourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var id int64
		var ts int64
		var action, rType, rID, ip, errMsg string
		var success int
		if err := rows.Scan(&id, &ts, &action, &rType, &rID, &ip, &success, &errMsg); err != nil {
			return nil, err
		}
		out = append(out, map[string]interface{}{
			"id": id, "timestamp": ts, "action": action,
			"resource_type": rType, "resource_id": rID,
			"ip_address": ip, "success": success != 0, "error": errMsg,
		})
	}
	return out, rows.Err()
}

func scanOperation(r interface{ Scan(...interface{}) error }) (*domain.Operation, error) {
	var op domain.Operation
	var opType, state, adapter string
	var createdAt, updatedAt int64
	var completedAt sql.NullInt64
	if err := r.Scan(&op.ID, &op.InstanceID, &opType, &state, &adapter, &createdAt, &updatedAt, &completedAt); err != nil {
		return nil, err
	}
	op.Type = domain.OperationType(opType)
	op.State = domain.OperationState(state)
	op.Adapter = adapter
	op.CreatedAt = time.Unix(createdAt, 0)
	op.UpdatedAt = time.Unix(updatedAt, 0)
	if completedAt.Valid {
		t := time.Unix(completedAt.Int64, 0)
		op.CompletedAt = &t
	}
	return &op, nil
}
