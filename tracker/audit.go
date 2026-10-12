package tracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// AuditLogger logs administrative actions for security auditing
type AuditLogger struct {
	db     *sql.DB
	logger *Logger
}

// NewAuditLogger creates a new audit logger
func NewAuditLogger(db *sql.DB) *AuditLogger {
	return &AuditLogger{
		db:     db,
		logger: GetDefaultLogger(),
	}
}

// AuditEntry represents a single audit log entry
type AuditEntry struct {
	ID           int64
	Timestamp    time.Time
	UserID       *int
	Action       string
	ResourceType string
	ResourceID   string
	IPAddress    string
	Success      bool
	ErrorMessage string
	Metadata     map[string]interface{}
}

// Log records an audit entry
func (al *AuditLogger) Log(ctx context.Context, action, resourceType, resourceID string, success bool, err error) error {
	userID := al.getUserIDFromContext(ctx)
	ip := al.getIPFromContext(ctx)

	errorMsg := ""
	if err != nil {
		errorMsg = err.Error()
	}

	// Extract metadata from context if available
	metadata := make(map[string]interface{})
	if meta, ok := ctx.Value("audit_metadata").(map[string]interface{}); ok {
		metadata = meta
	}

	metadataJSON, _ := json.Marshal(metadata)

	query := `INSERT INTO audit_log
		(timestamp, user_id, action, resource_type, resource_id, ip_address, success, error_message, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, dbErr := al.db.Exec(query,
		time.Now().Unix(),
		userID,
		action,
		resourceType,
		resourceID,
		ip,
		success,
		errorMsg,
		string(metadataJSON),
	)

	if dbErr != nil {
		al.logger.Error("failed to write audit log", dbErr,
			"action", action,
			"resource_type", resourceType,
			"resource_id", resourceID,
		)
		return dbErr
	}

	al.logger.Info("audit log recorded",
		"action", action,
		"resource_type", resourceType,
		"resource_id", resourceID,
		"success", success,
		"user_id", userID,
		"ip", ip,
	)

	return nil
}

// Query retrieves audit logs with filters
func (al *AuditLogger) Query(filters AuditFilters) ([]*AuditEntry, error) {
	query := `SELECT id, timestamp, user_id, action, resource_type, resource_id,
		ip_address, success, error_message, metadata
		FROM audit_log WHERE 1=1`

	args := []interface{}{}

	if filters.UserID != nil {
		query += " AND user_id = ?"
		args = append(args, *filters.UserID)
	}

	if filters.Action != "" {
		query += " AND action = ?"
		args = append(args, filters.Action)
	}

	if filters.ResourceType != "" {
		query += " AND resource_type = ?"
		args = append(args, filters.ResourceType)
	}

	if filters.StartTime != nil {
		query += " AND timestamp >= ?"
		args = append(args, filters.StartTime.Unix())
	}

	if filters.EndTime != nil {
		query += " AND timestamp <= ?"
		args = append(args, filters.EndTime.Unix())
	}

	query += " ORDER BY timestamp DESC LIMIT ?"
	args = append(args, filters.Limit)

	rows, err := al.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []*AuditEntry{}
	for rows.Next() {
		var entry AuditEntry
		var timestampUnix int64
		var userIDPtr *int
		var metadataJSON string

		err := rows.Scan(
			&entry.ID,
			&timestampUnix,
			&userIDPtr,
			&entry.Action,
			&entry.ResourceType,
			&entry.ResourceID,
			&entry.IPAddress,
			&entry.Success,
			&entry.ErrorMessage,
			&metadataJSON,
		)

		if err != nil {
			continue
		}

		entry.Timestamp = time.Unix(timestampUnix, 0)
		entry.UserID = userIDPtr

		if metadataJSON != "" {
			json.Unmarshal([]byte(metadataJSON), &entry.Metadata)
		}

		entries = append(entries, &entry)
	}

	return entries, nil
}

// AuditFilters defines query filters for audit logs
type AuditFilters struct {
	UserID       *int
	Action       string
	ResourceType string
	StartTime    *time.Time
	EndTime      *time.Time
	Limit        int
}

func (al *AuditLogger) getUserIDFromContext(ctx context.Context) *int {
	if userID, ok := ctx.Value("user_id").(int); ok {
		return &userID
	}
	return nil
}

func (al *AuditLogger) getIPFromContext(ctx context.Context) string {
	if ip, ok := ctx.Value("ip").(string); ok {
		return ip
	}
	return ""
}

// CreateAuditLogTable creates the audit_log table
func CreateAuditLogTable(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL,
		user_id INTEGER,
		action TEXT NOT NULL,
		resource_type TEXT NOT NULL,
		resource_id TEXT,
		ip_address TEXT,
		success BOOLEAN NOT NULL,
		error_message TEXT,
		metadata TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp);
	CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_log(user_id);
	CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action);
	CREATE INDEX IF NOT EXISTS idx_audit_resource ON audit_log(resource_type, resource_id);`

	_, err := db.Exec(query)
	return err
}

// LogSuccess records a successful operation. Convenience wrapper around Log.
func (al *AuditLogger) LogSuccess(ctx context.Context, action, resourceType, resourceID string) error {
	return al.Log(ctx, action, resourceType, resourceID, true, nil)
}

// LogFailure records a failed operation. Convenience wrapper around Log.
func (al *AuditLogger) LogFailure(ctx context.Context, action, resourceType, resourceID string, err error) error {
	return al.Log(ctx, action, resourceType, resourceID, false, err)
}
