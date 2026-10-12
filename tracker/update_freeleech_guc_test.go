package tracker

// GUC test coverage for freeleech update operations in update.go.
// Distributed across four analytical lenses:
//   Knuth  – algorithmic correctness, loop invariants, data-structure invariants
//   Turing – termination conditions, halting on invalid input, decidability
//   Church – functional purity, side-effect isolation, referential transparency
//   Gödel  – formal consistency, impossible-state detection, contradiction checks

import (
	"encoding/json"
	"net/url"
	"sync"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// changeFreeleechReq builds a HandleUpdate request for the change_freeleech action.
func changeFreeleechReq(infoHash, freeType string) *updateReqParams {
	return &updateReqParams{infoHash: infoHash, freeType: freeType}
}

type updateReqParams struct {
	infoHash string
	freeType string
}

func (p *updateReqParams) build() *updateReqResult {
	f := newTestFixture()
	extra := url.Values{}
	if p.infoHash != "" {
		extra.Set("info_hash", p.infoHash)
	}
	if p.freeType != "" {
		extra.Set("free_type", p.freeType)
	}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, err := f.worker.HandleUpdate(req)
	return &updateReqResult{data: data, err: err, fixture: f}
}

type updateReqResult struct {
	data    []byte
	err     error
	fixture *testFixture
}

func (r *updateReqResult) response(t *testing.T) UpdateResponse {
	t.Helper()
	var resp UpdateResponse
	if jsonErr := json.Unmarshal(r.data, &resp); jsonErr != nil {
		t.Fatalf("response is not valid JSON: %v (raw: %q)", jsonErr, r.data)
	}
	return resp
}

// ── Knuth: algorithmic correctness ───────────────────────────────────────────

// TestGUC_Freeleech_Knuth_ChangeToFreeNormal verifies that free_type=0 stores
// FreeNormal in the torrent and returns a successful response.
func TestGUC_Freeleech_Knuth_ChangeToFreeNormal(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {testInfoHash}, "free_type": {"0"}}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, err := f.worker.HandleUpdate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp UpdateResponse
	if e := json.Unmarshal(data, &resp); e != nil {
		t.Fatalf("invalid JSON: %v", e)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false (error: %q)", resp.Error)
	}
	torrent, ok := f.worker.Torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent not found after update")
	}
	torrent.mu.RLock()
	got := torrent.FreeType
	torrent.mu.RUnlock()
	if got != FreeNormal {
		t.Errorf("expected FreeType=FreeNormal(0), got %d", got)
	}
}

// TestGUC_Freeleech_Knuth_ChangeToFreeFree verifies that free_type=1 stores
// FreeFree and the response status is "ok".
func TestGUC_Freeleech_Knuth_ChangeToFreeFree(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {testInfoHash}, "free_type": {"1"}}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, err := f.worker.HandleUpdate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp UpdateResponse
	if e := json.Unmarshal(data, &resp); e != nil {
		t.Fatalf("invalid JSON: %v", e)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status=ok, got %q", resp.Status)
	}
	torrent, _ := f.worker.Torrents.Get(testInfoHash)
	torrent.mu.RLock()
	got := torrent.FreeType
	torrent.mu.RUnlock()
	if got != FreeFree {
		t.Errorf("expected FreeType=FreeFree(1), got %d", got)
	}
}

// TestGUC_Freeleech_Knuth_ChangeToFreeNeutral verifies that free_type=2 stores
// FreeNeutral in the torrent data structure.
func TestGUC_Freeleech_Knuth_ChangeToFreeNeutral(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {testInfoHash}, "free_type": {"2"}}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, err := f.worker.HandleUpdate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp UpdateResponse
	if e := json.Unmarshal(data, &resp); e != nil {
		t.Fatalf("invalid JSON: %v", e)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false")
	}
	torrent, _ := f.worker.Torrents.Get(testInfoHash)
	torrent.mu.RLock()
	got := torrent.FreeType
	torrent.mu.RUnlock()
	if got != FreeNeutral {
		t.Errorf("expected FreeType=FreeNeutral(2), got %d", got)
	}
}

