package tracker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/mgdavisxvs/Ocelot/commons"
)

// HandleUpdate processes tracker update requests from the admin panel.
// Requests are GET with URL query parameters.
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
	case "remove_user":
		return w.removeUser(q)
	case "update_user":
		return w.updateUser(q)
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
	case "set_priority_class":
		return w.setPriorityClass(q)
	case "set_budget":
		return w.setBudget(q)
	default:
		return w.updateError(fmt.Sprintf("unknown action: %s", action))
	}
}

func (w *Worker) addTorrent(q map[string][]string) ([]byte, error) {
	idStr := urlParam(q, "id")
	if idStr == "" {
		return w.updateError("missing id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return w.updateError("invalid id")
	}
	infoHash := urlParam(q, "info_hash")
	if infoHash == "" {
		return w.updateError("missing info_hash")
	}

	freeType := FreeNormal
	if ft := urlParam(q, "free_type"); ft != "" {
		n, err := strconv.Atoi(ft)
		if err != nil || n < 0 || n > 2 {
			return w.updateError("invalid free_type")
		}
		freeType = FreeType(n)
	}

	if _, ok := w.Torrents.Get(infoHash); ok {
		return w.updateError("torrent already exists")
	}

	tor := NewTorrent(TorrentID(id))
	tor.FreeType = freeType
	w.Torrents.Set(infoHash, tor)

	return w.updateOK()
}

func (w *Worker) updateTorrent(q map[string][]string) ([]byte, error) {
	infoHash := urlParam(q, "info_hash")
	if infoHash == "" {
		return w.updateError("missing info_hash")
	}

	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return w.updateError("torrent not found")
	}

	torrent.mu.Lock()
	defer torrent.mu.Unlock()

	if ft := urlParam(q, "free_type"); ft != "" {
		n, err := strconv.Atoi(ft)
		if err != nil || n < 0 || n > 2 {
			return w.updateError("invalid free_type")
		}
		torrent.FreeType = FreeType(n)
	}

	return w.updateOK()
}

func (w *Worker) deleteTorrent(q map[string][]string) ([]byte, error) {
	infoHash := urlParam(q, "info_hash")
	if infoHash == "" {
		return w.updateError("missing info_hash")
	}

	w.Torrents.mu.Lock()
	_, found := w.Torrents.torrents[infoHash]
	if found {
		delete(w.Torrents.torrents, infoHash)
	}
	w.Torrents.mu.Unlock()

	if !found {
		return w.updateError("torrent not found")
	}
	return w.updateOK()
}

func (w *Worker) changeFreeleech(q map[string][]string) ([]byte, error) {
	infoHash := urlParam(q, "info_hash")
	if infoHash == "" {
		return w.updateError("missing info_hash")
	}

	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return w.updateError("torrent not found")
	}

	ft := urlParam(q, "free_type")
	n, err := strconv.Atoi(ft)
	if err != nil || n < 0 || n > 2 {
		return w.updateError("invalid free_type")
	}

	torrent.mu.Lock()
	torrent.FreeType = FreeType(n)
	torrent.mu.Unlock()

	return w.updateOK()
}

