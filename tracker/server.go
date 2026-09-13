package tracker

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Server is the high-performance tracker server
// Replaces C++ connection_mother and connection_middleman (events.cpp)
//
// Go's approach: Instead of manually managing epoll/kqueue/select with libev,
// Go's runtime handles this automatically through its netpoller:
//
// - On Linux: uses epoll (just like C++ libev)
// - On BSD/macOS: uses kqueue
// - On Windows: uses IOCP
//
// The key difference: Go abstracts this into goroutines, making code simpler
// while maintaining (or exceeding) C++ performance.
type Server struct {
	// One entry per accept loop: plaintext, and TLS when configured.
	listeners []net.Listener
	worker    *Worker
	config    *Config

	// Connection management
	mu             sync.Mutex
	activeConns    map[net.Conn]struct{}
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	wg             sync.WaitGroup

	// shuttingDown is set under mu once Shutdown begins, so the accept loops
	// stop registering work before wg.Wait starts.
	shuttingDown bool

	// Per-IP admission control. Nil when rate limiting is disabled.
	limiter *RateLimiter

	// Audit trail for security-relevant events. Nil when unconfigured.
	audit *AuditLogger

	// Statistics
	stats *Stats
}

// Config holds server configuration
type Config struct {
	ListenAddr       string
	AnnounceInterval int
	PeersTimeout     int // Seconds of silence before a peer is reaped
	ReapInterval     int // Seconds between reaper sweeps
	MaxMiddlemen     int // Max concurrent connections
	NumWantLimit     int
	KeepaliveTimeout time.Duration
	SitePassword     string
	ReportPassword   string
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration

	// MetricsAddr is the admin listener serving /metrics and the health
	// probes. It must not be the tracker port, which only routes
	// /{passkey}/{action}. Empty disables the admin listener.
	MetricsAddr string

	// RateLimitRPS and RateLimitBurst bound per-IP request rate. A real
	// client announces roughly twice an hour, so these are generous.
	// Zero RPS disables rate limiting.
	RateLimitRPS   int
	RateLimitBurst int

	// AuditRetentionDays bounds how long audit rows are kept. Zero keeps
	// them forever, which will eventually make it the largest table.
	AuditRetentionDays int

	// TLS. Passkeys travel in the request path, so without TLS every announce
	// hands a credential to anyone on the wire. Both files must be set to
	// enable the TLS listener; TLSAddr defaults to :34443.
	TLSCertFile string
	TLSKeyFile  string
	TLSAddr     string
}

// TLSEnabled reports whether a TLS listener should be started.
func (c *Config) TLSEnabled() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

// Sentinel reasons recorded in the audit trail.
var (
	errInvalidPasskeyLength = errors.New("passkey is not 32 characters")
	errAdminAuthFailure     = errors.New("site password mismatch")
	errUnknownPasskey       = errors.New("passkey not found")
)

// NewServer creates a new tracker server
func NewServer(config *Config, worker *Worker) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	s := &Server{
		worker:         worker,
		config:         config,
		activeConns:    make(map[net.Conn]struct{}),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
		stats:          worker.Stats,
	}

	if config.RateLimitRPS > 0 {
		s.limiter = NewRateLimiter(config.RateLimitRPS, config.RateLimitBurst)
		s.limiter.Cleanup()
	}

	return s
}

// sitePasswordMatches compares in constant time so response latency does not
// leak how much of the admin password an attacker has guessed.
func (s *Server) sitePasswordMatches(candidate string) bool {
	expected := s.config.SitePassword
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

// Addrs returns the addresses currently being served, which is how a caller
// learns the real port when the configured address uses port 0.
func (s *Server) Addrs() []net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()

	addrs := make([]net.Addr, 0, len(s.listeners))
	for _, listener := range s.listeners {
		addrs = append(addrs, listener.Addr())
	}
	return addrs
}

// SetAuditLogger attaches an audit trail for security-relevant events.
func (s *Server) SetAuditLogger(audit *AuditLogger) {
	s.audit = audit
}

