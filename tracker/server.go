package tracker

import (
	"bufio"
	"context"
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
	listener net.Listener
	worker   *Worker
	config   *Config

	// Connection management
	mu              sync.Mutex
	activeConns     map[net.Conn]struct{}
	shutdownCtx     context.Context
	shutdownCancel  context.CancelFunc
	wg              sync.WaitGroup

	// Statistics
	stats *Stats
}

// Config holds server configuration
type Config struct {
	ListenAddr       string
	AnnounceInterval int
	PeersTimeout     int
	MaxMiddlemen     int // Max concurrent connections
	NumWantLimit     int
	KeepaliveTimeout time.Duration
	SitePassword     string
	ReportPassword   string
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
}

// NewServer creates a new tracker server
func NewServer(config *Config, worker *Worker) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		worker:         worker,
		config:         config,
		activeConns:    make(map[net.Conn]struct{}),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
		stats:          worker.Stats,
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
	// Create listener - this sets up the epoll/kqueue fd
	listener, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	s.listener = listener

	fmt.Printf("Ocelot tracker listening on %s (using %s netpoller)\n",
		s.config.ListenAddr, s.getNetpollerType())
	fmt.Printf("Worker goroutines: %d (GOMAXPROCS=%d)\n",
		runtime.NumGoroutine(), runtime.GOMAXPROCS(0))

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

		// Track connection
		s.mu.Lock()
		if len(s.activeConns) >= s.config.MaxMiddlemen {
			s.mu.Unlock()
			conn.Close()
			continue
		}
		s.activeConns[conn] = struct{}{}
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
		s.wg.Add(1)
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

	// Set TCP options (matches C++ events.cpp:200-208)
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)              // Disable Nagle's algorithm
		tcpConn.SetKeepAlive(true)            // Enable TCP keepalive
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

		// Process request and generate response
		response, httpClose := s.handleRequest(request, clientIP)

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

	if len(parts) < 2 {
		return s.errorResponse("Malformed announce", httpClose), httpClose
	}

	passkey := parts[0]
	action := parts[1]

	// Validate passkey length (32 characters)
	if len(passkey) != 32 {
		return s.errorResponse("Malformed announce", httpClose), httpClose
	}

	// Route to appropriate handler
	switch action {
	case "announce":
		s.stats.Announcements.Add(1)
		return s.handleAnnounce(req, passkey, clientIP, httpClose), httpClose

	case "scrape":
		s.stats.Scrapes.Add(1)
		return s.handleScrape(req, passkey, httpClose), httpClose

	case "update":
		if passkey == s.config.SitePassword {
			return s.handleUpdate(req, httpClose), httpClose
		}
		return s.errorResponse("Authentication failure", httpClose), httpClose

	default:
		return s.response("Nothing to see here", httpClose, false), httpClose
	}
}

// handleAnnounce processes a BitTorrent announce request
func (s *Server) handleAnnounce(req *http.Request, passkey string, clientIP net.IP, httpClose bool) []byte {
	// Look up user by passkey
	user, ok := s.worker.Users.Get(passkey)
	if !ok {
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
func (s *Server) handleScrape(req *http.Request, passkey string, httpClose bool) []byte {
	user, ok := s.worker.Users.Get(passkey)
	if !ok {
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
	// Implementation would handle add_torrent, update_user, etc.
	// Similar to C++ worker::update() (worker.cpp:768-996)
	return s.response("success", httpClose, false)
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

// response wraps content in HTTP response
func (s *Server) response(content string, httpClose bool, html bool) []byte {
	var b strings.Builder

	b.WriteString("HTTP/1.1 200 OK\r\n")

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

	if s.listener != nil {
		s.listener.Close()
	}

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
