package tracker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is the high-performance tracker server.
// Go's runtime netpoller replaces C++ libev (epoll on Linux, kqueue on BSD).
type Server struct {
	listener       net.Listener
	worker         *Worker
	config         *Config
	mu             sync.Mutex
	activeConns    map[net.Conn]struct{}
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	wg             sync.WaitGroup
	stats          *Stats

	// adapters maps action verb → DomainAdapter for vocab-driven routing.
	// Built via RegisterAdapter; the legacy BT routes remain as fast paths.
	adaptersMu sync.RWMutex
	adapters   map[string]DomainAdapter // key: action verb (e.g. "checkin")
}

// Config holds server and tracker configuration.
type Config struct {
	ListenAddr        string
	AnnounceInterval  int
	PeersTimeout      int
	MaxMiddlemen      int
	NumWantLimit      int
	KeepaliveTimeout  time.Duration
	SitePassword      string
	ReportPassword    string
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	ScheduleInterval  int
	ReapPeersInterval int
	GazelleURL        string
	MetricsPort       string
	// RateLimiter config — 0 disables rate limiting.
	RateLimitRPS   int
	RateLimitBurst int
	// BatchBufferCap is the async write queue capacity for BufferedDB.
	BatchBufferCap int
	// TLS configuration — CertFile empty means plaintext.
	TLS TLSConfig
}

func NewServer(config *Config, worker *Worker) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		worker:         worker,
		config:         config,
		activeConns:    make(map[net.Conn]struct{}),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
		stats:          worker.Stats,
		adapters:       make(map[string]DomainAdapter),
	}
}

// RegisterAdapter registers a DomainAdapter for its EventAction (and QueryAction
// if non-empty). Custom domains route through handleDomainRequest instead of
// the legacy BitTorrent fast paths.
func (s *Server) RegisterAdapter(a DomainAdapter) {
	s.adaptersMu.Lock()
	defer s.adaptersMu.Unlock()
	s.adapters[a.EventAction()] = a
	if q := a.QueryAction(); q != "" && q != a.EventAction() {
		s.adapters[q] = a
	}
}

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	return s.serve(ln)
}

// ListenAndServeTLS starts the tracker with TLS using the given cert and key
// files.  The server's ListenAddr is used as the bind address.
func (s *Server) ListenAndServeTLS(certFile, keyFile string) error {
	ln, err := newTLSListener(s.config.ListenAddr, certFile, keyFile)
	if err != nil {
		return err
	}
	return s.serve(ln)
}

// ListenAndServeAutoTLS starts the tracker with Let's Encrypt-managed TLS.
func (s *Server) ListenAndServeAutoTLS(domain, cacheDir string) error {
	ln, err := newAutoTLSListener(s.config.ListenAddr, domain, cacheDir)
	if err != nil {
		return err
	}
	return s.serve(ln)
}

// serve runs the accept loop on an already-created listener.
func (s *Server) serve(ln net.Listener) error {
	s.listener = ln

	fmt.Printf("Ocelot tracker listening on %s (%s netpoller)\n",
		s.config.ListenAddr, netpollerType())
	fmt.Printf("GOMAXPROCS=%d\n", runtime.GOMAXPROCS(0))

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.shutdownCtx.Done():
				return nil
			default:
				fmt.Printf("Accept error: %v\n", err)
				continue
			}
		}

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

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.activeConns, conn)
		s.mu.Unlock()
		s.stats.OpenConnections.Add(^uint32(0))
	}()

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(2 * time.Minute)
	}

	reader := bufio.NewReaderSize(conn, 4096)
	keepalive := s.config.KeepaliveTimeout > 0

	for {
		if s.config.ReadTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))
		}

		request, err := http.ReadRequest(reader)
		if err != nil {
			if err != io.EOF {
				// timeout or malformed request — close silently
			}
			return
		}

		s.stats.Requests.Add(1)
		s.stats.BytesRead.Add(uint64(request.ContentLength))

		clientIP := s.getClientIP(conn, request)
		response, httpClose := s.handleRequest(request, clientIP)

		if s.config.WriteTimeout > 0 {
			conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
		}

		written, err := conn.Write(response)
		if err != nil {
			return
		}
		s.stats.BytesWritten.Add(uint64(written))

		if httpClose || !keepalive {
			return
		}
	}
}

