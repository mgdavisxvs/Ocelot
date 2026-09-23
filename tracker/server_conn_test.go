package tracker

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── handleConnection ──────────────────────────────────────────────────────────

// TestHandleConnection_ServesRequest verifies that handleConnection reads an
// HTTP/1.0 request from a net.Pipe, routes it through handleRequest, writes the
// response, and returns (since HTTP/1.0 implies httpClose=true).
func TestHandleConnection_ServesRequest(t *testing.T) {
	f := newTestFixture()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	// Replicate what ListenAndServe does before spawning the goroutine.
	f.server.mu.Lock()
	f.server.activeConns[serverConn] = struct{}{}
	f.server.mu.Unlock()
	f.server.stats.OpenConnections.Add(1)
	f.server.wg.Add(1)

	go f.server.handleConnection(serverConn)

	// Write a minimal HTTP/1.0 request so handleConnection closes after one round.
	rawReq := "GET /" + sitePass + "/stats HTTP/1.0\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	clientConn.Write([]byte(rawReq))

	// Read the full response (serverConn is closed after HTTP/1.0 response, EOF terminates).
	buf := make([]byte, 4096)
	var total int
	for total < len(buf) {
		n, err := clientConn.Read(buf[total:])
		total += n
		if err != nil {
			break
		}
	}

	response := string(buf[:total])
	if !strings.Contains(response, "200 OK") {
		t.Errorf("handleConnection: expected 200 OK, got: %q", response)
	}

	// Block until handleConnection's defer wg.Done() runs.
	f.server.wg.Wait()
}

// TestHandleConnection_EOF_ClosesCleanly verifies that an immediate client close
// (EOF on the first ReadRequest) causes handleConnection to exit without panic.
func TestHandleConnection_EOF_ClosesCleanly(t *testing.T) {
	f := newTestFixture()

	serverConn, clientConn := net.Pipe()

	f.server.mu.Lock()
	f.server.activeConns[serverConn] = struct{}{}
	f.server.mu.Unlock()
	f.server.stats.OpenConnections.Add(1)
	f.server.wg.Add(1)

	go f.server.handleConnection(serverConn)

	// Close client immediately — server gets io.EOF on http.ReadRequest.
	clientConn.Close()

	f.server.wg.Wait()
}

// ── RedirectHTTPToHTTPS ───────────────────────────────────────────────────────

func TestRedirectHTTPToHTTPS_NoQuery(t *testing.T) {
	handler := RedirectHTTPToHTTPS()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://") {
		t.Errorf("Location = %q, want https:// prefix", loc)
	}
	if strings.Contains(loc, "?") {
		t.Errorf("Location has unexpected query string: %q", loc)
	}
}

func TestRedirectHTTPToHTTPS_WithQuery(t *testing.T) {
	handler := RedirectHTTPToHTTPS()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/path?foo=bar", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "?foo=bar") {
		t.Errorf("Location = %q, expected query string ?foo=bar", loc)
	}
}
