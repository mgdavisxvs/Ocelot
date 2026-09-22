package tracker

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// newAdminRequest builds a GET request with the given query params.
func newAdminRequest(params url.Values) *http.Request {
	req, _ := http.NewRequest("GET", "/?"+params.Encode(), nil)
	return req
}

// newAdminWorker builds a Worker with a mockDB for admin tests.
func newAdminWorker() *Worker {
	return &Worker{
		Config:    &Config{AnnounceInterval: 1800},
		DB:        &mockDB{},
		SiteComm:  &mockSiteComm{},
		Torrents:  NewTorrentList(),
		Users:     NewUserList(),
		Whitelist: NewWhitelist(),
		Stats:     &Stats{StartTime: time.Now()},
	}
}

// ── add_torrent ───────────────────────────────────────────────────────────────

func TestHandleUpdate_AddTorrent(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action":    {"add_torrent"},
		"id":        {"42"},
		"info_hash": {"testhash000000000001"},
		"free_type": {"1"},
	})
	data, err := w.HandleUpdate(req)
	if err != nil {
		t.Fatalf("add_torrent: %v", err)
	}
	if _, ok := w.Torrents.Get("testhash000000000001"); !ok {
		t.Error("torrent not added to TorrentList")
	}
	var resp map[string]string
	json.Unmarshal(data, &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want \"ok\"", resp["status"])
	}

	// Verify FreeType was set
	tor, _ := w.Torrents.Get("testhash000000000001")
	if tor.FreeType != FreeFree {
		t.Errorf("FreeType = %d, want FreeFree (%d)", tor.FreeType, FreeFree)
	}
	if tor.ID != 42 {
		t.Errorf("ID = %d, want 42", tor.ID)
	}
}

func TestHandleUpdate_AddTorrent_MissingID(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action":    {"add_torrent"},
		"info_hash": {"testhash000000000001"},
	})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error when id is missing")
	}
}

func TestHandleUpdate_AddTorrent_DefaultFreeType(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action":    {"add_torrent"},
		"id":        {"1"},
		"info_hash": {"hash1"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatal(err)
	}
	tor, _ := w.Torrents.Get("hash1")
	if tor.FreeType != FreeNormal {
		t.Errorf("default FreeType = %d, want FreeNormal", tor.FreeType)
	}
}

// ── delete_torrent ────────────────────────────────────────────────────────────

func TestHandleUpdate_DeleteTorrent(t *testing.T) {
	w := newAdminWorker()
	w.Torrents.Set("hash1", NewTorrent(1))

	req := newAdminRequest(url.Values{
		"action":    {"delete_torrent"},
		"info_hash": {"hash1"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatalf("delete_torrent: %v", err)
	}
	if _, ok := w.Torrents.Get("hash1"); ok {
		t.Error("torrent still present after delete")
	}
}

func TestHandleUpdate_DeleteTorrent_Missing(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action": {"delete_torrent"},
		// info_hash missing
	})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error when info_hash missing")
	}
}

// ── update_torrent ────────────────────────────────────────────────────────────

func TestHandleUpdate_UpdateTorrent_FreeType(t *testing.T) {
	w := newAdminWorker()
	w.Torrents.Set("hash1", NewTorrent(1))

	req := newAdminRequest(url.Values{
		"action":    {"update_torrent"},
		"info_hash": {"hash1"},
		"free_type": {"2"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatalf("update_torrent: %v", err)
	}
	tor, _ := w.Torrents.Get("hash1")
	if tor.FreeType != FreeNeutral {
		t.Errorf("FreeType = %d, want FreeNeutral", tor.FreeType)
	}
}

func TestHandleUpdate_UpdateTorrent_NotFound(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action":    {"update_torrent"},
		"info_hash": {"nonexistent"},
		"free_type": {"1"},
	})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error for nonexistent torrent")
	}
}

// ── add_user ──────────────────────────────────────────────────────────────────

func TestHandleUpdate_AddUser(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action":     {"add_user"},
		"id":         {"1234"},
		"passkey":    {"abcdef0123456789abcdef0123456789"},
		"can_leech":  {"1"},
		"protect_ip": {"1"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatalf("add_user: %v", err)
	}
	u, ok := w.Users.Get("abcdef0123456789abcdef0123456789")
	if !ok {
		t.Fatal("user not in UserList")
	}
	if u.ID != 1234 {
		t.Errorf("ID = %d, want 1234", u.ID)
	}
	if !u.CanLeech.Load() {
		t.Error("CanLeech should be true")
	}
	if !u.ProtectIP.Load() {
		t.Error("ProtectIP should be true")
	}
}