// TestGUC_Freeleech_Knuth_AllValidFreeTypes exercises all three valid free_type
// values in a table-driven loop and checks the torrent state after each.
func TestGUC_Freeleech_Knuth_AllValidFreeTypes(t *testing.T) {
	tests := []struct {
		freeTypeStr string
		wantType    FreeType
		wantName    string
	}{
		{"0", FreeNormal, "normal"},
		{"1", FreeFree, "free"},
		{"2", FreeNeutral, "neutral"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.wantName, func(t *testing.T) {
			f := newTestFixture()
			extra := url.Values{"info_hash": {testInfoHash}, "free_type": {tc.freeTypeStr}}
			req := buildUpdateURL(sitePass, "change_freeleech", extra)
			data, err := f.worker.HandleUpdate(req)
			if err != nil {
				t.Fatalf("unexpected error for free_type=%s: %v", tc.freeTypeStr, err)
			}
			var resp UpdateResponse
			if e := json.Unmarshal(data, &resp); e != nil {
				t.Fatalf("invalid JSON: %v", e)
			}
			if !resp.Success {
				t.Errorf("expected success for free_type=%s, got error=%q", tc.freeTypeStr, resp.Error)
			}
			torrent, ok := f.worker.Torrents.Get(testInfoHash)
			if !ok {
				t.Fatal("torrent missing after update")
			}
			torrent.mu.RLock()
			got := torrent.FreeType
			torrent.mu.RUnlock()
			if got != tc.wantType {
				t.Errorf("free_type=%s: expected FreeType=%d, got %d", tc.freeTypeStr, tc.wantType, got)
			}
		})
	}
}

// TestGUC_Freeleech_Knuth_SuccessResponseContainsMessage verifies the JSON
// response on success includes a non-empty message field.
func TestGUC_Freeleech_Knuth_SuccessResponseContainsMessage(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {testInfoHash}, "free_type": {"1"}}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, err := f.worker.HandleUpdate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp UpdateResponse
	if e := json.Unmarshal(data, &resp); e != nil {
		t.Fatalf("invalid JSON: %v", e)
	}
	if resp.Message == "" {
		t.Errorf("expected non-empty message field on success, got empty string")
	}
}

// ── Turing: termination / halting behavior ───────────────────────────────────

// TestGUC_Freeleech_Turing_NonexistentTorrentReturnsError verifies that
// requesting freeleech on a non-existent info_hash returns an error response
// and a non-nil error, not a panic or infinite loop.
func TestGUC_Freeleech_Turing_NonexistentTorrentReturnsError(t *testing.T) {
	f := newTestFixture()
	bogusHash := "nonexistent_info_hash_00000000000"
	extra := url.Values{"info_hash": {bogusHash}, "free_type": {"1"}}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, err := f.worker.HandleUpdate(req)
	if err == nil {
		t.Error("expected non-nil error for nonexistent torrent, got nil")
	}
	if data == nil {
		t.Fatal("expected JSON error body, got nil")
	}
	var resp UpdateResponse
	if e := json.Unmarshal(data, &resp); e != nil {
		t.Fatalf("invalid JSON: %v", e)
	}
	if resp.Success {
		t.Error("expected success=false for nonexistent torrent")
	}
	if resp.Status != "error" {
		t.Errorf("expected status=error, got %q", resp.Status)
	}
}

// TestGUC_Freeleech_Turing_InvalidFreeTypeReturnsError verifies that an
// out-of-range free_type (3) terminates with an error, not with a corrupt state.
func TestGUC_Freeleech_Turing_InvalidFreeTypeReturnsError(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {testInfoHash}, "free_type": {"3"}}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, err := f.worker.HandleUpdate(req)
	if err == nil {
		t.Error("expected error for free_type=3, got nil")
	}
	var resp UpdateResponse
	if e := json.Unmarshal(data, &resp); e != nil {
		t.Fatalf("invalid JSON in error response: %v", e)
	}
	if resp.Success {
		t.Error("expected success=false for invalid free_type=3")
	}
	// The torrent's FreeType must remain unchanged (FreeNormal=0, its initial value).
	torrent, _ := f.worker.Torrents.Get(testInfoHash)
	torrent.mu.RLock()
	got := torrent.FreeType
	torrent.mu.RUnlock()
	if got != FreeNormal {
		t.Errorf("torrent FreeType corrupted by invalid request: expected FreeNormal(0), got %d", got)
	}
}

