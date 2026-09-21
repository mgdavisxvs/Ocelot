package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// CreateService persists a validated service manifest. Returns the assigned integer ID.
func (s *VSStore) CreateService(ctx context.Context, m domain.ServiceManifest) (int64, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return 0, fmt.Errorf("marshal manifest: %w", err)
	}
	now := time.Now().Unix()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO virtualserver_services
		(namespace,name,manifest_json,desired_count,state,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?)`,
		m.Metadata.Namespace, m.Metadata.Name, string(data),
		m.Spec.Instances, "declared", now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert service: %w", err)
	}
	id, _ := res.LastInsertId()

	// Record version 1
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO virtualserver_service_versions(service_id,version,manifest_json,created_at)
		VALUES (?,?,?,?)`,
		id, 1, string(data), now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert service version: %w", err)
	}
	return id, nil
}

// GetService retrieves a service by integer ID.
func (s *VSStore) GetService(ctx context.Context, id int64) (*domain.Service, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,namespace,name,manifest_json,desired_count,state,created_at,updated_at
		FROM virtualserver_services WHERE id=?`, id)
	return scanService(row)
}

// GetServiceByName retrieves a service by namespace + name.
func (s *VSStore) GetServiceByName(ctx context.Context, namespace, name string) (*domain.Service, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,namespace,name,manifest_json,desired_count,state,created_at,updated_at
		FROM virtualserver_services WHERE namespace=? AND name=?`, namespace, name)
	svc, err := scanService(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return svc, err
}

// ListServices returns all services, optionally filtered by namespace.
func (s *VSStore) ListServices(ctx context.Context, namespace string) ([]domain.Service, error) {
	var rows *sql.Rows
	var err error
	if namespace != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id,namespace,name,manifest_json,desired_count,state,created_at,updated_at
			FROM virtualserver_services WHERE namespace=? ORDER BY name`, namespace)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id,namespace,name,manifest_json,desired_count,state,created_at,updated_at
			FROM virtualserver_services ORDER BY namespace, name`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var svcs []domain.Service
	for rows.Next() {
		svc, err := scanServiceRow(rows)
		if err != nil {
			return nil, err
		}
		svcs = append(svcs, *svc)
	}
	return svcs, rows.Err()
}

// UpdateService replaces the manifest for an existing service and increments version.
func (s *VSStore) UpdateService(ctx context.Context, id int64, m domain.ServiceManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	now := time.Now().Unix()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Determine next version number
	var maxVer int
	s.db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version),0) FROM virtualserver_service_versions WHERE service_id=?", id,
	).Scan(&maxVer)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		UPDATE virtualserver_services
		SET manifest_json=?, desired_count=?, updated_at=?
		WHERE id=?`,
		string(data), m.Spec.Instances, now, id,
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO virtualserver_service_versions(service_id,version,manifest_json,created_at)
		VALUES (?,?,?,?)`,
		id, maxVer+1, string(data), now,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteService removes a service by ID. Fails if active instances exist.
func (s *VSStore) DeleteService(ctx context.Context, id int64) error {
	var active int
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM virtualserver_instances
		WHERE service_id=? AND state NOT IN ('terminated','failed')`, id,
	).Scan(&active)
	if active > 0 {
		return fmt.Errorf("cannot delete service with %d active instance(s)", active)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM virtualserver_service_versions WHERE service_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM virtualserver_services WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

type serviceScanner interface {
	Scan(dest ...interface{}) error
}

func scanService(r serviceScanner) (*domain.Service, error) {
	return scanServiceFields(r)
}

func scanServiceRow(rows *sql.Rows) (*domain.Service, error) {
	return scanServiceFields(rows)
}

func scanServiceFields(r serviceScanner) (*domain.Service, error) {
	var svc domain.Service
	var manifestJSON string
	var createdAt, updatedAt int64
	if err := r.Scan(&svc.ID, &svc.Manifest.Metadata.Namespace, &svc.Manifest.Metadata.Name,
		&manifestJSON, &svc.DesiredCount, &svc.State, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(manifestJSON), &svc.Manifest); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	svc.CreatedAt = time.Unix(createdAt, 0)
	svc.UpdatedAt = time.Unix(updatedAt, 0)
	return &svc, nil
}
