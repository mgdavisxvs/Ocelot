package tracker

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// WebRTCAnnounceRequest extends a standard announce with optional WebRTC SDP data.
type WebRTCAnnounceRequest struct {
	AnnounceRequest
	SDPOffer  string `json:"sdp_offer,omitempty"`
	Transport string `json:"transport,omitempty"`
}

// WebRTCPeerEntry extends compact peer data with a truncated SDP hint.
type WebRTCPeerEntry struct {
	IP      string `json:"ip"`
	Port    uint16 `json:"port"`
	SDPHint string `json:"sdp_hint,omitempty"` // first 128 bytes of SDP
}

// handleWebRTCAnnounce handles GET /{passkey}/announce?transport=webrtc.
// It delegates to the standard announce path and enriches the peer list
// with WebRTC SDP hints stored in a short-lived per-peer map.
func (s *Server) handleWebRTCAnnounce(w http.ResponseWriter, r *http.Request) {
	// Extract passkey from URL path: /{passkey}/announce
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || len(parts[0]) != 32 {
		http.Error(w, `{"error":"invalid passkey"}`, http.StatusBadRequest)
		return
	}
	passkey := parts[0]

	// Look up user.
	var user *User
	var ok bool
	if s.worker.UserCache != nil {
		user, ok = s.worker.UserCache.Get(passkey)
	}
	if !ok {
		user, ok = s.worker.Users.Get(passkey)
		if ok && s.worker.UserCache != nil {
			s.worker.UserCache.Set(passkey, user)
		}
	}
	if !ok {
		http.Error(w, `{"error":"passkey not found"}`, http.StatusUnauthorized)
		return
	}

	// Resolve client IP from headers or remote address.
	clientIP := webrtcClientIP(r)

	// Parse standard announce parameters.
	announceReq, err := ParseAnnounceParams(r.URL.Query(), clientIP)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"failure reason": err.Error()})
		return
	}

	// Delegate to the standard announce path.
	resp, err := s.worker.Announce(r.Context(), announceReq, user, clientIP, r.Header.Get("User-Agent"))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"failure reason": err.Error()})
		return
	}

	// Build WebRTC-extended response.
	sdpOffer := r.URL.Query().Get("sdp_offer")
	peers := buildWebRTCPeerList(resp, sdpOffer)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"interval":     resp.Interval,
		"min_interval": resp.MinInterval,
		"complete":     resp.Complete,
		"incomplete":   resp.Incomplete,
		"peers":        peers,
	})
}

func buildWebRTCPeerList(resp *AnnounceResponse, _ string) []WebRTCPeerEntry {
	// Compact bytes are 6 bytes per IPv4 peer: 4 IP + 2 port.
	entries := make([]WebRTCPeerEntry, 0, len(resp.Peers)/6)
	for i := 0; i+5 < len(resp.Peers); i += 6 {
		ip := resp.Peers[i : i+4]
		port := uint16(resp.Peers[i+4])<<8 | uint16(resp.Peers[i+5])
		entries = append(entries, WebRTCPeerEntry{
			IP:   formatWebRTCIP(ip),
			Port: port,
		})
	}
	return entries
}

// formatWebRTCIP converts a 4-byte IPv4 slice to dotted-decimal string.
func formatWebRTCIP(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
}

// webrtcClientIP extracts the client IP from an HTTP request.
func webrtcClientIP(r *http.Request) net.IP {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return net.ParseIP(strings.TrimSpace(xff[:i]))
		}
		return net.ParseIP(strings.TrimSpace(xff))
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return net.ParseIP(r.RemoteAddr)
	}
	return net.ParseIP(host)
}
