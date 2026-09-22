package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ── JWT claims ────────────────────────────────────────────────────────────────

type ocelotClaims struct {
	AccountID string `json:"account_id"`
	OrgID     string `json:"org_id"`
	Role      string `json:"role"`
	jwt.RegisteredClaims
}

type ctxKeyAuth struct{}

func authFromContext(ctx context.Context) (*ocelotClaims, bool) {
	c, ok := ctx.Value(ctxKeyAuth{}).(*ocelotClaims)
	return c, ok && c != nil
}

// ── AuthHandler ───────────────────────────────────────────────────────────────

type AuthHandler struct {
	db             *sql.DB
	bootstrapToken string
	jwtSecret      []byte
	jwtTTL         time.Duration
}

func NewAuthHandler(db *sql.DB, bootstrapToken string, jwtSecret []byte) *AuthHandler {
	return &AuthHandler{
		db:             db,
		bootstrapToken: bootstrapToken,
		jwtSecret:      jwtSecret,
		jwtTTL:         24 * time.Hour,
	}
}

// Mount registers authentication and admin bootstrap endpoints.
func (a *AuthHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/login", a.handleLogin)
	mux.HandleFunc("POST /v1/admin/orgs", a.handleCreateOrg)
	mux.HandleFunc("POST /v1/admin/accounts", a.handleCreateAccount)
}

// protect returns a handler that validates the JWT and enforces role membership.
// Pass no roles to allow any authenticated account.
func (a *AuthHandler) protect(next http.HandlerFunc, roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHdr := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHdr, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid Authorization header", false)
			return
		}
		tokenStr := strings.TrimPrefix(authHdr, "Bearer ")

		claims := &ocelotClaims{}
		tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return a.jwtSecret, nil
		})
		if err != nil || !tok.Valid {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token", false)
			return
		}

		if len(roles) > 0 {
			allowed := false
			for _, role := range roles {
				if claims.Role == role {
					allowed = true
					break
				}
			}
			if !allowed {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "insufficient role", false)
				return
			}
		}

		ctx := context.WithValue(r.Context(), ctxKeyAuth{}, claims)
		next(w, r.WithContext(ctx))
	}
}

// issueToken generates a signed JWT for the given account.
func (a *AuthHandler) issueToken(accountID, orgID, role string) (string, int64, error) {
	exp := time.Now().Add(a.jwtTTL)
	claims := &ocelotClaims{
		AccountID: accountID,
		OrgID:     orgID,
		Role:      role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   accountID,
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(a.jwtSecret)
	return signed, exp.UnixMilli(), err
}

// ── POST /v1/auth/login ───────────────────────────────────────────────────────

func (a *AuthHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), false)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "username and password required", false)
		return
	}

	var accountID, orgID, role, hash string
	var deletedAt sql.NullInt64
	err = a.db.QueryRowContext(r.Context(), `
		SELECT id, org_id, role, secret_hash, deleted_at
		FROM accounts WHERE username = ?`, req.Username).
		Scan(&accountID, &orgID, &role, &hash, &deletedAt)
	if err == sql.ErrNoRows || (err == nil && deletedAt.Valid) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid credentials", false)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid credentials", false)
		return
	}

	token, expiresAt, err := a.issueToken(accountID, orgID, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "token generation failed", true)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"expires_at": expiresAt,
		"account_id": accountID,
		"org_id":     orgID,
		"role":       role,
	})
}

// ── POST /v1/admin/orgs ───────────────────────────────────────────────────────

func (a *AuthHandler) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	if !a.verifyBootstrap(r) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid bootstrap token", false)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), false)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "name required", false)
		return
	}

	orgID := uuid.New().String()
	if _, err = a.db.ExecContext(r.Context(),
		"INSERT INTO orgs (id, name, created_at) VALUES (?, ?, ?)",
		orgID, req.Name, nowMs()); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "ORG_CONFLICT", "org name already exists", false)
			return
		}
		slog.Error("create org", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	slog.Info("org created", "org_id", orgID, "name", req.Name)
	writeJSON(w, http.StatusCreated, map[string]string{"id": orgID, "name": req.Name})
}

// ── POST /v1/admin/accounts ───────────────────────────────────────────────────

func (a *AuthHandler) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	if !a.verifyBootstrap(r) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid bootstrap token", false)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), false)
		return
	}
	var req struct {
		OrgID     string `json:"org_id"`
		ProjectID string `json:"project_id,omitempty"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		Role      string `json:"role"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON", false)
		return
	}
	if req.OrgID == "" || req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "org_id, username, and password required", false)
		return
	}
	validRoles := map[string]bool{"operator": true, "project": true, "user": true, "service": true, "auditor": true}
	if !validRoles[req.Role] {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "role must be one of: operator, project, user, service, auditor", false)
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "password must be at least 8 characters", false)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "password hashing failed", true)
		return
	}

	accountID := uuid.New().String()
	var projectIDArg any
	if req.ProjectID != "" {
		projectIDArg = req.ProjectID
	}

	if _, err = a.db.ExecContext(r.Context(), `
		INSERT INTO accounts (id, org_id, project_id, username, role, secret_hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		accountID, req.OrgID, projectIDArg, req.Username, req.Role, string(hash), nowMs()); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "USERNAME_CONFLICT", "username already exists", false)
			return
		}
		slog.Error("create account", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	slog.Info("account created", "account_id", accountID, "username", req.Username, "role", req.Role)
	writeJSON(w, http.StatusCreated, map[string]string{
		"id":       accountID,
		"username": req.Username,
		"role":     req.Role,
		"org_id":   req.OrgID,
	})
}

func (a *AuthHandler) verifyBootstrap(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	return strings.HasPrefix(auth, "Bearer ") &&
		strings.TrimPrefix(auth, "Bearer ") == a.bootstrapToken
}
