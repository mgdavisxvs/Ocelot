package tracker

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// TestHandleConnection_TCPConn_SetsNoDelay uses a real TCP loopback pair so
// the (*net.TCPConn) type assertion succeeds and SetNoDelay/KeepAlive are covered.
func TestHandleConnection_TCPConn_SetsNoDelay(t *testing.T) {
	f := newTestFixture()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	serverConn := <-accepted

	f.server.mu.Lock()
	f.server.activeConns[serverConn] = struct{}{}
	f.server.mu.Unlock()
	f.server.stats.OpenConnections.Add(1)
	f.server.wg.Add(1)

	go f.server.handleConnection(serverConn)

	rawReq := "GET /" + sitePass + "/stats HTTP/1.0\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	clientConn.Write([]byte(rawReq))

	buf := make([]byte, 4096)
	var total int
	for total < len(buf) {
		n, err := clientConn.Read(buf[total:])
		total += n
		if err != nil {
			break
		}
	}

	if !strings.Contains(string(buf[:total]), "200 OK") {
		t.Errorf("TCP conn: expected 200 OK, got: %q", string(buf[:total]))
	}

	f.server.wg.Wait()
}

// TestHandleConnection_MalformedRequest covers the non-EOF error path in the
// http.ReadRequest loop (if err != io.EOF {} branch).
func TestHandleConnection_MalformedRequest_ClosesCleanly(t *testing.T) {
	f := newTestFixture()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	f.server.mu.Lock()
	f.server.activeConns[serverConn] = struct{}{}
	f.server.mu.Unlock()
	f.server.stats.OpenConnections.Add(1)
	f.server.wg.Add(1)

	go f.server.handleConnection(serverConn)

	// Send malformed HTTP — ReadRequest returns non-EOF error, server exits.
	clientConn.Write([]byte("NOTHTTP\r\n\r\n"))

	f.server.wg.Wait()
}

// TestHandleConnection_WriteError covers the conn.Write error return path by
// closing the client connection before the server can write the response.
func TestHandleConnection_WriteError_ClosesCleanly(t *testing.T) {
	f := newTestFixture()

	serverConn, clientConn := net.Pipe()

	f.server.mu.Lock()
	f.server.activeConns[serverConn] = struct{}{}
	f.server.mu.Unlock()
	f.server.stats.OpenConnections.Add(1)
	f.server.wg.Add(1)

	go f.server.handleConnection(serverConn)

	// Write the request, then immediately close the client so the server's
	// conn.Write(response) fails with a closed-pipe error.
	rawReq := "GET /" + sitePass + "/stats HTTP/1.0\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	clientConn.Write([]byte(rawReq))
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

// ── ListenAndServe ────────────────────────────────────────────────────────────

// TestListenAndServe_InvalidAddr_ReturnsError verifies the net.Listen error
// path (server.go:63-65) by giving ListenAndServe an unusable address.
func TestListenAndServe_InvalidAddr_ReturnsError(t *testing.T) {
	f := newTestFixture()
	f.server.config.ListenAddr = "not-a-valid-address:::"
	err := f.server.ListenAndServe()
	if err == nil {
		t.Error("expected error for invalid listen address, got nil")
	}
}

// TestListenAndServe_AcceptAndShutdown covers the accept loop + graceful
// shutdown path (server.go:67-99). The test grabs a free port, points the
// server at it, starts ListenAndServe in a goroutine, connects a client
// that sends one HTTP/1.0 request, then calls Shutdown and waits for the
// goroutine to exit cleanly.
func TestListenAndServe_AcceptAndShutdown(t *testing.T) {
	// Grab a free port without holding it — TOCTOU is acceptable in tests.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	f := newTestFixture()
	f.server.config.ListenAddr = addr

	done := make(chan error, 1)
	go func() { done <- f.server.ListenAndServe() }()

	// Retry-connect until the server is ready (at most 200 ms).
	var clientConn net.Conn
	for i := 0; i < 100; i++ {
		time.Sleep(2 * time.Millisecond)
		c, dialErr := net.DialTimeout("tcp", addr, time.Second)
		if dialErr == nil {
			clientConn = c
			break
		}
	}
	if clientConn == nil {
		t.Fatal("could not connect to ListenAndServe within 200 ms")
	}

	// Send a valid HTTP/1.0 request so handleConnection processes it.
	rawReq := "GET /" + sitePass + "/stats HTTP/1.0\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	clientConn.Write([]byte(rawReq))

	buf := make([]byte, 4096)
	var total int
	for total < len(buf) {
		n, readErr := clientConn.Read(buf[total:])
		total += n
		if readErr != nil {
			break
		}
	}
	clientConn.Close()

	if !strings.Contains(string(buf[:total]), "200 OK") {
		t.Errorf("expected 200 OK, got: %q", string(buf[:total]))
	}

	if err := f.server.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	if err := <-done; err != nil {
		t.Errorf("ListenAndServe returned: %v", err)
	}
}

// TestListenAndServe_MaxMiddlemen_DropsConnection covers the MaxMiddlemen
// branch (server.go:86-89) by setting MaxMiddlemen to 0 so every incoming
// connection is immediately closed.
func TestListenAndServe_MaxMiddlemen_DropsConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	f := newTestFixture()
	f.server.config.ListenAddr = addr
	f.server.config.MaxMiddlemen = 0

	done := make(chan error, 1)
	go func() { done <- f.server.ListenAndServe() }()

	// Wait for the server to start then connect.
	var clientConn net.Conn
	for i := 0; i < 100; i++ {
		time.Sleep(2 * time.Millisecond)
		c, dialErr := net.DialTimeout("tcp", addr, time.Second)
		if dialErr == nil {
			clientConn = c
			break
		}
	}
	if clientConn == nil {
		t.Fatal("could not connect within 200 ms")
	}
	defer clientConn.Close()

	// The server drops the connection immediately (conn.Close); reads return EOF.
	clientConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 64)
	n, _ := clientConn.Read(buf)
	if n != 0 {
		t.Errorf("expected 0 bytes from dropped connection, got %d", n)
	}

	if err := f.server.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	if err := <-done; err != nil {
		t.Errorf("ListenAndServe returned: %v", err)
	}
}
