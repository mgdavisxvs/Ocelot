package tracker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HandleUpdate processes admin update requests from the Gazelle site.
// Dispatches on the "action" query parameter.
func (w *Worker) HandleUpdate(req *http.Request) ([]byte, error) {
	params := req.URL.Query()
	action := params.Get("action")

	if w.Config.Readonly && action != "info" {
		return nil, fmt.Errorf("tracker is in readonly mode")
	}

	switch action {
	case "add_torrent":
		return w.adminAddTorrent(params)
	case "delete_torrent":
		return w.adminDeleteTorrent(params)
	case "update_torrent":
		return w.adminUpdateTorrent(params)
	case "add_user":
		return w.adminAddUser(params)
	case "remove_user":
		return w.adminRemoveUser(params)
	case "change_passkey":
		return w.adminChangePasskey(params)
	case "add_whitelist":
		return w.adminAddWhitelist(params)
	case "remove_whitelist":
		return w.adminRemoveWhitelist(params)
	case "info":
		return w.adminInfo()
	default:
		return nil, fmt.Errorf("unknown action: %q", action)
	}
}

func (w *Worker) adminAddTorrent(params map[string][]string) ([]byte, error) {
	idStr := getParam(params, "id")
	infoHash := getParam(params, "info_hash")
	if idStr == "" || infoHash == "" {
		return nil, fmt.Errorf("add_torrent requires id and info_hash")
	}
	id, err := parseUint32(idStr)
	if err != nil {
		return nil, fmt.Errorf("invalid id: %w", err)
	}
	ftStr := getParam(params, "free_type")
	ft := FreeNormal
	if ftStr != "" {
		v, _ := parseUint32(ftStr)
		ft = FreeType(v)
	}

	t := NewTorrent(TorrentID(id))
	t.FreeType = ft
	w.Torrents.Set(infoHash, t)
	if err := w.DB.RecordTorrentHash(TorrentID(id), infoHash); err != nil {
		return nil, fmt.Errorf("persist torrent hash: %w", err)
	}
	return jsonOK("torrent added")
}

func (w *Worker) adminDeleteTorrent(params map[string][]string) ([]byte, error) {
	infoHash := getParam(params, "info_hash")
	if infoHash == "" {
		return nil, fmt.Errorf("delete_torrent requires info_hash")
	}
	w.Torrents.Delete(infoHash)
	return jsonOK("torrent deleted")
}

func (w *Worker) adminUpdateTorrent(params map[string][]string) ([]byte, error) {
	infoHash := getParam(params, "info_hash")
	if infoHash == "" {
		return nil, fmt.Errorf("update_torrent requires info_hash")
	}
	t, ok := w.Torrents.Get(infoHash)
	if !ok {
		return nil, fmt.Errorf("torrent not found")
	}
	if ftStr := getParam(params, "free_type"); ftStr != "" {
		v, _ := parseUint32(ftStr)
		t.mu.Lock()
		t.FreeType = FreeType(v)
		t.mu.Unlock()
	}
	return jsonOK("torrent updated")
}

func (w *Worker) adminAddUser(params map[string][]string) ([]byte, error) {
	idStr := getParam(params, "id")
	passkey := getParam(params, "passkey")
	if idStr == "" || passkey == "" {
		return nil, fmt.Errorf("add_user requires id and passkey")
	}
	id, err := parseUint32(idStr)
	if err != nil {
		return nil, fmt.Errorf("invalid id: %w", err)
	}
	canLeech := getParam(params, "can_leech") != "0"
	protectIP := getParam(params, "protect_ip") == "1"

	u := NewUser(UserID(id), canLeech, protectIP)
	w.Users.Set(passkey, u)
	if err := w.DB.RecordUserPasskey(UserID(id), passkey, canLeech, protectIP); err != nil {
		return nil, fmt.Errorf("persist user passkey: %w", err)
	}
	return jsonOK("user added")
}

func (w *Worker) adminRemoveUser(params map[string][]string) ([]byte, error) {
	passkey := getParam(params, "passkey")
	if passkey == "" {
		return nil, fmt.Errorf("remove_user requires passkey")
	}
	w.Users.Delete(passkey)
	return jsonOK("user removed")
}

func (w *Worker) adminChangePasskey(params map[string][]string) ([]byte, error) {
	oldPasskey := getParam(params, "old_passkey")
	newPasskey := getParam(params, "new_passkey")
	if oldPasskey == "" || newPasskey == "" {
		return nil, fmt.Errorf("change_passkey requires old_passkey and new_passkey")
	}
	u, ok := w.Users.Get(oldPasskey)
	if !ok {
		return nil, fmt.Errorf("user not found")
	}
	w.Users.Delete(oldPasskey)
	w.Users.Set(newPasskey, u)
	if err := w.DB.RecordUserPasskey(u.ID, newPasskey, u.CanLeech.Load(), u.ProtectIP.Load()); err != nil {
		return nil, fmt.Errorf("persist passkey change: %w", err)
	}
	return jsonOK("passkey changed")
}

