package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ControlServer serves the management REST API on :34001.
// Authentication: Bearer token matching config.SitePassword.
// All routes under /api/v1/*.
//
// The legacy /<sitepassword>/update route on :34000 is kept as a compatibility
// shim with Deprecation and Sunset headers (see server.go handleRequest).
type ControlServer struct {
	worker  *Worker
	config  *Config
	httpSrv *http.Server
}

func NewControlServer(config *Config, worker *Worker) *ControlServer {
	cs := &ControlServer{worker: worker, config: config}
	mux := http.NewServeMux()

	// Torrents
	mux.HandleFunc("/api/v1/torrents", cs.authMiddleware(cs.handleTorrents))
	// Users
	mux.HandleFunc("/api/v1/users", cs.authMiddleware(cs.handleUsers))
	// Whitelist
	mux.HandleFunc("/api/v1/whitelist", cs.authMiddleware(cs.handleWhitelist))
	// Tokens
	mux.HandleFunc("/api/v1/tokens", cs.authMiddleware(cs.handleTokens))
	// Stats (GET only)
	mux.HandleFunc("/api/v1/stats", cs.authMiddleware(cs.handleAPIStats))
	// Peers (GET only)
	mux.HandleFunc("/api/v1/peers", cs.authMiddleware(cs.handleAPIPeers))

	cs.httpSrv = &http.Server{
		Addr:         config.ControlAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return cs
}

func (cs *ControlServer) ListenAndServe() error {
	fmt.Printf("Control API listening on %s\n", cs.config.ControlAddr)
	return cs.httpSrv.ListenAndServe()
}

func (cs *ControlServer) Shutdown(ctx context.Context) error {
	return cs.httpSrv.Shutdown(ctx)
}

// authMiddleware validates the Bearer token against config.SitePassword.
func (cs *ControlServer) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != cs.config.SitePassword {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ── /api/v1/torrents ─────────────────────────────────────────────────────────

func (cs *ControlServer) handleTorrents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := queryIntHTTP(r, "limit", 100)
		data, err := cs.worker.GetTorrents(limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)

	case http.MethodPost:
		var req struct {
			ID       uint32 `json:"id"`
			InfoHash string `json:"info_hash"`
			FreeType uint8  `json:"free_type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.ID == 0 || req.InfoHash == "" {
			writeErr(w, http.StatusBadRequest, "id and info_hash are required")
			return
		}
		if _, ok := cs.worker.Torrents.Get(req.InfoHash); ok {
			writeErr(w, http.StatusConflict, "torrent already exists")
			return
		}
		t := NewTorrent(TorrentID(req.ID))
		t.FreeType = FreeType(req.FreeType)
		cs.worker.Torrents.Set(req.InfoHash, t)
		if err := cs.worker.DB.RecordTorrentHash(TorrentID(req.ID), req.InfoHash); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok", "message": "torrent added"})

	case http.MethodDelete:
		hash := r.URL.Query().Get("info_hash")
		if hash == "" {
			writeErr(w, http.StatusBadRequest, "info_hash query param required")
			return
		}
		cs.worker.Torrents.Delete(hash)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "torrent deleted"})

	case http.MethodPatch:
		hash := r.URL.Query().Get("info_hash")
		if hash == "" {
			writeErr(w, http.StatusBadRequest, "info_hash query param required")
			return
		}
		t, ok := cs.worker.Torrents.Get(hash)
		if !ok {
			writeErr(w, http.StatusNotFound, "torrent not found")
			return
		}
		var req struct {
			FreeType *uint8 `json:"free_type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.FreeType != nil {
			t.mu.Lock()
			t.FreeType = FreeType(*req.FreeType)
			t.mu.Unlock()
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "torrent updated"})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── /api/v1/users ─────────────────────────────────────────────────────────────

func (cs *ControlServer) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			ID         uint32 `json:"id"`
			Passkey    string `json:"passkey"`
			NewPasskey string `json:"new_passkey"`
			CanLeech   *bool  `json:"can_leech"`
			ProtectIP  *bool  `json:"protect_ip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		// Passkey change
		if req.NewPasskey != "" {
			if len(req.NewPasskey) != 32 {
				writeErr(w, http.StatusBadRequest, "new_passkey must be 32 characters")
				return
			}
			u, ok := cs.worker.Users.Get(req.Passkey)
			if !ok {
				writeErr(w, http.StatusNotFound, "user not found")
				return
			}
			cs.worker.Users.Delete(req.Passkey)
			cs.worker.Users.Set(req.NewPasskey, u)
			if err := cs.worker.DB.RecordUserPasskey(u.ID, req.NewPasskey, u.CanLeech.Load(), u.ProtectIP.Load()); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "passkey changed"})
			return
		}
		// Add user
		if req.ID == 0 || req.Passkey == "" {
			writeErr(w, http.StatusBadRequest, "id and passkey are required")
			return
		}
		if len(req.Passkey) != 32 {
			writeErr(w, http.StatusBadRequest, "passkey must be 32 characters")
			return
		}
		if _, ok := cs.worker.Users.Get(req.Passkey); ok {
			writeErr(w, http.StatusConflict, "passkey already exists")
			return
		}
		canLeech := true
		protectIP := false
		if req.CanLeech != nil {
			canLeech = *req.CanLeech
		}
		if req.ProtectIP != nil {
			protectIP = *req.ProtectIP
		}
		u := NewUser(UserID(req.ID), canLeech, protectIP)
		cs.worker.Users.Set(req.Passkey, u)
		if err := cs.worker.DB.RecordUserPasskey(UserID(req.ID), req.Passkey, canLeech, protectIP); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok", "message": "user added"})

	case http.MethodPatch:
		passkey := r.URL.Query().Get("passkey")
		if passkey == "" {
			writeErr(w, http.StatusBadRequest, "passkey query param required")
			return
		}
		u, ok := cs.worker.Users.Get(passkey)
		if !ok {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		var req struct {
			CanLeech  *bool `json:"can_leech"`
			ProtectIP *bool `json:"protect_ip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.CanLeech != nil {
			u.CanLeech.Store(*req.CanLeech)
		}
		if req.ProtectIP != nil {
			u.ProtectIP.Store(*req.ProtectIP)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "user updated"})

	case http.MethodDelete:
		passkey := r.URL.Query().Get("passkey")
		if passkey == "" {
			writeErr(w, http.StatusBadRequest, "passkey query param required")
			return
		}
		cs.worker.Users.Delete(passkey)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "user removed"})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── /api/v1/whitelist ─────────────────────────────────────────────────────────

