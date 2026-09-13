package tracker

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hexInfoHash returns the 40-character form. A raw 20-byte hash cannot survive
// a JSON round trip, since JSON strings must be valid UTF-8, so the /update
// API works in hex.
func hexInfoHash(marker byte) string {
	return hex.EncodeToString([]byte(testInfoHash(marker)))
}

// postUpdate sends an update action and returns the decoded response.
func postUpdate(t *testing.T, h *testHarness, body string) (UpdateResponse, error) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/update", strings.NewReader(body))

	data, err := h.worker.HandleUpdate(req)

	var resp UpdateResponse
	if unmarshalErr := json.Unmarshal(data, &resp); unmarshalErr != nil {
		t.Fatalf("response is not valid JSON: %q: %v", data, unmarshalErr)
	}

	return resp, err
}

func TestHandleUpdateRejectsInvalidJSON(t *testing.T) {
	h := newTestHarness(t)

	resp, err := postUpdate(t, h, `{not json`)

	if err == nil {
		t.Error("expected an error for malformed JSON")
	}
	if resp.Success {
		t.Error("malformed JSON should not report success")
	}
	if !strings.Contains(resp.Error, "Invalid JSON") {
		t.Errorf("error = %q, want it to mention invalid JSON", resp.Error)
	}
}

func TestHandleUpdateRejectsUnknownAction(t *testing.T) {
	h := newTestHarness(t)

	resp, err := postUpdate(t, h, `{"action":"drop_database"}`)

	if err == nil {
		t.Error("expected an error for an unknown action")
	}
	if !strings.Contains(resp.Error, "Unknown action") {
		t.Errorf("error = %q, want it to name the unknown action", resp.Error)
	}
}

// Torrents

func TestUpdateAddTorrent(t *testing.T) {
	h := newTestHarness(t)
	infoHash := hexInfoHash(0xB1)

	body, _ := json.Marshal(map[string]any{
		"action":     "add_torrent",
		"torrent_id": 42,
		"info_hash":  infoHash,
	})

	resp, err := postUpdate(t, h, string(body))
	if err != nil {
		t.Fatalf("add_torrent failed: %v (%s)", err, resp.Error)
	}
	if !resp.Success {
		t.Errorf("Success = false: %s", resp.Error)
	}

	torrent, ok := h.worker.Torrents.Get(infoHash)
	if !ok {
		t.Fatal("torrent was not added to the in-memory list")
	}
	if torrent.ID != 42 {
		t.Errorf("torrent ID = %d, want 42", torrent.ID)
	}
	if torrent.Seeders == nil || torrent.Leechers == nil {
		t.Error("torrent was created without peer lists, so announces will panic")
	}
}

func TestUpdateAddTorrentValidation(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "missing torrent_id",
			body: map[string]any{"action": "add_torrent", "info_hash": hexInfoHash(0xB2)},
			want: "torrent_id",
		},
		{
			name: "negative torrent_id",
			body: map[string]any{"action": "add_torrent", "torrent_id": -1, "info_hash": hexInfoHash(0xB2)},
			want: "torrent_id",
		},
		{
			name: "missing info_hash",
			body: map[string]any{"action": "add_torrent", "torrent_id": 1},
			want: "info_hash",
		},
		{
			name: "wrong length info_hash",
			body: map[string]any{"action": "add_torrent", "torrent_id": 1, "info_hash": "short"},
			want: "20 or 40",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHarness(t)
			body, _ := json.Marshal(tt.body)

			resp, err := postUpdate(t, h, string(body))
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(resp.Error, tt.want) {
				t.Errorf("error = %q, want it to mention %q", resp.Error, tt.want)
			}
		})
	}
}

func TestUpdateAddTorrentRejectsDuplicate(t *testing.T) {
	h := newTestHarness(t)
	h.worker.Torrents.Set(hexInfoHash(0xB3), NewTorrent(TorrentID(98)))

	body, _ := json.Marshal(map[string]any{
		"action":     "add_torrent",
		"torrent_id": 99,
		"info_hash":  hexInfoHash(0xB3),
	})

	resp, err := postUpdate(t, h, string(body))
	if err == nil {
		t.Fatal("expected a duplicate torrent to be rejected")
	}
	if !strings.Contains(resp.Error, "already exists") {
		t.Errorf("error = %q", resp.Error)
	}
}