// auditFailure records a rejected request. Only authentication and
// authorization failures are audited, never successful announces, which would
// dominate write volume. Rate limiting runs before this, so the write rate an
// attacker can provoke is already bounded per IP.
func (s *Server) auditFailure(clientIP net.IP, action, resourceType, resourceID string, reason error) {
	if s.audit == nil {
		return
	}

	ctx := context.Background()
	if clientIP != nil {
		ctx = context.WithValue(ctx, "ip", clientIP.String())
	}

	if err := s.audit.LogFailure(ctx, action, resourceType, resourceID, reason); err != nil {
		GetDefaultLogger().Error("failed to write audit entry", err, "action", action)
	}
}

// ListenAndServe starts the high-performance tracker server
//
// Go's net.Listen automatically uses:
// - epoll on Linux (via internal/poll package)
// - kqueue on BSD/macOS
// - IOCP on Windows
//
// This matches C++ libev's behavior but with zero manual setup!
func (s *Server) ListenAndServe() error {
	listener, err := s.listen(s.config.ListenAddr)
	if err != nil {
		return err
	}

	fmt.Printf("Ocelot tracker listening on %s (dual-stack IPv4/IPv6, using %s netpoller)\n",
		listener.Addr(), s.getNetpollerType())
	fmt.Printf("Worker goroutines: %d (GOMAXPROCS=%d)\n",
		runtime.NumGoroutine(), runtime.GOMAXPROCS(0))

	return s.serve(listener)
}

// ListenAndServeTLS serves the tracker over TLS on the configured address.
//
// TLS terminates at the listener, so every connection continues through the
// same pipeline as plaintext: routing, rate limiting, and audit logging all
// apply unchanged. Run it alongside ListenAndServe to offer both.
func (s *Server) ListenAndServeTLS() error {
	if s.config.TLSCertFile == "" || s.config.TLSKeyFile == "" {
		return fmt.Errorf("TLS requires both tls_cert_file and tls_key_file")
	}

	cert, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("failed to load TLS keypair: %w", err)
	}

	listener, err := s.listen(s.config.TLSAddr)
	if err != nil {
		return err
	}

	// TLS 1.2 is the floor: 1.3-only would lock out older BitTorrent clients.
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})

	s.mu.Lock()
	s.listeners = append(s.listeners, tlsListener)
	s.mu.Unlock()

	fmt.Printf("Ocelot tracker listening for TLS on %s\n", listener.Addr())

	return s.serve(tlsListener)
}

// listen opens a dual-stack TCP listener and registers it for shutdown.
func (s *Server) listen(addr string) (net.Listener, error) {
	// Listen on [::] for dual-stack IPv4/IPv6 support
	if !strings.Contains(addr, ":") || addr[0] != '[' {
		if strings.HasPrefix(addr, ":") {
			addr = "[::]" + addr
		}
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.mu.Lock()
	s.listeners = append(s.listeners, listener)
	s.mu.Unlock()

	return listener, nil
}

// serve runs the accept loop for a listener.
func (s *Server) serve(listener net.Listener) error {
	// Accept loop (replaces C++ connection_mother::handle_connect)
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-s.shutdownCtx.Done():
				return nil // Clean shutdown
			default:
				fmt.Printf("Accept error: %v\n", err)
				continue
			}
		}

		// Track connection. wg.Add happens under the same lock that Shutdown
		// uses to set shuttingDown, so no Add can race an in-flight Wait.
		s.mu.Lock()
		if s.shuttingDown {
			s.mu.Unlock()
			conn.Close()
			return nil
		}
		if len(s.activeConns) >= s.config.MaxMiddlemen {
			s.mu.Unlock()
			conn.Close()
			continue
		}
		s.activeConns[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()

		s.stats.OpenConnections.Add(1)
		s.stats.OpenedConnections.Add(1)

		// Handle connection in new goroutine (replaces C++ connection_middleman)
		// This is WHERE THE MAGIC HAPPENS:
		//
		// In C++: libev manually manages epoll events, callbacks, and state machines
		// In Go: The runtime's netpoller handles epoll, goroutine scheduling is automatic
		//
		// When a goroutine blocks on conn.Read(), the Go runtime:
		// 1. Parks the goroutine
		// 2. Registers the fd with epoll (EPOLLIN)
		// 3. Schedules other goroutines on the OS thread
		// 4. When epoll signals readable, resumes the goroutine
		//
		// Result: Thousands of concurrent connections with minimal OS threads!
		go s.handleConnection(conn)
	}
}