func (s *Server) handleRequest(req *http.Request, clientIP net.IP) ([]byte, bool) {
	httpClose := true
	if s.config.KeepaliveTimeout > 0 {
		if req.ProtoMajor == 1 && req.ProtoMinor == 0 {
			httpClose = true
		} else {
			httpClose = strings.ToLower(req.Header.Get("Connection")) == "close"
		}
	}

	// Per-IP rate limiting — checked before any authentication or routing.
	if s.worker.RateLimiter != nil {
		ipStr := ""
		if clientIP != nil {
			ipStr = clientIP.String()
		}
		if ipStr == "" {
			ipStr = req.RemoteAddr
		}
		if !s.worker.RateLimiter.Allow(ipStr) {
			return s.rateLimitResponse(httpClose), httpClose
		}
	}

	path := strings.TrimPrefix(req.URL.Path, "/")
	parts := strings.Split(path, "/")

	if len(parts) < 2 {
		return s.errorResponse("Malformed announce", httpClose), httpClose
	}

	passkey := parts[0]
	action := parts[1]

	if len(passkey) != 32 {
		return s.errorResponse("Malformed announce", httpClose), httpClose
	}

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

	case "stats":
		if passkey == s.config.SitePassword {
			return s.handleStatsAPI(httpClose), httpClose
		}
		return s.errorResponse("Authentication failure", httpClose), httpClose

	case "torrents":
		if passkey == s.config.SitePassword {
			return s.handleTorrentsAPI(req, httpClose), httpClose
		}
		return s.errorResponse("Authentication failure", httpClose), httpClose

	case "peers":
		if passkey == s.config.SitePassword {
			return s.handlePeersAPI(req, httpClose), httpClose
		}
		return s.errorResponse("Authentication failure", httpClose), httpClose

	case "whitelist":
		if passkey == s.config.SitePassword {
			return s.handleWhitelistAPI(httpClose), httpClose
		}
		return s.errorResponse("Authentication failure", httpClose), httpClose

	default:
		// Try registered domain adapters before giving up.
		s.adaptersMu.RLock()
		adapter, ok := s.adapters[action]
		s.adaptersMu.RUnlock()
		if ok {
			return s.handleDomainRequest(req, passkey, clientIP, httpClose, adapter), httpClose
		}
		return s.response("Nothing to see here", httpClose, false), httpClose
	}
}

// handleDomainRequest dispatches to a DomainAdapter for non-BT domains.
func (s *Server) handleDomainRequest(req *http.Request, passkey string, clientIP net.IP, httpClose bool, a DomainAdapter) []byte {
	user, ok := s.worker.Users.Get(passkey)
	if !ok {
		return a.FormatError("Passkey not found", httpClose)
	}

	opts := ClientOpts{
		Passkey:   passkey,
		UserAgent: req.Header.Get("User-Agent"),
		ClientIP:  clientIP,
	}

	action := strings.TrimPrefix(req.URL.Path, "/")
	parts := strings.SplitN(action, "/", 3)
	verb := ""
	if len(parts) >= 2 {
		verb = parts[1]
	}

	if verb == a.QueryAction() && a.QueryAction() != "" {
		keys, err := a.ParseQuery(req, opts)
		if err != nil {
			return a.FormatError(err.Error(), httpClose)
		}
		qresp := &QueryResponse{}
		for _, key := range keys {
			torrent, exists := s.worker.Torrents.Get(key)
			if !exists {
				continue
			}
			torrent.mu.RLock()
			qresp.Entries = append(qresp.Entries, QueryEntry{
				ResourceKey: key,
				Providers:   int32(torrent.Seeders.Size()),
				Consumers:   int32(torrent.Leechers.Size()),
				Completed:   torrent.Completed,
			})
			torrent.mu.RUnlock()
		}
		return a.FormatQueryResponse(qresp, httpClose)
	}

	event, err := a.ParseEvent(req, opts)
	if err != nil {
		return a.FormatError(err.Error(), httpClose)
	}

	if !a.ValidateAgent(event.AgentID) {
		return a.FormatError("agent not on allowlist", httpClose)
	}

	// Handle X-Forwarded-For override.
	if event.IP == nil || event.IP.IsUnspecified() {
		if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.Index(xff, ","); i > 0 {
				event.IP = net.ParseIP(strings.TrimSpace(xff[:i]))
			} else {
				event.IP = net.ParseIP(xff)
			}
		}
	}

	announceReq := EventToAnnounceRequest(event, nil)
	announceResp, err := s.worker.Announce(announceReq, user, clientIP, opts.UserAgent)
	if err != nil {
		return a.FormatError(err.Error(), httpClose)
	}

	s.stats.Announcements.Add(1)
	return a.FormatEventResponse(EventResponseFromAnnounce(announceResp), httpClose)
}