func (w *Worker) adminAddWhitelist(params map[string][]string) ([]byte, error) {
	prefix := getParam(params, "prefix")
	if prefix == "" {
		return nil, fmt.Errorf("add_whitelist requires prefix")
	}
	w.Whitelist.Add(prefix)
	if err := w.DB.AddWhitelistEntry(prefix); err != nil {
		return nil, fmt.Errorf("persist whitelist entry: %w", err)
	}
	return jsonOK("whitelist entry added")
}

func (w *Worker) adminRemoveWhitelist(params map[string][]string) ([]byte, error) {
	prefix := getParam(params, "prefix")
	if prefix == "" {
		return nil, fmt.Errorf("remove_whitelist requires prefix")
	}
	w.Whitelist.Remove(prefix)
	if err := w.DB.RemoveWhitelistEntry(prefix); err != nil {
		return nil, fmt.Errorf("persist whitelist removal: %w", err)
	}
	return jsonOK("whitelist entry removed")
}

func (w *Worker) adminInfo() ([]byte, error) {
	return w.GetStats()
}

// GetStats returns tracker runtime statistics as JSON.
func (w *Worker) GetStats() ([]byte, error) {
	uptime := time.Since(w.Stats.StartTime)
	data := map[string]interface{}{
		"uptime_seconds":      int64(uptime.Seconds()),
		"open_connections":    w.Stats.OpenConnections.Load(),
		"opened_connections":  w.Stats.OpenedConnections.Load(),
		"seeders":             w.Stats.Seeders.Load(),
		"leechers":            w.Stats.Leechers.Load(),
		"requests":            w.Stats.Requests.Load(),
		"announcements":       w.Stats.Announcements.Load(),
		"succ_announcements":  w.Stats.SuccAnnouncements.Load(),
		"scrapes":             w.Stats.Scrapes.Load(),
		"bytes_read":          w.Stats.BytesRead.Load(),
		"bytes_written":       w.Stats.BytesWritten.Load(),
		"torrent_count":       w.Torrents.Size(),
		"user_count":          w.Users.Size(),
		"whitelist_count":     len(w.Whitelist.GetAll()),
	}
	return json.Marshal(data)
}

// GetTorrents returns a paginated JSON list of torrents with seeder/leecher counts.
func (w *Worker) GetTorrents(limit int) ([]byte, error) {
	type torrentEntry struct {
		InfoHash  string `json:"info_hash"`
		ID        uint32 `json:"id"`
		Seeders   int    `json:"seeders"`
		Leechers  int    `json:"leechers"`
		Completed uint32 `json:"completed"`
		FreeType  uint8  `json:"free_type"`
	}

	var list []torrentEntry
	count := 0
	w.Torrents.ForEach(func(hash string, t *Torrent) bool {
		if count >= limit {
			return false
		}
		t.mu.RLock()
		entry := torrentEntry{
			InfoHash:  hash,
			ID:        uint32(t.ID),
			Seeders:   t.Seeders.Size(),
			Leechers:  t.Leechers.Size(),
			Completed: t.Completed,
			FreeType:  uint8(t.FreeType),
		}
		t.mu.RUnlock()
		list = append(list, entry)
		count++
		return true
	})

	return json.Marshal(list)
}

// GetPeers returns a paginated JSON list of peers for the given torrent.
func (w *Worker) GetPeers(infoHash string, limit int) ([]byte, error) {
	t, ok := w.Torrents.Get(infoHash)
	if !ok {
		return nil, fmt.Errorf("torrent not found")
	}

	type peerEntry struct {
		UserID     uint32 `json:"user_id"`
		IP         string `json:"ip"`
		Port       uint16 `json:"port"`
		Uploaded   int64  `json:"uploaded"`
		Downloaded int64  `json:"downloaded"`
		Left       int64  `json:"left"`
		Seeder     bool   `json:"seeder"`
	}

	var list []peerEntry
	count := 0

	addPeer := func(p *Peer, seeder bool) bool {
		if count >= limit {
			return false
		}
		list = append(list, peerEntry{
			UserID:     uint32(p.UserID),
			IP:         p.IP.String(),
			Port:       p.Port,
			Uploaded:   p.Uploaded,
			Downloaded: p.Downloaded,
			Left:       p.Left,
			Seeder:     seeder,
		})
		count++
		return true
	}

	t.mu.RLock()
	t.Seeders.ForEach(func(_ string, p *Peer) bool { return addPeer(p, true) })
	if count < limit {
		t.Leechers.ForEach(func(_ string, p *Peer) bool { return addPeer(p, false) })
	}
	t.mu.RUnlock()

	return json.Marshal(list)
}

// GetWhitelist returns the current peer_id prefix whitelist as JSON.
func (w *Worker) GetWhitelist() ([]byte, error) {
	return json.Marshal(w.Whitelist.GetAll())
}

// ── helpers ───────────────────────────────────────────────────────────────────

func getParam(params map[string][]string, key string) string {
	if vals, ok := params[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}

func parseUint32(s string) (uint32, error) {
	var v uint32
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}

func jsonOK(msg string) ([]byte, error) {
	return json.Marshal(map[string]string{"status": "ok", "message": msg})
}