func TestHandleUpdate_AddUser_CanLeech_Default(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action":  {"add_user"},
		"id":      {"5"},
		"passkey": {"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa5"},
		// can_leech absent → defaults to true (anything != "0")
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatal(err)
	}
	u, _ := w.Users.Get("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa5")
	if !u.CanLeech.Load() {
		t.Error("missing can_leech should default to allowed")
	}
}

func TestHandleUpdate_AddUser_CanLeech_Zero(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action":    {"add_user"},
		"id":        {"6"},
		"passkey":   {"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb06"},
		"can_leech": {"0"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatal(err)
	}
	u, _ := w.Users.Get("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb06")
	if u.CanLeech.Load() {
		t.Error("can_leech=0 should be false")
	}
}

// ── remove_user ───────────────────────────────────────────────────────────────

func TestHandleUpdate_RemoveUser(t *testing.T) {
	w := newAdminWorker()
	w.Users.Set("testpasskey0000000000000000000t", NewUser(1, true, false))

	req := newAdminRequest(url.Values{
		"action":  {"remove_user"},
		"passkey": {"testpasskey0000000000000000000t"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatalf("remove_user: %v", err)
	}
	if _, ok := w.Users.Get("testpasskey0000000000000000000t"); ok {
		t.Error("user still present after remove")
	}
}

// ── change_passkey ────────────────────────────────────────────────────────────

func TestHandleUpdate_ChangePasskey(t *testing.T) {
	w := newAdminWorker()
	u := NewUser(7, true, false)
	w.Users.Set("oldpasskey000000000000000000000", u)

	req := newAdminRequest(url.Values{
		"action":      {"change_passkey"},
		"old_passkey": {"oldpasskey000000000000000000000"},
		"new_passkey": {"newpasskey000000000000000000000"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatalf("change_passkey: %v", err)
	}
	if _, ok := w.Users.Get("oldpasskey000000000000000000000"); ok {
		t.Error("old passkey still present")
	}
	got, ok := w.Users.Get("newpasskey000000000000000000000")
	if !ok {
		t.Fatal("new passkey not found")
	}
	if got.ID != 7 {
		t.Errorf("user ID = %d, want 7", got.ID)
	}
}

func TestHandleUpdate_ChangePasskey_NotFound(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action":      {"change_passkey"},
		"old_passkey": {"doesnotexist00000000000000000000"},
		"new_passkey": {"newpasskey000000000000000000000"},
	})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error for nonexistent old passkey")
	}
}

// ── add_whitelist / remove_whitelist ─────────────────────────────────────────

func TestHandleUpdate_AddWhitelist(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action": {"add_whitelist"},
		"prefix": {"-qB5"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatalf("add_whitelist: %v", err)
	}
	if !w.Whitelist.IsAllowed([]byte("-qB50000000000000000")) {
		t.Error("prefix not found in whitelist after add")
	}
}

func TestHandleUpdate_RemoveWhitelist(t *testing.T) {
	w := newAdminWorker()
	w.Whitelist.Add("-qB4")

	req := newAdminRequest(url.Values{
		"action": {"remove_whitelist"},
		"prefix": {"-qB4"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatalf("remove_whitelist: %v", err)
	}
	// After removal, list is empty → allow-all (IsAllowed = true for anything)
	// Verify the prefix is gone from GetAll
	for _, p := range w.Whitelist.GetAll() {
		if p == "-qB4" {
			t.Error("prefix still in whitelist after remove")
		}
	}
}

// ── unknown action ────────────────────────────────────────────────────────────

func TestHandleUpdate_UnknownAction(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{"action": {"do_something_weird"}})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

// ── GetStats ──────────────────────────────────────────────────────────────────

func TestGetStats_Fields(t *testing.T) {
	w := newAdminWorker()
	w.Torrents.Set("h1", NewTorrent(1))
	w.Torrents.Set("h2", NewTorrent(2))
	w.Users.Set("pk1", NewUser(1, true, false))
	w.Stats.Seeders.Store(5)
	w.Stats.Leechers.Store(3)
	w.Stats.Announcements.Store(100)

	data, err := w.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}

	checkFloat := func(key string, want float64) {
		t.Helper()
		v, ok := m[key]
		if !ok {
			t.Errorf("missing field %q", key)
			return
		}
		if v.(float64) != want {
			t.Errorf("%q = %v, want %v", key, v, want)
		}
	}
	checkFloat("torrent_count", 2)
	checkFloat("user_count", 1)
	checkFloat("seeders", 5)
	checkFloat("leechers", 3)
	checkFloat("announcements", 100)

	if _, ok := m["uptime_seconds"]; !ok {
		t.Error("missing uptime_seconds")
	}
}

// ── GetTorrents ───────────────────────────────────────────────────────────────

func TestGetTorrents_List(t *testing.T) {
	w := newAdminWorker()
	for i := 1; i <= 5; i++ {
		tor := NewTorrent(TorrentID(i))
		tor.Completed = uint32(i * 10)
		w.Torrents.Set(string(rune('a'+i)), tor)
	}

	data, err := w.GetTorrents(3)
	if err != nil {
		t.Fatalf("GetTorrents: %v", err)
	}
	var list []map[string]interface{}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("GetTorrents(3) returned %d, want 3", len(list))
	}
	for _, entry := range list {
		if _, ok := entry["id"]; !ok {
			t.Error("torrent entry missing id")
		}
		if _, ok := entry["seeders"]; !ok {
			t.Error("torrent entry missing seeders")
		}
	}
}

func TestGetTorrents_Empty(t *testing.T) {
	w := newAdminWorker()
	data, err := w.GetTorrents(100)
	if err != nil {
		t.Fatal(err)
	}
	var list []interface{}
	json.Unmarshal(data, &list)
	if list != nil && len(list) != 0 {
		t.Errorf("expected empty list, got %d entries", len(list))
	}
}

// ── GetPeers ──────────────────────────────────────────────────────────────────

func TestGetPeers_NotFound(t *testing.T) {
	w := newAdminWorker()
	_, err := w.GetPeers("nonexistent", 10)
	if err == nil {
		t.Error("expected error for nonexistent torrent")
	}
}

func TestGetPeers_ListsPeers(t *testing.T) {
	w := newAdminWorker()
	tor := NewTorrent(1)
	p := &Peer{
		UserID:  5,
		IP:      net.ParseIP("1.2.3.4"),
		Port:    6881,
		Visible: true,
	}
	tor.Seeders.Set("k1", p)
	w.Torrents.Set("hash1", tor)

	data, err := w.GetPeers("hash1", 10)
	if err != nil {
		t.Fatalf("GetPeers: %v", err)
	}
	var list []map[string]interface{}
	json.Unmarshal(data, &list)
	if len(list) != 1 {
		t.Errorf("expected 1 peer, got %d", len(list))
	}
	if list[0]["seeder"] != true {
		t.Error("peer should be seeder")
	}
	if list[0]["user_id"].(float64) != 5 {
		t.Errorf("user_id = %v, want 5", list[0]["user_id"])
	}
}

// ── GetWhitelist ──────────────────────────────────────────────────────────────

func TestGetWhitelist(t *testing.T) {
	w := newAdminWorker()
	w.Whitelist.Add("-qB4")
	w.Whitelist.Add("-DE1")

	data, err := w.GetWhitelist()
	if err != nil {
		t.Fatalf("GetWhitelist: %v", err)
	}
	var list []string
	json.Unmarshal(data, &list)
	if len(list) != 2 {
		t.Errorf("expected 2 entries, got %d: %v", len(list), list)
	}
}

func TestGetWhitelist_Empty(t *testing.T) {
	w := newAdminWorker()
	data, err := w.GetWhitelist()
	if err != nil {
		t.Fatal(err)
	}
	var list []string
	json.Unmarshal(data, &list)
	// null JSON array is fine for empty whitelist (allow-all)
}

// ── set_priority_class ────────────────────────────────────────────────────────

func newAdminWorkerWithCommons() *Worker {
	w := newAdminWorker()
	w.Commons = newMockCommons()
	return w
}

func TestHandleUpdate_SetPriorityClass(t *testing.T) {
	w := newAdminWorkerWithCommons()
	req := newAdminRequest(url.Values{
		"action":         {"set_priority_class"},
		"id":             {"42"},
		"priority_class": {"1"},
	})
	data, err := w.HandleUpdate(req)
	if err != nil {
		t.Fatalf("set_priority_class: %v", err)
	}
	var resp map[string]string
	json.Unmarshal(data, &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want \"ok\"", resp["status"])
	}

	mc := w.Commons.(*MockCommons)
	if len(mc.PrioritySet) != 1 {
		t.Fatalf("PrioritySet len = %d, want 1", len(mc.PrioritySet))
	}
	if mc.PrioritySet[0].UserID != 42 {
		t.Errorf("UserID = %d, want 42", mc.PrioritySet[0].UserID)
	}
}

