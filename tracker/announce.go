package tracker

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// AnnounceRequest represents a parsed BitTorrent announce request
type AnnounceRequest struct {
	InfoHash   string
	PeerID     []byte
	Port       uint16
	Uploaded   int64
	Downloaded int64
	Left       int64
	Corrupt    int64
	Compact    bool
	NoPeerID   bool
	Event      string // "started", "completed", "stopped", or ""
	IP         net.IP
	NumWant    int32
	Key        string
	TrackerID  string
}

// AnnounceResponse represents the tracker's response
type AnnounceResponse struct {
	Interval    int32
	MinInterval int32
	Complete    int32  // Number of seeders
	Incomplete  int32  // Number of leechers
	Peers       []byte // Compact format: 6 bytes per peer
	Warning     string
}

// Announce handles a BitTorrent announce request
// This is the Go equivalent of worker::announce() (worker.cpp:266-735)
//
// Following Donald Knuth's principle: "Premature optimization is the root of all evil"
// BUT "We should forget about small efficiencies, say about 97% of the time"
// This function is in the critical 3% - it's called thousands of times per second
func (w *Worker) Announce(req *AnnounceRequest, user *User, clientIP net.IP, userAgent string) (*AnnounceResponse, error) {
	now := time.Now()

	// Validation: Require compact announces (BitTorrent BEP 23)
	if !req.Compact {
		return nil, fmt.Errorf("your client does not support compact announces")
	}

	// Validation: peer_id must be exactly 20 bytes (BitTorrent spec)
	if len(req.PeerID) != 20 {
		return nil, fmt.Errorf("invalid peer ID")
	}

	// Check whitelist (if enabled)
	if !w.Whitelist.IsAllowed(req.PeerID) {
		return nil, fmt.Errorf("your client is not on the whitelist")
	}

	// Get torrent (already validated by caller)
	torrent, ok := w.Torrents.Get(req.InfoHash)
	if !ok {
		return nil, fmt.Errorf("unregistered torrent")
	}

	// Generate peer key (matches C++ logic: worker.cpp:314-318)
	peerKey := PeerKey(req.PeerID, user.ID, torrent.ID)

	// Track state changes for database updates
	var (
		inserted         = false
		updateTorrent    = false
		completedTorrent = false
		stoppedTorrent   = false
		expireToken      = false
		peerChanged      = false
		invalidIP        = false
		incLeechers      = false
		incSeeders       = false
		decLeechers      = false
		decSeeders       = false
		snatched         = 0
		active           = 1
	)

	// Handle special events
	if req.Event == "completed" {
		completedTorrent = (req.Left == 0) // Sanity check
	} else if req.Event == "stopped" {
		stoppedTorrent = true
		peerChanged = true
		updateTorrent = true
		active = 0
	}

	// Find or insert peer in appropriate list
	var peer *Peer

	torrent.mu.Lock() // Lock torrent for peer list modifications

	if req.Left > 0 {
		// Peer is a leecher
		peer, inserted = w.findOrCreatePeer(torrent.Leechers, peerKey, user)
		if inserted {
			incLeechers = true
		}
	} else if completedTorrent {
		// Peer just completed - transition from leecher to seeder
		peer, _ = torrent.Leechers.Get(peerKey)
		if peer == nil {
			// Check if already in seeders
			peer, _ = torrent.Seeders.Get(peerKey)
			if peer == nil {
				peer, inserted = w.findOrCreatePeer(torrent.Seeders, peerKey, user)
				incSeeders = true
			} else {
				completedTorrent = false // Already completed before
			}
		} else {
			// Check for duplicate in both lists
			if _, exists := torrent.Seeders.Get(peerKey); exists {
				decSeeders = true
			}
		}
	} else {
		// Peer is a seeder
		peer, _ = torrent.Seeders.Get(peerKey)
		if peer == nil {
			// Check leechers list (might be transitioning)
			peer, _ = torrent.Leechers.Get(peerKey)
			if peer != nil {
				// Move from leechers to seeders
				torrent.Leechers.Delete(peerKey)
				torrent.Seeders.Set(peerKey, peer)
				peerChanged = true
				decLeechers = true
			} else {
				peer, inserted = w.findOrCreatePeer(torrent.Seeders, peerKey, user)
			}
			incSeeders = true
		}
	}

	// Calculate upload/download speeds and deltas
	var upSpeed, downSpeed int64

	if inserted || req.Event == "started" {
		// New peer on this torrent
		updateTorrent = true
		peer.FirstAnnounced = now
		peer.LastAnnounced = time.Time{}
		peer.Uploaded = req.Uploaded
		peer.Downloaded = req.Downloaded
		peer.Corrupt = req.Corrupt
		peer.Announces = 1
		peerChanged = true
	} else if req.Uploaded < peer.Uploaded || req.Downloaded < peer.Downloaded {
		// Client reset (new session or counter overflow)
		peer.Announces++
		peer.Uploaded = req.Uploaded
		peer.Downloaded = req.Downloaded
		peerChanged = true
	} else {
		// Normal announce - calculate deltas
		peer.Announces++

		uploadedChange := req.Uploaded - peer.Uploaded
		downloadedChange := req.Downloaded - peer.Downloaded
		corruptChange := req.Corrupt - peer.Corrupt

		if uploadedChange > 0 {
			peer.Uploaded = req.Uploaded
		}
		if downloadedChange > 0 {
			peer.Downloaded = req.Downloaded
		}
		if corruptChange > 0 {
			peer.Corrupt = req.Corrupt
			torrent.Balance -= corruptChange
			updateTorrent = true
		}

		peerChanged = peerChanged || uploadedChange > 0 || downloadedChange > 0 || corruptChange > 0

		// Calculate transfer speeds
		if uploadedChange > 0 || downloadedChange > 0 {
			torrent.Balance += uploadedChange
			torrent.Balance -= downloadedChange
			updateTorrent = true

			if !peer.LastAnnounced.IsZero() {
				timeDelta := now.Sub(peer.LastAnnounced).Seconds()
				if timeDelta > 0 {
					upSpeed = int64(float64(uploadedChange) / timeDelta)
					downSpeed = int64(float64(downloadedChange) / timeDelta)
				}
			}

			// Apply freeleech logic (matches C++ worker.cpp:429-442)
			_, hasToken := torrent.TokenedUsers[user.ID]
			switch torrent.FreeType {
			case FreeNeutral:
				// Neutral torrent: no stats counted
				downloadedChange = 0
				uploadedChange = 0
			case FreeFree:
				// Free torrent: downloads don't count
				downloadedChange = 0
			default:
				// Check for user-specific token
				if hasToken {
					expireToken = true
					w.DB.RecordToken(user.ID, torrent.ID, downloadedChange)
					downloadedChange = 0
				}
			}

			// Queue user stats update
			if uploadedChange > 0 || downloadedChange > 0 {
				w.DB.RecordUserStats(user.ID, uploadedChange, downloadedChange)
			}
		}
	}

	peer.Left = req.Left

	// Parse IP address (handle ip, ipv4, or X-Forwarded-For)
	ip := req.IP
	if ip == nil || ip.IsUnspecified() {
		ip = clientIP
	}

	// Generate compact IP:port representation
	if inserted || peer.Port != req.Port || !peer.IP.Equal(ip) {
		peer.Port = req.Port
		peer.IP = ip
		peer.IPPort = CompactIPPort(ip, req.Port)
		if peer.IPPort == nil {
			invalidIP = true
			peer.InvalidIP = true
		}
	} else {
		invalidIP = peer.InvalidIP
	}

	// Update peer timestamp
	peer.LastAnnounced = now
	peer.Visible = w.peerIsVisible(user, peer)

	torrent.mu.Unlock() // Release torrent lock

	// Queue peer update to database
	if peerChanged {
		announceTime := uint32(now.Sub(peer.FirstAnnounced).Seconds())
		ipStr := ""
		if !user.ProtectIP.Load() {
			ipStr = ip.String()
		}
		w.DB.RecordPeer(user.ID, torrent.ID, active, req.Uploaded, req.Downloaded,
			upSpeed, downSpeed, req.Left, req.Corrupt, announceTime, peer.Announces,
			ipStr, string(req.PeerID), userAgent)
	} else {
		announceTime := uint32(now.Sub(peer.FirstAnnounced).Seconds())
		w.DB.RecordPeerLight(user.ID, torrent.ID, announceTime, peer.Announces, string(req.PeerID))
	}

	// Determine numwant (how many peers to return)
	numwant := req.NumWant
	if numwant <= 0 {
		numwant = int32(w.Config.NumWantLimit)
	} else if numwant > int32(w.Config.NumWantLimit) {
		numwant = int32(w.Config.NumWantLimit)
	}

	// Handle stopped event
	if stoppedTorrent {
		numwant = 0
		if req.Left > 0 {
			decLeechers = true
		} else {
			decSeeders = true
		}
	} else if completedTorrent {
		// Handle completion event (snatch)
		snatched = 1
		updateTorrent = true
		torrent.mu.Lock()
		torrent.Completed++
		torrent.mu.Unlock()

		ipStr := ""
		if !user.ProtectIP.Load() {
			ipStr = ip.String()
		}
		w.DB.RecordSnatch(user.ID, torrent.ID, now, ipStr)

		// Move to seeders if not already inserted there
		if !inserted {
			torrent.mu.Lock()
			if oldPeer, ok := torrent.Leechers.Get(peerKey); ok {
				torrent.Seeders.Set(peerKey, oldPeer)
				torrent.Leechers.Delete(peerKey)
				peer = oldPeer
				decLeechers = true
				incSeeders = true
			}
			torrent.mu.Unlock()
		}

		// Expire freeleech token if applicable
		if expireToken {
			w.SiteComm.ExpireToken(torrent.ID, user.ID)
			torrent.mu.Lock()
			delete(torrent.TokenedUsers, user.ID)
			torrent.mu.Unlock()
		}
	} else if !user.CanLeech.Load() && req.Left > 0 {
		// User can't leech - don't return peers
		numwant = 0
	}

	// Select peers to return (matches C++ worker.cpp:574-643)
	peers := w.selectPeers(torrent, peer, user.ID, numwant, req.Left > 0)

	// Update global statistics (using atomics - no mutex needed!)
	w.Stats.SuccAnnouncements.Add(1)
	if incLeechers {
		user.Leeching.Add(1)
		w.Stats.Leechers.Add(1)
	}
	if incSeeders {
		user.Seeding.Add(1)
		w.Stats.Seeders.Add(1)
	}
	if decLeechers {
		user.Leeching.Add(^uint32(0)) // Atomic decrement
		w.Stats.Leechers.Add(^uint32(0))
	}
	if decSeeders {
		user.Seeding.Add(^uint32(0))
		w.Stats.Seeders.Add(^uint32(0))
	}

	// Delete peer if stopped
	if stoppedTorrent {
		torrent.mu.Lock()
		if req.Left > 0 {
			torrent.Leechers.Delete(peerKey)
		} else {
			torrent.Seeders.Delete(peerKey)
		}
		torrent.mu.Unlock()
	}

	// Update torrent in database (periodically or when changed)
	torrent.mu.Lock()
	if updateTorrent || now.Sub(torrent.LastFlushed) > time.Hour {
		torrent.LastFlushed = now
		w.DB.RecordTorrent(torrent.ID, uint32(torrent.Seeders.Size()),
			uint32(torrent.Leechers.Size()), snatched, torrent.Balance)
	}
	seederCount := torrent.Seeders.Size()
	leecherCount := torrent.Leechers.Size()
	_ = torrent.Completed // Captured for potential future use
	torrent.mu.Unlock()

	// Deny leeching if user doesn't have permission
	if !user.CanLeech.Load() && req.Left > 0 {
		return nil, fmt.Errorf("access denied, leeching forbidden")
	}

	// Build response (bencoded format will be handled by caller)
	response := &AnnounceResponse{
		Interval:    int32(w.Config.AnnounceInterval + min(600, seederCount)),
		MinInterval: int32(w.Config.AnnounceInterval),
		Complete:    int32(seederCount),
		Incomplete:  int32(leecherCount),
		Peers:       peers,
	}

	if invalidIP {
		response.Warning = "Illegal character found in IP address. IPv6 is not supported"
	}

	return response, nil
}

