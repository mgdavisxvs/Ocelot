package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// ── Request / response types ──────────────────────────────────────────────────

type RegisterArtifactRequest struct {
	Infohash     string          `json:"infohash"`
	Type         string          `json:"type"`
	Name         string          `json:"name"`
	Version      string          `json:"version,omitempty"`
	Arch         string          `json:"arch,omitempty"`
	SizeBytes    int64           `json:"size_bytes"`
	Entrypoint   string          `json:"entrypoint,omitempty"`
	Dependencies []string        `json:"dependencies,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	Publisher    string          `json:"publisher,omitempty"`
	Manifest     json.RawMessage `json:"manifest,omitempty"`
}

type ArtifactSummary struct {
	Infohash  string `json:"infohash"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Arch      string `json:"arch"`
	SizeBytes int64  `json:"size_bytes"`
	Entrypoint string `json:"entrypoint,omitempty"`
	Publisher  string `json:"publisher,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type ArtifactDetail struct {
	ArtifactSummary
	Dependencies []string        `json:"dependencies"`
	Signature    string          `json:"signature,omitempty"`
	Manifest     json.RawMessage `json:"manifest"`
}

// ── ArtifactAPI ───────────────────────────────────────────────────────────────

type ArtifactAPI struct {
	db   *sql.DB
	auth *AuthHandler
}

func NewArtifactAPI(db *sql.DB, auth *AuthHandler) *ArtifactAPI {
	return &ArtifactAPI{db: db, auth: auth}
}

func (a *ArtifactAPI) Mount(mux *http.ServeMux) {
	all := []string{"operator", "project", "user", "service", "auditor"}
	rw  := []string{"operator", "project", "service"}

	mux.HandleFunc("POST /v1/artifacts",              a.auth.protect(a.handleRegister, rw...))
	mux.HandleFunc("GET /v1/artifacts",               a.auth.protect(a.handleList, all...))
	mux.HandleFunc("GET /v1/artifacts/{infohash}",    a.auth.protect(a.handleGet, all...))
	mux.HandleFunc("DELETE /v1/artifacts/{infohash}", a.auth.protect(a.handleDelete, "operator"))
}

// ── POST /v1/artifacts ────────────────────────────────────────────────────────

func (a *ArtifactAPI) handleRegister(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), false)
		return
	}
	var req RegisterArtifactRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON: "+err.Error(), false)
		return
	}
	if err := validateArtifactRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), false)
		return
	}
	if req.Version == "" {
		req.Version = "unversioned"
	}
	if req.Arch == "" {
		req.Arch = "any"
	}
	depsJSON, _ := json.Marshal(req.Dependencies)
	manifestJSON := req.Manifest
	if len(manifestJSON) == 0 {
		manifestJSON = json.RawMessage("{}")
	}

	_, err = a.db.ExecContext(r.Context(), `
		INSERT INTO artifacts
			(infohash, type, name, version, arch, size_bytes, entrypoint,
			 dependencies, signature, publisher, manifest, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.Infohash, req.Type, req.Name, req.Version, req.Arch,
		req.SizeBytes, nullableStr(req.Entrypoint), string(depsJSON),
		nullableStr(req.Signature), nullableStr(req.Publisher),
		string(manifestJSON), nowMs())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "ARTIFACT_CONFLICT", "artifact with this infohash already registered", false)
			return
		}
		slog.Error("register artifact", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"infohash": req.Infohash})
}

// ── GET /v1/artifacts ─────────────────────────────────────────────────────────

func (a *ArtifactAPI) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	where := []string{"1=1"}
	args := []any{}
	if t := q.Get("type"); t != "" {
		where = append(where, "type = ?")
		args = append(args, t)
	}
	if arch := q.Get("arch"); arch != "" {
		where = append(where, "arch = ?")
		args = append(args, arch)
	}
	if name := q.Get("name"); name != "" {
		where = append(where, "name LIKE ?")
		args = append(args, "%"+name+"%")
	}

	query := fmt.Sprintf(`
		SELECT infohash, type, name, version, arch, size_bytes, entrypoint, publisher, created_at
		FROM artifacts WHERE %s
		ORDER BY created_at DESC LIMIT ?`, strings.Join(where, " AND "))
	args = append(args, limit)

	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		slog.Error("list artifacts", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	defer rows.Close()

	var artifacts []ArtifactSummary
	for rows.Next() {
		var as ArtifactSummary
		var entrypoint, publisher sql.NullString
		if err := rows.Scan(&as.Infohash, &as.Type, &as.Name, &as.Version, &as.Arch,
			&as.SizeBytes, &entrypoint, &publisher, &as.CreatedAt); err != nil {
			continue
		}
		as.Entrypoint = entrypoint.String
		as.Publisher = publisher.String
		artifacts = append(artifacts, as)
	}
	if artifacts == nil {
		artifacts = []ArtifactSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": artifacts})
}

// ── GET /v1/artifacts/{infohash} ──────────────────────────────────────────────

func (a *ArtifactAPI) handleGet(w http.ResponseWriter, r *http.Request) {
	infohash := r.PathValue("infohash")
	var ad ArtifactDetail
	var entrypoint, signature, publisher sql.NullString
	var depsJSON, manifestJSON string

	err := a.db.QueryRowContext(r.Context(), `
		SELECT infohash, type, name, version, arch, size_bytes, entrypoint,
		       dependencies, signature, publisher, manifest, created_at
		FROM artifacts WHERE infohash = ?`, infohash).Scan(
		&ad.Infohash, &ad.Type, &ad.Name, &ad.Version, &ad.Arch, &ad.SizeBytes,
		&entrypoint, &depsJSON, &signature, &publisher, &manifestJSON, &ad.CreatedAt)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "artifact not found", false)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	ad.Entrypoint = entrypoint.String
	ad.Signature = signature.String
	ad.Publisher = publisher.String
	_ = json.Unmarshal([]byte(depsJSON), &ad.Dependencies)
	if ad.Dependencies == nil {
		ad.Dependencies = []string{}
	}
	ad.Manifest = json.RawMessage(manifestJSON)
	writeJSON(w, http.StatusOK, ad)
}

// ── DELETE /v1/artifacts/{infohash} ───────────────────────────────────────────

func (a *ArtifactAPI) handleDelete(w http.ResponseWriter, r *http.Request) {
	infohash := r.PathValue("infohash")
	res, err := a.db.ExecContext(r.Context(), "DELETE FROM artifacts WHERE infohash = ?", infohash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "artifact not found", false)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Validation ────────────────────────────────────────────────────────────────

func validateArtifactRequest(req *RegisterArtifactRequest) error {
	if len(req.Infohash) != 64 {
		return fmt.Errorf("infohash: must be 64-char SHA-256 hex string")
	}
	validTypes := map[string]bool{
		"binary": true, "container": true, "model": true, "dataset": true,
		"archive": true, "vm_image": true, "script": true,
	}
	if !validTypes[req.Type] {
		return fmt.Errorf("type: must be one of binary, container, model, dataset, archive, vm_image, script")
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 256 {
		return fmt.Errorf("name: required, max 256 chars")
	}
	if req.Arch != "" {
		validArches := map[string]bool{"amd64": true, "arm64": true, "any": true}
		if !validArches[req.Arch] {
			return fmt.Errorf("arch: must be amd64, arm64, or any")
		}
	}
	if len(req.Dependencies) > 64 {
		return fmt.Errorf("dependencies: max 64 entries")
	}
	return nil
}

// nullableStr returns nil (SQL NULL) when s is empty, otherwise s.
func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
