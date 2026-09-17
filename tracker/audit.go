package tracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"
)

// AuditLogger logs administrative actions for security auditing.
// dbFn is called on every write so the logger always targets the current shard
// even after a rotation.
type AuditLogger struct {
	dbFn     func() *sql.DB
	shardsFn func() []*sql.DB // optional; enables cross-shard queries (M3 fix)
	logger   *Logger
}

// NewAuditLogger creates a new audit logger. Pass SQLiteShardManager.CurrentDB
// as dbFn so writes always land in the active shard.
func NewAuditLogger(dbFn func() *sql.DB) *AuditLogger {
	return &AuditLogger{
		dbFn:   dbFn,
		logger: GetDefaultLogger(),
	}
}

// SetShardsFn registers a function that returns all known DB shards so that
// QueryAllShards can search historical audit entries beyond the current shard.
func (al *AuditLogger) SetShardsFn(fn func() []*sql.DB) {
	al.shardsFn = fn
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

	db := al.dbFn()
	if db == nil {
		return nil // DB not yet ready; silently skip
	}
	_, dbErr := db.Exec(query,
		time.Now().UnixNano(), // nanosecond precision (S5 fix)
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

// LogSuccess logs a successful action
func (al *AuditLogger) LogSuccess(ctx context.Context, action, resourceType, resourceID string) error {
	return al.Log(ctx, action, resourceType, resourceID, true, nil)
}

// LogFailure logs a failed action
func (al *AuditLogger) LogFailure(ctx context.Context, action, resourceType, resourceID string, err error) error {
	return al.Log(ctx, action, resourceType, resourceID, false, err)
}


// QueryAllShards queries audit logs across every known DB shard and merges the
// results in descending timestamp order. Falls back to the current shard if no
// shardsFn has been registered. Duplicate IDs from the same shard are not
// possible but IDs are shard-local and may overlap between shards; entries are
// identified by (shard, id) in practice but only Timestamp + fields are returned.
func (al *AuditLogger) QueryAllShards(filters AuditFilters) ([]*AuditEntry, error) {
	var dbs []*sql.DB
	if al.shardsFn != nil {
		dbs = al.shardsFn()
	}
	if len(dbs) == 0 {
		// Fallback: query only the current shard (same as Query).
		if db := al.dbFn(); db != nil {
			dbs = []*sql.DB{db}
		}
	}

	var all []*AuditEntry
	for _, db := range dbs {
		if db == nil {
			continue
		}
		entries, err := al.queryDB(db, filters)
		if err != nil {
			al.logger.Error("audit shard query failed", err)
			continue
		}
		all = append(all, entries...)
	}

	// Merge sort by timestamp descending.
	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.After(all[j].Timestamp)
	})

	if filters.Limit > 0 && len(all) > filters.Limit {
		all = all[:filters.Limit]
	}
	return all, nil
}

// Query retrieves audit logs from the current shard only.
// Use QueryAllShards to search across all historical shards.
func (al *AuditLogger) Query(filters AuditFilters) ([]*AuditEntry, error) {
	db := al.dbFn()
	if db == nil {
		return []*AuditEntry{}, nil
	}
	return al.queryDB(db, filters)
}

// queryDB executes a filtered audit query against a specific *sql.DB.
func (al *AuditLogger) queryDB(db *sql.DB, filters AuditFilters) ([]*AuditEntry, error) {
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
		args = append(args, filters.StartTime.UnixNano())
	}
	if filters.EndTime != nil {
		query += " AND timestamp <= ?"
		args = append(args, filters.EndTime.UnixNano())
	}
	query += " ORDER BY timestamp DESC LIMIT ?"
	args = append(args, filters.Limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*AuditEntry
	for rows.Next() {
		var entry AuditEntry
		var timestampNano int64
		var userIDPtr *int
		var metadataJSON string

		if err := rows.Scan(
			&entry.ID, &timestampNano, &userIDPtr,
			&entry.Action, &entry.ResourceType, &entry.ResourceID,
			&entry.IPAddress, &entry.Success, &entry.ErrorMessage, &metadataJSON,
		); err != nil {
			continue
		}

		entry.Timestamp = time.Unix(0, timestampNano)
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

