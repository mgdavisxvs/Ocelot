package tracker

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestAuditDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := CreateAuditLogTable(db); err != nil {
		t.Fatalf("CreateAuditLogTable: %v", err)
	}
	return db
}

func TestCreateAuditLogTable_Idempotent(t *testing.T) {
	db := newTestAuditDB(t)
	// Calling twice should not error (IF NOT EXISTS semantics).
	if err := CreateAuditLogTable(db); err != nil {
		t.Errorf("second CreateAuditLogTable call returned error: %v", err)
	}
}

func TestAuditLogger_LogSuccess(t *testing.T) {
	db := newTestAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.Background()
	if err := al.Log(ctx, "add_torrent", "torrent", "42", true, nil); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='add_torrent' AND success=1").Scan(&count) //nolint:errcheck
	if count != 1 {
		t.Errorf("expected 1 audit row, got %d", count)
	}
}

func TestAuditLogger_LogFailure(t *testing.T) {
	db := newTestAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.Background()
	fakeErr := errors.New("db timeout")
	if err := al.Log(ctx, "delete_torrent", "torrent", "99", false, fakeErr); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	var errMsg string
	db.QueryRow("SELECT error_message FROM audit_log WHERE action='delete_torrent'").Scan(&errMsg) //nolint:errcheck
	if errMsg != fakeErr.Error() {
		t.Errorf("error_message = %q, want %q", errMsg, fakeErr.Error())
	}
}

func TestAuditLogger_LogSuccess_Helper(t *testing.T) {
	db := newTestAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.Background()
	if err := al.LogSuccess(ctx, "add_user", "user", "7"); err != nil {
		t.Fatalf("LogSuccess: %v", err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='add_user' AND success=1").Scan(&count) //nolint:errcheck
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestAuditLogger_LogFailure_Helper(t *testing.T) {
	db := newTestAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.Background()
	if err := al.LogFailure(ctx, "add_user", "user", "8", errors.New("invalid")); err != nil {
		t.Fatalf("LogFailure: %v", err)
	}

	var success bool
	db.QueryRow("SELECT success FROM audit_log WHERE action='add_user'").Scan(&success) //nolint:errcheck
	if success {
		t.Error("LogFailure should record success=false")
	}
}

func TestAuditLogger_Query_FilterByAction(t *testing.T) {
	db := newTestAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.Background()
	al.Log(ctx, "add_torrent", "torrent", "1", true, nil)  //nolint:errcheck
	al.Log(ctx, "delete_torrent", "torrent", "2", true, nil) //nolint:errcheck
	al.Log(ctx, "add_torrent", "torrent", "3", false, errors.New("x")) //nolint:errcheck

	entries, err := al.Query(AuditFilters{Action: "add_torrent", Limit: 100})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("Query(add_torrent) returned %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		if e.Action != "add_torrent" {
			t.Errorf("unexpected action %q", e.Action)
		}
	}
}

func TestAuditLogger_Query_FilterByResourceType(t *testing.T) {
	db := newTestAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.Background()
	al.Log(ctx, "add_torrent", "torrent", "1", true, nil) //nolint:errcheck
	al.Log(ctx, "add_user", "user", "2", true, nil)       //nolint:errcheck

	entries, err := al.Query(AuditFilters{ResourceType: "user", Limit: 100})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("Query(user) = %d, want 1", len(entries))
	}
}

func TestAuditLogger_Query_TimeRange(t *testing.T) {
	db := newTestAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.Background()
	al.Log(ctx, "act", "res", "1", true, nil) //nolint:errcheck

	start := time.Now().Add(-time.Minute)
	end := time.Now().Add(time.Minute)
	entries, err := al.Query(AuditFilters{StartTime: &start, EndTime: &end, Limit: 100})
	if err != nil {
		t.Fatalf("Query with time range: %v", err)
	}
	if len(entries) < 1 {
		t.Errorf("expected ≥1 entries in time range, got %d", len(entries))
	}
}

func TestAuditLogger_Query_Limit(t *testing.T) {
	db := newTestAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		al.Log(ctx, "act", "res", "1", true, nil) //nolint:errcheck
	}

	entries, err := al.Query(AuditFilters{Limit: 3})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("Limit=3 returned %d entries", len(entries))
	}
}

func TestAuditLogger_ContextIPAndUserID(t *testing.T) {
	db := newTestAuditDB(t)
	al := NewAuditLogger(db)

	ctx := context.WithValue(context.Background(), "ip", "10.0.0.1")
	ctx = context.WithValue(ctx, "user_id", 42)

	if err := al.Log(ctx, "change_passkey", "user", "pk1", true, nil); err != nil {
		t.Fatalf("Log: %v", err)
	}

	var ip string
	var userID *int
	row := db.QueryRow("SELECT ip_address, user_id FROM audit_log WHERE action='change_passkey'")
	row.Scan(&ip, &userID) //nolint:errcheck
	if ip != "10.0.0.1" {
		t.Errorf("ip_address = %q, want \"10.0.0.1\"", ip)
	}
	if userID == nil || *userID != 42 {
		t.Errorf("user_id = %v, want 42", userID)
	}
}
