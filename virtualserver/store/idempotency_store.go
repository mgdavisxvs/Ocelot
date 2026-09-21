package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"
)

const idempotencyTTL = 24 * time.Hour

// IdempotencyRecord is a cached response for a previously seen idempotency key.
type IdempotencyRecord struct {
	Response   string
	StatusCode int
}

// CheckIdempotency looks up a key. Returns (record, true) if already executed.
func (s *VSStore) CheckIdempotency(ctx context.Context, key string) (*IdempotencyRecord, bool, error) {
	hash := hashKey(key)
	row := s.db.QueryRowContext(ctx, `
		SELECT response, status_code, expires_at
		FROM virtualserver_idempotency_keys WHERE key_hash=?`, hash)
	var rec IdempotencyRecord
	var expiresAt int64
	err := row.Scan(&rec.Response, &rec.StatusCode, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if time.Now().Unix() > expiresAt {
		// Expired — treat as not found; cleanup happens on schedule
		return nil, false, nil
	}
	return &rec, true, nil
}

// StoreIdempotency persists the response for a given key.
func (s *VSStore) StoreIdempotency(ctx context.Context, key, response string, statusCode int) error {
	hash := hashKey(key)
	now := time.Now()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO virtualserver_idempotency_keys(key_hash,response,status_code,created_at,expires_at)
		VALUES (?,?,?,?,?)`,
		hash, response, statusCode, now.Unix(), now.Add(idempotencyTTL).Unix(),
	)
	return err
}

// ExpireIdempotencyKeys deletes all keys past their expiry time.
func (s *VSStore) ExpireIdempotencyKeys(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM virtualserver_idempotency_keys WHERE expires_at < ?", time.Now().Unix())
	return err
}

// CreateNamespace creates a new namespace.
func (s *VSStore) CreateNamespace(ctx context.Context, name, description string) (int64, error) {
	now := time.Now().Unix()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO virtualserver_namespaces(name,description,created_at,updated_at)
		VALUES (?,?,?,?)`,
		name, description, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("create namespace: %w", err)
	}
	return res.LastInsertId()
}

// ListNamespaces returns all namespace names.
func (s *VSStore) ListNamespaces(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM virtualserver_namespaces ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", h)
}
