package tracker

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newAuditDB opens an in-memory SQLite DB with the audit_log table.
func newAuditDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open audit db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := CreateAuditLogTable(db); err != nil {
		t.Fatalf("CreateAuditLogTable: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ── CreateAuditLogTable ───────────────────────────────────────────────────────

func TestCreateAuditLogTable_Idempotent(t *testing.T) {
	db := newAuditDB(t)
	if err := CreateAuditLogTable(db); err != nil {
		t.Errorf("second CreateAuditLogTable: %v", err)
	}
}

// ── NewAuditLogger ────────────────────────────────────────────────────────────

func TestNewAuditLogger_NotNil(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)
	if al == nil {
		t.Fatal("NewAuditLogger returned nil")
	}
}

// ── Log ───────────────────────────────────────────────────────────────────────

func TestAuditLogger_Log_Success(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)
	ctx := context.Background()

	if err := al.Log(ctx, "create", "torrent", "abc", true, nil); err != nil {
		t.Fatalf("Log: %v", err)
	}

	// Verify row was written.
	var count int
	db.QueryRow("SELECT COUNT(*) FROM audit_log").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestAuditLogger_Log_WithError(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)

	if err := al.Log(context.Background(), "delete", "user", "42", false, errors.New("not found")); err != nil {
		t.Fatalf("Log with error: %v", err)
	}

	var errMsg string
	db.QueryRow("SELECT error_message FROM audit_log WHERE action = 'delete'").Scan(&errMsg)
	if errMsg != "not found" {
		t.Errorf("error_message = %q, want \"not found\"", errMsg)
	}
}

func TestAuditLogger_Log_WithContextUserAndIP(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.WithValue(context.Background(), "user_id", 77)
	ctx = context.WithValue(ctx, "ip", "10.0.0.1")

	if err := al.Log(ctx, "update", "config", "x", true, nil); err != nil {
		t.Fatalf("Log: %v", err)
	}

	var ip string
	var userID *int
	db.QueryRow("SELECT user_id, ip_address FROM audit_log WHERE action = 'update'").Scan(&userID, &ip)
	if userID == nil || *userID != 77 {
		t.Errorf("user_id = %v, want 77", userID)
	}
	if ip != "10.0.0.1" {
		t.Errorf("ip_address = %q, want \"10.0.0.1\"", ip)
	}
}

func TestAuditLogger_Log_WithAuditMetadata(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)

	meta := map[string]interface{}{"key": "value"}
	ctx := context.WithValue(context.Background(), "audit_metadata", meta)

	if err := al.Log(ctx, "action", "resource", "id", true, nil); err != nil {
		t.Fatalf("Log with metadata: %v", err)
	}

	var metadata string
	db.QueryRow("SELECT metadata FROM audit_log WHERE action = 'action'").Scan(&metadata)
	if metadata == "" {
		t.Error("expected non-empty metadata in DB")
	}
}

// ── LogSuccess / LogFailure ───────────────────────────────────────────────────

func TestAuditLogger_LogSuccess(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)

	if err := al.LogSuccess(context.Background(), "read", "torrent", "hash1"); err != nil {
		t.Fatalf("LogSuccess: %v", err)
	}

	var success bool
	db.QueryRow("SELECT success FROM audit_log WHERE action = 'read'").Scan(&success)
	if !success {
		t.Error("LogSuccess should record success=true")
	}
}

func TestAuditLogger_LogFailure(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)

	if err := al.LogFailure(context.Background(), "write", "peer", "p1", errors.New("disk full")); err != nil {
		t.Fatalf("LogFailure: %v", err)
	}

	var success bool
	var errMsg string
	db.QueryRow("SELECT success, error_message FROM audit_log WHERE action = 'write'").Scan(&success, &errMsg)
	if success {
		t.Error("LogFailure should record success=false")
	}
	if errMsg != "disk full" {
		t.Errorf("error_message = %q, want \"disk full\"", errMsg)
	}
}

// ── Query ─────────────────────────────────────────────────────────────────────

func TestAuditLogger_Query_Empty(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)

	entries, err := al.Query(AuditFilters{Limit: 10})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries on empty table, got %d", len(entries))
	}
}

