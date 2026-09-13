package tracker

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestGenerateToken(t *testing.T) {
	config := AuthConfig{
		JWTSecret:     []byte("test-secret-key-for-testing"),
		TokenDuration: 1 * time.Hour,
	}

	token, err := GenerateToken(123, "admin", config)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	if token == "" {
		t.Error("Generated token is empty")
	}

	// Token should be a valid JWT (3 parts separated by dots)
	parts := len(splitToken(token))
	if parts != 3 {
		t.Errorf("Expected 3 token parts, got %d", parts)
	}
}

func TestValidateToken(t *testing.T) {
	secret := []byte("test-secret-key-for-validation")
	config := AuthConfig{
		JWTSecret:     secret,
		TokenDuration: 1 * time.Hour,
	}

	// Generate a valid token
	token, err := GenerateToken(456, "user", config)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	// Validate the token
	claims, err := ValidateToken(token, secret)
	if err != nil {
		t.Fatalf("Token validation failed: %v", err)
	}

	if claims.UserID != 456 {
		t.Errorf("Expected UserID 456, got %d", claims.UserID)
	}

	if claims.Role != "user" {
		t.Errorf("Expected role 'user', got '%s'", claims.Role)
	}

	// Check expiration is set correctly
	if claims.ExpiresAt == nil {
		t.Error("Token ExpiresAt is nil")
	} else {
		expectedExpiry := time.Now().Add(1 * time.Hour)
		if claims.ExpiresAt.Time.Before(time.Now()) {
			t.Error("Token is already expired")
		}
		if claims.ExpiresAt.Time.After(expectedExpiry.Add(1 * time.Minute)) {
			t.Error("Token expiry is too far in the future")
		}
	}
}

func TestValidateTokenInvalidSecret(t *testing.T) {
	correctSecret := []byte("correct-secret")
	wrongSecret := []byte("wrong-secret")

	config := AuthConfig{
		JWTSecret:     correctSecret,
		TokenDuration: 1 * time.Hour,
	}

	token, err := GenerateToken(789, "moderator", config)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	// Try to validate with wrong secret
	_, err = ValidateToken(token, wrongSecret)
	if err == nil {
		t.Error("Expected validation to fail with wrong secret")
	}
}

func TestValidateTokenExpired(t *testing.T) {
	secret := []byte("expiry-test-secret")
	config := AuthConfig{
		JWTSecret:     secret,
		TokenDuration: 1 * time.Nanosecond, // Expires immediately
	}

	token, err := GenerateToken(999, "guest", config)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	// Wait for token to expire
	time.Sleep(10 * time.Millisecond)

	// Validation should fail
	_, err = ValidateToken(token, secret)
	if err == nil {
		t.Error("Expected validation to fail for expired token")
	}
}

func TestHashAPIKey(t *testing.T) {
	key1 := "test-api-key-123"
	key2 := "test-api-key-456"

	hash1 := hashAPIKey(key1)
	hash2 := hashAPIKey(key2)

	// Hashes should be deterministic
	if hashAPIKey(key1) != hash1 {
		t.Error("Hash is not deterministic")
	}

	// Different keys should produce different hashes
	if hash1 == hash2 {
		t.Error("Different keys produced same hash")
	}

	// Hash should be 64 characters (SHA256 hex)
	if len(hash1) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash1))
	}
}

func TestCreateAPIKey(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	// Create API keys table
	err := CreateAPIKeysTable(db)
	if err != nil {
		t.Fatalf("Failed to create api_keys table: %v", err)
	}

	userID := 100
	permissions := []string{"read", "write"}
	expiresAt := timePtr(time.Now().Add(24 * time.Hour))

	key, err := CreateAPIKey(db, userID, permissions, expiresAt)
	if err != nil {
		t.Fatalf("Failed to create API key: %v", err)
	}

	if key == "" {
		t.Error("Generated API key is empty")
	}

	// Verify key was stored (hash should exist in database)
	hash := hashAPIKey(key)
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM api_keys WHERE key_hash = ?", hash).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query database: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 API key record, found %d", count)
	}
}

func TestValidateAPIKey(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAPIKeysTable(db)
	if err != nil {
		t.Fatalf("Failed to create api_keys table: %v", err)
	}

	// Create a valid API key
	userID := 200
	permissions := []string{"admin", "delete"}
	expiresAt := timePtr(time.Now().Add(48 * time.Hour))

	key, err := CreateAPIKey(db, userID, permissions, expiresAt)
	if err != nil {
		t.Fatalf("Failed to create API key: %v", err)
	}

	// Validate the key
	apiKey, err := ValidateAPIKey(db, key)
	if err != nil {
		t.Fatalf("API key validation failed: %v", err)
	}

	if apiKey.UserID != userID {
		t.Errorf("Expected UserID %d, got %d", userID, apiKey.UserID)
	}

	if len(apiKey.Permissions) != 2 {
		t.Errorf("Expected 2 permissions, got %d", len(apiKey.Permissions))
	}

	if apiKey.Revoked {
		t.Error("API key should not be revoked")
	}
}