// TestGUC_Freeleech_Turing_MissingInfoHashHaltsGracefully verifies that a
// request with no info_hash terminates with an error response, not a crash.
func TestGUC_Freeleech_Turing_MissingInfoHashHaltsGracefully(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"free_type": {"1"}} // deliberately omit info_hash
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, err := f.worker.HandleUpdate(req)
	if err == nil {
		t.Error("expected error when info_hash is missing")
	}
	var resp UpdateResponse
	if e := json.Unmarshal(data, &resp); e != nil {
		t.Fatalf("invalid JSON in error body: %v", e)
	}
	if resp.Success {
		t.Error("expected success=false when info_hash is missing")
	}
}

// TestGUC_Freeleech_Turing_UpdateAllTorrentsType0 is a placeholder for the
// update_all_torrents_free_type action, which is not yet implemented.
func TestGUC_Freeleech_Turing_UpdateAllTorrentsType0(t *testing.T) {
	t.Skip("not yet implemented: update_all_torrents_free_type")
}

// TestGUC_Freeleech_Turing_UpdateAllTorrentsInvalidType is a placeholder for
// rejecting invalid types in update_all_torrents_free_type.
func TestGUC_Freeleech_Turing_UpdateAllTorrentsInvalidType(t *testing.T) {
	t.Skip("not yet implemented: update_all_torrents_free_type")
}

// ── Church: functional purity / side-effect isolation ────────────────────────

// TestGUC_Freeleech_Church_ChangingOneTorrentDoesNotAffectAnother verifies that
// updating the freeleech status of testInfoHash leaves a second torrent intact.
func TestGUC_Freeleech_Church_ChangingOneTorrentDoesNotAffectAnother(t *testing.T) {
	f := newTestFixture()
	// Add a second torrent with a known FreeType.
	secondHash := "secondhash000000000000000000000000"
	secondTorrent := NewTorrent(TorrentID(2))
	secondTorrent.FreeType = FreeFree
	f.worker.Torrents.Set(secondHash, secondTorrent)

	// Change freeleech on the first torrent only.
	extra := url.Values{"info_hash": {testInfoHash}, "free_type": {"2"}}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	_, err := f.worker.HandleUpdate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second torrent must be unaffected.
	got, ok := f.worker.Torrents.Get(secondHash)
	if !ok {
		t.Fatal("second torrent disappeared after update")
	}
	got.mu.RLock()
	ft := got.FreeType
	got.mu.RUnlock()
	if ft != FreeFree {
		t.Errorf("second torrent FreeType changed unexpectedly: expected FreeFree(1), got %d", ft)
	}
}

// TestGUC_Freeleech_Church_ErrorResponseHasNoMessageField verifies that an
// error response does not leak a human-facing message in the message field
// (purity: error and success paths are cleanly separated).
func TestGUC_Freeleech_Church_ErrorResponseHasNoMessageField(t *testing.T) {
	f := newTestFixture()
	extra := url.Values{"info_hash": {testInfoHash}, "free_type": {"99"}}
	req := buildUpdateURL(sitePass, "change_freeleech", extra)
	data, _ := f.worker.HandleUpdate(req)
	var resp UpdateResponse
	if e := json.Unmarshal(data, &resp); e != nil {
		t.Fatalf("invalid JSON: %v", e)
	}
	if resp.Message != "" {
		t.Errorf("error response should have empty message field, got %q", resp.Message)
	}
}

