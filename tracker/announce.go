package tracker

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// AnnounceRequest represents a parsed BitTorrent announce request.
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

// AnnounceResponse represents the tracker's response to an announce.
type AnnounceResponse struct {
	Interval    int32
	MinInterval int32
	Complete    int32  // number of seeders
	Incomplete  int32  // number of leechers
	Peers       []byte // compact format: 6 bytes per peer
	Warning     string
}

// Announce handles a BitTorrent announce request.
// Go equivalent of worker::announce() (worker.cpp:266-735).
func (w *Worker) Announce(req *AnnounceRequest, user *User, clientIP net.IP, userAgent string) (*AnnounceResponse, error) {
	now := time.Now()

	if !req.Compact {
		return nil, fmt.Errorf("your client does not support compact announces")
	}
	if len(req.PeerID) != 20 {
		return nil, fmt.Errorf("invalid peer ID")
	}
	if !w.Whitelist.IsAllowed(req.PeerID) {
		return nil, fmt.Errorf("your client is not on the whitelist")
	}

	torrent, ok := w.Torrents.Get(req.InfoHash)
	if !ok {
		return nil, fmt.Errorf("unregistered torrent")
	}

	peerKey := PeerKeyPrime(req.PeerID, user.ID, torrent.ID)

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

	if req.Event == "completed" {
		completedTorrent = (req.Left == 0)
	} else if req.Event == "stopped" {
		stoppedTorrent = true
		peerChanged = true
		updateTorrent = true
		active = 0
	}

	var peer *Peer

	torrent.mu.Lock()

	if req.Left > 0 {
		peer, inserted = w.findOrCreatePeer(torrent.Leechers, peerKey, user)
		if inserted {
			incLeechers = true
		}
	} else if completedTorrent {
		peer, _ = torrent.Leechers.Get(peerKey)
		if peer == nil {
			peer, _ = torrent.Seeders.Get(peerKey)
			if peer == nil {
				peer, inserted = w.findOrCreatePeer(torrent.Seeders, peerKey, user)
				incSeeders = true
			} else {
				completedTorrent = false
			}
		} else {
			if _, exists := torrent.Seeders.Get(peerKey); exists {
				decSeeders = true
			}
		}
	} else {
		peer, _ = torrent.Seeders.Get(peerKey)
		if peer == nil {
			peer, _ = torrent.Leechers.Get(peerKey)
			if peer != nil {
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

	var upSpeed, downSpeed int64

	if inserted || req.Event == "started" {
		updateTorrent = true
		peer.FirstAnnounced = now
		peer.LastAnnounced = time.Time{}
		peer.Uploaded = req.Uploaded
		peer.Downloaded = req.Downloaded
		peer.Corrupt = req.Corrupt
		peer.Announces = 1
		peerChanged = true
	} else if req.Uploaded < peer.Uploaded || req.Downloaded < peer.Downloaded {
		peer.Announces++
		peer.Uploaded = req.Uploaded
		peer.Downloaded = req.Downloaded
		peerChanged = true
	} else {
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

			_, hasToken := torrent.TokenedUsers[user.ID]
			switch torrent.FreeType {
			case FreeNeutral:
				downloadedChange = 0
				uploadedChange = 0
			case FreeFree:
				downloadedChange = 0
			default:
				if hasToken {
					expireToken = true
					w.DB.RecordToken(user.ID, torrent.ID, downloadedChange)
					downloadedChange = 0
				}
			}

			if uploadedChange > 0 || downloadedChange > 0 {
				w.DB.RecordUserStats(user.ID, uploadedChange, downloadedChange)
			}
		}
	}

	peer.Left = req.Left

	ip := req.IP
	if ip == nil || ip.IsUnspecified() {
		ip = clientIP
	}

	if !ValidateIPNotPrivate(ip) {
		invalidIP = true
		peer.InvalidIP = true
		peer.Port = req.Port
		peer.IP = ip
		peer.IPPort = CompactIPPort(ip, req.Port)
	} else if inserted || peer.Port != req.Port || !peer.IP.Equal(ip) {
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

	peer.LastAnnounced = now
	peer.Visible = w.peerIsVisible(user, peer)

	torrent.mu.Unlock()

	// Anomaly detection — runs after the lock is released to avoid holding it
	// during a potentially-logging-heavy detection pass.
	if w.Detector != nil {
		if isAnomaly, reason := w.Detector.Detect(peer, upSpeed, downSpeed); isAnomaly {
			w.Stats.AnomalyDetections.Add(1)
			_ = w.SiteComm.ReportAnomaly(int64(user.ID), 1.0)
			_ = w.SiteComm.BanUser(int64(user.ID))
			torrent.mu.Lock()
			torrent.Leechers.Delete(peerKey)
			torrent.Seeders.Delete(peerKey)
			torrent.mu.Unlock()
			GetDefaultLogger().Warn("anomaly detected, peer banned",
				"user_id", user.ID,
				"torrent_id", torrent.ID,
				"reason", reason,
			)
			return nil, fmt.Errorf("anomalous activity detected: %s", reason)
		}
	}

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

	numwant := req.NumWant
	if numwant <= 0 {
		numwant = int32(w.Config.NumWantLimit)
	} else if numwant > int32(w.Config.NumWantLimit) {
		numwant = int32(w.Config.NumWantLimit)
	}

	if stoppedTorrent {
		numwant = 0
		if req.Left > 0 {
			decLeechers = true
		} else {
			decSeeders = true
		}
	} else if completedTorrent {
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

		if expireToken {
			w.SiteComm.ExpireToken(torrent.ID, user.ID)
			torrent.mu.Lock()
			delete(torrent.TokenedUsers, user.ID)
			torrent.mu.Unlock()
		}
	} else if !user.CanLeech.Load() && req.Left > 0 {
		numwant = 0
	}

	peers := SelectPeersOptimized(torrent, peer, user.ID, numwant, req.Left > 0)

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
		user.Leeching.Add(^uint32(0))
		w.Stats.Leechers.Add(^uint32(0))
	}
	if decSeeders {
		user.Seeding.Add(^uint32(0))
		w.Stats.Seeders.Add(^uint32(0))
	}

	if stoppedTorrent {
		torrent.mu.Lock()
		if req.Left > 0 {
			torrent.Leechers.Delete(peerKey)
		} else {
			torrent.Seeders.Delete(peerKey)
		}
		torrent.mu.Unlock()
	}

	torrent.mu.Lock()
	if updateTorrent || now.Sub(torrent.LastFlushed) > time.Hour {
		torrent.LastFlushed = now
		w.DB.RecordTorrent(torrent.ID, uint32(torrent.Seeders.Size()),
			uint32(torrent.Leechers.Size()), snatched, torrent.Balance)
	}
	seederCount := torrent.Seeders.Size()
	leecherCount := torrent.Leechers.Size()
	torrent.mu.Unlock()

	if !user.CanLeech.Load() && req.Left > 0 {
		return nil, fmt.Errorf("access denied, leeching forbidden")
	}

	adaptiveInterval := AdaptiveInterval(seederCount, leecherCount, w.Config.AnnounceInterval)
	baseInterval := int32(w.Config.AnnounceInterval)
	if adaptiveInterval < baseInterval {
		adaptiveInterval = baseInterval
	}

	response := &AnnounceResponse{
		Interval:    adaptiveInterval,
		MinInterval: baseInterval,
		Complete:    int32(seederCount),
		Incomplete:  int32(leecherCount),
		Peers:       peers,
	}
	if invalidIP {
		response.Warning = "Illegal character found in IP address"
	}

	return response, nil
}

// selectPeers picks up to numwant peers to return. Leechers receive seeders
// first (round-robin), then other leechers. Seeders receive only leechers.
func (w *Worker) selectPeers(torrent *Torrent, self *Peer, userID UserID, numwant int32, isLeecher bool) []byte {
	if numwant <= 0 {
		return []byte{}
	}

	peers := make([]byte, 0, numwant*6)
	found := 0

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()

	if isLeecher {
		seederCount := torrent.Seeders.Size()
		if seederCount > 0 {
			seederKeys := make([]string, 0, seederCount)
			seederMap := make(map[string]*Peer, seederCount)
			torrent.Seeders.ForEach(func(key string, peer *Peer) bool {
				seederKeys = append(seederKeys, key)
				seederMap[key] = peer
				return true
			})

			startIdx := 0
			if torrent.LastSelectedSeeder != "" {
				for i, key := range seederKeys {
					if key == torrent.LastSelectedSeeder {
						startIdx = (i + 1) % len(seederKeys)
						break
					}
				}
			}

			for i := 0; i < len(seederKeys) && found < int(numwant); i++ {
				idx := (startIdx + i) % len(seederKeys)
				key := seederKeys[idx]
				peer := seederMap[key]
				if peer.UserID == userID || !peer.Visible {
					continue
				}
				if len(peer.IPPort) == 6 {
					peers = append(peers, peer.IPPort...)
					found++
					torrent.LastSelectedSeeder = key
				}
			}
		}

		if found < int(numwant) && torrent.Leechers.Size() > 1 {
			torrent.Leechers.ForEach(func(_ string, peer *Peer) bool {
				if found >= int(numwant) {
					return false
				}
				if peer.UserID == userID || !peer.Visible {
					return true
				}
				if len(peer.IPPort) == 6 {
					peers = append(peers, peer.IPPort...)
					found++
				}
				return true
			})
		}
	} else {
		torrent.Leechers.ForEach(func(_ string, peer *Peer) bool {
			if found >= int(numwant) {
				return false
			}
			if peer.UserID == userID || !peer.Visible {
				return true
			}
			if len(peer.IPPort) == 6 {
				peers = append(peers, peer.IPPort...)
				found++
			}
			return true
		})
	}

	return peers
}

func (w *Worker) findOrCreatePeer(peerList *PeerList, peerKey string, user *User) (*Peer, bool) {
	if peer, ok := peerList.Get(peerKey); ok {
		return peer, false
	}
	peer := &Peer{UserID: user.ID}
	peerList.Set(peerKey, peer)
	return peer, true
}

func (w *Worker) peerIsVisible(user *User, peer *Peer) bool {
	return peer.Left == 0 || user.CanLeech.Load()
}

// ParseAnnounceParams parses URL query parameters into an AnnounceRequest.
func ParseAnnounceParams(params url.Values, clientIP net.IP) (*AnnounceRequest, error) {
	req := &AnnounceRequest{IP: clientIP}

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

	req.Uploaded = parseInt64(params.Get("uploaded"))
	req.Downloaded = parseInt64(params.Get("downloaded"))
	req.Left = parseInt64(params.Get("left"))
	req.Corrupt = parseInt64(params.Get("corrupt"))

	req.Event = params.Get("event")
	req.Compact = params.Get("compact") == "1"
	req.NoPeerID = params.Get("no_peer_id") == "1"
	req.NumWant = int32(parseInt64(params.Get("numwant")))
	req.Key = params.Get("key")
	req.TrackerID = params.Get("trackerid")

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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