func TestHandleUpdate_SetPriorityClass_NoCommons(t *testing.T) {
	w := newAdminWorker() // Commons = nil
	req := newAdminRequest(url.Values{
		"action":         {"set_priority_class"},
		"id":             {"1"},
		"priority_class": {"2"},
	})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error when Commons is nil")
	}
}

func TestHandleUpdate_SetPriorityClass_InvalidClass(t *testing.T) {
	w := newAdminWorkerWithCommons()
	req := newAdminRequest(url.Values{
		"action":         {"set_priority_class"},
		"id":             {"1"},
		"priority_class": {"99"},
	})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error for invalid priority class 99")
	}
}

func TestHandleUpdate_SetPriorityClass_MissingID(t *testing.T) {
	w := newAdminWorkerWithCommons()
	req := newAdminRequest(url.Values{
		"action":         {"set_priority_class"},
		"priority_class": {"1"},
	})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error when id is missing")
	}
}

// ── set_budget ────────────────────────────────────────────────────────────────

func TestHandleUpdate_SetBudget(t *testing.T) {
	w := newAdminWorkerWithCommons()
	req := newAdminRequest(url.Values{
		"action":      {"set_budget"},
		"id":          {"7"},
		"max_credits": {"500"},
		"torrent_id":  {"3"},
	})
	data, err := w.HandleUpdate(req)
	if err != nil {
		t.Fatalf("set_budget: %v", err)
	}
	var resp map[string]string
	json.Unmarshal(data, &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want \"ok\"", resp["status"])
	}

	mc := w.Commons.(*MockCommons)
	if len(mc.BudgetSet) != 1 {
		t.Fatalf("BudgetSet len = %d, want 1", len(mc.BudgetSet))
	}
	b := mc.BudgetSet[0]
	if b.UserID != 7 {
		t.Errorf("UserID = %d, want 7", b.UserID)
	}
	if b.TorrentID != 3 {
		t.Errorf("TorrentID = %d, want 3", b.TorrentID)
	}
	if b.MaxCredits != 500 {
		t.Errorf("MaxCredits = %d, want 500", b.MaxCredits)
	}
}