// selectPeers implements the peer selection algorithm
// For leechers: return seeders first (round-robin), then other leechers
// For seeders: return only leechers
// Matches C++ worker.cpp:574-643
func (w *Worker) selectPeers(torrent *Torrent, self *Peer, userID UserID, numwant int32, isLeecher bool) []byte {
	if numwant <= 0 {
		return []byte{}
	}

	// Pre-allocate buffer (6 bytes per peer in compact format)
	peers := make([]byte, 0, numwant*6)
	foundPeers := 0

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()

	if isLeecher {
		// Leecher wants seeders first
		seederCount := torrent.Seeders.Size()
		if seederCount > 0 {
			// Implement round-robin seeder selection (C++ lines 580-619)
			// This ensures all seeders get exposure to leechers evenly

			// Convert map to slice for iteration control
			seederKeys := make([]string, 0, seederCount)
			seederPeers := make(map[string]*Peer)

			torrent.Seeders.ForEach(func(key string, peer *Peer) bool {
				seederKeys = append(seederKeys, key)
				seederPeers[key] = peer
				return true
			})

			// Find starting position based on last selected seeder
			startIdx := 0
			if torrent.LastSelectedSeeder != "" {
				for i, key := range seederKeys {
					if key == torrent.LastSelectedSeeder {
						startIdx = (i + 1) % len(seederKeys)
						break
					}
				}
			}

			// Cycle through seeders starting from startIdx
			for i := 0; i < len(seederKeys) && foundPeers < int(numwant); i++ {
				idx := (startIdx + i) % len(seederKeys)
				key := seederKeys[idx]
				peer := seederPeers[key]

				// Skip deleted users, self, and invisible peers
				if peer.UserID == userID || !peer.Visible {
					continue
				}

				if len(peer.IPPort) == 6 {
					peers = append(peers, peer.IPPort...)
					foundPeers++
					torrent.LastSelectedSeeder = key
				}
			}
		}

		// Add leechers if we haven't filled numwant
		if foundPeers < int(numwant) && torrent.Leechers.Size() > 1 {
			torrent.Leechers.ForEach(func(key string, peer *Peer) bool {
				if foundPeers >= int(numwant) {
					return false
				}
				// Don't show self or invisible peers
				if peer.UserID == userID || !peer.Visible {
					return true
				}
				if len(peer.IPPort) == 6 {
					peers = append(peers, peer.IPPort...)
					foundPeers++
				}
				return true
			})
		}
	} else {
		// Seeder wants leechers only
		torrent.Leechers.ForEach(func(key string, peer *Peer) bool {
			if foundPeers >= int(numwant) {
				return false
			}
			// Don't show self or invisible peers
			if peer.UserID == userID || !peer.Visible {
				return true
			}
			if len(peer.IPPort) == 6 {
				peers = append(peers, peer.IPPort...)
				foundPeers++
			}
			return true
		})
	}

	return peers
}

