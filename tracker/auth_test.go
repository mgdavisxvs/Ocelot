package tracker

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newAuthDB opens an in-memory SQLite DB with the api_keys table created.
func newAuthDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := CreateAPIKeysTable(db); err != nil {
		t.Fatalf("CreateAPIKeysTable: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ── GenerateToken / ValidateToken ─────────────────────────────────────────────

func TestGenerateToken_Valid(t *testing.T) {
	cfg := AuthConfig{
		JWTSecret:     []byte("test-secret"),
		TokenDuration: time.Hour,
	}
	token, err := GenerateToken(42, "admin", cfg)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if token == "" {
		t.Fatal("GenerateToken returned empty string")
	}
}

func TestValidateToken_RoundTrip(t *testing.T) {
	secret := []byte("round-trip-secret")
	cfg := AuthConfig{
		JWTSecret:     secret,
		TokenDuration: time.Hour,
	}
	token, err := GenerateToken(7, "user", cfg)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	claims, err := ValidateToken(token, secret)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.UserID != 7 {
		t.Errorf("UserID = %d, want 7", claims.UserID)
	}
	if claims.Role != "user" {
		t.Errorf("Role = %q, want \"user\"", claims.Role)
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	cfg := AuthConfig{JWTSecret: []byte("secret-a"), TokenDuration: time.Hour}
	token, _ := GenerateToken(1, "user", cfg)

	_, err := ValidateToken(token, []byte("secret-b"))
	if err == nil {
		t.Error("expected error when validating with wrong secret")
	}
}

func TestValidateToken_Expired(t *testing.T) {
	cfg := AuthConfig{
		JWTSecret:     []byte("expire-secret"),
		TokenDuration: -time.Second, // already expired
	}
	token, err := GenerateToken(1, "user", cfg)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	_, err = ValidateToken(token, cfg.JWTSecret)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestValidateToken_Garbage(t *testing.T) {
	_, err := ValidateToken("this.is.not.a.valid.jwt", []byte("secret"))
	if err == nil {
		t.Error("expected error for garbage token string")
	}
}

// ── hashAPIKey ────────────────────────────────────────────────────────────────

func TestHashAPIKey_Deterministic(t *testing.T) {
	h1 := hashAPIKey("my-api-key")
	h2 := hashAPIKey("my-api-key")
	if h1 != h2 {
		t.Error("hashAPIKey is not deterministic")
	}
}

func TestHashAPIKey_DifferentKeys(t *testing.T) {
	h1 := hashAPIKey("key-one")
	h2 := hashAPIKey("key-two")
	if h1 == h2 {
		t.Error("different keys produced same hash")
	}
}

func TestHashAPIKey_Length(t *testing.T) {
	h := hashAPIKey("anything")
	// SHA256 hex = 64 chars
	if len(h) != 64 {
		t.Errorf("hash length = %d, want 64", len(h))
	}
}

// ── generateRandomKey ─────────────────────────────────────────────────────────

func TestGenerateRandomKey_Length(t *testing.T) {
	k := generateRandomKey(32)
	if len(k) != 32 {
		t.Errorf("key length = %d, want 32", len(k))
	}
}

func TestGenerateRandomKey_Charset(t *testing.T) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	k := generateRandomKey(64)
	for _, c := range k {
		found := false
		for _, valid := range charset {
			if c == valid {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("character %q not in charset", c)
		}
	}
}

// ── CreateAPIKey / ValidateAPIKey ─────────────────────────────────────────────

func TestCreateAPIKey_ReturnsKey(t *testing.T) {
	db := newAuthDB(t)
	key, err := CreateAPIKey(db, 1, []string{"read", "write"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if key == "" {
		t.Error("CreateAPIKey returned empty key")
	}
}

func TestCreateAPIKey_MultipleKeys_Distinct(t *testing.T) {
	db := newAuthDB(t)
	k1, _ := CreateAPIKey(db, 2, []string{"read"}, nil)
	k2, _ := CreateAPIKey(db, 2, []string{"read"}, nil)
	// Keys should be distinct (with overwhelming probability, even with the
	// time-seeded generator they use different nano-second samples).
	if k1 == k2 {
		t.Error("two CreateAPIKey calls returned the same key")
	}
}

func TestValidateAPIKey_InvalidKey(t *testing.T) {
	db := newAuthDB(t)
	_, err := ValidateAPIKey(db, "completely-invalid-key")
	if err == nil {
		t.Error("expected error for non-existent API key")
	}
}

func TestValidateAPIKey_RevokedKey(t *testing.T) {
	db := newAuthDB(t)
	key, _ := CreateAPIKey(db, 5, []string{"read"}, nil)
	keyHash := hashAPIKey(key)

	// Manually revoke the key.
	db.Exec("UPDATE api_keys SET revoked = 1 WHERE key_hash = ?", keyHash)

	_, err := ValidateAPIKey(db, key)
	if err == nil {
		t.Error("expected error for revoked API key")
	}
}

func TestValidateAPIKey_ExpiredKey(t *testing.T) {
	db := newAuthDB(t)
	past := time.Now().Add(-time.Hour)
	key, _ := CreateAPIKey(db, 6, []string{"read"}, &past)

	_, err := ValidateAPIKey(db, key)
	if err == nil {
		t.Error("expected error for expired API key")
	}
}

// ── CreateAPIKeysTable ────────────────────────────────────────────────────────

func TestCreateAPIKeysTable_Idempotent(t *testing.T) {
	db := newAuthDB(t) // already called once inside newAuthDB
	// Calling again must not fail (IF NOT EXISTS).
	if err := CreateAPIKeysTable(db); err != nil {
		t.Errorf("second CreateAPIKeysTable: %v", err)
	}
}

func TestValidateAPIKey_DBClosed_ReturnsError(t *testing.T) {
	db := newAuthDB(t)
	db.Close() // force a DB-level error on QueryRow
	_, err := ValidateAPIKey(db, "some-api-key")
	if err == nil {
		t.Error("expected error when DB is closed")
	}
}

func TestCreateAPIKey_DBClosed_ReturnsError(t *testing.T) {
	db := newAuthDB(t)
	db.Close()
	_, err := CreateAPIKey(db, 1, []string{"read"}, nil)
	if err == nil {
		t.Error("expected error when DB is closed, got nil")
	}
}

// ── AuthMiddleware ────────────────────────────────────────────────────────────

func TestAuthMiddleware_NoAuth_Returns401(t *testing.T) {
	db := newAuthDB(t)
	cfg := AuthConfig{JWTSecret: []byte("testsecret"), TokenDuration: time.Hour}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware(cfg, db)(next)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidAPIKey_Returns401(t *testing.T) {
	db := newAuthDB(t)
	cfg := AuthConfig{JWTSecret: []byte("testsecret"), TokenDuration: time.Hour}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware(cfg, db)(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "not-in-db-api-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid API key, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidBearerToken_Returns401(t *testing.T) {
	db := newAuthDB(t)
	cfg := AuthConfig{JWTSecret: []byte("testsecret"), TokenDuration: time.Hour}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware(cfg, db)(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.jwt")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid bearer, got %d", rec.Code)
	}
}

func TestValidateAPIKey_FutureExpiry_Succeeds(t *testing.T) {
	db := newAuthDB(t)
	future := time.Now().Add(time.Hour)
	key, err := CreateAPIKey(db, 11, []string{"read"}, &future)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	ak, err := ValidateAPIKey(db, key)
	if err != nil {
		t.Fatalf("ValidateAPIKey with future expiry: %v", err)
	}
	if ak.UserID != 11 {
		t.Errorf("UserID = %d, want 11", ak.UserID)
	}
}

func TestValidateAPIKey_ValidKey(t *testing.T) {
	db := newAuthDB(t)
	key, err := CreateAPIKey(db, 10, []string{"read", "write"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	ak, err := ValidateAPIKey(db, key)
	if err != nil {
		t.Fatalf("ValidateAPIKey: %v", err)
	}
	if ak.UserID != 10 {
		t.Errorf("UserID = %d, want 10", ak.UserID)
	}
	if len(ak.Permissions) == 0 {
		t.Error("expected non-empty permissions")
	}
}

func TestAuthMiddleware_ValidAPIKey_Passes(t *testing.T) {
	db := newAuthDB(t)
	cfg := AuthConfig{JWTSecret: []byte("testsecret"), TokenDuration: time.Hour}

	key, err := CreateAPIKey(db, 7, []string{"read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware(cfg, db)(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with valid API key, got %d", rec.Code)
	}
	if !reached {
		t.Error("next handler was not called with valid API key")
	}
}

func TestAuthMiddleware_ValidBearerToken_Passes(t *testing.T) {
	db := newAuthDB(t)
	secret := []byte("mw-secret")
	cfg := AuthConfig{JWTSecret: secret, TokenDuration: time.Hour}

	token, err := GenerateToken(42, "admin", cfg)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware(cfg, db)(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !reached {
		t.Error("next handler was not called with valid bearer token")
	}
}