// handleConnection handles a single client connection
// This replaces C++ connection_middleman (events.cpp:168-396)
//
// Key architectural difference from C++:
// - C++: State machine with ev::io read_event, write_event, timeout_event
// - Go: Straightforward sequential code, runtime handles I/O multiplexing
func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.activeConns, conn)
		s.mu.Unlock()
		s.stats.OpenConnections.Add(^uint32(0)) // Atomic decrement
	}()

	// Set TCP options (matches C++ events.cpp:200-208). Unwrap first: a TLS
	// connection is not a *net.TCPConn, so tuning would silently be skipped.
	tuneTarget := conn
	if tlsConn, ok := conn.(*tls.Conn); ok {
		tuneTarget = tlsConn.NetConn()
	}
	if tcpConn, ok := tuneTarget.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)   // Disable Nagle's algorithm
		tcpConn.SetKeepAlive(true) // Enable TCP keepalive
		tcpConn.SetKeepAlivePeriod(2 * time.Minute)
	}

	reader := bufio.NewReaderSize(conn, 4096)
	keepalive := s.config.KeepaliveTimeout > 0

	for {
		// Set read deadline (replaces C++ ev::timer timeout_event)
		if s.config.ReadTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))
		}

		// Read HTTP request (replaces C++ connection_middleman::handle_read)
		// When this blocks, Go runtime:
		// 1. Parks this goroutine
		// 2. Adds conn's fd to epoll with EPOLLIN | EPOLLONESHOT
		// 3. Runs other goroutines on this OS thread
		// 4. When epoll returns (data available), resumes this goroutine
		request, err := http.ReadRequest(reader)
		if err != nil {
			if err != io.EOF {
				// Could be timeout or malformed request
			}
			return
		}

		s.stats.Requests.Add(1)
		s.stats.BytesRead.Add(uint64(request.ContentLength))

		// Get client IP (handle X-Forwarded-For)
		clientIP := s.getClientIP(conn, request)

		// Admission control before any routing or database work.
		var response []byte
		var httpClose bool
		if s.limiter != nil && !s.limiter.Allow(clientIP.String()) {
			GetMetricsRecorder().RecordRateLimitExceeded()
			response = s.responseWithStatus(http.StatusTooManyRequests,
				"Rate limit exceeded", true, false)
			httpClose = true
		} else {
			response, httpClose = s.handleRequest(request, clientIP)
		}

		// Set write deadline (replaces C++ ev::timer)
		if s.config.WriteTimeout > 0 {
			conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
		}

		// Write response (replaces C++ connection_middleman::handle_write)
		// Same epoll magic as Read, but with EPOLLOUT
		written, err := conn.Write(response)
		if err != nil {
			return
		}

		s.stats.BytesWritten.Add(uint64(written))

		// Handle connection close
		if httpClose || !keepalive {
			return
		}

		// Reuse connection for next request (HTTP keep-alive)
	}
}

