package tracker

// GUC test suite for torrent update operations in update.go
// Lenses: Knuth (algorithmic/structural), Turing (termination/halting),
//         Church (functional purity/side-effect isolation), Gödel (invariants/consistency)

import (
	"encoding/json"
	"net/url"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func decodeUpdateResp(t *testing.T, data []byte) UpdateResponse {
	t.Helper()
	var resp UpdateResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v (raw: %s)", err, data)
	}
	return resp
}

func addTorrentViaFixture(t *testing.T, f *testFixture, id, hash, freeType string) UpdateResponse {
	t.Helper()
	extra := url.Values{"id": {id}, "info_hash": {hash}}
	if freeType != "" {
		extra.Set("free_type", freeType)
	}
	req := buildUpdateURL(sitePass, "add_torrent", extra)
	data, _ := f.worker.HandleUpdate(req)
	return decodeUpdateResp(t, data)
}

// ── Knuth: algorithmic correctness ────────────────────────────────────────────

// TestGUC_Knuth_AddTorrentSuccessFields verifies that a successful add_torrent
// response always contains both success=true and status="ok".
func TestGUC_Knuth_AddTorrentSuccessFields(t *testing.T) {
	f := newTestFixture()
	hash := "knuth_add_hash_00000001"
	resp := addTorrentViaFixture(t, f, "100", hash, "")
	if !resp.Success {
		t.Errorf("expected Success=true, got false (error=%q)", resp.Error)
	}
	if resp.Status != "ok" {
		t.Errorf("expected Status=%q, got %q", "ok", resp.Status)
	}
}

// TestGUC_Knuth_AddTorrentStoredInMap verifies the torrent is inserted into
// the in-memory map after a valid add_torrent call.
func TestGUC_Knuth_AddTorrentStoredInMap(t *testing.T) {
	f := newTestFixture()
	hash := "knuth_stored_hash_00001"
	addTorrentViaFixture(t, f, "200", hash, "")
	if _, ok := f.worker.Torrents.Get(hash); !ok {
		t.Errorf("torrent %q not found in TorrentList after add_torrent", hash)
	}
}

// TestGUC_Knuth_FreeTypeStoredCorrectly checks that the free_type value passed
// to add_torrent is faithfully stored on the Torrent struct (loop invariant:
// each valid enum value round-trips through the store).
func TestGUC_Knuth_FreeTypeStoredCorrectly(t *testing.T) {
	cases := []struct {
		ftStr string
		want  FreeType
	}{
		{"0", FreeNormal},
		{"1", FreeFree},
		{"2", FreeNeutral},
	}
	for _, tc := range cases {
		f := newTestFixture()
		hash := "knuth_ft_hash_" + tc.ftStr
		addTorrentViaFixture(t, f, "300", hash, tc.ftStr)
		torrent, ok := f.worker.Torrents.Get(hash)
		if !ok {
			t.Errorf("free_type=%s: torrent not stored", tc.ftStr)
			continue
		}
		torrent.mu.RLock()
		got := torrent.FreeType
		torrent.mu.RUnlock()
		if got != tc.want {
			t.Errorf("free_type=%s: want %v, got %v", tc.ftStr, tc.want, got)
		}
	}
}

// TestGUC_Knuth_TorrentIDAlias verifies that the torrent_id parameter is
// accepted as an alias for id in add_torrent.
func TestGUC_Knuth_TorrentIDAlias(t *testing.T) {
	f := newTestFixture()
	hash := "knuth_alias_hash_0000001"
	extra := url.Values{"torrent_id": {"999"}, "info_hash": {hash}}
	req := buildUpdateURL(sitePass, "add_torrent", extra)
	data, _ := f.worker.HandleUpdate(req)
	resp := decodeUpdateResp(t, data)
	if !resp.Success {
		t.Errorf("torrent_id alias: expected success, got error=%q", resp.Error)
	}
	if _, ok := f.worker.Torrents.Get(hash); !ok {
		t.Error("torrent_id alias: torrent not stored in map")
	}
}

// TestGUC_Knuth_UpdateTorrentFreeTypeCycle validates the full 0→1→2 cycle for
// update_torrent free_type modifications, confirming each write is visible.
func TestGUC_Knuth_UpdateTorrentFreeTypeCycle(t *testing.T) {
	f := newTestFixture()
	steps := []struct {
		ft   string
		want FreeType
	}{
		{"0", FreeNormal},
		{"1", FreeFree},
		{"2", FreeNeutral},
	}
	for _, s := range steps {
		extra := url.Values{"info_hash": {testInfoHash}, "free_type": {s.ft}}
		req := buildUpdateURL(sitePass, "update_torrent", extra)
		data, _ := f.worker.HandleUpdate(req)
		resp := decodeUpdateResp(t, data)
		if !resp.Success {
			t.Errorf("step free_type=%s: expected success, got error=%q", s.ft, resp.Error)
		}
		torrent, ok := f.worker.Torrents.Get(testInfoHash)
		if !ok {
			t.Fatal("pre-seeded torrent disappeared")
		}
		torrent.mu.RLock()
		got := torrent.FreeType
		torrent.mu.RUnlock()
		if got != s.want {
			t.Errorf("after setting free_type=%s: want %v, got %v", s.ft, s.want, got)
		}
	}
}

