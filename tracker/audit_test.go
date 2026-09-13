package tracker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAuditLoggerCreation(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	al := NewAuditLogger(db)

	if al == nil {
		t.Fatal("NewAuditLogger returned nil")
	}

	if al.db != db {
		t.Error("AuditLogger database not set correctly")
	}
}

func TestAuditLogSuccess(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	ctx := context.Background()
	ctx = context.WithValue(ctx, "user_id", 123)
	ctx = context.WithValue(ctx, "ip", "192.168.1.1")

	err = al.LogSuccess(ctx, "api_key_create", "api_key", "key_123")
	if err != nil {
		t.Fatalf("LogSuccess failed: %v", err)
	}

	// Verify log was written
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM audit_log").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query audit_log: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 audit log entry, got %d", count)
	}

	// Verify details
	var action, resourceType, resourceID string
	var success bool
	var userID int
	var ipAddress string
	err = db.QueryRow(`SELECT action, resource_type, resource_id, success, user_id, ip_address
		FROM audit_log LIMIT 1`).Scan(&action, &resourceType, &resourceID, &success, &userID, &ipAddress)
	if err != nil {
		t.Fatalf("Failed to read audit log: %v", err)
	}

	if action != "api_key_create" {
		t.Errorf("Expected action 'api_key_create', got '%s'", action)
	}

	if !success {
		t.Error("Expected success=true")
	}

	if userID != 123 {
		t.Errorf("Expected user_id 123, got %d", userID)
	}

	if ipAddress != "192.168.1.1" {
		t.Errorf("Expected IP '192.168.1.1', got '%s'", ipAddress)
	}
}

func TestAuditLogFailure(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	ctx := context.Background()
	ctx = context.WithValue(ctx, "user_id", 456)

	testErr := errors.New("authentication failed")
	err = al.LogFailure(ctx, "login", "user", "user_456", testErr)
	if err != nil {
		t.Fatalf("LogFailure failed: %v", err)
	}

	// Verify failure was logged
	var success bool
	var errorMsg string
	err = db.QueryRow("SELECT success, error_message FROM audit_log LIMIT 1").Scan(&success, &errorMsg)
	if err != nil {
		t.Fatalf("Failed to read audit log: %v", err)
	}

	if success {
		t.Error("Expected success=false for failure")
	}

	if errorMsg != "authentication failed" {
		t.Errorf("Expected error message 'authentication failed', got '%s'", errorMsg)
	}
}

func TestAuditLogWithMetadata(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	metadata := map[string]interface{}{
		"permissions": []string{"read", "write"},
		"expires_at":  "2026-12-31",
	}

	ctx := context.Background()
	ctx = context.WithValue(ctx, "audit_metadata", metadata)

	err = al.LogSuccess(ctx, "api_key_update", "api_key", "key_789")
	if err != nil {
		t.Fatalf("LogSuccess failed: %v", err)
	}

	// Verify metadata was stored
	var metadataJSON string
	err = db.QueryRow("SELECT metadata FROM audit_log LIMIT 1").Scan(&metadataJSON)
	if err != nil {
		t.Fatalf("Failed to read metadata: %v", err)
	}

	if metadataJSON == "" || metadataJSON == "{}" {
		t.Error("Expected metadata to be stored")
	}
}

func TestAuditLogQuery(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	// Insert multiple log entries
	for i := 0; i < 5; i++ {
		ctx := context.Background()
		ctx = context.WithValue(ctx, "user_id", 100+i)
		al.LogSuccess(ctx, "test_action", "test_resource", "resource_"+string(rune('A'+i)))
	}

	// Query all logs
	filters := AuditFilters{
		Limit: 10,
	}

	entries, err := al.Query(filters)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(entries) != 5 {
		t.Errorf("Expected 5 entries, got %d", len(entries))
	}
}

