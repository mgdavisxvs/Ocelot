package store

import (
	"context"
	"fmt"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// RecordHealthObservation inserts a health probe result for an instance.
func (s *VSStore) RecordHealthObservation(ctx context.Context, instanceID, nodeID, status, message string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO virtualserver_health_observations(instance_id,node_id,status,message,observed_at)
		VALUES (?,?,?,?,?)`,
		instanceID, nodeID, status, message, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("record health observation: %w", err)
	}
	return nil
}

// ListHealthObservations returns the most recent health observations for an instance,
// newest first, capped at 200 rows.
func (s *VSStore) ListHealthObservations(ctx context.Context, instanceID string) ([]domain.HealthObservation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,instance_id,node_id,status,message,observed_at
		FROM virtualserver_health_observations
		WHERE instance_id=?
		ORDER BY observed_at DESC LIMIT 200`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list health observations: %w", err)
	}
	defer rows.Close()
	var out []domain.HealthObservation
	for rows.Next() {
		var h domain.HealthObservation
		var observedAt int64
		if err := rows.Scan(&h.ID, &h.InstanceID, &h.NodeID, &h.Status, &h.Message, &observedAt); err != nil {
			return nil, fmt.Errorf("scan health observation: %w", err)
		}
		h.ObservedAt = time.Unix(observedAt, 0)
		out = append(out, h)
	}
	return out, rows.Err()
}
