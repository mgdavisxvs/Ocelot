package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	vsmetrics "github.com/mgdavisxvs/Ocelot/virtualserver/metrics"
)

const (
	headerRequestID    = "X-Request-ID"
	headerIdempotency  = "Idempotency-Key"
	ctxKeyRequestID    = contextKey("request_id")
	maxIdempotencyLen  = 255
)

type contextKey string

// requestIDMiddleware injects a unique request ID into the response headers.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id == "" {
			id = uuid.New().String()
		}
		w.Header().Set(headerRequestID, id)
		next.ServeHTTP(w, r)
	})
}

// authMiddleware enforces Bearer token authentication.
// Uses constant-time comparison to prevent timing attacks.
func authMiddleware(adminKey string) func(http.Handler) http.Handler {
	expected := []byte("Bearer " + adminKey)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if subtle.ConstantTimeCompare([]byte(auth), expected) != 1 {
				writeError(w, r, http.StatusUnauthorized, "unauthorized", "AUTH_REQUIRED")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// maxBodyMiddleware limits request body size.
func maxBodyMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// metricsMiddleware records request count and latency per path/method/status.
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		t0 := time.Now()
		next.ServeHTTP(rw, r)
		path := canonicalPath(r.URL.Path)
		vsmetrics.APIRequests.WithLabelValues(r.Method, path, fmt.Sprintf("%d", rw.status)).Inc()
		vsmetrics.APIRequestDuration.WithLabelValues(r.Method, path).Observe(time.Since(t0).Seconds())
	})
}

// chain applies middleware in the order listed (outermost first).
func chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// statusRecorder captures the HTTP status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// canonicalPath reduces /v1/nodes/abc123 → /v1/nodes/{id} to bound cardinality.
func canonicalPath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	// Heuristic: segments after a known plural noun are IDs
	knownCollections := map[string]bool{"nodes": true, "services": true, "instances": true, "namespaces": true}
	for i := 1; i < len(parts); i++ {
		if knownCollections[parts[i-1]] {
			parts[i] = "{id}"
		}
	}
	return "/" + strings.Join(parts, "/")
}

// writeJSON encodes v as JSON and sends it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// writeError sends a standard ErrorResponse JSON body.
func writeError(w http.ResponseWriter, r *http.Request, status int, msg, code string) {
	reqID := w.Header().Get(headerRequestID)
	writeJSON(w, status, ErrorResponse{Error: msg, Code: code, ReqID: reqID})
}