// handleRequest processes a tracker request
// This is the entry point that calls worker.Announce() or worker.Scrape()
func (s *Server) handleRequest(req *http.Request, clientIP net.IP) ([]byte, bool) {
	httpClose := true
	if s.config.KeepaliveTimeout > 0 {
		// Check Connection header for keep-alive
		if req.ProtoMajor == 1 && req.ProtoMinor == 0 {
			httpClose = true
		} else {
			httpClose = strings.ToLower(req.Header.Get("Connection")) == "close"
		}
	}

	// Parse URL path: /{passkey}/{action}?params
	// Example: /0123456789abcdef0123456789abcdef/announce?info_hash=...
	path := strings.TrimPrefix(req.URL.Path, "/")
	parts := strings.Split(path, "/")

	// Fewer than two segments is not a tracker request at all (a stray probe
	// or a browser hit), so answer 404 rather than a 200 bencode error.
	if len(parts) < 2 {
		return s.responseWithStatus(http.StatusNotFound, "Not found", httpClose, false), httpClose
	}

	passkey := parts[0]
	action := parts[1]

	// Validate passkey length (32 characters)
	if len(passkey) != 32 {
		s.auditFailure(clientIP, "auth_failure", "passkey", "", errInvalidPasskeyLength)
		return s.errorResponse("Malformed announce", httpClose), httpClose
	}

	// Route to appropriate handler
	switch action {
	case "announce":
		s.stats.Announcements.Add(1)
		return s.handleAnnounce(req, passkey, clientIP, httpClose), httpClose

	case "scrape":
		s.stats.Scrapes.Add(1)
		return s.handleScrape(req, passkey, clientIP, httpClose), httpClose

	case "update":
		if s.sitePasswordMatches(passkey) {
			return s.handleUpdate(req, httpClose), httpClose
		}
		s.auditFailure(clientIP, "admin_auth_failure", "endpoint", action, errAdminAuthFailure)
		return s.errorResponse("Authentication failure", httpClose), httpClose

	case "stats":
		if s.sitePasswordMatches(passkey) {
			return s.handleStatsAPI(httpClose), httpClose
		}
		s.auditFailure(clientIP, "admin_auth_failure", "endpoint", action, errAdminAuthFailure)
		return s.errorResponse("Authentication failure", httpClose), httpClose

	case "torrents":
		if s.sitePasswordMatches(passkey) {
			return s.handleTorrentsAPI(req, httpClose), httpClose
		}
		s.auditFailure(clientIP, "admin_auth_failure", "endpoint", action, errAdminAuthFailure)
		return s.errorResponse("Authentication failure", httpClose), httpClose

	case "peers":
		if s.sitePasswordMatches(passkey) {
			return s.handlePeersAPI(req, httpClose), httpClose
		}
		s.auditFailure(clientIP, "admin_auth_failure", "endpoint", action, errAdminAuthFailure)
		return s.errorResponse("Authentication failure", httpClose), httpClose

	case "whitelist":
		if s.sitePasswordMatches(passkey) {
			return s.handleWhitelistAPI(httpClose), httpClose
		}
		s.auditFailure(clientIP, "admin_auth_failure", "endpoint", action, errAdminAuthFailure)
		return s.errorResponse("Authentication failure", httpClose), httpClose

	default:
		return s.responseWithStatus(http.StatusNotFound, "Not found", httpClose, false), httpClose
	}
}

// handleAnnounce processes a BitTorrent announce request
func (s *Server) handleAnnounce(req *http.Request, passkey string, clientIP net.IP, httpClose bool) []byte {
	// Look up user by passkey
	user, ok := s.worker.Users.Get(passkey)
	if !ok {
		s.auditFailure(clientIP, "auth_failure", "passkey", "", errUnknownPasskey)
		return s.errorResponse("Passkey not found", httpClose)
	}

	// Parse announce parameters
	params := req.URL.Query()
	announceReq, err := ParseAnnounceParams(params, clientIP)
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}

	// Handle X-Forwarded-For if IP not in params
	if announceReq.IP == nil || announceReq.IP.IsUnspecified() {
		if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
			if commaIdx := strings.Index(xff, ","); commaIdx > 0 {
				announceReq.IP = net.ParseIP(strings.TrimSpace(xff[:commaIdx]))
			} else {
				announceReq.IP = net.ParseIP(xff)
			}
		}
	}

	// Get info_hash and look up torrent
	infoHash := params.Get("info_hash")
	if infoHash == "" {
		return s.errorResponse("Missing info_hash", httpClose)
	}

	_, ok = s.worker.Torrents.Get(infoHash)
	if !ok {
		return s.errorResponse("Unregistered torrent", httpClose)
	}

	// Call the announce handler
	userAgent := req.Header.Get("User-Agent")
	announceResp, err := s.worker.Announce(announceReq, user, clientIP, userAgent)
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}

	// Build bencoded response
	return s.bencodedAnnounceResponse(announceResp, httpClose)
}

