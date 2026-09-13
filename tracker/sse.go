package tracker

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SSEHandler returns an http.HandlerFunc that streams tracker events as
// Server-Sent Events. Clients must authenticate with:
//
//   Authorization: Bearer <site_password>
//   or
//   ?auth=<site_password>
//
// Each event is emitted as:
//
//	data: <json>\n\n
//
// A ": keepalive" comment is sent every 30 s to prevent proxy timeouts.
func SSEHandler(bus *EventBus, sitePassword string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ── Authentication ─────────────────────────────────────────────────────
		authed := false
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			authed = strings.TrimPrefix(auth, "Bearer ") == sitePassword
		}
		if !authed {
			authed = r.URL.Query().Get("auth") == sitePassword
		}
		if !authed {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// ── SSE preamble ───────────────────────────────────────────────────────
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no") // disable nginx/caddy buffering

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		subID := fmt.Sprintf("sse-%s-%d", r.RemoteAddr, time.Now().UnixNano())
		ch := bus.Subscribe(subID, 256)
		defer bus.Unsubscribe(subID)

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				data := evt.JSON()
				if data == nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			case <-ticker.C:
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	}
}