func TestAuditLogger_Query_FilterByAction(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)
	ctx := context.Background()

	al.Log(ctx, "login", "user", "1", true, nil)
	al.Log(ctx, "logout", "user", "1", true, nil)
	al.Log(ctx, "login", "user", "2", false, nil)

	entries, err := al.Query(AuditFilters{Action: "login", Limit: 50})
	if err != nil {
		t.Fatalf("Query by action: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 login entries, got %d", len(entries))
	}
}

func TestAuditLogger_Query_FilterByUserID(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.WithValue(context.Background(), "user_id", 42)
	al.Log(ctx, "act1", "res", "id1", true, nil)
	al.Log(context.Background(), "act2", "res", "id2", true, nil) // no user_id

	uid := 42
	entries, err := al.Query(AuditFilters{UserID: &uid, Limit: 50})
	if err != nil {
		t.Fatalf("Query by user_id: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry for user 42, got %d", len(entries))
	}
}

func TestAuditLogger_Query_FilterByTimeRange(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)

	al.Log(context.Background(), "old_action", "res", "id", true, nil)
	al.Log(context.Background(), "new_action", "res", "id", true, nil)

	// Use start time = now to exclude the already-inserted rows; effectively 0 results.
	future := time.Now().Add(time.Minute)
	entries, err := al.Query(AuditFilters{StartTime: &future, Limit: 50})
	if err != nil {
		t.Fatalf("Query by time: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after future start time, got %d", len(entries))
	}
}

func TestAuditLogger_Query_FilterByResourceType(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)
	ctx := context.Background()

	al.Log(ctx, "act", "torrent", "t1", true, nil)
	al.Log(ctx, "act", "user", "u1", true, nil)

	entries, err := al.Query(AuditFilters{ResourceType: "torrent", Limit: 50})
	if err != nil {
		t.Fatalf("Query by resource_type: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 torrent entry, got %d", len(entries))
	}
}

// ── error paths ───────────────────────────────────────────────────────────────

func TestAuditLogger_Log_DBClosed_ReturnsError(t *testing.T) {
	db := newAuditDB(t)
	db.Close()
	al := NewAuditLogger(db)
	if err := al.Log(context.Background(), "test", "res", "id", true, nil); err == nil {
		t.Error("expected error when DB is closed, got nil")
	}
}

func TestAuditLogger_Query_DBClosed_ReturnsError(t *testing.T) {
	db := newAuditDB(t)
	db.Close()
	al := NewAuditLogger(db)
	if _, err := al.Query(AuditFilters{Limit: 10}); err == nil {
		t.Error("expected error when DB is closed, got nil")
	}
}

func TestAuditLogger_Query_FilterByEndTime(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)
	ctx := context.Background()

	al.Log(ctx, "old_act", "res", "id", true, nil)

	// EndTime in the past → should exclude the row we just inserted
	past := time.Now().Add(-time.Minute)
	entries, err := al.Query(AuditFilters{EndTime: &past, Limit: 50})
	if err != nil {
		t.Fatalf("Query by end time: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries before past end time, got %d", len(entries))
	}
}

// TestAuditLogger_Query_ScanError_SkipsRow covers the continue path at
// audit.go:166 by inserting a row whose timestamp column holds a
// non-numeric string, causing rows.Scan to fail and the row to be skipped.
// One valid row is also inserted so entries is non-empty if scan succeeds.
func TestAuditLogger_Query_ScanError_SkipsRow(t *testing.T) {
	db := newAuditDB(t)
	al := NewAuditLogger(db)
	ctx := context.Background()

	// Insert a valid row first.
	al.Log(ctx, "good_action", "res", "id", true, nil)

	// Insert a corrupted row with a text timestamp that cannot be scanned
	// into int64 — rows.Scan will return an error and the row is skipped.
	_, err := db.Exec(`INSERT INTO audit_log
		(timestamp, user_id, action, resource_type, resource_id,
		 ip_address, success, error_message, metadata)
		VALUES ('not_a_timestamp', NULL, 'bad', 'res', 'id', '127.0.0.1', 1, '', '{}')`)
	if err != nil {
		t.Fatalf("insert corrupted row: %v", err)
	}

	entries, queryErr := al.Query(AuditFilters{Limit: 50})
	if queryErr != nil {
		t.Fatalf("Query: %v", queryErr)
	}
	// Only the valid row should be in the result; the bad row is skipped.
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (bad row skipped), got %d", len(entries))
	}
}