// handleScrape processes a BitTorrent scrape request
func (s *Server) handleScrape(req *http.Request, passkey string, clientIP net.IP, httpClose bool) []byte {
	user, ok := s.worker.Users.Get(passkey)
	if !ok {
		s.auditFailure(clientIP, "auth_failure", "passkey", "", errUnknownPasskey)
		return s.errorResponse("Passkey not found", httpClose)
	}
	_ = user // User validated, scrape doesn't need user object

	// Parse info_hash list
	params := req.URL.Query()
	infoHashes := params["info_hash"]

	// Build scrape response
	response := "d5:filesd"

	for _, infoHash := range infoHashes {
		torrent, ok := s.worker.Torrents.Get(infoHash)
		if !ok {
			continue
		}

		torrent.mu.RLock()
		seederCount := torrent.Seeders.Size()
		leecherCount := torrent.Leechers.Size()
		completed := torrent.Completed
		torrent.mu.RUnlock()

		// Bencode: length:hash d8:completei<seeders>e10:incompletei<leechers>e10:downloadedi<completed>ee
		response += fmt.Sprintf("%d:%s", len(infoHash), infoHash)
		response += fmt.Sprintf("d8:completei%de10:incompletei%de10:downloadedi%dee",
			seederCount, leecherCount, completed)
	}

	response += "ee"
	return s.response(response, httpClose, false)
}

// handleUpdate processes tracker update requests (admin API)
func (s *Server) handleUpdate(req *http.Request, httpClose bool) []byte {
	jsonData, err := s.worker.HandleUpdate(req)
	if err != nil {
		return s.jsonResponse(jsonData, httpClose)
	}
	return s.jsonResponse(jsonData, httpClose)
}

// handleStatsAPI returns tracker statistics as JSON
func (s *Server) handleStatsAPI(httpClose bool) []byte {
	jsonData, err := s.worker.GetStats()
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}
	return s.jsonResponse(jsonData, httpClose)
}

// handleTorrentsAPI returns torrent list as JSON
func (s *Server) handleTorrentsAPI(req *http.Request, httpClose bool) []byte {
	limit := queryInt(req, "limit", 100)
	jsonData, err := s.worker.GetTorrents(limit)
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}
	return s.jsonResponse(jsonData, httpClose)
}

// handlePeersAPI returns peer list for a torrent as JSON
func (s *Server) handlePeersAPI(req *http.Request, httpClose bool) []byte {
	infoHash := req.URL.Query().Get("info_hash")
	if infoHash == "" {
		return s.errorResponse("Missing info_hash", httpClose)
	}
	limit := queryInt(req, "limit", 100)
	jsonData, err := s.worker.GetPeers(infoHash, limit)
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}
	return s.jsonResponse(jsonData, httpClose)
}

// handleWhitelistAPI returns whitelist as JSON
func (s *Server) handleWhitelistAPI(httpClose bool) []byte {
	jsonData, err := s.worker.GetWhitelist()
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}
	return s.jsonResponse(jsonData, httpClose)
}

// jsonResponse wraps JSON content in HTTP response
func (s *Server) jsonResponse(content []byte, httpClose bool) []byte {
	var b strings.Builder

	b.WriteString("HTTP/1.1 200 OK\r\n")
	b.WriteString("Content-Type: application/json\r\n")
	b.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(content)))

	if httpClose {
		b.WriteString("Connection: close\r\n")
	} else {
		b.WriteString("Connection: keep-alive\r\n")
	}

	b.WriteString("\r\n")
	b.Write(content)

	return []byte(b.String())
}

// bencodedAnnounceResponse builds a bencoded announce response
func (s *Server) bencodedAnnounceResponse(resp *AnnounceResponse, httpClose bool) []byte {
	// Build bencoded dictionary (matches C++ worker.cpp:703-725)
	var b strings.Builder
	b.Grow(350)

	b.WriteString("d8:completei")
	b.WriteString(fmt.Sprintf("%d", resp.Complete))
	b.WriteString("e10:downloadedi")
	b.WriteString(fmt.Sprintf("%d", 0)) // TODO: get from torrent
	b.WriteString("e10:incompletei")
	b.WriteString(fmt.Sprintf("%d", resp.Incomplete))
	b.WriteString("e8:intervali")
	b.WriteString(fmt.Sprintf("%d", resp.Interval))
	b.WriteString("e12:min intervali")
	b.WriteString(fmt.Sprintf("%d", resp.MinInterval))
	b.WriteString("e5:peers")

	if len(resp.Peers) == 0 {
		b.WriteString("0:")
	} else {
		b.WriteString(fmt.Sprintf("%d:", len(resp.Peers)))
		b.Write(resp.Peers)
	}

	// BEP 7 puts IPv6 peers in a separate key. Bencode dictionaries are
	// ordered by key, and "peers" sorts before "peers6" before "warning
	// message", so this belongs here. Omitted when empty, as BEP 7 allows.
	if len(resp.Peers6) > 0 {
		b.WriteString("6:peers6")
		b.WriteString(fmt.Sprintf("%d:", len(resp.Peers6)))
		b.Write(resp.Peers6)
	}

	if resp.Warning != "" {
		// Add warning message
		b.WriteString("15:warning message")
		b.WriteString(fmt.Sprintf("%d:", len(resp.Warning)))
		b.WriteString(resp.Warning)
	}

	b.WriteString("e")

	return s.response(b.String(), httpClose, false)
}