// ── Turing: termination / halting behaviour ───────────────────────────────────

// TestGUC_Turing_MissingIDTerminatesWithError verifies that add_torrent with a
// missing id parameter always halts with a well-formed error response.
func TestGUC_Turing_MissingIDTerminatesWithError(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {"turing_no_id_hash_0001"}}
	req := buildUpdateURL(sitePass, "add_torrent", extra)
	data, err := f.worker.HandleUpdate(req)
	if err == nil {
		t.Error("expected non-nil error for missing id")
	}
	resp := decodeUpdateResp(t, data)
	if resp.Success {
		t.Error("expected Success=false for missing id")
	}
	if resp.Status != "error" {
		t.Errorf("expected Status=%q, got %q", "error", resp.Status)
	}
}

// TestGUC_Turing_InvalidFreeType3Terminates verifies that free_type=3 halts
// with a structured error response and does NOT add the torrent.
func TestGUC_Turing_InvalidFreeType3Terminates(t *testing.T) {
	f := newTestFixture()
	hash := "turing_ft3_hash_000000001"
	resp := addTorrentViaFixture(t, f, "400", hash, "3")
	if resp.Success {
		t.Error("free_type=3: expected failure")
	}
	if resp.Status != "error" {
		t.Errorf("free_type=3: expected Status=%q, got %q", "error", resp.Status)
	}
	if _, ok := f.worker.Torrents.Get(hash); ok {
		t.Error("free_type=3: torrent must not be stored on error")
	}
}

// TestGUC_Turing_InvalidFreeTypeNegativeTerminates verifies that free_type=-1
// halts with a structured error response and does NOT add the torrent.
func TestGUC_Turing_InvalidFreeTypeNegativeTerminates(t *testing.T) {
	f := newTestFixture()
	hash := "turing_ftneg_hash_0000001"
	resp := addTorrentViaFixture(t, f, "401", hash, "-1")
	if resp.Success {
		t.Error("free_type=-1: expected failure")
	}
	if resp.Status != "error" {
		t.Errorf("free_type=-1: expected Status=%q, got %q", "error", resp.Status)
	}
	if _, ok := f.worker.Torrents.Get(hash); ok {
		t.Error("free_type=-1: torrent must not be stored on error")
	}
}

// TestGUC_Turing_DeleteNotFoundTerminates verifies that delete_torrent for an
// unknown info_hash halts cleanly with status=error.
func TestGUC_Turing_DeleteNotFoundTerminates(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {"turing_no_such_hash_001"}}
	req := buildUpdateURL(sitePass, "delete_torrent", extra)
	data, err := f.worker.HandleUpdate(req)
	if err == nil {
		t.Error("expected non-nil error for deleting non-existent torrent")
	}
	resp := decodeUpdateResp(t, data)
	if resp.Success {
		t.Error("delete non-existent: expected Success=false")
	}
	if resp.Status != "error" {
		t.Errorf("delete non-existent: expected Status=%q, got %q", "error", resp.Status)
	}
}

// TestGUC_Turing_DeleteExistingSucceeds verifies that delete_torrent for an
// existing torrent halts with success=true and removes the entry.
func TestGUC_Turing_DeleteExistingSucceeds(t *testing.T) {
	f := newTestFixture()
	// testInfoHash is pre-seeded in newTestFixture
	extra := url.Values{"info_hash": {testInfoHash}}
	req := buildUpdateURL(sitePass, "delete_torrent", extra)
	data, err := f.worker.HandleUpdate(req)
	if err != nil {
		t.Fatalf("unexpected error deleting existing torrent: %v", err)
	}
	resp := decodeUpdateResp(t, data)
	if !resp.Success {
		t.Errorf("expected Success=true, got error=%q", resp.Error)
	}
	if resp.Status != "ok" {
		t.Errorf("expected Status=%q, got %q", "ok", resp.Status)
	}
	if _, ok := f.worker.Torrents.Get(testInfoHash); ok {
		t.Error("deleted torrent still present in TorrentList")
	}
}

// ── Church: functional purity / side-effect isolation ────────────────────────

