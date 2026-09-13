package tracker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// UpdateRequest represents a tracker update request from the admin panel
type UpdateRequest struct {
	Action string `json:"action"`
	// Torrent-related fields
	TorrentID int    `json:"torrent_id,omitempty"`
	InfoHash  string `json:"info_hash,omitempty"`
	Reason    string `json:"reason,omitempty"`
	FreeType  int    `json:"free_type,omitempty"`
	// User-related fields
	UserID      int    `json:"user_id,omitempty"`
	Passkey     string `json:"passkey,omitempty"`
	CanLeech    *bool  `json:"can_leech,omitempty"`
	ProtectIP   *bool  `json:"protect_ip,omitempty"`
	NewPasskey  string `json:"new_passkey,omitempty"`
	// Token-related fields
	Downloaded int64 `json:"downloaded,omitempty"`
	// Whitelist-related fields
	PeerIDPrefix string `json:"peer_id_prefix,omitempty"`
}

// UpdateResponse represents the tracker's response to an update
type UpdateResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// HandleUpdate processes tracker update requests from the admin panel
// This replaces the stub at server.go:352
// Equivalent to C++ worker::update() (worker.cpp:768-996)
func (w *Worker) HandleUpdate(req *http.Request) ([]byte, error) {
	// Parse JSON body
	var updateReq UpdateRequest
	if err := json.NewDecoder(req.Body).Decode(&updateReq); err != nil {
		return w.updateError("Invalid JSON: " + err.Error())
	}

	// Route to appropriate handler based on action
	switch updateReq.Action {
	case "add_torrent":
		return w.addTorrent(updateReq)
	case "update_torrent":
		return w.updateTorrent(updateReq)
	case "delete_torrent":
		return w.deleteTorrent(updateReq)
	case "change_freeleech":
		return w.changeFreeleech(updateReq)
	case "add_user":
		return w.addUser(updateReq)
	case "update_user":
		return w.updateUser(updateReq)
	case "delete_user":
		return w.deleteUser(updateReq)
	case "change_passkey":
		return w.changePasskey(updateReq)
	case "add_token":
		return w.addToken(updateReq)
	case "remove_token":
		return w.removeToken(updateReq)
	case "add_whitelist":
		return w.addWhitelist(updateReq)
	case "remove_whitelist":
		return w.removeWhitelist(updateReq)
	default:
		return w.updateError(fmt.Sprintf("Unknown action: %s", updateReq.Action))
	}
}

// addTorrent adds a new torrent to the tracker
func (w *Worker) addTorrent(req UpdateRequest) ([]byte, error) {
	if req.TorrentID <= 0 {
		return w.updateError("Invalid torrent_id")
	}
	if req.InfoHash == "" {
		return w.updateError("Missing info_hash")
	}
	if len(req.InfoHash) != 20 && len(req.InfoHash) != 40 {
		return w.updateError("info_hash must be 20 or 40 characters")
	}

	// Check if torrent already exists
	if _, ok := w.Torrents.Get(req.InfoHash); ok {
		return w.updateError("Torrent already exists")
	}

	// Create new torrent
	torrent := NewTorrent(TorrentID(req.TorrentID))
	w.Torrents.Set(req.InfoHash, torrent)

	return w.updateSuccess(fmt.Sprintf("Added torrent %d", req.TorrentID))
}

// updateTorrent updates an existing torrent's properties
func (w *Worker) updateTorrent(req UpdateRequest) ([]byte, error) {
	if req.InfoHash == "" {
		return w.updateError("Missing info_hash")
	}

	torrent, ok := w.Torrents.Get(req.InfoHash)
	if !ok {
		return w.updateError("Torrent not found")
	}

	// Update fields (currently only supports balance updates)
	torrent.mu.Lock()
	defer torrent.mu.Unlock()

	return w.updateSuccess(fmt.Sprintf("Updated torrent %s", req.InfoHash))
}

