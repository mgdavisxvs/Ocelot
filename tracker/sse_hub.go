package tracker

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// sseClient represents a connected SSE consumer.
type sseClient struct {
	ch     chan string
	closed chan struct{}
}

// SSEHub broadcasts events to connected admin SSE clients over HTTP.
// It subscribes to the bus and fans every matching event out to all clients.
type SSEHub struct {
	bus     *Bus
	mu      sync.RWMutex
	clients map[*sseClient]struct{}
}

// NewSSEHub creates the hub and subscribes to all topics relevant to the
// admin dashboard.
func NewSSEHub(bus *Bus) *SSEHub {
	h := &SSEHub{
		bus:     bus,
		clients: make(map[*sseClient]struct{}),
	}
	bus.Subscribe("anomaly.*", h.forward)
	bus.Subscribe("user.flagged", h.forward)
	bus.Subscribe("user.unflagged", h.forward)
	bus.Subscribe("user.banned", h.forward)
	bus.Subscribe("freeleech.granted", h.forward)
	bus.Subscribe("infra.bus_drop", h.forward)
	return h
}

// ServeHTTP implements http.Handler. Each connection receives a stream of
// server-sent events until the client disconnects or the server shuts down.
func (h *SSEHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	client := &sseClient{
		ch:     make(chan string, 64),
		closed: make(chan struct{}),
	}
	h.addClient(client)
	defer h.removeClient(client)

	// Send initial keepalive comment.
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg := <-client.ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-client.closed:
			return
		}
	}
}

func (h *SSEHub) forward(e Event) {
	payload, err := json.Marshal(struct {
		Topic     string `json:"topic"`
		OccurredAt int64  `json:"occurred_at"`
		Data      Event  `json:"data"`
	}{
		Topic:      e.Topic(),
		OccurredAt: e.OccurredAt().UnixMilli(),
		Data:       e,
	})
	if err != nil {
		log.Printf("sse_hub: marshal error: %v", err)
		return
	}
	msg := string(payload)

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.ch <- msg:
		default:
			// Slow client — drop rather than block the bus dispatch goroutine.
		}
	}
}

func (h *SSEHub) addClient(c *sseClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *SSEHub) removeClient(c *sseClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// ClientCount returns the number of active SSE connections.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