func TestHandleUpdate_SetBudget_GlobalBudget(t *testing.T) {
	w := newAdminWorkerWithCommons()
	// No torrent_id → global budget (TorrentID=0)
	req := newAdminRequest(url.Values{
		"action":      {"set_budget"},
		"id":          {"9"},
		"max_credits": {"1000"},
	})
	if _, err := w.HandleUpdate(req); err != nil {
		t.Fatalf("set_budget global: %v", err)
	}

	mc := w.Commons.(*MockCommons)
	if mc.BudgetSet[0].TorrentID != 0 {
		t.Errorf("global budget TorrentID = %d, want 0", mc.BudgetSet[0].TorrentID)
	}
}

func TestHandleUpdate_SetBudget_NoCommons(t *testing.T) {
	w := newAdminWorker()
	req := newAdminRequest(url.Values{
		"action":      {"set_budget"},
		"id":          {"1"},
		"max_credits": {"100"},
	})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error when Commons is nil")
	}
}

func TestHandleUpdate_SetBudget_MissingID(t *testing.T) {
	w := newAdminWorkerWithCommons()
	req := newAdminRequest(url.Values{
		"action":      {"set_budget"},
		"max_credits": {"100"},
	})
	_, err := w.HandleUpdate(req)
	if err == nil {
		t.Error("expected error when id is missing")
	}
}