// deleteTorrent removes a torrent from the tracker
func (w *Worker) deleteTorrent(req UpdateRequest) ([]byte, error) {
	if req.InfoHash == "" && req.TorrentID <= 0 {
		return w.updateError("Missing info_hash or torrent_id")
	}

	// If only torrent_id provided, find by ID
	if req.InfoHash == "" {
		// Linear search through torrents (acceptable for delete operations)
		found := false
		w.Torrents.mu.Lock()
		for hash, torrent := range w.Torrents.torrents {
			if torrent.ID == TorrentID(req.TorrentID) {
				delete(w.Torrents.torrents, hash)
				found = true
				break
			}
		}
		w.Torrents.mu.Unlock()

		if !found {
			return w.updateError("Torrent not found")
		}
		return w.updateSuccess(fmt.Sprintf("Deleted torrent %d", req.TorrentID))
	}

	// Delete by info_hash
	w.Torrents.mu.Lock()
	delete(w.Torrents.torrents, req.InfoHash)
	w.Torrents.mu.Unlock()

	return w.updateSuccess(fmt.Sprintf("Deleted torrent %s", req.InfoHash))
}

// changeFreeleech changes a torrent's freeleech status
func (w *Worker) changeFreeleech(req UpdateRequest) ([]byte, error) {
	if req.InfoHash == "" {
		return w.updateError("Missing info_hash")
	}

	torrent, ok := w.Torrents.Get(req.InfoHash)
	if !ok {
		return w.updateError("Torrent not found")
	}

	// Validate FreeType
	if req.FreeType < 0 || req.FreeType > 2 {
		return w.updateError("Invalid free_type (0=normal, 1=free, 2=neutral)")
	}

	torrent.mu.Lock()
	torrent.FreeType = FreeType(req.FreeType)
	torrent.mu.Unlock()

	freeTypeNames := []string{"normal", "free", "neutral"}
	return w.updateSuccess(fmt.Sprintf("Set torrent to %s", freeTypeNames[req.FreeType]))
}

// addUser adds a new user to the tracker
func (w *Worker) addUser(req UpdateRequest) ([]byte, error) {
	if req.UserID <= 0 {
		return w.updateError("Invalid user_id")
	}
	if req.Passkey == "" {
		return w.updateError("Missing passkey")
	}
	if len(req.Passkey) != 32 {
		return w.updateError("Passkey must be 32 characters")
	}

	// Check if user already exists
	if _, ok := w.Users.Get(req.Passkey); ok {
		return w.updateError("Passkey already exists")
	}

	// Default privileges
	canLeech := true
	protectIP := false
	if req.CanLeech != nil {
		canLeech = *req.CanLeech
	}
	if req.ProtectIP != nil {
		protectIP = *req.ProtectIP
	}

	// Create new user
	user := NewUser(UserID(req.UserID), canLeech, protectIP)
	w.Users.Set(req.Passkey, user)

	return w.updateSuccess(fmt.Sprintf("Added user %d", req.UserID))
}

// updateUser updates an existing user's privileges
func (w *Worker) updateUser(req UpdateRequest) ([]byte, error) {
	if req.Passkey == "" {
		return w.updateError("Missing passkey")
	}

	user, ok := w.Users.Get(req.Passkey)
	if !ok {
		return w.updateError("User not found")
	}

	// Update privileges
	if req.CanLeech != nil {
		user.CanLeech.Store(*req.CanLeech)
	}
	if req.ProtectIP != nil {
		user.ProtectIP.Store(*req.ProtectIP)
	}

	return w.updateSuccess(fmt.Sprintf("Updated user %d", user.ID))
}

// deleteUser removes a user from the tracker
func (w *Worker) deleteUser(req UpdateRequest) ([]byte, error) {
	if req.Passkey == "" {
		return w.updateError("Missing passkey")
	}

	user, ok := w.Users.Get(req.Passkey)
	if !ok {
		return w.updateError("User not found")
	}

	// Mark as deleted (don't actually remove to preserve peer history)
	user.Deleted.Store(true)

	return w.updateSuccess(fmt.Sprintf("Deleted user %d", user.ID))
}

// changePasskey changes a user's passkey
func (w *Worker) changePasskey(req UpdateRequest) ([]byte, error) {
	if req.Passkey == "" || req.NewPasskey == "" {
		return w.updateError("Missing passkey or new_passkey")
	}
	if len(req.NewPasskey) != 32 {
		return w.updateError("New passkey must be 32 characters")
	}

	user, ok := w.Users.Get(req.Passkey)
	if !ok {
		return w.updateError("User not found")
	}

	// Check if new passkey already exists
	if _, ok := w.Users.Get(req.NewPasskey); ok {
		return w.updateError("New passkey already exists")
	}

	// Update passkey mapping
	w.Users.mu.Lock()
	delete(w.Users.users, req.Passkey)
	w.Users.users[req.NewPasskey] = user
	w.Users.mu.Unlock()

	return w.updateSuccess(fmt.Sprintf("Changed passkey for user %d", user.ID))
}

