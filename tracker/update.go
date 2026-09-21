package tracker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// UpdateResponse represents the tracker's response to an update
type UpdateResponse struct {
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HandleUpdate processes tracker update requests from the admin panel.
// Parameters are read from URL query string (GET/POST form).
func (w *Worker) HandleUpdate(req *http.Request) ([]byte, error) {
	q := req.URL.Query()
	action := q.Get("action")

	switch action {
	case "add_torrent":
		return w.addTorrent(q)
	case "update_torrent":
		return w.updateTorrent(q)
	case "delete_torrent":
		return w.deleteTorrent(q)
	case "change_freeleech":
		return w.changeFreeleech(q)
	case "add_user":
		return w.addUser(q)
	case "update_user":
		return w.updateUser(q)
	case "remove_user":
		return w.removeUser(q)
	case "change_passkey":
		return w.changePasskey(q)
	case "add_token":
		return w.addToken(q)
	case "remove_token":
		return w.removeToken(q)
	case "add_whitelist":
		return w.addWhitelist(q)
	case "remove_whitelist":
		return w.removeWhitelist(q)
	default:
		return w.updateError(fmt.Sprintf("Unknown action: %s", action))
	}
}

func (w *Worker) addTorrent(q url.Values) ([]byte, error) {
	idStr := q.Get("id")
	if idStr == "" {
		return w.updateError("Invalid torrent_id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return w.updateError("Invalid torrent_id")
	}

	infoHash := q.Get("info_hash")
	if infoHash == "" {
		return w.updateError("Missing info_hash")
	}

	if _, ok := w.Torrents.Get(infoHash); ok {
		return w.updateError("Torrent already exists")
	}

	freeType := 0
	if ft := q.Get("free_type"); ft != "" {
		if n, err2 := strconv.Atoi(ft); err2 == nil {
			freeType = n
		}
	}

	torrent := NewTorrent(TorrentID(id))
	torrent.FreeType = FreeType(freeType)
	w.Torrents.Set(infoHash, torrent)

	return w.updateSuccess(fmt.Sprintf("Added torrent %d", id))
}

func (w *Worker) updateTorrent(q url.Values) ([]byte, error) {
	infoHash := q.Get("info_hash")
	if infoHash == "" {
		return w.updateError("Missing info_hash")
	}

	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return w.updateError("Torrent not found")
	}

	torrent.mu.Lock()
	if ft := q.Get("free_type"); ft != "" {
		if n, err := strconv.Atoi(ft); err == nil && n >= 0 && n <= 2 {
			torrent.FreeType = FreeType(n)
		}
	}
	torrent.mu.Unlock()

	return w.updateSuccess(fmt.Sprintf("Updated torrent %s", infoHash))
}

func (w *Worker) deleteTorrent(q url.Values) ([]byte, error) {
	infoHash := q.Get("info_hash")
	idStr := q.Get("id")

	if infoHash == "" && idStr == "" {
		return w.updateError("Missing info_hash or id")
	}

	if infoHash == "" {
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			return w.updateError("Invalid torrent id")
		}
		found := false
		w.Torrents.mu.Lock()
		for hash, torrent := range w.Torrents.torrents {
			if torrent.ID == TorrentID(id) {
				delete(w.Torrents.torrents, hash)
				found = true
				break
			}
		}
		w.Torrents.mu.Unlock()
		if !found {
			return w.updateError("Torrent not found")
		}
		return w.updateSuccess(fmt.Sprintf("Deleted torrent %d", id))
	}

	w.Torrents.mu.Lock()
	delete(w.Torrents.torrents, infoHash)
	w.Torrents.mu.Unlock()

	return w.updateSuccess(fmt.Sprintf("Deleted torrent %s", infoHash))
}

func (w *Worker) changeFreeleech(q url.Values) ([]byte, error) {
	infoHash := q.Get("info_hash")
	if infoHash == "" {
		return w.updateError("Missing info_hash")
	}
	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return w.updateError("Torrent not found")
	}
	ft, err := strconv.Atoi(q.Get("free_type"))
	if err != nil || ft < 0 || ft > 2 {
		return w.updateError("Invalid free_type (0=normal, 1=free, 2=neutral)")
	}
	torrent.mu.Lock()
	torrent.FreeType = FreeType(ft)
	torrent.mu.Unlock()
	freeTypeNames := []string{"normal", "free", "neutral"}
	return w.updateSuccess(fmt.Sprintf("Set torrent to %s", freeTypeNames[ft]))
}

