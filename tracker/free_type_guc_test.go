package tracker

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
)

// ── shared helpers ────────────────────────────────────────────────────────────

func gucParseUpdateResp(t *testing.T, data []byte) UpdateResponse {
	t.Helper()
	var resp UpdateResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("gucParseUpdateResp: failed to unmarshal %q: %v", data, err)
	}
	return resp
}

// gucAddTorrent issues add_torrent with the given id, info_hash and free_type.
// Pass freeType="" to omit the parameter entirely.
func gucAddTorrent(w *Worker, id int, infoHash, freeType string) (UpdateResponse, error) {
	extra := url.Values{
		"id":        {fmt.Sprintf("%d", id)},
		"info_hash": {infoHash},
	}
	if freeType != "" {
		extra.Set("free_type", freeType)
	}
	req := buildUpdateURL(sitePass, "add_torrent", extra)
	data, err := w.HandleUpdate(req)
	var resp UpdateResponse
	json.Unmarshal(data, &resp) //nolint:errcheck — data always valid from handleUpdate
	return resp, err
}

// gucUpdateTorrent issues update_torrent with the given info_hash and free_type.
func gucUpdateTorrent(w *Worker, infoHash, freeType string) (UpdateResponse, error) {
	extra := url.Values{
		"info_hash": {infoHash},
		"free_type": {freeType},
	}
	req := buildUpdateURL(sitePass, "update_torrent", extra)
	data, err := w.HandleUpdate(req)
	var resp UpdateResponse
	json.Unmarshal(data, &resp) //nolint:errcheck
	return resp, err
}

// gucReadFreeType reads FreeType from a torrent under its read lock.
func gucReadFreeType(t *testing.T, w *Worker, infoHash string) FreeType {
	t.Helper()
	torrent, ok := w.Torrents.Get(infoHash)
	if !ok {
		t.Fatalf("gucReadFreeType: torrent %q not found", infoHash)
	}
	torrent.mu.RLock()
	ft := torrent.FreeType
	torrent.mu.RUnlock()
	return ft
}

// ── Knuth lens: algorithmic correctness, range invariants ─────────────────────
// Five tests covering valid-range acceptance and constant identity.