// findOrCreatePeer finds an existing peer or creates a new one
func (w *Worker) findOrCreatePeer(peerList *PeerList, peerKey string, user *User) (*Peer, bool) {
	if peer, ok := peerList.Get(peerKey); ok {
		return peer, false
	}

	peer := &Peer{
		UserID: user.ID,
	}
	peerList.Set(peerKey, peer)
	return peer, true
}

// peerIsVisible checks if a peer should be visible to others
// Leechers without download privileges or invalid IPs are invisible
func (w *Worker) peerIsVisible(user *User, peer *Peer) bool {
	return (peer.Left == 0 || user.CanLeech.Load()) && !peer.InvalidIP
}

// ParseAnnounceParams parses URL query parameters into an AnnounceRequest
func ParseAnnounceParams(params url.Values, clientIP net.IP) (*AnnounceRequest, error) {
	req := &AnnounceRequest{
		IP: clientIP,
	}

	// Required parameters
	infoHash := params.Get("info_hash")
	if infoHash == "" {
		return nil, fmt.Errorf("missing info_hash")
	}
	req.InfoHash = infoHash

	peerID := params.Get("peer_id")
	if peerID == "" {
		return nil, fmt.Errorf("missing peer_id")
	}
	req.PeerID = []byte(peerID)

	portStr := params.Get("port")
	if portStr == "" {
		return nil, fmt.Errorf("missing port")
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid port")
	}
	req.Port = uint16(port)

	// Parse numeric parameters
	req.Uploaded = parseInt64(params.Get("uploaded"))
	req.Downloaded = parseInt64(params.Get("downloaded"))
	req.Left = parseInt64(params.Get("left"))
	req.Corrupt = parseInt64(params.Get("corrupt"))

	// Optional parameters
	req.Event = params.Get("event")
	req.Compact = params.Get("compact") == "1"
	req.NoPeerID = params.Get("no_peer_id") == "1"
	req.NumWant = int32(parseInt64(params.Get("numwant")))
	req.Key = params.Get("key")
	req.TrackerID = params.Get("trackerid")

	// IP override (ip or ipv4 parameter)
	if ipParam := params.Get("ip"); ipParam != "" {
		req.IP = net.ParseIP(ipParam)
	} else if ipv4Param := params.Get("ipv4"); ipv4Param != "" {
		req.IP = net.ParseIP(ipv4Param)
	}

	return req, nil
}

func parseInt64(s string) int64 {
	if s == "" {
		return 0
	}
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil || val < 0 {
		return 0
	}
	return val
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