// TestGUC_Freeleech_Church_ApplyingSameFreeTypeIsIdempotent checks that
// calling change_freeleech twice with the same value leaves the torrent in the
// same state (referential transparency of the state transition).
func TestGUC_Freeleech_Church_ApplyingSameFreeTypeIsIdempotent(t *testing.T) {
	f := newTestFixture()
	applyFree := func() {
		extra := url.Values{"info_hash": {testInfoHash}, "free_type": {"1"}}
		req := buildUpdateURL(sitePass, "change_freeleech", extra)
		if _, err := f.worker.HandleUpdate(req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	applyFree()
	applyFree()

	torrent, _ := f.worker.Torrents.Get(testInfoHash)
	torrent.mu.RLock()
	got := torrent.FreeType
	torrent.mu.RUnlock()
	if got != FreeFree {
		t.Errorf("expected FreeFree(1) after idempotent apply, got %d", got)
	}
}

// TestGUC_Freeleech_Church_PerTorrentFreeleechIsIndependent verifies that two
// torrents can independently hold different FreeType values, confirming that
// per-torrent state is fully isolated.
func TestGUC_Freeleech_Church_PerTorrentFreeleechIsIndependent(t *testing.T) {
	f := newTestFixture()
	hashA := testInfoHash
	hashB := "torrentb000000000000000000000000000"
	tb := NewTorrent(TorrentID(3))
	f.worker.Torrents.Set(hashB, tb)

	setFT := func(hash, ft string) {
		extra := url.Values{"info_hash": {hash}, "free_type": {ft}}
		req := buildUpdateURL(sitePass, "change_freeleech", extra)
		if _, err := f.worker.HandleUpdate(req); err != nil {
			t.Fatalf("error setting free_type=%s on %q: %v", ft, hash, err)
		}
	}
	setFT(hashA, "1")
	setFT(hashB, "2")

	readFT := func(hash string) FreeType {
		torrent, ok := f.worker.Torrents.Get(hash)
		if !ok {
			t.Fatalf("torrent %q not found", hash)
		}
		torrent.mu.RLock()
		defer torrent.mu.RUnlock()
		return torrent.FreeType
	}
	if readFT(hashA) != FreeFree {
		t.Errorf("torrent A: expected FreeFree(1), got %d", readFT(hashA))
	}
	if readFT(hashB) != FreeNeutral {
		t.Errorf("torrent B: expected FreeNeutral(2), got %d", readFT(hashB))
	}
}

// TestGUC_Freeleech_Church_GlobalFreeleechOn is a placeholder for enabling a
// global freeleech flag that is not yet implemented in this codebase.
func TestGUC_Freeleech_Church_GlobalFreeleechOn(t *testing.T) {
	t.Skip("not yet implemented: global freeleech flag")
}

// ── Gödel: formal consistency / invariant preservation ───────────────────────

// TestGUC_Freeleech_Godel_FreeTypeAlwaysValidAfterChange verifies the
// data-structure invariant: after any valid change_freeleech call, FreeType is
// exactly one of {0,1,2}.
func TestGUC_Freeleech_Godel_FreeTypeAlwaysValidAfterChange(t *testing.T) {
	validTypes := []string{"0", "1", "2"}
	for _, ft := range validTypes {
		f := newTestFixture()
		extra := url.Values{"info_hash": {testInfoHash}, "free_type": {ft}}
		req := buildUpdateURL(sitePass, "change_freeleech", extra)
		if _, err := f.worker.HandleUpdate(req); err != nil {
			t.Fatalf("unexpected error for free_type=%s: %v", ft, err)
		}
		torrent, _ := f.worker.Torrents.Get(testInfoHash)
		torrent.mu.RLock()
		got := torrent.FreeType
		torrent.mu.RUnlock()
		if got != FreeNormal && got != FreeFree && got != FreeNeutral {
			t.Errorf("impossible FreeType %d stored after setting %s", got, ft)
		}
	}
}

// TestGUC_Freeleech_Godel_ImpossibleFreeTypeIsNeverStored confirms that
// requesting free_type=3 (or higher) never mutates the torrent: the impossible
// state cannot be reached.
func TestGUC_Freeleech_Godel_ImpossibleFreeTypeIsNeverStored(t *testing.T) {
	badTypes := []struct {
		label string
		value string
	}{
		{"type3", "3"},
		{"type4", "4"},
		{"type99", "99"},
		{"negative", "-1"},
		{"non-numeric", "abc"},
	}
	for _, tc := range badTypes {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			f := newTestFixture()
			extra := url.Values{"info_hash": {testInfoHash}, "free_type": {tc.value}}
			req := buildUpdateURL(sitePass, "change_freeleech", extra)
			data, err := f.worker.HandleUpdate(req)
			if err == nil {
				t.Errorf("free_type=%q: expected error, got nil", tc.value)
			}
			var resp UpdateResponse
			if e := json.Unmarshal(data, &resp); e != nil {
				t.Fatalf("invalid JSON: %v", e)
			}
			if resp.Success {
				t.Errorf("free_type=%q: expected success=false", tc.value)
			}
			torrent, _ := f.worker.Torrents.Get(testInfoHash)
			torrent.mu.RLock()
			got := torrent.FreeType
			torrent.mu.RUnlock()
			if got != FreeNormal {
				t.Errorf("free_type=%q: torrent FreeType changed to %d, invariant violated", tc.value, got)
			}
		})
	}
}

