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

// ── EventSummary ──────────────────────────────────────────────────────────────

type EventSummary struct {
	ID          string          `json:"id"`
	Ts          int64           `json:"ts"`
	OrgID       string          `json:"org_id,omitempty"`
	Actor       string          `json:"actor"`
	Kind        string          `json:"kind"`
	SubjectType string          `json:"subject_type"`
	SubjectID   string          `json:"subject_id"`
	Payload     json.RawMessage `json:"payload"`
}

// ── EventAPI ──────────────────────────────────────────────────────────────────

type EventAPI struct {
	db   *sql.DB
	auth *AuthHandler
}

func NewEventAPI(db *sql.DB, auth *AuthHandler) *EventAPI {
	return &EventAPI{db: db, auth: auth}
}

func (e *EventAPI) Mount(mux *http.ServeMux) {
	all := []string{"operator", "project", "user", "service", "auditor"}
	mux.HandleFunc("GET /v1/events", e.auth.protect(e.handleList, all...))
}

// ── GET /v1/events ────────────────────────────────────────────────────────────

func (e *EventAPI) handleList(w http.ResponseWriter, r *http.Request) {
	claims, _ := authFromContext(r.Context())
	q := r.URL.Query()

	limit := 100
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	// Show events belonging to this org plus node-initiated events (org_id='').
	where := []string{"(org_id = ? OR org_id = '')"}
	args := []any{claims.OrgID}

	if kind := q.Get("kind"); kind != "" {
		where = append(where, "kind = ?")
		args = append(args, kind)
	}
	if st := q.Get("subject_type"); st != "" {
		where = append(where, "subject_type = ?")
		args = append(args, st)
	}
	if sid := q.Get("subject_id"); sid != "" {
		where = append(where, "subject_id = ?")
		args = append(args, sid)
	}
	if actor := q.Get("actor"); actor != "" {
		where = append(where, "actor = ?")
		args = append(args, actor)
	}
	if cursor := q.Get("cursor"); cursor != "" {
		where = append(where, "ts < (SELECT ts FROM events WHERE id = ?)")
		args = append(args, cursor)
	}

	query := fmt.Sprintf(`
		SELECT id, ts, org_id, actor, kind, subject_type, subject_id, payload
		FROM events
		WHERE %s
		ORDER BY ts DESC
		LIMIT ?`, strings.Join(where, " AND "))
	args = append(args, limit+1)

	rows, err := e.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		slog.Error("list events", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}
	defer rows.Close()

	var events []EventSummary
	for rows.Next() {
		var ev EventSummary
		var payloadJSON string
		if err := rows.Scan(&ev.ID, &ev.Ts, &ev.OrgID, &ev.Actor, &ev.Kind,
			&ev.SubjectType, &ev.SubjectID, &payloadJSON); err != nil {
			slog.Warn("list events: scan row", "err", err)
			continue
		}
		ev.Payload = json.RawMessage(payloadJSON)
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		slog.Error("list events: rows error", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "database error", true)
		return
	}

	var nextCursor string
	if len(events) > limit {
		nextCursor = events[limit-1].ID
		events = events[:limit]
	}
	if events == nil {
		events = []EventSummary{}
	}

	out := map[string]any{"events": events}
	if nextCursor != "" {
		out["next_cursor"] = nextCursor
	}
	writeJSON(w, http.StatusOK, out)
}