func TestUpdateDeleteTorrent(t *testing.T) {
	h := newTestHarness(t)
	infoHash := hexInfoHash(0xD1)
	h.worker.Torrents.Set(infoHash, NewTorrent(TorrentID(11)))

	body, _ := json.Marshal(map[string]any{
		"action":    "delete_torrent",
		"info_hash": infoHash,
		"reason":    "dupe",
	})

	if _, err := postUpdate(t, h, string(body)); err != nil {
		t.Fatalf("delete_torrent failed: %v", err)
	}

	if _, ok := h.worker.Torrents.Get(infoHash); ok {
		t.Error("torrent is still registered after delete_torrent")
	}
}

func TestUpdateChangeFreeleech(t *testing.T) {
	h := newTestHarness(t)
	infoHash := hexInfoHash(0xD2)
	h.worker.Torrents.Set(infoHash, NewTorrent(TorrentID(12)))

	body, _ := json.Marshal(map[string]any{
		"action":    "change_freeleech",
		"info_hash": infoHash,
		"free_type": int(FreeFree),
	})

	if _, err := postUpdate(t, h, string(body)); err != nil {
		t.Fatalf("change_freeleech failed: %v", err)
	}

	torrent, _ := h.worker.Torrents.Get(infoHash)
	if torrent.FreeType != FreeFree {
		t.Errorf("FreeType = %d, want %d", torrent.FreeType, FreeFree)
	}
}

// Users

func TestUpdateAddUser(t *testing.T) {
	h := newTestHarness(t)
	passkey := strings.Repeat("a", 32)

	body, _ := json.Marshal(map[string]any{
		"action":  "add_user",
		"user_id": 7,
		"passkey": passkey,
	})

	if _, err := postUpdate(t, h, string(body)); err != nil {
		t.Fatalf("add_user failed: %v", err)
	}

	user, ok := h.worker.Users.Get(passkey)
	if !ok {
		t.Fatal("user was not added")
	}
	if user.ID != 7 {
		t.Errorf("user ID = %d, want 7", user.ID)
	}
	if !user.CanLeech.Load() {
		t.Error("CanLeech defaulted to false, which blocks every leech announce")
	}
}

func TestUpdateAddUserHonoursPrivileges(t *testing.T) {
	h := newTestHarness(t)
	passkey := strings.Repeat("b", 32)

	body, _ := json.Marshal(map[string]any{
		"action":     "add_user",
		"user_id":    8,
		"passkey":    passkey,
		"can_leech":  false,
		"protect_ip": true,
	})

	if _, err := postUpdate(t, h, string(body)); err != nil {
		t.Fatalf("add_user failed: %v", err)
	}

	user, _ := h.worker.Users.Get(passkey)
	if user.CanLeech.Load() {
		t.Error("can_leech=false was not applied")
	}
	if !user.ProtectIP.Load() {
		t.Error("protect_ip=true was not applied")
	}
}

func TestUpdateAddUserValidation(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "missing user_id",
			body: map[string]any{"action": "add_user", "passkey": strings.Repeat("c", 32)},
			want: "user_id",
		},
		{
			name: "missing passkey",
			body: map[string]any{"action": "add_user", "user_id": 1},
			want: "passkey",
		},
		{
			name: "short passkey",
			body: map[string]any{"action": "add_user", "user_id": 1, "passkey": "tooshort"},
			want: "32 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHarness(t)
			body, _ := json.Marshal(tt.body)

			resp, err := postUpdate(t, h, string(body))
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(resp.Error, tt.want) {
				t.Errorf("error = %q, want it to mention %q", resp.Error, tt.want)
			}
		})
	}
}

func TestUpdateChangePasskey(t *testing.T) {
	h := newTestHarness(t)
	_, oldPasskey := h.addUser(t, 5, true)
	newPasskey := strings.Repeat("d", 32)

	body, _ := json.Marshal(map[string]any{
		"action":      "change_passkey",
		"passkey":     oldPasskey,
		"new_passkey": newPasskey,
	})

	if _, err := postUpdate(t, h, string(body)); err != nil {
		t.Fatalf("change_passkey failed: %v", err)
	}

	if _, ok := h.worker.Users.Get(oldPasskey); ok {
		t.Error("the old passkey still authenticates after a rotation")
	}

	user, ok := h.worker.Users.Get(newPasskey)
	if !ok {
		t.Fatal("the new passkey does not authenticate")
	}
	if user.ID != 5 {
		t.Errorf("user ID = %d, want 5 — rotation must preserve identity", user.ID)
	}
}