func (cs *ControlServer) handleWhitelist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := cs.worker.GetWhitelist()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)

	case http.MethodPost:
		var req struct {
			Prefix string `json:"prefix"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prefix == "" {
			writeErr(w, http.StatusBadRequest, "prefix required")
			return
		}
		cs.worker.Whitelist.Add(req.Prefix)
		if err := cs.worker.DB.AddWhitelistEntry(req.Prefix); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok", "message": "entry added"})

	case http.MethodDelete:
		prefix := r.URL.Query().Get("prefix")
		if prefix == "" {
			writeErr(w, http.StatusBadRequest, "prefix query param required")
			return
		}
		cs.worker.Whitelist.Remove(prefix)
		if err := cs.worker.DB.RemoveWhitelistEntry(prefix); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "entry removed"})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── /api/v1/tokens ────────────────────────────────────────────────────────────

func (cs *ControlServer) handleTokens(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID   uint32 `json:"user_id"`
		InfoHash string `json:"info_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.UserID == 0 || req.InfoHash == "" {
		writeErr(w, http.StatusBadRequest, "user_id and info_hash are required")
		return
	}
	t, ok := cs.worker.Torrents.Get(req.InfoHash)
	if !ok {
		writeErr(w, http.StatusNotFound, "torrent not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		t.mu.Lock()
		t.TokenedUsers[UserID(req.UserID)] = struct{}{}
		t.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "token added"})
	case http.MethodDelete:
		t.mu.Lock()
		delete(t.TokenedUsers, UserID(req.UserID))
		t.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "token removed"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── /api/v1/stats ─────────────────────────────────────────────────────────────

func (cs *ControlServer) handleAPIStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	data, err := cs.worker.GetStats()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// ── /api/v1/peers ─────────────────────────────────────────────────────────────

func (cs *ControlServer) handleAPIPeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	hash := r.URL.Query().Get("info_hash")
	if hash == "" {
		writeErr(w, http.StatusBadRequest, "info_hash query param required")
		return
	}
	limit := queryIntHTTP(r, "limit", 100)
	data, err := cs.worker.GetPeers(hash, limit)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func queryIntHTTP(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil || v <= 0 {
		return defaultVal
	}
	return v
}
