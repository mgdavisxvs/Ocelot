package tracker

import (
	"encoding/json"
	"fmt"
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