func TestUpdateDeleteUser(t *testing.T) {
	h := newTestHarness(t)
	_, passkey := h.addUser(t, 6, true)

	body, _ := json.Marshal(map[string]any{
		"action":  "delete_user",
		"passkey": passkey,
	})

	if _, err := postUpdate(t, h, string(body)); err != nil {
		t.Fatalf("delete_user failed: %v", err)
	}

	user, ok := h.worker.Users.Get(passkey)
	if !ok {
		t.Fatal("the user entry was removed; peer history is meant to be preserved")
	}
	if !user.Deleted.Load() {
		t.Error("user was not marked deleted")
	}

	// The flag has to actually block announces, or a ban does nothing.
	req := announceParams(h.infoHash, testPeerID("peer0006"), 6881, 1<<30, "started")
	if _, err := h.worker.Announce(req, user, req.IP, "qB"); err == nil {
		t.Error("a deleted user was still able to announce")
	}
}

// Whitelist

func TestUpdateWhitelistAddAndRemove(t *testing.T) {
	h := newTestHarness(t)

	add, _ := json.Marshal(map[string]any{
		"action":         "add_whitelist",
		"peer_id_prefix": "-qB43",
	})
	if _, err := postUpdate(t, h, string(add)); err != nil {
		t.Fatalf("add_whitelist failed: %v", err)
	}

	if !h.worker.Whitelist.IsAllowed(testPeerID("peer0001")) {
		t.Error("a whitelisted client prefix was rejected")
	}
	if h.worker.Whitelist.IsAllowed([]byte("-XX0000-000000000000")) {
		t.Error("a non-whitelisted client was allowed while a whitelist is active")
	}

	remove, _ := json.Marshal(map[string]any{
		"action":         "remove_whitelist",
		"peer_id_prefix": "-qB43",
	})
	if _, err := postUpdate(t, h, string(remove)); err != nil {
		t.Fatalf("remove_whitelist failed: %v", err)
	}

	// An empty whitelist allows everything again.
	if !h.worker.Whitelist.IsAllowed([]byte("-XX0000-000000000000")) {
		t.Error("removing the last prefix should return the tracker to allow-all")
	}
}

// Tokens

func TestUpdateTokenAddAndRemove(t *testing.T) {
	h := newTestHarness(t)

	infoHash := hexInfoHash(0xD3)
	h.worker.Torrents.Set(infoHash, NewTorrent(TorrentID(13)))

	add, _ := json.Marshal(map[string]any{
		"action":    "add_token",
		"info_hash": infoHash,
		"user_id":   3,
	})
	if _, err := postUpdate(t, h, string(add)); err != nil {
		t.Fatalf("add_token failed: %v", err)
	}

	torrent, _ := h.worker.Torrents.Get(infoHash)
	if _, ok := torrent.TokenedUsers[3]; !ok {
		t.Fatal("token was not recorded against the torrent")
	}

	remove, _ := json.Marshal(map[string]any{
		"action":    "remove_token",
		"info_hash": infoHash,
		"user_id":   3,
	})
	if _, err := postUpdate(t, h, string(remove)); err != nil {
		t.Fatalf("remove_token failed: %v", err)
	}

	if _, ok := torrent.TokenedUsers[3]; ok {
		t.Error("token is still present after remove_token")
	}
}

// End-to-end: a torrent and user pushed over /update must serve announces.

func TestUpdateProvisionedTorrentAcceptsAnnounces(t *testing.T) {
	h := newTestHarness(t)

	infoHash := hexInfoHash(0xC7)
	passkey := strings.Repeat("e", 32)

	addTorrent, _ := json.Marshal(map[string]any{
		"action":     "add_torrent",
		"torrent_id": 77,
		"info_hash":  infoHash,
	})
	if _, err := postUpdate(t, h, string(addTorrent)); err != nil {
		t.Fatalf("add_torrent failed: %v", err)
	}

	addUser, _ := json.Marshal(map[string]any{
		"action":  "add_user",
		"user_id": 77,
		"passkey": passkey,
	})
	if _, err := postUpdate(t, h, string(addUser)); err != nil {
		t.Fatalf("add_user failed: %v", err)
	}

	user, ok := h.worker.Users.Get(passkey)
	if !ok {
		t.Fatal("provisioned user is missing")
	}

	req := announceParams(infoHash, testPeerID("peer0077"), 6881, 1<<30, "started")
	resp, err := h.worker.Announce(req, user, req.IP, "qB")
	if err != nil {
		t.Fatalf("announce against a provisioned torrent failed: %v", err)
	}
	if resp.Incomplete != 1 {
		t.Errorf("incomplete = %d, want 1", resp.Incomplete)
	}
}