func TestAuditLogQueryByUserID(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	// Insert logs for different users
	for i := 0; i < 3; i++ {
		ctx := context.Background()
		ctx = context.WithValue(ctx, "user_id", 100)
		al.LogSuccess(ctx, "action_a", "resource", "r"+string(rune('A'+i)))
	}

	for i := 0; i < 2; i++ {
		ctx := context.Background()
		ctx = context.WithValue(ctx, "user_id", 200)
		al.LogSuccess(ctx, "action_b", "resource", "r"+string(rune('D'+i)))
	}

	// Query by user_id
	userID := 100
	filters := AuditFilters{
		UserID: &userID,
		Limit:  10,
	}

	entries, err := al.Query(filters)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("Expected 3 entries for user 100, got %d", len(entries))
	}

	for _, entry := range entries {
		if entry.UserID == nil || *entry.UserID != 100 {
			t.Error("Returned entry does not belong to user 100")
		}
	}
}

func TestAuditLogQueryByAction(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	ctx := context.Background()

	// Insert different actions
	al.LogSuccess(ctx, "login", "user", "user1")
	al.LogSuccess(ctx, "login", "user", "user2")
	al.LogSuccess(ctx, "logout", "user", "user1")

	// Query by action
	filters := AuditFilters{
		Action: "login",
		Limit:  10,
	}

	entries, err := al.Query(filters)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("Expected 2 'login' entries, got %d", len(entries))
	}

	for _, entry := range entries {
		if entry.Action != "login" {
			t.Errorf("Expected action 'login', got '%s'", entry.Action)
		}
	}
}

func TestAuditLogQueryByTimeRange(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	ctx := context.Background()

	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now.Add(1 * time.Hour)

	// Insert logs
	al.LogSuccess(ctx, "test", "resource", "r1")
	al.LogSuccess(ctx, "test", "resource", "r2")

	// Query by time range
	filters := AuditFilters{
		StartTime: &startTime,
		EndTime:   &endTime,
		Limit:     10,
	}

	entries, err := al.Query(filters)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries in time range, got %d", len(entries))
	}
}

func TestAuditLogQueryLimit(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	ctx := context.Background()

	// Insert 10 logs
	for i := 0; i < 10; i++ {
		al.LogSuccess(ctx, "test", "resource", "r"+string(rune('0'+i)))
	}

	// Query with limit 5
	filters := AuditFilters{
		Limit: 5,
	}

	entries, err := al.Query(filters)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(entries) != 5 {
		t.Errorf("Expected 5 entries (limit), got %d", len(entries))
	}
}

func TestAuditLogWithoutUserID(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	// Context without user_id
	ctx := context.Background()

	err = al.LogSuccess(ctx, "anonymous_action", "resource", "r1")
	if err != nil {
		t.Fatalf("LogSuccess failed: %v", err)
	}

	// Verify user_id is NULL
	var userIDPtr *int
	err = db.QueryRow("SELECT user_id FROM audit_log LIMIT 1").Scan(&userIDPtr)
	if err != nil {
		t.Fatalf("Failed to read user_id: %v", err)
	}

	if userIDPtr != nil {
		t.Errorf("Expected NULL user_id, got %d", *userIDPtr)
	}
}

func TestAuditLogOrderedByTimestamp(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAuditLogTable(db)
	if err != nil {
		t.Fatalf("Failed to create audit log table: %v", err)
	}

	al := NewAuditLogger(db)

	ctx := context.Background()

	// Insert logs with slight delays
	for i := 0; i < 3; i++ {
		al.LogSuccess(ctx, "test", "resource", "r"+string(rune('0'+i)))
		time.Sleep(10 * time.Millisecond)
	}

	filters := AuditFilters{
		Limit: 10,
	}

	entries, err := al.Query(filters)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Verify descending order (most recent first)
	for i := 1; i < len(entries); i++ {
		if entries[i].Timestamp.After(entries[i-1].Timestamp) {
			t.Error("Entries not in descending timestamp order")
		}
	}
}
