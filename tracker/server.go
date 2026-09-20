package tracker

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mgdavisxvs/Ocelot/ml"
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
	pool           *WorkerPool // bounded goroutine pool for connection handlers

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
	MarkovAPIURL      string // Markov engine HTTP API base URL (empty = disabled)
	MetricsPort       string
	// RateLimiter config — 0 disables rate limiting.
	RateLimitRPS   int
	RateLimitBurst int
	// BatchBufferCap is the async write queue capacity for BufferedDB.
	BatchBufferCap int
	// TLS configuration — CertFile empty means plaintext.
	TLS TLSConfig
	// FreeleechPollSec is how often the freeleech poller fires (0 = disabled).
	FreeleechPollSec     int
	FreeleechNotifyHours int
	// MaxReadBuffer is the per-connection read buffer size in bytes.
	MaxReadBuffer int
	// MaxRequestSize is the maximum acceptable HTTP Content-Length in bytes
	// (0 = unlimited).
	MaxRequestSize int
	// RequestLogSize is the number of recent requests kept in the ring log.
	RequestLogSize int
	// DelReasonLifetime is how long (in seconds) deletion-reason records are
	// retained before the scheduler purges them.
	DelReasonLifetime int
	// Readonly disables all database writes when true.
	Readonly bool
	// AdminAPIPort is the address (e.g. ":8081") for the authenticated admin
	// REST API server. Empty disables it.
	AdminAPIPort string
	// JWTSecret is the HMAC secret used to sign and validate admin JWT tokens.
	JWTSecret []byte
}

