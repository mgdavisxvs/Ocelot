package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// HandleUpdate processes tracker update requests from the admin panel.
// Requests arrive as GET with URL query parameters.
// Equivalent to C++ worker::update() (worker.cpp:768-996).
func (w *Worker) HandleUpdate(req *http.Request) ([]byte, error) {
	params := req.URL.Query()
	action := params.Get("action")

	switch action {
	case "add_torrent":
		return w.addTorrent(params)
	case "update_torrent":
		return w.updateTorrent(params)
	case "delete_torrent":
		return w.deleteTorrent(params)
	case "add_user":
		return w.addUser(params)
	case "remove_user", "delete_user":
		return w.removeUser(params)
	case "change_passkey":
		return w.changePasskey(params)
	case "add_whitelist":
		return w.addWhitelist(params)
	case "remove_whitelist":
		return w.removeWhitelist(params)
	default:
		return w.updateError(fmt.Sprintf("unknown action: %s", action))
	}
}

// ── update helpers ──────────────────────────────────────────────────────────

type queryParams = map[string][]string

func qpGet(p queryParams, key string) string {
	if vals, ok := p[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}

func qpInt(p queryParams, key string) int {
	v := qpGet(p, key)
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

// ── admin actions ───────────────────────────────────────────────────────────

func (w *Worker) addTorrent(p queryParams) ([]byte, error) {
	idStr := qpGet(p, "id")
	if idStr == "" {
		return w.updateError("missing id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return w.updateError("invalid id")
	}

	infoHash := qpGet(p, "info_hash")
	if infoHash == "" {
		return w.updateError("missing info_hash")
	}
	if _, ok := w.Torrents.Get(infoHash); ok {
		return w.updateError("torrent already exists")
	}

	torrent := NewTorrent(TorrentID(id))
	if ft := qpGet(p, "free_type"); ft != "" {
		n, _ := strconv.Atoi(ft)
		if n >= 0 && n <= 2 {
			torrent.FreeType = FreeType(n)
		}
	}
	if sizeStr := qpGet(p, "size"); sizeStr != "" {
		if sz, err := strconv.ParseInt(sizeStr, 10, 64); err == nil && sz > 0 {
			torrent.Size = sz
		}
	}
	w.Torrents.Set(infoHash, torrent)
	if err := w.DB.RecordTorrentHash(torrent.ID, infoHash); err != nil {
		w.logAudit("add_torrent", "torrent", idStr, false, err)
		return w.updateError("failed to persist torrent hash: " + err.Error())
	}
	w.logAudit("add_torrent", "torrent", idStr, true, nil)
	return w.updateOK()
}

func (w *Worker) updateTorrent(p queryParams) ([]byte, error) {
	infoHash := qpGet(p, "info_hash")
	if infoHash == "" {
		return w.updateError("missing info_hash")
	}

	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		return w.updateError("torrent not found")
	}

	torrent.mu.Lock()
	if ft := qpGet(p, "free_type"); ft != "" {
		n, _ := strconv.Atoi(ft)
		if n >= 0 && n <= 2 {
			torrent.FreeType = FreeType(n)
		}
	}
	torrent.mu.Unlock()
	return w.updateOK()
}

func (w *Worker) deleteTorrent(p queryParams) ([]byte, error) {
	infoHash := qpGet(p, "info_hash")
	if infoHash == "" {
		return w.updateError("missing info_hash")
	}
	if _, ok := w.Torrents.Get(infoHash); !ok {
		return w.updateError("torrent not found")
	}
	w.Torrents.Delete(infoHash)
	w.logAudit("delete_torrent", "torrent", infoHash, true, nil)
	return w.updateOK()
}

func (w *Worker) addUser(p queryParams) ([]byte, error) {
	idStr := qpGet(p, "id")
	if idStr == "" {
		return w.updateError("missing id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return w.updateError("invalid id")
	}

	passkey := qpGet(p, "passkey")
	if passkey == "" {
		return w.updateError("missing passkey")
	}
	if len(passkey) != 32 {
		return w.updateError("passkey must be 32 characters")
	}

	if _, ok := w.Users.Get(passkey); ok {
		return w.updateError("passkey already exists")
	}

	canLeech := qpGet(p, "can_leech") != "0"
	protectIP := qpGet(p, "protect_ip") == "1"

	user := NewUser(UserID(id), canLeech, protectIP)
	w.Users.Set(passkey, user)
	if err := w.DB.RecordUserPasskey(user.ID, passkey, canLeech, protectIP); err != nil {
		w.logAudit("add_user", "user", idStr, false, err)
		return w.updateError("failed to persist user passkey: " + err.Error())
	}
	w.logAudit("add_user", "user", idStr, true, nil)
	return w.updateOK()
}

func (w *Worker) removeUser(p queryParams) ([]byte, error) {
	passkey := qpGet(p, "passkey")
	if passkey == "" {
		return w.updateError("missing passkey")
	}
	if _, ok := w.Users.Get(passkey); !ok {
		return w.updateError("user not found")
	}
	w.Users.Delete(passkey)
	w.logAudit("remove_user", "user", passkey, true, nil)
	return w.updateOK()
}

func (w *Worker) changePasskey(p queryParams) ([]byte, error) {
	oldPasskey := qpGet(p, "old_passkey")
	newPasskey := qpGet(p, "new_passkey")
	if oldPasskey == "" || newPasskey == "" {
		return w.updateError("missing old_passkey or new_passkey")
	}

	user, ok := w.Users.Get(oldPasskey)
	if !ok {
		return w.updateError("user not found")
	}
	if _, ok := w.Users.Get(newPasskey); ok {
		return w.updateError("new passkey already exists")
	}

	w.Users.mu.Lock()
	delete(w.Users.users, oldPasskey)
	w.Users.users[newPasskey] = user
	w.Users.mu.Unlock()
	return w.updateOK()
}

func (w *Worker) addWhitelist(p queryParams) ([]byte, error) {
	prefix := qpGet(p, "prefix")
	if prefix == "" {
		return w.updateError("missing prefix")
	}
	w.Whitelist.Add(prefix)
	return w.updateOK()
}

func (w *Worker) removeWhitelist(p queryParams) ([]byte, error) {
	prefix := qpGet(p, "prefix")
	if prefix == "" {
		return w.updateError("missing prefix")
	}
	w.Whitelist.Remove(prefix)
	return w.updateOK()
}

// logAudit emits an audit record when an AuditLogger is wired into the Worker.
func (w *Worker) logAudit(action, resourceType, resourceID string, success bool, err error) {
	if w.AuditLog == nil {
		return
	}
	if aerr := w.AuditLog.Log(context.Background(), action, resourceType, resourceID, success, err); aerr != nil {
		GetDefaultLogger().Warn("audit log write failed", "err", aerr.Error())
	}
}

// ── response helpers ────────────────────────────────────────────────────────

func (w *Worker) updateOK() ([]byte, error) {
	return json.Marshal(map[string]string{"status": "ok"})
}

func (w *Worker) updateError(msg string) ([]byte, error) {
	data, _ := json.Marshal(map[string]string{"error": msg})
	return data, fmt.Errorf("%s", msg)
}

// ── Stats / Torrents / Peers / Whitelist JSON APIs ──────────────────────────

// StatsResponse contains live tracker statistics for the JSON API.
type StatsResponse struct {
	UptimeSeconds float64 `json:"uptime_seconds"`
	TorrentCount  int     `json:"torrent_count"`
	UserCount     int     `json:"user_count"`
	Seeders       uint32  `json:"seeders"`
	Leechers      uint32  `json:"leechers"`
	Connections   uint32  `json:"connections"`
	Announcements uint64  `json:"announcements"`
	Scrapes       uint64  `json:"scrapes"`
	BytesRead     uint64  `json:"bytes_read"`
	BytesWritten  uint64  `json:"bytes_written"`
	// Subsystem health counters
	EvictedPeers      uint64 `json:"evicted_peers"`
	AnomalyDetections uint64 `json:"anomaly_detections"`
	DBQueueDepth      int    `json:"db_queue_depth"`
	DBDroppedOps      uint64 `json:"db_dropped_ops"`
}

// GetStats returns current tracker statistics as JSON.
func (w *Worker) GetStats() ([]byte, error) {
	stats := StatsResponse{
		UptimeSeconds:     time.Since(w.Stats.StartTime).Seconds(),
		TorrentCount:      w.Torrents.Size(),
		UserCount:         w.Users.Size(),
		Seeders:           w.Stats.Seeders.Load(),
		Leechers:          w.Stats.Leechers.Load(),
		Connections:       w.Stats.OpenConnections.Load(),
		Announcements:     w.Stats.Announcements.Load(),
		Scrapes:           w.Stats.Scrapes.Load(),
		BytesRead:         w.Stats.BytesRead.Load(),
		BytesWritten:      w.Stats.BytesWritten.Load(),
		EvictedPeers:      w.Stats.EvictedPeers.Load(),
		AnomalyDetections: w.Stats.AnomalyDetections.Load(),
	}
	if bdb, ok := w.DB.(*BufferedDB); ok {
		stats.DBQueueDepth = bdb.QueueDepth()
		stats.DBDroppedOps = bdb.DroppedOps()
	}
	return json.Marshal(stats)
}

// TorrentInfo represents torrent details for the JSON API.
type TorrentInfo struct {
	ID        uint32 `json:"id"`
	InfoHash  string `json:"info_hash"`
	Seeders   int    `json:"seeders"`
	Leechers  int    `json:"leechers"`
	Completed uint32 `json:"completed"`
	FreeType  uint8  `json:"free_type"`
	Balance   int64  `json:"balance"`
}

// GetTorrents returns a list of active torrents as JSON.
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
			ID:        uint32(torrent.ID),
			InfoHash:  hash,
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

	if torrents == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(torrents)
}

// PeerInfo represents peer details for the JSON API.
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

// GetPeers returns a list of peers for a torrent as JSON.
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
	ipStr := ""
	if peer.IP != nil {
		ipStr = peer.IP.String()
	}
	lastAnnounce := ""
	if !peer.LastAnnounced.IsZero() {
		lastAnnounce = peer.LastAnnounced.Format(time.RFC3339)
	}
	return PeerInfo{
		UserID:       uint32(peer.UserID),
		IP:           ipStr,
		Port:         peer.Port,
		Uploaded:     peer.Uploaded,
		Downloaded:   peer.Downloaded,
		Left:         peer.Left,
		LastAnnounce: lastAnnounce,
		Announces:    peer.Announces,
		Seeder:       seeder,
	}
}

// GetWhitelist returns the whitelist prefix slice as JSON.
func (w *Worker) GetWhitelist() ([]byte, error) {
	prefixes := w.Whitelist.GetAll()
	if len(prefixes) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(prefixes)
}