func (w *Worker) addUser(q map[string][]string) ([]byte, error) {
	idStr := urlParam(q, "id")
	if idStr == "" {
		return w.updateError("missing id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return w.updateError("invalid id")
	}

	passkey := urlParam(q, "passkey")
	if passkey == "" {
		return w.updateError("missing passkey")
	}
	if len(passkey) != 32 {
		return w.updateError("passkey must be 32 characters")
	}

	if _, ok := w.Users.Get(passkey); ok {
		return w.updateError("passkey already exists")
	}

	canLeech := urlParam(q, "can_leech") != "0"
	protectIP := urlParam(q, "protect_ip") == "1"

	user := NewUser(UserID(id), canLeech, protectIP)
	w.Users.Set(passkey, user)

	return w.updateOK()
}

func (w *Worker) removeUser(q map[string][]string) ([]byte, error) {
	passkey := urlParam(q, "passkey")
	if passkey == "" {
		return w.updateError("missing passkey")
	}

	w.Users.mu.Lock()
	_, found := w.Users.users[passkey]
	if found {
		delete(w.Users.users, passkey)
	}
	w.Users.mu.Unlock()

	if !found {
		return w.updateError("user not found")
	}
	return w.updateOK()
}

func (w *Worker) updateUser(q map[string][]string) ([]byte, error) {
	passkey := urlParam(q, "passkey")
	if passkey == "" {
		return w.updateError("missing passkey")
	}

	user, ok := w.Users.Get(passkey)
	if !ok {
		return w.updateError("user not found")
	}

	if cl := urlParam(q, "can_leech"); cl != "" {
		user.CanLeech.Store(cl != "0")
	}
	if pi := urlParam(q, "protect_ip"); pi != "" {
		user.ProtectIP.Store(pi == "1")
	}

	return w.updateOK()
}

func (w *Worker) changePasskey(q map[string][]string) ([]byte, error) {
	oldKey := urlParam(q, "old_passkey")
	newKey := urlParam(q, "new_passkey")
	if oldKey == "" || newKey == "" {
		return w.updateError("missing old_passkey or new_passkey")
	}
	w.Users.mu.Lock()
	user, found := w.Users.users[oldKey]
	if found {
		if _, exists := w.Users.users[newKey]; exists {
			w.Users.mu.Unlock()
			return w.updateError("new passkey already exists")
		}
		delete(w.Users.users, oldKey)
		w.Users.users[newKey] = user
	}
	w.Users.mu.Unlock()

	if !found {
		return w.updateError("user not found")
	}
	return w.updateOK()
}

func (w *Worker) addToken(q map[string][]string) ([]byte, error) {
	idStr := urlParam(q, "user_id")
	if idStr == "" {
		idStr = urlParam(q, "id")
	}
	if idStr == "" {
		return w.updateError("missing user_id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return w.updateError("invalid user_id")
	}

	infoHash := urlParam(q, "info_hash")
	if infoHash == "" {
		return w.updateError("missing info_hash")
	}

	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return w.updateError("torrent not found")
	}

	torrent.mu.Lock()
	torrent.TokenedUsers[UserID(id)] = struct{}{}
	torrent.mu.Unlock()

	return w.updateOK()
}

func (w *Worker) removeToken(q map[string][]string) ([]byte, error) {
	idStr := urlParam(q, "user_id")
	if idStr == "" {
		idStr = urlParam(q, "id")
	}
	if idStr == "" {
		return w.updateError("missing user_id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return w.updateError("invalid user_id")
	}

	infoHash := urlParam(q, "info_hash")
	if infoHash == "" {
		return w.updateError("missing info_hash")
	}

	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return w.updateError("torrent not found")
	}

	torrent.mu.Lock()
	delete(torrent.TokenedUsers, UserID(id))
	torrent.mu.Unlock()

	return w.updateOK()
}

func (w *Worker) addWhitelist(q map[string][]string) ([]byte, error) {
	prefix := urlParam(q, "prefix")
	if prefix == "" {
		return w.updateError("missing prefix")
	}
	w.Whitelist.Add(prefix)
	return w.updateOK()
}

func (w *Worker) removeWhitelist(q map[string][]string) ([]byte, error) {
	prefix := urlParam(q, "prefix")
	if prefix == "" {
		return w.updateError("missing prefix")
	}
	w.Whitelist.Remove(prefix)
	return w.updateOK()
}

// setPriorityClass sets the ComputeCommons priority class (0–3) for a user.
// Requires: id (user_id), priority_class (0=P0Critical … 3=P3Opportunistic).
func (w *Worker) setPriorityClass(q map[string][]string) ([]byte, error) {
	if w.Commons == nil {
		return w.updateError("compute commons not enabled")
	}
	idStr := urlParam(q, "id")
	if idStr == "" {
		return w.updateError("missing id")
	}
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil || id == 0 {
		return w.updateError("invalid id")
	}
	pcStr := urlParam(q, "priority_class")
	if pcStr == "" {
		return w.updateError("missing priority_class")
	}
	pcInt, err := strconv.Atoi(pcStr)
	if err != nil {
		return w.updateError("invalid priority_class")
	}
	pc := commons.PriorityClass(pcInt)
	if !pc.IsValid() {
		return w.updateError(fmt.Sprintf("priority_class must be 0–3, got %d", pcInt))
	}
	if err := w.Commons.SetUserPriority(uint32(id), pc); err != nil {
		return w.updateError(err.Error())
	}
	return w.updateOK()
}

// setBudget sets a per-torrent (or global) CC spending cap for a user.
// Requires: id (user_id), max_credits (whole CC units).
// Optional: torrent_id (omit or 0 for global cap).
func (w *Worker) setBudget(q map[string][]string) ([]byte, error) {
	if w.Commons == nil {
		return w.updateError("compute commons not enabled")
	}
	idStr := urlParam(q, "id")
	if idStr == "" {
		return w.updateError("missing id")
	}
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil || id == 0 {
		return w.updateError("invalid id")
	}
	mcStr := urlParam(q, "max_credits")
	if mcStr == "" {
		return w.updateError("missing max_credits")
	}
	maxCredits, err := strconv.ParseInt(mcStr, 10, 64)
	if err != nil || maxCredits < 0 {
		return w.updateError("invalid max_credits")
	}
	var torrentID uint32
	if tidStr := urlParam(q, "torrent_id"); tidStr != "" {
		tid, err := strconv.ParseUint(tidStr, 10, 32)
		if err != nil {
			return w.updateError("invalid torrent_id")
		}
		torrentID = uint32(tid)
	}
	if err := w.Commons.SetUserBudget(uint32(id), torrentID, maxCredits); err != nil {
		return w.updateError(err.Error())
	}
	return w.updateOK()
}

func (w *Worker) updateOK() ([]byte, error) {
	return json.Marshal(map[string]string{"status": "ok"})
}

func (w *Worker) updateError(msg string) ([]byte, error) {
	data, _ := json.Marshal(map[string]string{"status": "error", "message": msg})
	return data, fmt.Errorf("%s", msg)
}

func urlParam(q map[string][]string, key string) string {
	if vals, ok := q[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// StatsResponse contains live tracker statistics for JSON API
type StatsResponse struct {
	Uptime        string `json:"uptime"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	TorrentCount  int    `json:"torrent_count"`
	UserCount     int    `json:"user_count"`
	Seeders       uint32 `json:"seeders"`
	Leechers      uint32 `json:"leechers"`
	Connections   uint32 `json:"connections"`
	Announcements uint64 `json:"announcements"`
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
		UptimeSeconds: int64(uptime.Seconds()),
		TorrentCount:  w.Torrents.Size(),
		UserCount:     w.Users.Size(),
		Seeders:       w.Stats.Seeders.Load(),
		Leechers:      w.Stats.Leechers.Load(),
		Connections:   w.Stats.OpenConnections.Load(),
		Announcements: w.Stats.Announcements.Load(),
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
	ID        uint32 `json:"id"`
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
			ID:        uint32(torrent.ID),
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

	// Add seeders
	torrent.Seeders.ForEach(func(_ string, peer *Peer) bool {
		if count >= limit {
			return false
		}
		info := w.peerToInfo(peer)
		info.Seeder = true
		peers = append(peers, info)
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

// GetWhitelist returns the whitelist as a flat JSON array
func (w *Worker) GetWhitelist() ([]byte, error) {
	return json.Marshal(w.Whitelist.GetAll())
}