// errorResponse returns a bencoded error response
func (s *Server) errorResponse(msg string, httpClose bool) []byte {
	response := fmt.Sprintf("d14:failure reason%d:%se", len(msg), msg)
	return s.response(response, httpClose, false)
}

// response wraps content in a 200 HTTP response
func (s *Server) response(content string, httpClose bool, html bool) []byte {
	return s.responseWithStatus(http.StatusOK, content, httpClose, html)
}

// responseWithStatus wraps content in an HTTP response carrying the given
// status. Non-protocol paths must not answer 200, or an external health probe
// pointed at this port passes while the tracker is unhealthy.
func (s *Server) responseWithStatus(status int, content string, httpClose bool, html bool) []byte {
	var b strings.Builder

	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))

	if html {
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	} else {
		b.WriteString("Content-Type: text/plain\r\n")
	}

	b.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(content)))

	if httpClose {
		b.WriteString("Connection: close\r\n")
	} else {
		b.WriteString("Connection: keep-alive\r\n")
	}

	b.WriteString("\r\n")
	b.WriteString(content)

	return []byte(b.String())
}

// getClientIP extracts the client IP from connection or headers
func (s *Server) getClientIP(conn net.Conn, req *http.Request) net.IP {
	// Check X-Forwarded-For header first
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		if commaIdx := strings.Index(xff, ","); commaIdx > 0 {
			return net.ParseIP(strings.TrimSpace(xff[:commaIdx]))
		}
		return net.ParseIP(xff)
	}

	// Fall back to connection remote address
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return tcpAddr.IP
	}

	return nil
}

// getNetpollerType returns the I/O multiplexing method used
func (s *Server) getNetpollerType() string {
	switch runtime.GOOS {
	case "linux":
		return "epoll"
	case "darwin", "freebsd", "openbsd", "netbsd":
		return "kqueue"
	case "windows":
		return "IOCP"
	default:
		return "select"
	}
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	s.shutdownCancel()

	s.mu.Lock()
	s.shuttingDown = true
	for _, listener := range s.listeners {
		listener.Close()
	}
	// Close connections too. Closing only the listeners leaves keep-alive
	// handlers parked in ReadRequest, so every shutdown would burn the full
	// timeout below before exiting.
	for conn := range s.activeConns {
		conn.Close()
	}
	s.mu.Unlock()

	// Safe to wait now: every wg.Add happens under mu, and no further Add can
	// occur because the accept loops see shuttingDown.
	// Wait for all connections to finish (with timeout)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("shutdown timeout")
	}
}

// Worker encapsulates tracker business logic
type Worker struct {
	Config    *Config
	DB        DatabaseInterface
	SiteComm  SiteCommInterface
	Torrents  *TorrentList
	Users     *UserList
	Whitelist *Whitelist
	Stats     *Stats
}

// DatabaseInterface abstracts database operations
// Replaces C++ mysql class (db.cpp)
type DatabaseInterface interface {
	RecordPeer(userID UserID, torrentID TorrentID, active int, uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64, announceTime, announces uint32, ip, peerID, userAgent string) error
	RecordPeerLight(userID UserID, torrentID TorrentID, announceTime, announces uint32, peerID string) error
	RecordUserStats(userID UserID, uploaded, downloaded int64) error
	RecordTorrent(torrentID TorrentID, seeders, leechers uint32, snatched int, balance int64) error
	RecordSnatch(userID UserID, torrentID TorrentID, time time.Time, ip string) error
	RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error
	Close() error
}

// SiteCommInterface abstracts site communication
type SiteCommInterface interface {
	ExpireToken(torrentID TorrentID, userID UserID)
}
