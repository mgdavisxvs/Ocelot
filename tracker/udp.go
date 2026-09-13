package tracker

import (
	"encoding/binary"
	"math/rand"
	"net"
	"sync"
	"time"
)

// UDP tracker protocol constants (BEP-15).
const (
	udpMagicConnectionID uint64 = 0x41727101980
	udpActionConnect     uint32 = 0
	udpActionAnnounce    uint32 = 1
	udpActionScrape      uint32 = 2
	udpActionError       uint32 = 3

	udpConnectReqSize  = 16
	udpAnnounceReqSize = 98
	udpScrapeMinReq    = 16 // header + at least 0 info_hashes

	udpConnIDTTL = 2 * time.Minute
)

// udpConnRecord tracks issued connection IDs with their expiry.
type udpConnRecord struct {
	ip      net.IP
	expires time.Time
}

// UDPServer is a BEP-15 UDP tracker server.
type UDPServer struct {
	conn       *net.UDPConn
	worker     *Worker
	mu         sync.Mutex
	connIDs    map[uint64]*udpConnRecord
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewUDPServer creates a UDPServer bound to addr.
func NewUDPServer(addr string, worker *Worker) (*UDPServer, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	return &UDPServer{
		conn:    conn,
		worker:  worker,
		connIDs: make(map[uint64]*udpConnRecord),
		stopCh:  make(chan struct{}),
	}, nil
}

// Serve starts the UDP receive loop (blocks until Stop is called).
func (s *UDPServer) Serve() {
	s.wg.Add(1)
	defer s.wg.Done()

	buf := make([]byte, 1500)
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}

		s.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-s.stopCh:
				return
			default:
			}
			continue
		}

		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		go s.handle(pkt, addr)
	}
}

// Stop shuts down the UDP server gracefully.
func (s *UDPServer) Stop() {
	close(s.stopCh)
	s.conn.Close()
	s.wg.Wait()
}

func (s *UDPServer) handle(pkt []byte, addr *net.UDPAddr) {
	if len(pkt) < 16 {
		return // too short for any valid request
	}

	action := binary.BigEndian.Uint32(pkt[8:12])
	transID := binary.BigEndian.Uint32(pkt[12:16])

	switch action {
	case udpActionConnect:
		s.handleConnect(pkt, addr, transID)
	case udpActionAnnounce:
		s.handleAnnounce(pkt, addr, transID)
	case udpActionScrape:
		s.handleScrape(pkt, addr, transID)
	default:
		s.sendError(addr, transID, "unknown action")
	}
}

// handleConnect processes a connect request and issues a connection ID.
func (s *UDPServer) handleConnect(pkt []byte, addr *net.UDPAddr, transID uint32) {
	if len(pkt) < udpConnectReqSize {
		return
	}
	connID := binary.BigEndian.Uint64(pkt[0:8])
	if connID != udpMagicConnectionID {
		s.sendError(addr, transID, "invalid connection id")
		return
	}

	newConnID := s.issueConnID(addr.IP)

	resp := make([]byte, 16)
	binary.BigEndian.PutUint32(resp[0:4], udpActionConnect)
	binary.BigEndian.PutUint32(resp[4:8], transID)
	binary.BigEndian.PutUint64(resp[8:16], newConnID)
	s.conn.WriteToUDP(resp, addr) //nolint:errcheck
}

// handleAnnounce processes a UDP announce request (BEP-15).
func (s *UDPServer) handleAnnounce(pkt []byte, addr *net.UDPAddr, transID uint32) {
	if len(pkt) < udpAnnounceReqSize {
		s.sendError(addr, transID, "packet too short")
		return
	}

	connID := binary.BigEndian.Uint64(pkt[0:8])
	if !s.validateConnID(connID, addr.IP) {
		s.sendError(addr, transID, "invalid or expired connection id")
		return
	}

	// Parse announce fields per BEP-15 spec.
	infoHash := string(pkt[16:36])
	peerID := string(pkt[36:56])
	downloaded := int64(binary.BigEndian.Uint64(pkt[56:64]))
	left := int64(binary.BigEndian.Uint64(pkt[64:72]))
	uploaded := int64(binary.BigEndian.Uint64(pkt[72:80]))
	eventCode := binary.BigEndian.Uint32(pkt[80:84])
	// ipOverride: 0 means use source address
	ipOverride := binary.BigEndian.Uint32(pkt[84:88])
	// key: pkt[88:92] — ignored for now
	numWantRaw := int32(binary.BigEndian.Uint32(pkt[92:96]))
	port := binary.BigEndian.Uint16(pkt[96:98])

	event := udpEvent(eventCode)

	clientIP := addr.IP
	if ipOverride != 0 {
		clientIP = net.IP{byte(ipOverride >> 24), byte(ipOverride >> 16), byte(ipOverride >> 8), byte(ipOverride)}
	}

	user, ok := s.worker.Users.Get("") // UDP has no passkey; use empty passkey lookup
	if !ok {
		// Try to find any user by IP match — not ideal, but UDP has no passkey.
		// In practice callers should use HTTP announce for authenticated requests.
		s.sendError(addr, transID, "passkey required for this tracker")
		return
	}

	numWant := numWantRaw
	if numWant < 0 || numWant > int32(s.worker.Config.NumWantLimit) {
		numWant = int32(s.worker.Config.NumWantLimit)
	}

	req := &AnnounceRequest{
		InfoHash:   infoHash,
		PeerID:     []byte(peerID),
		Port:       port,
		Uploaded:   uploaded,
		Downloaded: downloaded,
		Left:       left,
		Compact:    true,
		Event:      event,
		IP:         clientIP,
		NumWant:    numWant,
	}

	resp, err := s.worker.Announce(req, user, clientIP, "UDP/BEP15")
	if err != nil {
		s.sendError(addr, transID, err.Error())
		return
	}

	// Response: action(4) + transID(4) + interval(4) + leechers(4) + seeders(4) + peers(6*n)
	peerCount := len(resp.Peers) / 6
	out := make([]byte, 20+peerCount*6)
	binary.BigEndian.PutUint32(out[0:4], udpActionAnnounce)
	binary.BigEndian.PutUint32(out[4:8], transID)
	binary.BigEndian.PutUint32(out[8:12], uint32(resp.Interval))
	binary.BigEndian.PutUint32(out[12:16], uint32(resp.Incomplete))
	binary.BigEndian.PutUint32(out[16:20], uint32(resp.Complete))
	copy(out[20:], resp.Peers)
	s.conn.WriteToUDP(out, addr) //nolint:errcheck
}