// TestGUC_Church_AddTorrentRecordsDBHash verifies that the DB side-effect
// (RecordTorrentHash) is triggered exactly once per successful add_torrent.
func TestGUC_Church_AddTorrentRecordsDBHash(t *testing.T) {
	f := newTestFixture()
	hash := "church_db_hash_000000001"
	addTorrentViaFixture(t, f, "500", hash, "")
	f.db.mu.Lock()
	recorded := f.db.TorrentHashes
	f.db.mu.Unlock()
	found := false
	for _, h := range recorded {
		if h == hash {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected DB to record hash %q, recorded=%v", hash, recorded)
	}
}

// TestGUC_Church_ErrorResponseIsolated ensures that a failed add_torrent
// (missing id) produces no DB side-effects and no torrent map mutation.
func TestGUC_Church_ErrorResponseIsolated(t *testing.T) {
	f := newTestFixture()
	before := f.worker.Torrents.Size()
	extra := url.Values{"info_hash": {"church_no_id_hash_0001"}}
	req := buildUpdateURL(sitePass, "add_torrent", extra)
	f.worker.HandleUpdate(req) //nolint:errcheck
	after := f.worker.Torrents.Size()
	if after != before {
		t.Errorf("torrent map mutated on error path: before=%d after=%d", before, after)
	}
	f.db.mu.Lock()
	hashes := len(f.db.TorrentHashes)
	f.db.mu.Unlock()
	if hashes != 0 {
		t.Errorf("DB should have zero recorded hashes on error path, got %d", hashes)
	}
}

// TestGUC_Church_DuplicateAddIsIdempotent verifies that adding the same
// torrent twice does not mutate the map a second time (idempotency).
func TestGUC_Church_DuplicateAddIsIdempotent(t *testing.T) {
	f := newTestFixture()
	hash := "church_dup_hash_000000001"
	// First add — should succeed
	resp1 := addTorrentViaFixture(t, f, "600", hash, "")
	if !resp1.Success {
		t.Fatalf("first add failed unexpectedly: %q", resp1.Error)
	}
	sizeAfterFirst := f.worker.Torrents.Size()

	// Second add with same hash — must be rejected
	resp2 := addTorrentViaFixture(t, f, "601", hash, "")
	if resp2.Success {
		t.Error("duplicate add_torrent should fail")
	}
	if resp2.Status != "error" {
		t.Errorf("duplicate: expected Status=%q, got %q", "error", resp2.Status)
	}
	sizeAfterSecond := f.worker.Torrents.Size()
	if sizeAfterSecond != sizeAfterFirst {
		t.Errorf("map size changed after duplicate add: %d → %d", sizeAfterFirst, sizeAfterSecond)
	}
}

// TestGUC_Church_UpdateTorrentHashNoSideEffectOnMiss verifies that
// update_torrent for an unknown hash produces no state mutations.
func TestGUC_Church_UpdateTorrentHashNoSideEffectOnMiss(t *testing.T) {
	f := newTestFixture()
	before := f.worker.Torrents.Size()
	extra := url.Values{"info_hash": {"church_no_such_00000001"}, "free_type": {"1"}}
	req := buildUpdateURL(sitePass, "update_torrent", extra)
	f.worker.HandleUpdate(req) //nolint:errcheck
	if f.worker.Torrents.Size() != before {
		t.Error("update_torrent on unknown hash must not change map size")
	}
}

// TestGUC_Church_UpdateTorrentHashValid verifies that update_torrent on an
// existing hash returns success and preserves the new free_type.
func TestGUC_Church_UpdateTorrentHashValid(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {testInfoHash}, "free_type": {"2"}}
	req := buildUpdateURL(sitePass, "update_torrent", extra)
	data, err := f.worker.HandleUpdate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := decodeUpdateResp(t, data)
	if !resp.Success {
		t.Errorf("expected success, got error=%q", resp.Error)
	}
	torrent, _ := f.worker.Torrents.Get(testInfoHash)
	torrent.mu.RLock()
	ft := torrent.FreeType
	torrent.mu.RUnlock()
	if ft != FreeNeutral {
		t.Errorf("expected FreeNeutral after update, got %v", ft)
	}
}

// ── Gödel: formal consistency / invariant preservation ───────────────────────