func NewServer(config *Config, worker *Worker) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	poolCap := config.MaxMiddlemen
	if poolCap <= 0 {
		poolCap = 4096
	}
	return &Server{
		worker:         worker,
		config:         config,
		activeConns:    make(map[net.Conn]struct{}),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
		stats:          worker.Stats,
		adapters:       make(map[string]DomainAdapter),
		pool:           NewWorkerPool(poolCap),
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
		if err := s.pool.Submit(func() { s.handleConnection(conn) }); err != nil {
			// Pool was shut down — context cancelled.
			s.wg.Done()
			conn.Close()
		}
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

	readBufSize := s.config.MaxReadBuffer
	if readBufSize <= 0 {
		readBufSize = 4096
	}
	reader := bufio.NewReaderSize(conn, readBufSize)
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

		if s.config.MaxRequestSize > 0 && request.ContentLength > int64(s.config.MaxRequestSize) {
			conn.Write([]byte("HTTP/1.1 413 Request Entity Too Large\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
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
	start := time.Now()
	httpStatus := 200
	defer func() {
		if s.worker.Metrics != nil {
			s.worker.Metrics.RecordHTTPRequest(req.Method, req.URL.Path, httpStatus, time.Since(start))
		}
	}()

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
			httpStatus = 429
			if s.worker.Metrics != nil {
				s.worker.Metrics.RecordRateLimitExceeded(ipStr)
			}
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

	case "report":
		if passkey == s.config.ReportPassword {
			return s.handleReport(req, httpClose), httpClose
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
	announceResp, err := s.worker.Announce(req.Context(), announceReq, user, clientIP, opts.UserAgent)
	if err != nil {
		return a.FormatError(err.Error(), httpClose)
	}

	s.stats.Announcements.Add(1)
	return a.FormatEventResponse(EventResponseFromAnnounce(announceResp), httpClose)
}

func (s *Server) handleAnnounce(req *http.Request, passkey string, clientIP net.IP, httpClose bool) []byte {
	_, span := TraceAnnounce(req.Context(),
		req.URL.Query().Get("info_hash"),
		req.URL.Query().Get("peer_id"),
	)
	defer span.End()

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
		AddSpanError(req.Context(), ErrInvalidPasskey)
		return s.errorResponse("Passkey not found", httpClose)
	}

	params := req.URL.Query()
	announceReq, err := ParseAnnounceParams(params, clientIP)
	if err != nil {
		AddSpanError(req.Context(), err)
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
	announceResp, err := s.worker.Announce(req.Context(), announceReq, user, clientIP, userAgent)
	if err != nil {
		AddSpanError(req.Context(), err)
		return s.errorResponse(err.Error(), httpClose)
	}

	return s.bencodedAnnounceResponse(announceResp, httpClose)
}

func (s *Server) handleScrape(req *http.Request, passkey string, httpClose bool) []byte {
	start := time.Now()
	infoHashes := req.URL.Query()["info_hash"]
	_, span := TraceScrape(req.Context(), infoHashes)
	defer func() {
		span.End()
		if s.worker.Metrics != nil {
			s.worker.Metrics.RecordScrape("ok", time.Since(start))
		}
	}()

	_, ok := s.worker.Users.Get(passkey)
	if !ok {
		AddSpanError(req.Context(), ErrInvalidPasskey)
		return s.errorResponse("Passkey not found", httpClose)
	}

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

// handleReport processes peer-ban and anomaly-report callbacks from the site.
// The calling site POSTs ?action=<ban|unban>&user_id=<id>&score=<float>.
// A "ban" report bans the user in the in-memory Users list and notifies the
// SiteComm layer. An "unban" report reverses that.
func (s *Server) handleReport(req *http.Request, httpClose bool) []byte {
	q := req.URL.Query()
	action := q.Get("action")
	userIDStr := q.Get("user_id")
	scoreStr := q.Get("score")

	if userIDStr == "" {
		return s.errorResponse("missing user_id", httpClose)
	}
	uid, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return s.errorResponse("invalid user_id", httpClose)
	}

	switch action {
	case "ban":
		s.worker.Users.SetBanned(UserID(uid), true)
		if scoreStr != "" {
			if score, err := strconv.ParseFloat(scoreStr, 64); err == nil {
				_ = s.worker.SiteComm.ReportAnomaly(uid, score)
			}
		}
		_ = s.worker.SiteComm.BanUser(uid)
	case "unban":
		s.worker.Users.SetBanned(UserID(uid), false)
		_ = s.worker.SiteComm.UnbanUser(uid)
	default:
		return s.errorResponse("unknown action: expected ban or unban", httpClose)
	}

	return s.jsonResponse([]byte(`{"ok":true}`), httpClose)
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

	if len(resp.Peers6) > 0 {
		b.WriteString("6:peers6")
		b.WriteString(strconv.Itoa(len(resp.Peers6)))
		b.WriteString(":")
		b.Write(resp.Peers6)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	select {
	case <-done:
		return s.pool.Shutdown(ctx)
	case <-ctx.Done():
		return fmt.Errorf("shutdown timeout")
	}
}

// StartAdminAPIServer starts an authenticated HTTP admin API server on
// config.AdminAPIPort (e.g. ":8081"). It returns immediately; the server
// runs in the background until the process exits.
// Routes:
//
//	GET  /admin/stats       — tracker statistics (JSON)
//	GET  /admin/torrents    — torrent list (JSON)
//	GET  /admin/peers       — peer list for an info_hash (JSON)
//	GET  /admin/whitelist   — current whitelist (JSON)
//	POST /admin/update      — apply delta update
//	POST /admin/report      — ban/unban a user
func (s *Server) StartAdminAPIServer(db *sql.DB) {
	if s.config.AdminAPIPort == "" || len(s.config.JWTSecret) == 0 {
		return
	}
	authCfg := AuthConfig{
		JWTSecret:     s.config.JWTSecret,
		TokenDuration: 24 * time.Hour,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(s.buildStatsJSON())
	})
	mux.HandleFunc("/admin/torrents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(s.buildTorrentsJSON(r))
	})
	mux.HandleFunc("/admin/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(s.buildPeersJSON(r))
	})
	mux.HandleFunc("/admin/whitelist", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(s.buildWhitelistJSON())
	})
	mux.HandleFunc("/admin/update", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := s.worker.handleAdminUpdate(r); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/admin/report", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := s.worker.handleAdminReport(r); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})

	// POST /admin/auth/token — issue a signed JWT for API access
	mux.HandleFunc("/admin/auth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			UserID int    `json:"user_id"`
			Role   string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		token, err := GenerateToken(req.UserID, req.Role, authCfg)
		if err != nil {
			http.Error(w, "failed to generate token", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": token})
	})

	// POST /admin/auth/api-key — create a persistent API key
	mux.HandleFunc("/admin/auth/api-key", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			UserID      int      `json:"user_id"`
			Permissions []string `json:"permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		key, err := CreateAPIKey(db, req.UserID, req.Permissions, nil)
		if err != nil {
			http.Error(w, "failed to create API key", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"api_key": key})
	})

	// GET /admin/audit — query audit log entries
	mux.HandleFunc("/admin/audit", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filters := AuditFilters{
			Action:       q.Get("action"),
			ResourceType: q.Get("resource_type"),
			Limit:        100,
		}
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				filters.Limit = n
			}
		}
		entries, err := s.worker.AuditLog.Query(filters)
		if err != nil {
			http.Error(w, "audit query failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	})

	// POST /admin/circuit-breaker/reset — manual circuit-breaker reset
	mux.HandleFunc("/admin/circuit-breaker/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if s.worker.CircuitBreak != nil {
			s.worker.CircuitBreak.Reset()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	// Stack: rate limit → auth → mux.  The rate limiter runs first so
	// unauthenticated callers cannot exhaust resources before the JWT check.
	var handler http.Handler = mux
	handler = AuthMiddleware(authCfg, db)(handler)
	if s.worker.RateLimiter != nil {
		handler = RateLimitMiddleware(s.worker.RateLimiter)(handler)
	}
	srv := &http.Server{
		Addr:         s.config.AdminAPIPort,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			GetDefaultLogger().Error("admin API server stopped", err)
		}
	}()
	GetDefaultLogger().Info("admin API server started", "addr", s.config.AdminAPIPort)
}

// buildStatsJSON returns the current tracker stats as JSON.
func (s *Server) buildStatsJSON() []byte {
	stats := s.stats

	cbState := "n/a"
	if s.worker.CircuitBreak != nil {
		cbState = s.worker.CircuitBreak.GetStateString()
	}

	dbQueue := 0
	switch db := s.worker.DB.(type) {
	case *BufferedDB:
		dbQueue = db.QueueDepth()
	case *BatchWriterDB:
		dbQueue = db.QueueDepth()
	}

	swarmHealth := s.worker.SwarmHealthSummary()

	// PredictCompletionTime: aggregate swarm-wide seeder/leecher counts and use
	// a heuristic avg upload speed of 1 MB/s per seeder.
	completionETA := int64(-1)
	if shp, ok := s.worker.SwarmPredictor.(*ml.SwarmHealthPredictor); ok {
		var totalSeeders, totalLeechers int
		s.worker.Torrents.ForEach(func(_ string, t *Torrent) bool {
			totalSeeders += t.Seeders.Size()
			totalLeechers += t.Leechers.Size()
			return true
		})
		const avgUploadBytesPerSec = 1 << 20 // 1 MB/s heuristic per seeder
		d := shp.PredictCompletionTime(totalSeeders, totalLeechers, avgUploadBytesPerSec, 700<<20)
		if d.Seconds() < float64(1<<62) { // exclude math.MaxInt64 sentinel
			completionETA = int64(d.Seconds())
		}
	}

	torrentCacheSize := 0
	if s.worker.TorrentCache != nil {
		torrentCacheSize = s.worker.TorrentCache.cache.Size()
	}
	userCacheSize := 0
	if s.worker.UserCache != nil {
		userCacheSize = s.worker.UserCache.cache.Size()
	}

	// Update Prometheus gauges that require explicit periodic refresh.
	poolActive := s.pool.Active()
	poolCapacity := s.pool.Capacity()
	torrentCount := s.worker.Torrents.Size()
	if s.worker.Metrics != nil {
		s.worker.Metrics.UpdateWorkerPool(poolActive, poolCapacity)
		s.worker.Metrics.UpdateTorrentCount(torrentCount)
	}

	return []byte(fmt.Sprintf(
		`{"open_connections":%d,"announcements":%d,"succ_announcements":%d,"scrapes":%d,`+
			`"seeders":%d,"leechers":%d,"evicted_peers":%d,"anomalies":%d,`+
			`"db_queue_depth":%d,"circuit_breaker":%q,"swarm_health":%d,`+
			`"torrent_cache_size":%d,"user_cache_size":%d,"completion_eta_sec":%d,`+
			`"worker_pool_active":%d,"worker_pool_capacity":%d,"torrent_count":%d}`,
		stats.OpenConnections.Load(), stats.Announcements.Load(),
		stats.SuccAnnouncements.Load(), stats.Scrapes.Load(),
		stats.Seeders.Load(), stats.Leechers.Load(),
		stats.EvictedPeers.Load(), stats.AnomalyDetections.Load(),
		dbQueue, cbState, swarmHealth,
		torrentCacheSize, userCacheSize, completionETA,
		poolActive, poolCapacity, torrentCount,
	))
}

// buildTorrentsJSON returns a JSON array of known torrent IDs.
func (s *Server) buildTorrentsJSON(r *http.Request) []byte {
	limit := 100
	count := 0
	out := []byte(`[`)
	s.worker.Torrents.ForEach(func(hash string, t *Torrent) bool {
		if count >= limit {
			return false
		}
		if count > 0 {
			out = append(out, ',')
		}
		out = append(out, []byte(fmt.Sprintf(`{"id":%d,"hash":%q,"seeders":%d,"leechers":%d}`,
			t.ID, hash, t.Seeders.Size(), t.Leechers.Size()))...)
		count++
		return true
	})
	return append(out, ']')
}

// buildPeersJSON returns a JSON array of peers for the given info_hash query param.
func (s *Server) buildPeersJSON(r *http.Request) []byte {
	hash := r.URL.Query().Get("info_hash")
	torrent, ok := s.worker.Torrents.Get(hash)
	if !ok {
		return []byte(`[]`)
	}
	out := []byte(`[`)
	first := true
	torrent.Seeders.ForEach(func(_ string, p *Peer) bool {
		if !first {
			out = append(out, ',')
		}
		out = append(out, []byte(fmt.Sprintf(`{"ip":%q,"port":%d,"seeder":true}`, p.IP, p.Port))...)
		first = false
		return true
	})
	torrent.Leechers.ForEach(func(_ string, p *Peer) bool {
		if !first {
			out = append(out, ',')
		}
		out = append(out, []byte(fmt.Sprintf(`{"ip":%q,"port":%d,"seeder":false}`, p.IP, p.Port))...)
		first = false
		return true
	})
	return append(out, ']')
}

// buildWhitelistJSON returns the whitelist as a JSON array of strings.
func (s *Server) buildWhitelistJSON() []byte {
	prefixes := s.worker.Whitelist.GetAll()
	out := []byte(`[`)
	for i, p := range prefixes {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, []byte(fmt.Sprintf("%q", p))...)
	}
	return append(out, ']')
}

// handleAdminUpdate applies a delta update from the admin API.
func (w *Worker) handleAdminUpdate(r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("parse form: %w", err)
	}
	if torrentID := r.FormValue("torrent_id"); torrentID != "" {
		tid, _ := strconv.ParseUint(torrentID, 10, 32)
		action := r.FormValue("action")
		switch action {
		case "delete":
			infoHash := r.FormValue("info_hash")
			w.Torrents.Delete(infoHash)
			if w.TorrentCache != nil {
				w.TorrentCache.Delete(infoHash)
			}
		case "freeleech":
			freeStr := r.FormValue("free_type")
			free, _ := strconv.ParseUint(freeStr, 10, 8)
			infoHash := r.FormValue("info_hash")
			if t, ok := w.Torrents.Get(infoHash); ok {
				t.FreeType = FreeType(free)
			}
		default:
			_ = tid
		}
	}
	if userID := r.FormValue("user_id"); userID != "" {
		uid, err := strconv.ParseUint(userID, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid user_id: %w", err)
		}
		action := r.FormValue("action")
		switch action {
		case "can_leech":
			passkey := r.FormValue("passkey")
			if u, ok := w.Users.Get(passkey); ok {
				u.CanLeech.Store(r.FormValue("value") == "1")
			}
			if w.UserCache != nil {
				w.UserCache.Delete(passkey)
			}
			_ = uid
		case "protect_ip":
			passkey := r.FormValue("passkey")
			if u, ok := w.Users.Get(passkey); ok {
				u.ProtectIP.Store(r.FormValue("value") == "1")
			}
			if w.UserCache != nil {
				w.UserCache.Delete(passkey)
			}
			_ = uid
		}
	}
	return nil
}

// handleAdminReport processes a ban/unban action from the admin API.
func (w *Worker) handleAdminReport(r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("parse form: %w", err)
	}
	action := r.FormValue("action")
	uidStr := r.FormValue("user_id")
	uid, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid user_id: %w", err)
	}
	switch action {
	case "ban":
		w.Users.SetBanned(UserID(uid), true)
		if err := w.SiteComm.ReportAnomaly(int64(uid), 1.0); err != nil {
			GetDefaultLogger().Warn("ReportAnomaly failed", "user_id", uid, "err", err)
		}
		if err := w.SiteComm.BanUser(int64(uid)); err != nil {
			return fmt.Errorf("ban user: %w", err)
		}
	case "unban":
		w.Users.SetBanned(UserID(uid), false)
		if err := w.SiteComm.UnbanUser(int64(uid)); err != nil {
			return fmt.Errorf("unban user: %w", err)
		}
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
	return nil
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
	Detector         BehaviorDetector      // per-peer behaviour anomaly detection; nil disables
	ClientDetector   ClientDetector        // client-pattern check at announce entry; nil disables
	SwarmPredictor   SwarmHealthInterface  // swarm health prediction; nil disables
	PeerScorer       *ml.PeerScorer        // ML peer scoring; nil falls back to reservoir sampling
	TorrentCache     *TorrentCache         // L1 cache for hot-torrent lookups; nil disables
	UserCache        *UserCache            // L1 cache for passkey→user lookups; nil disables
	reaper           *Reaper               // created by Start()
}

// SwarmHealthInterface is the seam for swarm health prediction.
type SwarmHealthInterface interface {
	HealthScore(seeders, leechers int) int
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

// SwarmHealthSummary returns the mean health score across all tracked torrents,
// or -1 if no predictor is configured.
func (w *Worker) SwarmHealthSummary() int {
	if w.SwarmPredictor == nil {
		return -1
	}
	total := 0
	count := 0
	w.Torrents.ForEach(func(_ string, t *Torrent) bool {
		s := t.Seeders.Size()
		l := t.Leechers.Size()
		total += w.SwarmPredictor.HealthScore(s, l)
		count++
		return true
	})
	if count == 0 {
		return -1
	}
	return total / count
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