// handleScrape processes a UDP scrape request.
func (s *UDPServer) handleScrape(pkt []byte, addr *net.UDPAddr, transID uint32) {
	if len(pkt) < udpScrapeMinReq {
		s.sendError(addr, transID, "packet too short")
		return
	}

	connID := binary.BigEndian.Uint64(pkt[0:8])
	if !s.validateConnID(connID, addr.IP) {
		s.sendError(addr, transID, "invalid or expired connection id")
		return
	}

	infoHashes := (len(pkt) - 16) / 20
	if infoHashes > 74 { // BEP-15 allows up to 74 info_hashes per scrape
		infoHashes = 74
	}

	// Response: action(4) + transID(4) + 12*n (seeders, completed, leechers per hash)
	out := make([]byte, 8+infoHashes*12)
	binary.BigEndian.PutUint32(out[0:4], udpActionScrape)
	binary.BigEndian.PutUint32(out[4:8], transID)

	for i := 0; i < infoHashes; i++ {
		offset := 16 + i*20
		if offset+20 > len(pkt) {
			break
		}
		infoHash := string(pkt[offset : offset+20])
		outOffset := 8 + i*12

		torrent, ok := s.worker.Torrents.Get(infoHash)
		if !ok {
			// Unknown torrent: report zeroes.
			binary.BigEndian.PutUint32(out[outOffset:outOffset+4], 0)
			binary.BigEndian.PutUint32(out[outOffset+4:outOffset+8], 0)
			binary.BigEndian.PutUint32(out[outOffset+8:outOffset+12], 0)
			continue
		}

		torrent.mu.RLock()
		seeders := uint32(torrent.Seeders.Size())
		leechers := uint32(torrent.Leechers.Size())
		completed := torrent.Completed
		torrent.mu.RUnlock()

		binary.BigEndian.PutUint32(out[outOffset:outOffset+4], seeders)
		binary.BigEndian.PutUint32(out[outOffset+4:outOffset+8], completed)
		binary.BigEndian.PutUint32(out[outOffset+8:outOffset+12], leechers)
	}

	s.conn.WriteToUDP(out, addr) //nolint:errcheck
}

func (s *UDPServer) sendError(addr *net.UDPAddr, transID uint32, msg string) {
	out := make([]byte, 8+len(msg))
	binary.BigEndian.PutUint32(out[0:4], udpActionError)
	binary.BigEndian.PutUint32(out[4:8], transID)
	copy(out[8:], msg)
	s.conn.WriteToUDP(out, addr) //nolint:errcheck
}

// issueConnID mints a random 64-bit connection ID and records it for validation.
func (s *UDPServer) issueConnID(ip net.IP) uint64 {
	id := rand.Uint64()
	s.mu.Lock()
	s.connIDs[id] = &udpConnRecord{ip: ip, expires: time.Now().Add(udpConnIDTTL)}
	s.mu.Unlock()
	s.evictExpired()
	return id
}

// validateConnID checks that a connection ID was issued to the given IP and has not expired.
func (s *UDPServer) validateConnID(id uint64, ip net.IP) bool {
	s.mu.Lock()
	rec, ok := s.connIDs[id]
	s.mu.Unlock()
	if !ok {
		return false
	}
	if time.Now().After(rec.expires) {
		s.mu.Lock()
		delete(s.connIDs, id)
		s.mu.Unlock()
		return false
	}
	return rec.ip.Equal(ip)
}

// evictExpired removes stale connection IDs from the map (called opportunistically).
func (s *UDPServer) evictExpired() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rec := range s.connIDs {
		if now.After(rec.expires) {
			delete(s.connIDs, id)
		}
	}
}

// udpEvent maps BEP-15 event codes to announce event strings.
func udpEvent(code uint32) string {
	switch code {
	case 1:
		return "completed"
	case 2:
		return "started"
	case 3:
		return "stopped"
	default:
		return ""
	}
}