func TestValidateAPIKeyInvalid(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAPIKeysTable(db)
	if err != nil {
		t.Fatalf("Failed to create api_keys table: %v", err)
	}

	// Try to validate a non-existent key
	_, err = ValidateAPIKey(db, "invalid-key-that-does-not-exist")
	if err == nil {
		t.Error("Expected validation to fail for invalid API key")
	}
}

func TestValidateAPIKeyRevoked(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAPIKeysTable(db)
	if err != nil {
		t.Fatalf("Failed to create api_keys table: %v", err)
	}

	// Create and then revoke a key
	userID := 300
	permissions := []string{"read"}
	key, err := CreateAPIKey(db, userID, permissions, nil)
	if err != nil {
		t.Fatalf("Failed to create API key: %v", err)
	}

	// Revoke the key
	hash := hashAPIKey(key)
	_, err = db.Exec("UPDATE api_keys SET revoked = 1 WHERE key_hash = ?", hash)
	if err != nil {
		t.Fatalf("Failed to revoke key: %v", err)
	}

	// Validation should fail
	_, err = ValidateAPIKey(db, key)
	if err == nil {
		t.Error("Expected validation to fail for revoked API key")
	}
}

func TestValidateAPIKeyExpired(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	err := CreateAPIKeysTable(db)
	if err != nil {
		t.Fatalf("Failed to create api_keys table: %v", err)
	}

	// Create a key that expires in the past
	userID := 400
	permissions := []string{"write"}
	expiresAt := timePtr(time.Now().Add(-1 * time.Hour)) // Already expired

	key, err := CreateAPIKey(db, userID, permissions, expiresAt)
	if err != nil {
		t.Fatalf("Failed to create API key: %v", err)
	}

	// Validation should fail
	_, err = ValidateAPIKey(db, key)
	if err == nil {
		t.Error("Expected validation to fail for expired API key")
	}
}

const keyCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func TestGenerateRandomKeyLength(t *testing.T) {
	for _, length := range []int{1, 16, 32, 64, 128} {
		key, err := generateRandomKey(length)
		if err != nil {
			t.Fatalf("generateRandomKey(%d) failed: %v", length, err)
		}
		if len(key) != length {
			t.Errorf("generateRandomKey(%d) returned %d characters", length, len(key))
		}
	}
}

func TestGenerateRandomKeyUsesOnlyCharset(t *testing.T) {
	key, err := generateRandomKey(512)
	if err != nil {
		t.Fatalf("generateRandomKey failed: %v", err)
	}

	for i, c := range key {
		if !strings.ContainsRune(keyCharset, c) {
			t.Fatalf("character %q at index %d is outside the charset", c, i)
		}
	}
}

func TestGenerateRandomKeysAreDistinct(t *testing.T) {
	const iterations = 2000

	seen := make(map[string]struct{}, iterations)
	for i := 0; i < iterations; i++ {
		key, err := generateRandomKey(32)
		if err != nil {
			t.Fatalf("generateRandomKey failed: %v", err)
		}
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("generated a duplicate key after %d iterations: %q", i, key)
		}
		seen[key] = struct{}{}
	}
}

// The previous generator derived every byte from time.Now().UnixNano() mod 62
// inside a tight loop, which clusters output rather than spreading it evenly.
// Rejection sampling over crypto/rand should be close to uniform.
func TestGenerateRandomKeyIsUniformlyDistributed(t *testing.T) {
	const sampleSize = 12400 // 200 expected occurrences per character

	key, err := generateRandomKey(sampleSize)
	if err != nil {
		t.Fatalf("generateRandomKey failed: %v", err)
	}

	counts := make(map[rune]int, len(keyCharset))
	for _, c := range key {
		counts[c]++
	}

	if len(counts) != len(keyCharset) {
		t.Errorf("only %d of %d characters appeared in %d draws",
			len(counts), len(keyCharset), sampleSize)
	}

	// Expected 200 per character, standard deviation about 14. These bounds
	// are several standard deviations out, so uniform output passes reliably
	// while a clustered generator fails.
	for _, c := range keyCharset {
		count := counts[c]
		if count < 100 || count > 350 {
			t.Errorf("character %q appeared %d times, want roughly 200", c, count)
		}
	}
}

func TestGenerateRandomKeyZeroLength(t *testing.T) {
	key, err := generateRandomKey(0)
	if err != nil {
		t.Fatalf("generateRandomKey(0) failed: %v", err)
	}
	if key != "" {
		t.Errorf("generateRandomKey(0) = %q, want an empty string", key)
	}
}

// Helper functions

func createTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	return db
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func splitToken(token string) []string {
	var parts []string
	current := ""
	for _, char := range token {
		if char == '.' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(char)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