func (s *Server) handleAnnounce(req *http.Request, passkey string, clientIP net.IP, httpClose bool) []byte {
	user, ok := s.worker.Users.Get(passkey)
	if !ok {
		return s.errorResponse("Passkey not found", httpClose)
	}

	params := req.URL.Query()
	announceReq, err := ParseAnnounceParams(params, clientIP)
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}

	if announceReq.IP == nil || announceReq.IP.IsUnspecified() {
		if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.Index(xff, ","); i > 0 {
				announceReq.IP = net.ParseIP(strings.TrimSpace(xff[:i]))
			} else {
				announceReq.IP = net.ParseIP(xff)
			}
		}
	}

	userAgent := req.Header.Get("User-Agent")
	announceResp, err := s.worker.Announce(announceReq, user, clientIP, userAgent)
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}

	return s.bencodedAnnounceResponse(announceResp, httpClose)
}

func (s *Server) handleScrape(req *http.Request, passkey string, httpClose bool) []byte {
	_, ok := s.worker.Users.Get(passkey)
	if !ok {
		return s.errorResponse("Passkey not found", httpClose)
	}

	infoHashes := req.URL.Query()["info_hash"]
	var b strings.Builder
	b.WriteString("d5:filesd")

	for _, infoHash := range infoHashes {
		torrent, ok := s.worker.Torrents.Get(infoHash)
		if !ok {
			continue
		}
		torrent.mu.RLock()
		seeders := torrent.Seeders.Size()
		leechers := torrent.Leechers.Size()
		completed := torrent.Completed
		torrent.mu.RUnlock()

		b.WriteString(fmt.Sprintf("%d:%s", len(infoHash), infoHash))
		b.WriteString(fmt.Sprintf("d8:completei%de10:incompletei%de10:downloadedi%dee",
			seeders, leechers, completed))
	}
	b.WriteString("ee")
	return s.response(b.String(), httpClose, false)
}

// handleUpdate processes admin update requests.
// Fixed: original port had both branches returning jsonResponse.
func (s *Server) handleUpdate(req *http.Request, httpClose bool) []byte {
	jsonData, err := s.worker.HandleUpdate(req)
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}
	return s.jsonResponse(jsonData, httpClose)
}

func (s *Server) handleStatsAPI(httpClose bool) []byte {
	jsonData, err := s.worker.GetStats()
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}
	return s.jsonResponse(jsonData, httpClose)
}

func (s *Server) handleTorrentsAPI(req *http.Request, httpClose bool) []byte {
	limit := queryInt(req, "limit", 100)
	jsonData, err := s.worker.GetTorrents(limit)
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}
	return s.jsonResponse(jsonData, httpClose)
}

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

func (s *Server) handleWhitelistAPI(httpClose bool) []byte {
	jsonData, err := s.worker.GetWhitelist()
	if err != nil {
		return s.errorResponse(err.Error(), httpClose)
	}
	return s.jsonResponse(jsonData, httpClose)
}

// ── Response helpers ──────────────────────────────────────────────────────────

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

func (s *Server) bencodedAnnounceResponse(resp *AnnounceResponse, httpClose bool) []byte {
	var b strings.Builder
	b.Grow(350)

	b.WriteString("d8:completei")
	b.WriteString(strconv.FormatInt(int64(resp.Complete), 10))
	b.WriteString("e10:incompletei")
	b.WriteString(strconv.FormatInt(int64(resp.Incomplete), 10))
	b.WriteString("e8:intervali")
	b.WriteString(strconv.FormatInt(int64(resp.Interval), 10))
	b.WriteString("e12:min intervali")
	b.WriteString(strconv.FormatInt(int64(resp.MinInterval), 10))
	b.WriteString("e5:peers")

	if len(resp.Peers) == 0 {
		b.WriteString("0:")
	} else {
		b.WriteString(strconv.Itoa(len(resp.Peers)))
		b.WriteString(":")
		b.Write(resp.Peers)
	}

	if resp.Warning != "" {
		b.WriteString("15:warning message")
		b.WriteString(strconv.Itoa(len(resp.Warning)))
		b.WriteString(":")
		b.WriteString(resp.Warning)
	}

	b.WriteString("e")
	return s.response(b.String(), httpClose, false)
}

func (s *Server) errorResponse(msg string, httpClose bool) []byte {
	resp := fmt.Sprintf("d14:failure reason%d:%se", len(msg), msg)
	return s.response(resp, httpClose, false)
}