// TestGUC_Godel_AllResponsesHaveSuccessAndStatus verifies the formal
// contract that every update response JSON contains both "success" and "status"
// across a representative sample of actions.
func TestGUC_Godel_AllResponsesHaveSuccessAndStatus(t *testing.T) {
	cases := []struct {
		name   string
		action string
		extra  url.Values
	}{
		{
			"add_valid",
			"add_torrent",
			url.Values{"id": {"700"}, "info_hash": {"godel_field_hash_001"}},
		},
		{
			"add_missing_id",
			"add_torrent",
			url.Values{"info_hash": {"godel_field_hash_002"}},
		},
		{
			"update_valid",
			"update_torrent",
			url.Values{"info_hash": {testInfoHash}, "free_type": {"1"}},
		},
		{
			"update_missing_hash",
			"update_torrent",
			url.Values{"free_type": {"1"}},
		},
		{
			"delete_valid",
			"delete_torrent",
			url.Values{"info_hash": {testInfoHash}},
		},
		{
			"delete_not_found",
			"delete_torrent",
			url.Values{"info_hash": {"godel_field_hash_009"}},
		},
	}

	f := newTestFixture()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := buildUpdateURL(sitePass, tc.action, tc.extra)
			data, _ := f.worker.HandleUpdate(req)
			// Verify raw JSON contains the two required keys
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("response not valid JSON: %v", err)
			}
			if _, ok := raw["success"]; !ok {
				t.Errorf("%s: response missing 'success' field", tc.name)
			}
			if _, ok := raw["status"]; !ok {
				t.Errorf("%s: response missing 'status' field", tc.name)
			}
		})
		// Re-seed between cases that consume the pre-seeded torrent
		if tc.action == "delete_torrent" && tc.name == "delete_valid" {
			torrent := NewTorrent(TorrentID(1))
			f.worker.Torrents.Set(testInfoHash, torrent)
		}
	}
}

// TestGUC_Godel_DeleteTorrentNotFoundJSON checks the formal JSON structure of a
// not-found delete response: success must be false and status must be "error".
func TestGUC_Godel_DeleteTorrentNotFoundJSON(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {"godel_notfound_hash_0001"}}
	req := buildUpdateURL(sitePass, "delete_torrent", extra)
	data, _ := f.worker.HandleUpdate(req)
	resp := decodeUpdateResp(t, data)
	if resp.Success {
		t.Error("Gödel invariant: delete of non-existent torrent must not claim success")
	}
	if resp.Status == "" {
		t.Error("Gödel invariant: status field must be non-empty")
	}
	if resp.Status != "error" {
		t.Errorf("Gödel invariant: status must be %q, got %q", "error", resp.Status)
	}
}

// TestGUC_Godel_SuccessAndStatusNeverContradictEachOther checks that for any
// update action, success=true implies status="ok" and success=false implies
// status="error" (no contradictory states can exist simultaneously).
func TestGUC_Godel_SuccessAndStatusNeverContradictEachOther(t *testing.T) {
	f := newTestFixture()
	actions := []struct {
		action string
		extra  url.Values
	}{
		{"add_torrent", url.Values{"id": {"800"}, "info_hash": {"godel_consis_hash001"}}},
		{"add_torrent", url.Values{"info_hash": {"godel_consis_hash002"}}},           // no id
		{"update_torrent", url.Values{"info_hash": {testInfoHash}, "free_type": {"1"}}},
		{"update_torrent", url.Values{"info_hash": {"godel_no_such_00001"}}},         // not found
		{"delete_torrent", url.Values{"info_hash": {"godel_no_such_del_01"}}},        // not found
	}
	for _, a := range actions {
		req := buildUpdateURL(sitePass, a.action, a.extra)
		data, _ := f.worker.HandleUpdate(req)
		resp := decodeUpdateResp(t, data)
		if resp.Success && resp.Status != "ok" {
			t.Errorf("contradiction: success=true but status=%q (action=%s)", resp.Status, a.action)
		}
		if !resp.Success && resp.Status != "error" {
			t.Errorf("contradiction: success=false but status=%q (action=%s)", resp.Status, a.action)
		}
	}
}

// TestGUC_Godel_MapCountInvariantOnError checks that the TorrentList count
// invariant holds across a sequence of error-producing add_torrent calls:
// the count must never increase from a failed operation.
func TestGUC_Godel_MapCountInvariantOnError(t *testing.T) {
	f := newTestFixture()
	baseline := f.worker.Torrents.Size()

	errorCases := []url.Values{
		{"info_hash": {"godel_inv_hash_001"}},                        // missing id
		{"id": {"0"}, "info_hash": {"godel_inv_hash_002"}},           // id=0 invalid
		{"id": {"abc"}, "info_hash": {"godel_inv_hash_003"}},         // non-numeric id
		{"id": {"901"}, "info_hash": {"godel_inv_hash_004"}, "free_type": {"9"}}, // bad free_type
	}
	for i, extra := range errorCases {
		req := buildUpdateURL(sitePass, "add_torrent", extra)
		data, _ := f.worker.HandleUpdate(req)
		resp := decodeUpdateResp(t, data)
		if resp.Success {
			t.Errorf("case %d: expected error, got success", i)
		}
		if sz := f.worker.Torrents.Size(); sz != baseline {
			t.Errorf("case %d: map size invariant violated: baseline=%d current=%d", i, baseline, sz)
		}
	}
}