// TestGUC_Freeleech_Godel_ResponseJSONIsAlwaysWellFormed verifies that every
// code path in changeFreeleech returns syntactically valid JSON — a consistency
// invariant over the error/success boundary.
func TestGUC_Freeleech_Godel_ResponseJSONIsAlwaysWellFormed(t *testing.T) {
	cases := []struct {
		label    string
		infoHash string
		freeType string
	}{
		{"valid", testInfoHash, "1"},
		{"bad_free_type", testInfoHash, "5"},
		{"no_torrent", "doesnotexist0000000000000000000000", "1"},
		{"missing_info_hash", "", "1"},
		{"missing_free_type", testInfoHash, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			f := newTestFixture()
			extra := url.Values{}
			if tc.infoHash != "" {
				extra.Set("info_hash", tc.infoHash)
			}
			if tc.freeType != "" {
				extra.Set("free_type", tc.freeType)
			}
			req := buildUpdateURL(sitePass, "change_freeleech", extra)
			data, _ := f.worker.HandleUpdate(req)
			if data == nil {
				t.Fatal("got nil data — expected at least an error JSON body")
			}
			var resp UpdateResponse
			if e := json.Unmarshal(data, &resp); e != nil {
				t.Errorf("response is not valid JSON: %v (raw: %q)", e, data)
			}
		})
	}
}

// TestGUC_Freeleech_Godel_ConcurrentFreeleechUpdatesAreConsistent fires
// multiple goroutines that each update the same torrent's FreeType and verifies
// that after all complete the value is one of the valid states (no tearing).
func TestGUC_Freeleech_Godel_ConcurrentFreeleechUpdatesAreConsistent(t *testing.T) {
	f := newTestFixture()
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		ft := itoa(i % 3) // cycles 0,1,2
		go func(freeType string) {
			defer wg.Done()
			extra := url.Values{"info_hash": {testInfoHash}, "free_type": {freeType}}
			req := buildUpdateURL(sitePass, "change_freeleech", extra)
			f.worker.HandleUpdate(req) //nolint:errcheck
		}(ft)
	}
	wg.Wait()

	torrent, ok := f.worker.Torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent missing after concurrent updates")
	}
	torrent.mu.RLock()
	final := torrent.FreeType
	torrent.mu.RUnlock()
	if final != FreeNormal && final != FreeFree && final != FreeNeutral {
		t.Errorf("concurrent updates produced impossible FreeType=%d", final)
	}
}

// TestGUC_Freeleech_Godel_FinalStateMatchesLastWrite verifies formal
// consistency: after sequential writes 0→1→2→0, the stored state equals 0.
func TestGUC_Freeleech_Godel_FinalStateMatchesLastWrite(t *testing.T) {
	f := newTestFixture()
	sequence := []string{"0", "1", "2", "0"}
	for _, ft := range sequence {
		extra := url.Values{"info_hash": {testInfoHash}, "free_type": {ft}}
		req := buildUpdateURL(sitePass, "change_freeleech", extra)
		if _, err := f.worker.HandleUpdate(req); err != nil {
			t.Fatalf("unexpected error at free_type=%s: %v", ft, err)
		}
	}
	torrent, _ := f.worker.Torrents.Get(testInfoHash)
	torrent.mu.RLock()
	got := torrent.FreeType
	torrent.mu.RUnlock()
	if got != FreeNormal {
		t.Errorf("expected FreeNormal(0) after sequence %v, got %d", sequence, got)
	}
}