func (s *Server) rateLimitResponse(httpClose bool) []byte {
	body := "d14:failure reason21:Rate limit exceedede"
	var b strings.Builder
	b.WriteString("HTTP/1.1 429 Too Many Requests\r\n")
	b.WriteString("Content-Type: text/plain\r\n")
	b.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
	b.WriteString("Retry-After: 60\r\n")
	if httpClose {
		b.WriteString("Connection: close\r\n")
	} else {
		b.WriteString("Connection: keep-alive\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

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

// ── Utility ───────────────────────────────────────────────────────────────────

func (s *Server) getClientIP(conn net.Conn, req *http.Request) net.IP {
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return net.ParseIP(strings.TrimSpace(xff[:i]))
		}
		return net.ParseIP(xff)
	}
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return tcpAddr.IP
	}
	return nil
}

// queryInt parses an integer query parameter, returning defaultVal if absent or invalid.
func queryInt(req *http.Request, key string, defaultVal int) int {
	s := req.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return defaultVal
	}
	return v
}

func netpollerType() string {
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

func (s *Server) Shutdown() error {
	s.shutdownCancel()
	if s.listener != nil {
		s.listener.Close()
	}
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

// ── Worker ────────────────────────────────────────────────────────────────────

// Worker encapsulates tracker business logic.
type Worker struct {
	Config       *Config
	DB           DatabaseInterface
	SiteComm     SiteCommInterface
	Torrents     *TorrentList
	Users        *UserList
	Whitelist    *Whitelist
	Stats        *Stats
	RateLimiter  *RateLimiter
	CircuitBreak *CircuitBreaker
	AuditLog     *AuditLogger
	Metrics      *MetricsRecorder
	Detector       BehaviorDetector // per-peer behaviour anomaly detection; nil disables
	ClientDetector ClientDetector   // client-pattern check at announce entry; nil disables
	reaper         *Reaper          // created by Start()
}

// Start initialises subsystems that depend on Worker fields being populated:
//   - RateLimiter (if RateLimitRPS > 0 and none was injected)
//   - Reaper goroutine
func (w *Worker) Start() {
	if w.RateLimiter == nil && w.Config.RateLimitRPS > 0 {
		burst := w.Config.RateLimitBurst
		if burst <= 0 {
			burst = w.Config.RateLimitRPS * 2
		}
		w.RateLimiter = NewRateLimiter(w.Config.RateLimitRPS, burst, 50_000)
	}

	interval := w.Config.ReapPeersInterval
	if interval <= 0 {
		interval = 1800
	}
	timeout := w.Config.PeersTimeout
	if timeout <= 0 {
		timeout = 7200
	}
	w.reaper = NewReaper(w.Torrents, w.Stats, interval, timeout)
	w.reaper.Start()
}

// Stop shuts down the Reaper and flushes any buffered DB writes.
func (w *Worker) Stop() {
	if w.reaper != nil {
		w.reaper.Stop()
	}
	if bdb, ok := w.DB.(*BufferedDB); ok {
		bdb.Flush()
	}
}

// DatabaseInterface abstracts all database operations used by the tracker.
type DatabaseInterface interface {
	// Announce-path writes
	RecordPeer(userID UserID, torrentID TorrentID, active int, uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64, announceTime, announces uint32, ip, peerID, userAgent string) error
	RecordPeerLight(userID UserID, torrentID TorrentID, announceTime, announces uint32, peerID string) error
	RecordUserStats(userID UserID, uploaded, downloaded int64) error
	RecordTorrent(torrentID TorrentID, seeders, leechers uint32, snatched int, balance int64) error
	RecordSnatch(userID UserID, torrentID TorrentID, t time.Time, ip string) error
	RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error

	// Admin writes
	RecordTorrentHash(id TorrentID, infoHash string) error
	RecordUserPasskey(id UserID, passkey string, canLeech, protectIP bool) error
	AddWhitelistEntry(prefix string) error
	RemoveWhitelistEntry(prefix string) error
	DeleteToken(userID UserID, torrentID TorrentID) error

	// State reload reads
	LoadTorrents() ([]torrentLoadRow, error)
	LoadUsers() ([]userLoadRow, error)
	LoadWhitelist() ([]string, error)
	LoadTokens() (map[string][]UserID, error)

	// Maintenance
	CheckpointWAL() error
	CheckRotation() error

	Close() error
}

// SiteCommInterface abstracts communication back to the Gazelle web application.
type SiteCommInterface interface {
	ExpireToken(torrentID TorrentID, userID UserID)
	NotifyFreeleech(torrentID int64, hours int) error
	ReportAnomaly(userID int64, score float64) error
	UpdateStats(seeders, leechers, completed int64) error
	BanUser(userID int64) error
	UnbanUser(userID int64) error
}