func (w *Worker) addUser(q url.Values) ([]byte, error) {
	idStr := q.Get("id")
	if idStr == "" {
		return w.updateError("Invalid user_id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return w.updateError("Invalid user_id")
	}

	passkey := q.Get("passkey")
	if passkey == "" {
		return w.updateError("Missing passkey")
	}
	if len(passkey) != 32 {
		return w.updateError("Passkey must be 32 characters")
	}
	if _, ok := w.Users.Get(passkey); ok {
		return w.updateError("Passkey already exists")
	}

	canLeech := q.Get("can_leech") != "0"
	protectIP := q.Get("protect_ip") == "1"

	user := NewUser(UserID(id), canLeech, protectIP)
	w.Users.Set(passkey, user)

	return w.updateSuccess(fmt.Sprintf("Added user %d", id))
}

func (w *Worker) updateUser(q url.Values) ([]byte, error) {
	passkey := q.Get("passkey")
	if passkey == "" {
		return w.updateError("Missing passkey")
	}
	user, ok := w.Users.Get(passkey)
	if !ok {
		return w.updateError("User not found")
	}
	if cl := q.Get("can_leech"); cl != "" {
		user.CanLeech.Store(cl != "0")
	}
	if pi := q.Get("protect_ip"); pi != "" {
		user.ProtectIP.Store(pi == "1")
	}
	return w.updateSuccess(fmt.Sprintf("Updated user %d", user.ID))
}

func (w *Worker) removeUser(q url.Values) ([]byte, error) {
	passkey := q.Get("passkey")
	if passkey == "" {
		return w.updateError("Missing passkey")
	}
	user, ok := w.Users.Get(passkey)
	if !ok {
		return w.updateError("User not found")
	}
	w.Users.mu.Lock()
	delete(w.Users.users, passkey)
	w.Users.mu.Unlock()
	return w.updateSuccess(fmt.Sprintf("Removed user %d", user.ID))
}

func (w *Worker) changePasskey(q url.Values) ([]byte, error) {
	oldPasskey := q.Get("old_passkey")
	newPasskey := q.Get("new_passkey")
	if oldPasskey == "" || newPasskey == "" {
		return w.updateError("Missing old_passkey or new_passkey")
	}

	user, ok := w.Users.Get(oldPasskey)
	if !ok {
		return w.updateError("User not found")
	}
	if _, ok := w.Users.Get(newPasskey); ok {
		return w.updateError("New passkey already exists")
	}

	w.Users.mu.Lock()
	delete(w.Users.users, oldPasskey)
	w.Users.users[newPasskey] = user
	w.Users.mu.Unlock()

	return w.updateSuccess(fmt.Sprintf("Changed passkey for user %d", user.ID))
}

func (w *Worker) addToken(q url.Values) ([]byte, error) {
	id, err := strconv.Atoi(q.Get("id"))
	if err != nil || id <= 0 {
		return w.updateError("Invalid user_id")
	}
	infoHash := q.Get("info_hash")
	if infoHash == "" {
		return w.updateError("Missing info_hash")
	}
	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return w.updateError("Torrent not found")
	}
	torrent.mu.Lock()
	torrent.TokenedUsers[UserID(id)] = struct{}{}
	torrent.mu.Unlock()
	return w.updateSuccess(fmt.Sprintf("Added token for user %d", id))
}

func (w *Worker) removeToken(q url.Values) ([]byte, error) {
	id, err := strconv.Atoi(q.Get("id"))
	if err != nil || id <= 0 {
		return w.updateError("Invalid user_id")
	}
	infoHash := q.Get("info_hash")
	if infoHash == "" {
		return w.updateError("Missing info_hash")
	}
	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return w.updateError("Torrent not found")
	}
	torrent.mu.Lock()
	delete(torrent.TokenedUsers, UserID(id))
	torrent.mu.Unlock()
	return w.updateSuccess(fmt.Sprintf("Removed token for user %d", id))
}

func (w *Worker) addWhitelist(q url.Values) ([]byte, error) {
	prefix := q.Get("prefix")
	if prefix == "" {
		return w.updateError("Missing prefix")
	}
	w.Whitelist.Add(prefix)
	return w.updateSuccess(fmt.Sprintf("Added whitelist prefix: %s", prefix))
}

func (w *Worker) removeWhitelist(q url.Values) ([]byte, error) {
	prefix := q.Get("prefix")
	if prefix == "" {
		return w.updateError("Missing prefix")
	}
	w.Whitelist.Remove(prefix)
	return w.updateSuccess(fmt.Sprintf("Removed whitelist prefix: %s", prefix))
}

func (w *Worker) updateSuccess(message string) ([]byte, error) {
	resp := UpdateResponse{Status: "ok", Message: message}
	return json.Marshal(resp)
}

func (w *Worker) updateError(errMsg string) ([]byte, error) {
	resp := UpdateResponse{Error: errMsg}
	data, _ := json.Marshal(resp)
	return data, fmt.Errorf("%s", errMsg)
}

// StatsResponse contains live tracker statistics for JSON API
type StatsResponse struct {
	Uptime        string `json:"uptime_seconds"`
	Torrents      int    `json:"torrent_count"`
	Users         int    `json:"user_count"`
	Seeders       uint32 `json:"seeders"`
	Leechers      uint32 `json:"leechers"`
	Connections   uint32 `json:"connections"`
	Announces     uint64 `json:"announcements"`
	SuccAnnounces uint64 `json:"successful_announces"`
	Scrapes       uint64 `json:"scrapes"`
	BytesRead     uint64 `json:"bytes_read"`
	BytesWritten  uint64 `json:"bytes_written"`
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
	TorrentID uint32 `json:"id"`
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
	Seeder       bool   `json:"seeder"`
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

	torrent.Seeders.ForEach(func(_ string, peer *Peer) bool {
		if count >= limit {
			return false
		}
		peers = append(peers, w.peerToInfo(peer, true))
		count++
		return true
	})

	if count < limit {
		torrent.Leechers.ForEach(func(_ string, peer *Peer) bool {
			if count >= limit {
				return false
			}
			peers = append(peers, w.peerToInfo(peer, false))
			count++
			return true
		})
	}

	return json.Marshal(peers)
}

func (w *Worker) peerToInfo(peer *Peer, seeder bool) PeerInfo {
	return PeerInfo{
		UserID:       uint32(peer.UserID),
		IP:           peer.IP.String(),
		Port:         peer.Port,
		Uploaded:     peer.Uploaded,
		Downloaded:   peer.Downloaded,
		Left:         peer.Left,
		LastAnnounce: peer.LastAnnounced.Format(time.RFC3339),
		Announces:    peer.Announces,
		Seeder:       seeder,
	}
}

// GetWhitelist returns the whitelist prefixes as a JSON array
func (w *Worker) GetWhitelist() ([]byte, error) {
	prefixes := w.Whitelist.GetAll()
	if prefixes == nil {
		prefixes = []string{}
	}
	return json.Marshal(prefixes)
}
