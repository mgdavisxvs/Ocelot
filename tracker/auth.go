package tracker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents JWT claims
type Claims struct {
	UserID int    `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// AuthConfig holds authentication configuration
type AuthConfig struct {
	JWTSecret     []byte
	TokenDuration time.Duration
}

// GenerateToken generates a JWT token for a user
func GenerateToken(userID int, role string, config AuthConfig) (string, error) {
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(config.TokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(config.JWTSecret)
}

// ValidateToken validates a JWT token and returns the claims
func ValidateToken(tokenString string, secret []byte) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})

	if err != nil {
		return nil, ErrUnauthorized.WithError(err)
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrUnauthorized.WithDetail("invalid token claims")
}

// APIKey represents an API key
type APIKey struct {
	ID          int
	KeyHash     string
	UserID      int
	Permissions []string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	LastUsedAt  *time.Time
	Revoked     bool
}

// CreateAPIKey creates a new API key
func CreateAPIKey(db *sql.DB, userID int, permissions []string, expiresAt *time.Time) (string, error) {
	// Generate random API key (32 bytes = 64 hex chars)
	key := generateRandomKey(32)
	keyHash := hashAPIKey(key)

	query := `INSERT INTO api_keys (key_hash, user_id, permissions, created_at, expires_at, revoked)
		VALUES (?, ?, ?, ?, ?, 0)`

	permJSON := strings.Join(permissions, ",")
	_, err := db.Exec(query, keyHash, userID, permJSON, time.Now().Unix(), expiresAt)
	if err != nil {
		return "", ErrDatabaseQuery.WithError(err)
	}

	return key, nil
}

// ValidateAPIKey validates an API key and returns the user ID
func ValidateAPIKey(db *sql.DB, apiKey string) (*APIKey, error) {
	keyHash := hashAPIKey(apiKey)

	query := `SELECT id, user_id, permissions, created_at, expires_at, last_used_at, revoked
		FROM api_keys WHERE key_hash = ?`

	var ak APIKey
	var permStr string
	var expiresUnix, lastUsedUnix *int64

	err := db.QueryRow(query, keyHash).Scan(
		&ak.ID, &ak.UserID, &permStr,
		&ak.CreatedAt, &expiresUnix, &lastUsedUnix, &ak.Revoked,
	)

	if err == sql.ErrNoRows {
		return nil, ErrUnauthorized.WithDetail("invalid API key")
	}
	if err != nil {
		return nil, ErrDatabaseQuery.WithError(err)
	}

	if ak.Revoked {
		return nil, ErrUnauthorized.WithDetail("API key has been revoked")
	}

	if expiresUnix != nil {
		expiresAt := time.Unix(*expiresUnix, 0)
		if time.Now().After(expiresAt) {
			return nil, ErrUnauthorized.WithDetail("API key has expired")
		}
	}

	ak.Permissions = strings.Split(permStr, ",")

	// Update last used timestamp
	db.Exec("UPDATE api_keys SET last_used_at = ? WHERE id = ?", time.Now().Unix(), ak.ID)

	return &ak, nil
}

// hashAPIKey hashes an API key using SHA256
func hashAPIKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// generateRandomKey generates a cryptographically random API key of the given length.
func generateRandomKey(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	for i, v := range raw {
		b[i] = charset[int(v)%len(charset)]
	}
	return string(b)
}

// AuthMiddleware validates JWT or API key authentication
func AuthMiddleware(config AuthConfig, db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger := GetDefaultLogger()

			// Try API key first (X-API-Key header)
			if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
				ak, err := ValidateAPIKey(db, apiKey)
				if err != nil {
					logger.Warn("invalid API key", "error", err)
					http.Error(w, "Invalid API key", http.StatusUnauthorized)
					return
				}

				// Add user info to context
				ctx := context.WithValue(r.Context(), "user_id", ak.UserID)
				ctx = context.WithValue(ctx, "permissions", ak.Permissions)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Try JWT token (Authorization: Bearer <token>)
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenString := strings.TrimPrefix(authHeader, "Bearer ")
				claims, err := ValidateToken(tokenString, config.JWTSecret)
				if err != nil {
					logger.Warn("invalid JWT token", "error", err)
					http.Error(w, "Invalid token", http.StatusUnauthorized)
					return
				}

				// Add user info to context
				ctx := context.WithValue(r.Context(), "user_id", claims.UserID)
				ctx = context.WithValue(ctx, "role", claims.Role)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// No valid authentication
			http.Error(w, "Authentication required", http.StatusUnauthorized)
		})
	}
}

// CreateAPIKeysTable creates the api_keys table
func CreateAPIKeysTable(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		key_hash TEXT NOT NULL UNIQUE,
		user_id INTEGER NOT NULL,
		permissions TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER,
		last_used_at INTEGER,
		revoked BOOLEAN DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);
	CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id);`

	_, err := db.Exec(query)
	return err
}
