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

	"github.com/mgdavisxvs/Ocelot/commons"
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
	adapters       []*ConfiguredAdapter
}

// Config holds server and tracker configuration.
type Config struct {
	ListenAddr        string
	AnnounceInterval  int
	PeersTimeout      int
	MaxMiddlemen      int
	MaxConnections    int
	MaxReadBuffer     int
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
	Readonly          bool

	// Extended / merged-in fields
	OTelEndpoint         string
	BatchBufferCap       int
	RateLimitRPS         int
	RateLimitBurst       int
	MarkovAPIURL         string
	FreeleechPollSec     int
	FreeleechNotifyHours int
	RedisAddr            string
	DelReasonLifetime    int
	TLS                  TLSConfig
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
	}
}

func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	s.listener = listener

	fmt.Printf("Ocelot tracker listening on %s (%s netpoller)\n",
		s.config.ListenAddr, netpollerType())
	fmt.Printf("GOMAXPROCS=%d\n", runtime.GOMAXPROCS(0))

	for {
		conn, err := listener.Accept()
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

	readBuf := s.config.MaxReadBuffer
	if readBuf <= 0 {
		readBuf = 4096
	}
	reader := bufio.NewReaderSize(conn, readBuf)
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
	if s.worker.RateLimiter != nil && !s.worker.RateLimiter.Allow(clientIP.String()) {
		return s.errorResponse("rate limit exceeded", true), true
	}

	httpClose := true
	if s.config.KeepaliveTimeout > 0 {
		if req.ProtoMajor == 1 && req.ProtoMinor == 0 {
			httpClose = true
		} else {
			httpClose = strings.ToLower(req.Header.Get("Connection")) == "close"
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
		return s.response("Nothing to see here", httpClose, false), httpClose
	}
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

// RegisterAdapter adds a domain adapter to the server's routing table.
func (s *Server) RegisterAdapter(adapter *ConfiguredAdapter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapters = append(s.adapters, adapter)
}

// ListenAndServeAutoTLS starts the tracker with automatic Let's Encrypt TLS.
func (s *Server) ListenAndServeAutoTLS(domain, cacheDir string) error {
	return s.startAutoTLS(domain)
}

// ListenAndServeTLS starts the tracker with manual TLS certificate files.
func (s *Server) ListenAndServeTLS(certFile, keyFile string) error {
	return s.startManualTLS(certFile, keyFile)
}

// SwarmHealthSummary returns the mean health score across all tracked swarms.
// Returns -1 when no predictor is configured or no torrents exist.
func (w *Worker) SwarmHealthSummary() int {
	if w.SwarmPredictor == nil {
		return -1
	}
	total, count := 0, 0
	w.Torrents.ForEach(func(_ string, t *Torrent) bool {
		t.mu.RLock()
		total += w.SwarmPredictor.HealthScore(t.Seeders.Size(), t.Leechers.Size())
		t.mu.RUnlock()
		count++
		return true
	})
	if count == 0 {
		return -1
	}
	return total / count
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
	PeerScorer     *ml.PeerScorer   // nil = random selection via SelectPeersOptimized
	BatchWriter    *BatchWriter     // nil = synchronous DB writes; non-nil = batched writes
	Commons        CommonsInterface // optional: nil disables economic settlement

	// ML / analytics subsystems (all optional; nil disables the feature)
	Detector       BehaviorDetector
	ClientDetector ClientDetector
	SwarmPredictor SwarmHealthInterface
	TorrentCache   *TorrentCache
	UserCache      *UserCache
	Redis          *RedisBackend
	Bus            *EventBus // internal event pub/sub; nil disables publishing

	reaper *Reaper // started/stopped by Start/Stop
}

// publish emits e to the internal EventBus if one is wired.
func (w *Worker) publish(e BusEvent) {
	if w.Bus != nil {
		w.Bus.Publish(e)
	}
}

// Start launches background subsystems (reaper). It is idempotent.
func (w *Worker) Start() {
	if w.reaper == nil && w.Config != nil {
		w.reaper = NewReaper(w.Torrents, w.Stats, w.Config.ReapPeersInterval, w.Config.PeersTimeout)
		w.reaper.Start()
	}
}

// Stop halts background subsystems and flushes buffered DB writes.
func (w *Worker) Stop() {
	if w.reaper != nil {
		w.reaper.Stop()
	}
	if w.BatchWriter != nil {
		w.BatchWriter.Stop()
	}
}

// CommonsInterface is the subset of commons.ComputeCommons used by the tracker.
// Defined as an interface to support mocking in tests.
type CommonsInterface interface {
	SettleAnnounce(stats *commons.AnnounceStats) error
	EvaluatePeers(req *commons.AllocationRequest) *commons.AllocationDecision
	SetUserPriority(userID uint32, pc commons.PriorityClass) error
	SetUserBudget(userID, torrentID uint32, maxCredits int64) error
}

// DatabaseInterface abstracts all database operations used by the tracker.
type DatabaseInterface interface {
	// Announce-path writes
	RecordPeer(userID UserID, torrentID TorrentID, active int, uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64, announceTime, announces uint32, ip, peerID, userAgent string, invalidIP bool) error
	RecordPeerLight(userID UserID, torrentID TorrentID, announceTime, announces uint32, peerID string) error
	RecordUserStats(userID UserID, uploaded, downloaded int64) error
	RecordTorrent(torrentID TorrentID, seeders, leechers uint32, snatched int, balance int64) error
	RecordSnatch(userID UserID, torrentID TorrentID, t time.Time, ip string) error
	RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error

	// Admin writes
	RecordTorrentHash(id TorrentID, infoHash string) error
	RecordUserPasskey(id UserID, passkey string, canLeech, protectIP bool) error
	DeleteTorrentHash(infoHash string) error
	DeleteUserPasskey(passkey string) error
	DeleteToken(userID UserID, torrentID TorrentID) error
	AddWhitelistEntry(prefix string) error
	RemoveWhitelistEntry(prefix string) error

	// State reload reads
	LoadTorrents() ([]torrentLoadRow, error)
	LoadUsers() ([]userLoadRow, error)
	LoadWhitelist() ([]string, error)
	LoadTokens() (map[string][]UserID, error)

	// Markov integration
	LoadRecommendedInterval(torrentID TorrentID) (int, bool)

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