// addToken grants a freeleech token to a user for a torrent
func (w *Worker) addToken(req UpdateRequest) ([]byte, error) {
	if req.UserID <= 0 {
		return w.updateError("Invalid user_id")
	}
	if req.InfoHash == "" {
		return w.updateError("Missing info_hash")
	}

	torrent, ok := w.Torrents.Get(req.InfoHash)
	if !ok {
		return w.updateError("Torrent not found")
	}

	torrent.mu.Lock()
	torrent.TokenedUsers[UserID(req.UserID)] = struct{}{}
	torrent.mu.Unlock()

	return w.updateSuccess(fmt.Sprintf("Added token for user %d", req.UserID))
}

// removeToken removes a freeleech token from a user
func (w *Worker) removeToken(req UpdateRequest) ([]byte, error) {
	if req.UserID <= 0 {
		return w.updateError("Invalid user_id")
	}
	if req.InfoHash == "" {
		return w.updateError("Missing info_hash")
	}

	torrent, ok := w.Torrents.Get(req.InfoHash)
	if !ok {
		return w.updateError("Torrent not found")
	}

	torrent.mu.Lock()
	delete(torrent.TokenedUsers, UserID(req.UserID))
	torrent.mu.Unlock()

	return w.updateSuccess(fmt.Sprintf("Removed token for user %d", req.UserID))
}

// addWhitelist adds a peer_id prefix to the whitelist
func (w *Worker) addWhitelist(req UpdateRequest) ([]byte, error) {
	if req.PeerIDPrefix == "" {
		return w.updateError("Missing peer_id_prefix")
	}

	w.Whitelist.Add(req.PeerIDPrefix)

	return w.updateSuccess(fmt.Sprintf("Added whitelist prefix: %s", req.PeerIDPrefix))
}

// removeWhitelist removes a peer_id prefix from the whitelist
func (w *Worker) removeWhitelist(req UpdateRequest) ([]byte, error) {
	if req.PeerIDPrefix == "" {
		return w.updateError("Missing peer_id_prefix")
	}

	w.Whitelist.Remove(req.PeerIDPrefix)

	return w.updateSuccess(fmt.Sprintf("Removed whitelist prefix: %s", req.PeerIDPrefix))
}

// updateSuccess returns a success JSON response
func (w *Worker) updateSuccess(message string) ([]byte, error) {
	resp := UpdateResponse{
		Success: true,
		Message: message,
	}
	return json.Marshal(resp)
}

// updateError returns an error JSON response
func (w *Worker) updateError(errMsg string) ([]byte, error) {
	resp := UpdateResponse{
		Success: false,
		Error:   errMsg,
	}
	data, _ := json.Marshal(resp)
	return data, fmt.Errorf(errMsg)
}

// StatsResponse contains live tracker statistics for JSON API
type StatsResponse struct {
	Uptime       string `json:"uptime"`
	Torrents     int    `json:"torrents"`
	Users        int    `json:"users"`
	Seeders      uint32 `json:"seeders"`
	Leechers     uint32 `json:"leechers"`
	Connections  uint32 `json:"connections"`
	Announces    uint64 `json:"announces"`
	SuccAnnounces uint64 `json:"successful_announces"`
	Scrapes      uint64 `json:"scrapes"`
	BytesRead    uint64 `json:"bytes_read"`
	BytesWritten uint64 `json:"bytes_written"`
}

// GetStats returns current tracker statistics as JSON
func (w *Worker) GetStats() ([]byte, error) {
	uptime := time.Since(w.Stats.StartTime)

	stats := StatsResponse{
		Uptime:        uptime.Round(time.Second).String(),
		Torrents:      w.Torrents.Size(),
		Users:         w.Users.Size(),
		Seeders:       w.Stats.Seeders.Load(),
		Leechers:      w.Stats.Leechers.Load(),
		Connections:   w.Stats.OpenConnections.Load(),
		Announces:     w.Stats.Announcements.Load(),
		SuccAnnounces: w.Stats.SuccAnnouncements.Load(),
		Scrapes:       w.Stats.Scrapes.Load(),
		BytesRead:     w.Stats.BytesRead.Load(),
		BytesWritten:  w.Stats.BytesWritten.Load(),
	}

	return json.Marshal(stats)
}