// TestGUC_FreeType_Knuth_ValidValuesAcceptedInAddTorrent verifies that each
// value in the valid set {0, 1, 2} is accepted by add_torrent and stored
// correctly, satisfying the loop invariant 0 ≤ freeType ≤ 2.
func TestGUC_FreeType_Knuth_ValidValuesAcceptedInAddTorrent(t *testing.T) {
	tests := []struct {
		ft   string
		want FreeType
		hash string
		id   int
	}{
		{"0", FreeNormal, "addft0_hash_________", 101},
		{"1", FreeFree, "addft1_hash_________", 102},
		{"2", FreeNeutral, "addft2_hash_________", 103},
	}
	for _, tc := range tests {
		tc := tc
		t.Run("free_type="+tc.ft, func(t *testing.T) {
			f := newTestFixture()
			resp, err := gucAddTorrent(f.worker, tc.id, tc.hash, tc.ft)
			if err != nil {
				t.Errorf("expected success; got err=%v resp=%+v", err, resp)
			}
			if !resp.Success {
				t.Errorf("Success=false for valid free_type=%s: %+v", tc.ft, resp)
			}
			if _, ok := f.worker.Torrents.Get(tc.hash); !ok {
				t.Fatalf("torrent not found in map after add")
			}
			got := gucReadFreeType(t, f.worker, tc.hash)
			if got != tc.want {
				t.Errorf("FreeType: got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestGUC_FreeType_Knuth_ValidValuesAcceptedInUpdateTorrent verifies the same
// range invariant for update_torrent: values {0, 1, 2} must all be accepted.
func TestGUC_FreeType_Knuth_ValidValuesAcceptedInUpdateTorrent(t *testing.T) {
	tests := []struct {
		ft   string
		want FreeType
	}{
		{"0", FreeNormal},
		{"1", FreeFree},
		{"2", FreeNeutral},
	}
	for _, tc := range tests {
		tc := tc
		t.Run("free_type="+tc.ft, func(t *testing.T) {
			f := newTestFixture()
			resp, err := gucUpdateTorrent(f.worker, testInfoHash, tc.ft)
			if err != nil {
				t.Errorf("expected success; got err=%v resp=%+v", err, resp)
			}
			if !resp.Success {
				t.Errorf("Success=false for valid free_type=%s: %+v", tc.ft, resp)
			}
			got := gucReadFreeType(t, f.worker, testInfoHash)
			if got != tc.want {
				t.Errorf("FreeType: got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestGUC_FreeType_Knuth_InvalidValuesRejectedInAddTorrent verifies the table of
// out-of-range values is rejected by add_torrent (boundary condition correctness).
func TestGUC_FreeType_Knuth_InvalidValuesRejectedInAddTorrent(t *testing.T) {
	tests := []struct {
		ft   string
		hash string
	}{
		{"3", "invaddft3___________"},
		{"-1", "invaddftm1__________"},
		{"255", "invaddft255_________"},
		{"4", "invaddft4___________"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run("free_type="+tc.ft, func(t *testing.T) {
			f := newTestFixture()
			before := f.worker.Torrents.Size()
			resp, err := gucAddTorrent(f.worker, 200, tc.hash, tc.ft)
			if err == nil {
				t.Errorf("expected error for free_type=%s, got nil", tc.ft)
			}
			if resp.Success {
				t.Errorf("Success must be false for invalid free_type=%s", tc.ft)
			}
			if f.worker.Torrents.Size() != before {
				t.Errorf("torrent map grew after rejected add (free_type=%s)", tc.ft)
			}
			if _, ok := f.worker.Torrents.Get(tc.hash); ok {
				t.Errorf("torrent must not be created for invalid free_type=%s", tc.ft)
			}
		})
	}
}

// TestGUC_FreeType_Knuth_InvalidValuesRejectedInUpdateTorrent verifies out-of-range
// values are rejected by update_torrent, maintaining data structure invariant.
func TestGUC_FreeType_Knuth_InvalidValuesRejectedInUpdateTorrent(t *testing.T) {
	tests := []string{"3", "-1", "255", "4"}
	for _, ft := range tests {
		ft := ft
		t.Run("free_type="+ft, func(t *testing.T) {
			f := newTestFixture()
			// Set a known baseline.
			torrent, _ := f.worker.Torrents.Get(testInfoHash)
			torrent.mu.Lock()
			torrent.FreeType = FreeNeutral
			torrent.mu.Unlock()

			resp, err := gucUpdateTorrent(f.worker, testInfoHash, ft)
			if err == nil {
				t.Errorf("expected error for free_type=%s, got nil", ft)
			}
			if resp.Success {
				t.Errorf("Success must be false for invalid free_type=%s", ft)
			}
			got := gucReadFreeType(t, f.worker, testInfoHash)
			if got != FreeNeutral {
				t.Errorf("FreeType must be unchanged after invalid update; got %d, want FreeNeutral(2)", got)
			}
		})
	}
}

// TestGUC_FreeType_Knuth_ConstantValues verifies the exact numeric values of the
// FreeType constants — a data structure invariant that other code depends upon.
func TestGUC_FreeType_Knuth_ConstantValues(t *testing.T) {
	if FreeNormal != 0 {
		t.Errorf("FreeNormal must be 0, got %d", FreeNormal)
	}
	if FreeFree != 1 {
		t.Errorf("FreeFree must be 1, got %d", FreeFree)
	}
	if FreeNeutral != 2 {
		t.Errorf("FreeNeutral must be 2, got %d", FreeNeutral)
	}
	if FreeNormal == FreeFree || FreeFree == FreeNeutral || FreeNormal == FreeNeutral {
		t.Error("all three FreeType constants must be distinct")
	}
}

// ── Turing lens: termination conditions, halting behavior ────────────────────
// Five tests verifying that invalid inputs cause immediate, deterministic halts.

// TestGUC_FreeType_Turing_Value3RejectedInBoth verifies that value 3 (one above
// the valid maximum) causes add_torrent and update_torrent to halt with error.
func TestGUC_FreeType_Turing_Value3RejectedInBoth(t *testing.T) {
	f := newTestFixture()
	resp, err := gucAddTorrent(f.worker, 301, "turing3add__________", "3")
	if err == nil || resp.Success {
		t.Error("add_torrent(free_type=3) must terminate with error")
	}

	resp2, err2 := gucUpdateTorrent(f.worker, testInfoHash, "3")
	if err2 == nil || resp2.Success {
		t.Error("update_torrent(free_type=3) must terminate with error")
	}
}

// TestGUC_FreeType_Turing_NegativeOneRejectedInBoth verifies that -1 causes an
// immediate halt — the tracker must not wrap or silently accept negative values.
func TestGUC_FreeType_Turing_NegativeOneRejectedInBoth(t *testing.T) {
	f := newTestFixture()
	resp, err := gucAddTorrent(f.worker, 302, "turingm1add_________", "-1")
	if err == nil || resp.Success {
		t.Error("add_torrent(free_type=-1) must terminate with error")
	}
	resp2, err2 := gucUpdateTorrent(f.worker, testInfoHash, "-1")
	if err2 == nil || resp2.Success {
		t.Error("update_torrent(free_type=-1) must terminate with error")
	}
}

// TestGUC_FreeType_Turing_Value255RejectedInBoth verifies the byte-max value 255
// halts immediately — it cannot wrap to an in-range value via uint8 truncation.
func TestGUC_FreeType_Turing_Value255RejectedInBoth(t *testing.T) {
	f := newTestFixture()
	resp, err := gucAddTorrent(f.worker, 303, "turing255add________", "255")
	if err == nil || resp.Success {
		t.Error("add_torrent(free_type=255) must terminate with error")
	}
	resp2, err2 := gucUpdateTorrent(f.worker, testInfoHash, "255")
	if err2 == nil || resp2.Success {
		t.Error("update_torrent(free_type=255) must terminate with error")
	}
}

// TestGUC_FreeType_Turing_Value4RejectedInBoth verifies that 4 (second boundary
// above valid maximum) causes both operations to halt with error.
func TestGUC_FreeType_Turing_Value4RejectedInBoth(t *testing.T) {
	f := newTestFixture()
	resp, err := gucAddTorrent(f.worker, 304, "turing4add__________", "4")
	if err == nil || resp.Success {
		t.Error("add_torrent(free_type=4) must terminate with error")
	}
	resp2, err2 := gucUpdateTorrent(f.worker, testInfoHash, "4")
	if err2 == nil || resp2.Success {
		t.Error("update_torrent(free_type=4) must terminate with error")
	}
}

// TestGUC_FreeType_Turing_AbsentFreeTypeDefaultsAndTerminates verifies that
// omitting free_type in add_torrent terminates successfully with FreeNormal=0.
func TestGUC_FreeType_Turing_AbsentFreeTypeDefaultsAndTerminates(t *testing.T) {
	f := newTestFixture()
	hash := "turingnoftadd_______"
	resp, err := gucAddTorrent(f.worker, 305, hash, "")
	if err != nil || !resp.Success {
		t.Fatalf("add_torrent without free_type should terminate successfully; err=%v resp=%+v", err, resp)
	}
	got := gucReadFreeType(t, f.worker, hash)
	if got != FreeNormal {
		t.Errorf("default FreeType: got %d, want FreeNormal(0)", got)
	}
}

// ── Church lens: functional purity, side-effect isolation ────────────────────
// Five tests verifying that rejected operations leave state unchanged.

// TestGUC_FreeType_Church_AddTorrent_NoSideEffectOnReject verifies that a
// rejected add_torrent (invalid free_type) produces no side effects: the
// torrent map is unchanged and no torrent is created.
func TestGUC_FreeType_Church_AddTorrentNoSideEffectOnReject(t *testing.T) {
	f := newTestFixture()
	initialSize := f.worker.Torrents.Size()
	hash := "churchrejectadd_____"
	_, _ = gucAddTorrent(f.worker, 400, hash, "3")

	if f.worker.Torrents.Size() != initialSize {
		t.Errorf("Torrents.Size() changed after rejected add: want %d, got %d",
			initialSize, f.worker.Torrents.Size())
	}
	if _, ok := f.worker.Torrents.Get(hash); ok {
		t.Error("torrent must not exist in map after rejected add_torrent")
	}
}

// TestGUC_FreeType_Church_UpdateTorrent_NoSideEffectOnReject verifies that a
// rejected update_torrent (invalid free_type) leaves FreeType referentially
// transparent — the torrent's state is identical before and after the call.
func TestGUC_FreeType_Church_UpdateTorrentNoSideEffectOnReject(t *testing.T) {
	f := newTestFixture()
	torrent, _ := f.worker.Torrents.Get(testInfoHash)
	torrent.mu.Lock()
	torrent.FreeType = FreeFree
	torrent.mu.Unlock()

	_, _ = gucUpdateTorrent(f.worker, testInfoHash, "99")

	got := gucReadFreeType(t, f.worker, testInfoHash)
	if got != FreeFree {
		t.Errorf("FreeType mutated by rejected update; got %d, want FreeFree(1)", got)
	}
}

// TestGUC_FreeType_Church_FreeFreeConstant_IsNoDownloadCharge verifies that
// FreeFree equals 1, the sentinel value for "downloads not counted". This
// test documents the referential identity that accounting code depends on.
func TestGUC_FreeType_Church_FreeFreeConstantIsNoDownloadCharge(t *testing.T) {
	const expectedFreeFreeValue FreeType = 1
	if FreeFree != expectedFreeFreeValue {
		t.Errorf("FreeFree must equal 1 (no-download-charge sentinel); got %d", FreeFree)
	}
	// Setting a torrent to FreeFree via API yields the same constant.
	f := newTestFixture()
	if _, err := gucUpdateTorrent(f.worker, testInfoHash, "1"); err != nil {
		t.Fatalf("setting free_type=1 failed: %v", err)
	}
	got := gucReadFreeType(t, f.worker, testInfoHash)
	if got != FreeFree {
		t.Errorf("after setting free_type=1, FreeType: got %d, want FreeFree(1)", got)
	}
}

// TestGUC_FreeType_Church_FreeNormal_StandardAccounting verifies that FreeNormal=0
// represents unmodified (full) accounting. The pre-seeded fixture torrent must
// start at FreeNormal and remain so after an explicit set-to-0 call.
func TestGUC_FreeType_Church_FreeNormalStandardAccounting(t *testing.T) {
	f := newTestFixture()
	// Pre-seeded torrent starts at FreeNormal.
	initial := gucReadFreeType(t, f.worker, testInfoHash)
	if initial != FreeNormal {
		t.Errorf("fixture torrent initial FreeType: got %d, want FreeNormal(0)", initial)
	}
	// Explicitly setting 0 is idempotent.
	resp, err := gucUpdateTorrent(f.worker, testInfoHash, "0")
	if err != nil || !resp.Success {
		t.Fatalf("setting free_type=0 failed: err=%v resp=%+v", err, resp)
	}
	got := gucReadFreeType(t, f.worker, testInfoHash)
	if got != FreeNormal {
		t.Errorf("FreeType after explicit set-to-0: got %d, want FreeNormal(0)", got)
	}
}

// TestGUC_FreeType_Church_FreeNeutral_NeutralAccounting verifies FreeNeutral=2
// semantics: no stats are counted. The value must be a pure data constant,
// distinct from both FreeNormal and FreeFree.
func TestGUC_FreeType_Church_FreeNeutralNeutralAccounting(t *testing.T) {
	f := newTestFixture()
	resp, err := gucUpdateTorrent(f.worker, testInfoHash, "2")
	if err != nil || !resp.Success {
		t.Fatalf("setting free_type=2 failed: err=%v resp=%+v", err, resp)
	}
	got := gucReadFreeType(t, f.worker, testInfoHash)
	if got != FreeNeutral {
		t.Errorf("FreeType after set-to-2: got %d, want FreeNeutral(2)", got)
	}
	if FreeNeutral == FreeNormal || FreeNeutral == FreeFree {
		t.Error("FreeNeutral must be distinct from FreeNormal and FreeFree")
	}
}

// ── Gödel lens: formal consistency, invariant preservation ───────────────────
// Five tests verifying that the system cannot reach contradictory or
// impossible states through any sequence of valid or invalid API calls.

// TestGUC_FreeType_Godel_PersistedAfterSequentialUpdates verifies that
// free_type is durably stored on the torrent after a sequence of updates —
// the final state is consistent with the last valid write.
func TestGUC_FreeType_Godel_PersistedAfterSequentialUpdates(t *testing.T) {
	f := newTestFixture()
	sequence := []struct {
		ft   string
		want FreeType
	}{
		{"1", FreeFree},
		{"2", FreeNeutral},
		{"0", FreeNormal},
		{"2", FreeNeutral},
	}
	for _, step := range sequence {
		if _, err := gucUpdateTorrent(f.worker, testInfoHash, step.ft); err != nil {
			t.Fatalf("update free_type=%s failed: %v", step.ft, err)
		}
		got := gucReadFreeType(t, f.worker, testInfoHash)
		if got != step.want {
			t.Errorf("after setting %s: got %d, want %d", step.ft, got, step.want)
		}
	}
}

// TestGUC_FreeType_Godel_RoundtripAllValidValues sets each valid FreeType via
// API and immediately reads it back, ensuring no value is silently corrupted
// during the set→store→get cycle.
func TestGUC_FreeType_Godel_RoundtripAllValidValues(t *testing.T) {
	cases := []struct {
		send string
		want FreeType
	}{
		{"0", FreeNormal},
		{"1", FreeFree},
		{"2", FreeNeutral},
		{"0", FreeNormal},
		{"2", FreeNeutral},
		{"1", FreeFree},
	}
	f := newTestFixture()
	for _, tc := range cases {
		tc := tc
		if _, err := gucUpdateTorrent(f.worker, testInfoHash, tc.send); err != nil {
			t.Errorf("update free_type=%s failed: %v", tc.send, err)
			continue
		}
		got := gucReadFreeType(t, f.worker, testInfoHash)
		if got != tc.want {
			t.Errorf("roundtrip free_type=%s: got %d, want %d", tc.send, got, tc.want)
		}
	}
}

// TestGUC_FreeType_Godel_ImpossibleStateCannotBeReached verifies that no
// sequence of invalid API calls can produce a Torrent with FreeType > 2,
// which would be a contradiction of the type invariant.
func TestGUC_FreeType_Godel_ImpossibleStateCannotBeReached(t *testing.T) {
	f := newTestFixture()
	// Attempt every out-of-range value including extreme cases.
	for _, ft := range []string{"3", "4", "5", "10", "100", "200", "255", "-1", "-100"} {
		_, _ = gucUpdateTorrent(f.worker, testInfoHash, ft)
	}
	got := gucReadFreeType(t, f.worker, testInfoHash)
	if got > 2 {
		t.Errorf("FreeType exceeded valid maximum after invalid updates; got %d (max 2)", got)
	}
}

// TestGUC_FreeType_Godel_ChangeFreeleechEnforcesSameConstraint verifies formal
// consistency: change_freeleech must reject free_type=3 just as update_torrent
// does — both paths must share the same invariant.
func TestGUC_FreeType_Godel_ChangeFreeleechEnforcesSameConstraint(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{
		"info_hash": {testInfoHash},
		"free_type": {"3"},
	}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, err := f.worker.HandleUpdate(req)
	resp := gucParseUpdateResp(t, data)
	if err == nil || resp.Success {
		t.Error("change_freeleech(free_type=3) must be rejected for consistency with update_torrent")
	}

	resp2, err2 := gucUpdateTorrent(f.worker, testInfoHash, "3")
	if err2 == nil || resp2.Success {
		t.Error("update_torrent(free_type=3) must also be rejected")
	}
}

// TestGUC_FreeType_Godel_BulkChangeValidatesEach verifies that when multiple
// torrents are updated in a batch, invalid values for individual torrents are
// caught independently — a valid update on one torrent must not be corrupted
// by an invalid update on another.
func TestGUC_FreeType_Godel_BulkChangeValidatesEach(t *testing.T) {
	f := newTestFixture()
	hash2 := "bulkft2guc__________"

	// Add a second torrent starting at FreeNormal.
	if resp, err := gucAddTorrent(f.worker, 500, hash2, "0"); err != nil || !resp.Success {
		t.Fatalf("setup: could not add second torrent: err=%v resp=%+v", err, resp)
	}

	// Valid update on hash2 → FreeNeutral.
	if resp, err := gucUpdateTorrent(f.worker, hash2, "2"); err != nil || !resp.Success {
		t.Errorf("valid update on hash2 failed: err=%v resp=%+v", err, resp)
	}

	// Invalid update on testInfoHash — must be rejected and leave FreeType unchanged.
	torrent1, _ := f.worker.Torrents.Get(testInfoHash)
	torrent1.mu.RLock()
	before := torrent1.FreeType
	torrent1.mu.RUnlock()
	_, _ = gucUpdateTorrent(f.worker, testInfoHash, "99")
	after := gucReadFreeType(t, f.worker, testInfoHash)
	if after != before {
		t.Errorf("testInfoHash FreeType changed after invalid bulk update: before=%d after=%d", before, after)
	}

	// hash2 must still hold FreeNeutral=2.
	ft2 := gucReadFreeType(t, f.worker, hash2)
	if ft2 != FreeNeutral {
		t.Errorf("hash2 FreeType: got %d, want FreeNeutral(2)", ft2)
	}
}