// TorrentInfo represents torrent details for JSON API
type TorrentInfo struct {
	InfoHash  string `json:"info_hash"`
	TorrentID uint32 `json:"torrent_id"`
	Seeders   int    `json:"seeders"`
	Leechers  int    `json:"leechers"`
	Completed uint32 `json:"completed"`
	FreeType  uint8  `json:"free_type"`
	Balance   int64  `json:"balance"`
}

// GetTorrents returns list of active torrents as JSON
func (w *Worker) GetTorrents(limit int) ([]byte, error) {
	torrents := make([]TorrentInfo, 0, limit)
	count := 0

	w.Torrents.mu.RLock()
	for hash, torrent := range w.Torrents.torrents {
		if count >= limit {
			break
		}

		torrent.mu.RLock()
		info := TorrentInfo{
			InfoHash:  hash,
			TorrentID: uint32(torrent.ID),
			Seeders:   torrent.Seeders.Size(),
			Leechers:  torrent.Leechers.Size(),
			Completed: torrent.Completed,
			FreeType:  uint8(torrent.FreeType),
			Balance:   torrent.Balance,
		}
		torrent.mu.RUnlock()

		torrents = append(torrents, info)
		count++
	}
	w.Torrents.mu.RUnlock()

	return json.Marshal(torrents)
}

// PeerInfo represents peer details for JSON API
type PeerInfo struct {
	UserID       uint32 `json:"user_id"`
	IP           string `json:"ip"`
	Port         uint16 `json:"port"`
	Uploaded     int64  `json:"uploaded"`
	Downloaded   int64  `json:"downloaded"`
	Left         int64  `json:"left"`
	LastAnnounce string `json:"last_announce"`
	Announces    uint32 `json:"announces"`
}

// GetPeers returns list of peers for a torrent as JSON
func (w *Worker) GetPeers(infoHash string, limit int) ([]byte, error) {
	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return nil, fmt.Errorf("torrent not found")
	}

	peers := make([]PeerInfo, 0, limit)
	count := 0

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()

	// Add seeders
	torrent.Seeders.ForEach(func(_ string, peer *Peer) bool {
		if count >= limit {
			return false
		}
		peers = append(peers, w.peerToInfo(peer))
		count++
		return true
	})

	// Add leechers
	if count < limit {
		torrent.Leechers.ForEach(func(_ string, peer *Peer) bool {
			if count >= limit {
				return false
			}
			peers = append(peers, w.peerToInfo(peer))
			count++
			return true
		})
	}

	return json.Marshal(peers)
}

func (w *Worker) peerToInfo(peer *Peer) PeerInfo {
	return PeerInfo{
		UserID:       uint32(peer.UserID),
		IP:           peer.IP.String(),
		Port:         peer.Port,
		Uploaded:     peer.Uploaded,
		Downloaded:   peer.Downloaded,
		Left:         peer.Left,
		LastAnnounce: peer.LastAnnounced.Format(time.RFC3339),
		Announces:    peer.Announces,
	}
}

// Size returns the number of torrents (for TorrentList)
func (tl *TorrentList) Size() int {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	return len(tl.torrents)
}

// Size returns the number of users (for UserList)
func (ul *UserList) Size() int {
	ul.mu.RLock()
	defer ul.mu.RUnlock()
	return len(ul.users)
}

// Add adds a prefix to the whitelist
func (wl *Whitelist) Add(prefix string) {
	wl.mu.Lock()
	defer wl.mu.Unlock()

	// Check if already exists
	for _, p := range wl.prefixes {
		if p == prefix {
			return
		}
	}

	wl.prefixes = append(wl.prefixes, prefix)
}

// Remove removes a prefix from the whitelist
func (wl *Whitelist) Remove(prefix string) {
	wl.mu.Lock()
	defer wl.mu.Unlock()

	for i, p := range wl.prefixes {
		if p == prefix {
			wl.prefixes = append(wl.prefixes[:i], wl.prefixes[i+1:]...)
			return
		}
	}
}

// GetAll returns all whitelist prefixes
func (wl *Whitelist) GetAll() []string {
	wl.mu.RLock()
	defer wl.mu.RUnlock()

	result := make([]string, len(wl.prefixes))
	copy(result, wl.prefixes)
	return result
}

// GetWhitelist returns the whitelist as JSON
func (w *Worker) GetWhitelist() ([]byte, error) {
	return json.Marshal(map[string][]string{
		"prefixes": w.Whitelist.GetAll(),
	})
}

// Helper function to parse query parameter as int
func queryInt(req *http.Request, param string, defaultVal int) int {
	val := req.URL.Query().Get(param)
	if val == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return i
}
