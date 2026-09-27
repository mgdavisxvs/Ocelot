package tracker

// GUC (Gödel Unified Council) test coverage for user-related update actions.
// Lenses: Knuth (algorithmic), Turing (termination), Church (functional purity), Gödel (invariants).

import (
	"encoding/json"
	"net/url"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func decodeUserUpdateResp(t *testing.T, data []byte) UpdateResponse {
	t.Helper()
	var r UpdateResponse
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("failed to decode UpdateResponse: %v (raw: %s)", err, data)
	}
	return r
}

func makeWorkerForUserTests() *Worker {
	f := newTestFixture()
	return f.worker
}

// newPasskey returns a fresh 32-char passkey not already present in the fixture.
func freshPasskey(suffix string) string {
	base := "00000000000000000000000000000000"
	if len(suffix) >= 32 {
		return suffix[:32]
	}
	return base[:32-len(suffix)] + suffix
}

// ── KNUTH lens – algorithmic correctness ─────────────────────────────────────

// TestGUC_AddUser_ValidIDAndPasskeySuccess verifies that add_user with a valid
// numeric id and a 32-character passkey succeeds and stores the user.
func TestGUC_AddUser_ValidIDAndPasskeySuccess(t *testing.T) {
	w := makeWorkerForUserTests()

	pk := freshPasskey("newuser01")
	q := url.Values{
		"id":      {"99"},
		"passkey": {pk},
	}
	data, err := w.addUser(q)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	r := decodeUserUpdateResp(t, data)
	if !r.Success {
		t.Errorf("expected Success=true, got false; message=%q error=%q", r.Message, r.Error)
	}
	if r.Status != "ok" {
		t.Errorf("expected status=ok, got %q", r.Status)
	}
	if _, ok := w.Users.Get(pk); !ok {
		t.Errorf("expected user to be stored under passkey %q", pk)
	}
}

// TestGUC_AddUser_IDAlias verifies that the user_id alias is accepted in place
// of the id parameter (algorithmic equivalence of parameter aliases).
func TestGUC_AddUser_IDAlias(t *testing.T) {
	w := makeWorkerForUserTests()

	pk := freshPasskey("alias9999")
	q := url.Values{
		"user_id": {"88"},
		"passkey": {pk},
	}
	data, err := w.addUser(q)
	if err != nil {
		t.Fatalf("expected no error with user_id alias, got: %v", err)
	}
	r := decodeUserUpdateResp(t, data)
	if !r.Success {
		t.Errorf("expected Success=true, got false; error=%q", r.Error)
	}
	if _, ok := w.Users.Get(pk); !ok {
		t.Error("expected user stored via user_id alias")
	}
}

// TestGUC_CanLeech_FalseWhenParamIsZero verifies the can_leech=0 flag is stored
// correctly (algorithmic flag mapping).
func TestGUC_CanLeech_FalseWhenParamIsZero(t *testing.T) {
	w := makeWorkerForUserTests()

	pk := freshPasskey("noleech11")
	q := url.Values{
		"id":        {"77"},
		"passkey":   {pk},
		"can_leech": {"0"},
	}
	if _, err := w.addUser(q); err != nil {
		t.Fatalf("addUser failed: %v", err)
	}
	u, ok := w.Users.Get(pk)
	if !ok {
		t.Fatal("user not found after add")
	}
	if u.CanLeech.Load() {
		t.Error("expected CanLeech=false when can_leech=0")
	}
}

// TestGUC_CanLeech_TrueByDefault verifies that omitting can_leech defaults to true.
func TestGUC_CanLeech_TrueByDefault(t *testing.T) {
	w := makeWorkerForUserTests()

	pk := freshPasskey("leechdef2")
	q := url.Values{
		"id":      {"76"},
		"passkey": {pk},
	}
	if _, err := w.addUser(q); err != nil {
		t.Fatalf("addUser failed: %v", err)
	}
	u, ok := w.Users.Get(pk)
	if !ok {
		t.Fatal("user not found after add")
	}
	if !u.CanLeech.Load() {
		t.Error("expected CanLeech=true when can_leech is omitted")
	}
}

// TestGUC_UpdateUser_CanLeechToggle verifies that update_user can toggle
// can_leech from true to false (algorithmic state transition).
func TestGUC_UpdateUser_CanLeechToggle(t *testing.T) {
	w := makeWorkerForUserTests()

	// testPasskey user has CanLeech=true from fixture
	q := url.Values{
		"passkey":   {testPasskey},
		"can_leech": {"0"},
	}
	data, err := w.updateUser(q)
	if err != nil {
		t.Fatalf("updateUser failed: %v", err)
	}
	r := decodeUserUpdateResp(t, data)
	if !r.Success {
		t.Errorf("expected Success=true, got false; error=%q", r.Error)
	}
	u, ok := w.Users.Get(testPasskey)
	if !ok {
		t.Fatal("user not found after update")
	}
	if u.CanLeech.Load() {
		t.Error("expected CanLeech=false after update with can_leech=0")
	}
}

// ── TURING lens – termination and halting behavior ───────────────────────────

// TestGUC_AddUser_MissingIDError verifies that add_user halts with an error
// when the id/user_id parameter is absent.
func TestGUC_AddUser_MissingIDError(t *testing.T) {
	w := makeWorkerForUserTests()

	pk := freshPasskey("missingid3")
	q := url.Values{
		"passkey": {pk},
	}
	data, err := w.addUser(q)
	if err == nil {
		t.Fatal("expected error for missing id, got nil")
	}
	r := decodeUserUpdateResp(t, data)
	if r.Success {
		t.Error("expected Success=false for missing id")
	}
	if r.Status != "error" {
		t.Errorf("expected status=error, got %q", r.Status)
	}
}

// TestGUC_AddUser_MissingPasskeyError verifies that add_user halts with an error
// when the passkey parameter is absent.
func TestGUC_AddUser_MissingPasskeyError(t *testing.T) {
	w := makeWorkerForUserTests()

	q := url.Values{
		"id": {"55"},
	}
	data, err := w.addUser(q)
	if err == nil {
		t.Fatal("expected error for missing passkey, got nil")
	}
	r := decodeUserUpdateResp(t, data)
	if r.Success {
		t.Error("expected Success=false for missing passkey")
	}
}

// TestGUC_RemoveUser_NotFound verifies that remove_user halts gracefully when
// the passkey does not identify any user (termination without panic).
func TestGUC_RemoveUser_NotFound(t *testing.T) {
	w := makeWorkerForUserTests()

	q := url.Values{
		"passkey": {"notexist" + freshPasskey("xx")},
	}
	data, err := w.removeUser(q)
	if err == nil {
		t.Fatal("expected error for unknown user passkey")
	}
	r := decodeUserUpdateResp(t, data)
	if r.Success {
		t.Error("expected Success=false for non-existent user")
	}
	if r.Status != "error" {
		t.Errorf("expected status=error, got %q", r.Status)
	}
}

// TestGUC_ChangePasskey_UnknownUser verifies that changePasskey halts with
// an error when the old passkey is not registered.
func TestGUC_ChangePasskey_UnknownUserError(t *testing.T) {
	w := makeWorkerForUserTests()

	q := url.Values{
		"old_passkey": {freshPasskey("ghost0001")},
		"new_passkey": {freshPasskey("ghost0002")},
	}
	data, err := w.changePasskey(q)
	if err == nil {
		t.Fatal("expected error for unknown old passkey")
	}
	r := decodeUserUpdateResp(t, data)
	if r.Success {
		t.Error("expected Success=false when old passkey not found")
	}
}

// TestGUC_AddUser_InvalidIDFormats verifies that non-positive / non-numeric ids
// are all rejected (termination on every bad input variant).
func TestGUC_AddUser_InvalidIDFormats(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"zero", "0"},
		{"negative", "-1"},
		{"text", "abc"},
		{"empty_after_alias", ""},
	}

	pk := freshPasskey("idformat1")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := makeWorkerForUserTests()
			q := url.Values{"passkey": {pk}}
			if tc.id != "" {
				q.Set("id", tc.id)
			}
			_, err := w.addUser(q)
			if err == nil {
				t.Errorf("expected error for id=%q", tc.id)
			}
		})
	}
}

// ── CHURCH lens – functional purity and side-effect isolation ─────────────────

// TestGUC_RemoveUser_ExistingSuccess verifies that remove_user succeeds for an
// existing user and removes it from the map without touching other users.
func TestGUC_RemoveUser_ExistingSuccess(t *testing.T) {
	w := makeWorkerForUserTests()

	sizeBefore := w.Users.Size()
	q := url.Values{"passkey": {testPasskey}}
	data, err := w.removeUser(q)
	if err != nil {
		t.Fatalf("removeUser failed: %v", err)
	}
	r := decodeUserUpdateResp(t, data)
	if !r.Success {
		t.Errorf("expected Success=true, got false; error=%q", r.Error)
	}
	if _, ok := w.Users.Get(testPasskey); ok {
		t.Error("expected user to be absent after removal")
	}
	// Other users must be intact
	if w.Users.Size() != sizeBefore-1 {
		t.Errorf("expected user count to decrease by 1, before=%d after=%d", sizeBefore, w.Users.Size())
	}
}

// TestGUC_ChangePasskey_ValidTransition verifies that change_passkey moves the
// user from old_passkey to new_passkey without duplicating the record.
func TestGUC_ChangePasskey_ValidTransition(t *testing.T) {
	w := makeWorkerForUserTests()

	newPK := freshPasskey("newpk0001")
	q := url.Values{
		"old_passkey": {testPasskey},
		"new_passkey": {newPK},
	}
	data, err := w.changePasskey(q)
	if err != nil {
		t.Fatalf("changePasskey failed: %v", err)
	}
	r := decodeUserUpdateResp(t, data)
	if !r.Success {
		t.Errorf("expected Success=true, got false; error=%q", r.Error)
	}
	if _, ok := w.Users.Get(testPasskey); ok {
		t.Error("old passkey should be absent after change")
	}
	if _, ok := w.Users.Get(newPK); !ok {
		t.Error("user should be accessible under new passkey")
	}
}

// TestGUC_AddUser_DBRecorded verifies that addUser writes the passkey to the
// MockDB (side-effect is properly scoped to the DB abstraction).
func TestGUC_AddUser_DBRecorded(t *testing.T) {
	f := newTestFixture()
	w := f.worker
	db := f.db

	pk := freshPasskey("dbcheck01")
	q := url.Values{
		"id":      {"50"},
		"passkey": {pk},
	}
	if _, err := w.addUser(q); err != nil {
		t.Fatalf("addUser failed: %v", err)
	}
	db.mu.Lock()
	passkeys := db.UserPasskeys
	db.mu.Unlock()

	found := false
	for _, p := range passkeys {
		if p == pk {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected passkey %q to be recorded in DB, got: %v", pk, passkeys)
	}
}

// TestGUC_RemoveUser_DeletedFlagSet verifies that removeUser sets user.Deleted=true
// on the removed user object (side-effect is isolated and the in-memory flag is set).
func TestGUC_RemoveUser_DeletedFlagSet(t *testing.T) {
	f := newTestFixture()
	w := f.worker

	// Capture a reference to the user before removal.
	u, ok := w.Users.Get(testPasskey)
	if !ok {
		t.Fatal("pre-condition failed: testPasskey user not in fixture")
	}

	q := url.Values{"passkey": {testPasskey}}
	if _, err := w.removeUser(q); err != nil {
		t.Fatalf("removeUser failed: %v", err)
	}

	if !u.Deleted.Load() {
		t.Error("expected user.Deleted=true after removeUser (side-effect isolation invariant)")
	}
}

// ── GÖDEL lens – invariant preservation and impossible-state detection ────────

// TestGUC_AllResponses_JSONWithSuccessAndStatus verifies that every user-action
// response is valid JSON containing both "success" and "status" fields (formal
// schema invariant).
func TestGUC_AllResponses_JSONWithSuccessAndStatus(t *testing.T) {
	type action struct {
		name  string
		query url.Values
	}
	pk := freshPasskey("jsoninv01")

	cases := []action{
		{
			name: "add_user_valid",
			query: url.Values{"id": {"200"}, "passkey": {pk}},
		},
		{
			name:  "add_user_missing_id",
			query: url.Values{"passkey": {pk}},
		},
		{
			name:  "remove_user_not_found",
			query: url.Values{"passkey": {freshPasskey("ghost9999")}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := makeWorkerForUserTests()
			var data []byte
			switch tc.name {
			case "add_user_valid", "add_user_missing_id":
				data, _ = w.addUser(tc.query)
			case "remove_user_not_found":
				data, _ = w.removeUser(tc.query)
			}
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("response is not valid JSON: %v (raw: %s)", err, data)
			}
			if _, ok := raw["success"]; !ok {
				t.Errorf("response missing 'success' field: %s", data)
			}
			if _, ok := raw["status"]; !ok {
				t.Errorf("response missing 'status' field: %s", data)
			}
		})
	}
}

// TestGUC_ChangePasskey_SameAsOldAccepted verifies that using the same value for
// old and new passkeys is rejected (new passkey already exists invariant).
func TestGUC_ChangePasskey_SameAsOldRejected(t *testing.T) {
	w := makeWorkerForUserTests()

	q := url.Values{
		"old_passkey": {testPasskey},
		"new_passkey": {testPasskey}, // same as old
	}
	data, err := w.changePasskey(q)
	if err == nil {
		t.Fatal("expected error when new_passkey equals old_passkey (passkey already exists)")
	}
	r := decodeUserUpdateResp(t, data)
	if r.Success {
		t.Error("expected Success=false when new passkey is already registered")
	}
}

// TestGUC_UserCount_Invariant verifies that add followed by remove preserves
// the original user count (conservation invariant).
func TestGUC_UserCount_Invariant(t *testing.T) {
	w := makeWorkerForUserTests()

	initial := w.Users.Size()

	pk := freshPasskey("countinv1")
	addQ := url.Values{"id": {"300"}, "passkey": {pk}}
	if _, err := w.addUser(addQ); err != nil {
		t.Fatalf("addUser failed: %v", err)
	}
	if w.Users.Size() != initial+1 {
		t.Fatalf("expected size %d after add, got %d", initial+1, w.Users.Size())
	}

	remQ := url.Values{"passkey": {pk}}
	if _, err := w.removeUser(remQ); err != nil {
		t.Fatalf("removeUser failed: %v", err)
	}
	if w.Users.Size() != initial {
		t.Errorf("expected size %d after add+remove, got %d (invariant violated)", initial, w.Users.Size())
	}
}

// TestGUC_AddUser_DuplicatePasskeyRejected verifies that inserting a passkey
// that already exists is rejected (uniqueness invariant).
func TestGUC_AddUser_DuplicatePasskeyRejected(t *testing.T) {
	w := makeWorkerForUserTests()

	// testPasskey is pre-seeded in the fixture
	q := url.Values{
		"id":      {"101"},
		"passkey": {testPasskey},
	}
	data, err := w.addUser(q)
	if err == nil {
		t.Fatal("expected error for duplicate passkey")
	}
	r := decodeUserUpdateResp(t, data)
	if r.Success {
		t.Error("expected Success=false for duplicate passkey insertion")
	}
}

// TestGUC_UpdateUser_NonExistentPasskeyError verifies that update_user for an
// unknown passkey returns an error (no phantom user creation invariant).
func TestGUC_UpdateUser_NonExistentPasskeyError(t *testing.T) {
	w := makeWorkerForUserTests()

	q := url.Values{
		"passkey":   {freshPasskey("unknown01")},
		"can_leech": {"1"},
	}
	data, err := w.updateUser(q)
	if err == nil {
		t.Fatal("expected error for unknown passkey in updateUser")
	}
	r := decodeUserUpdateResp(t, data)
	if r.Success {
		t.Error("expected Success=false for unknown passkey in updateUser")
	}
	// Ensure no phantom user was created
	if _, ok := w.Users.Get(freshPasskey("unknown01")); ok {
		t.Error("updateUser must not create a user that does not exist (phantom user invariant)")
	}
}

// TestGUC_ChangePasskey_UserObjectPreserved verifies that after a passkey change
// the same user object (same ID) is reachable under the new passkey (identity
// invariant: no new user is created, the existing record is re-keyed).
func TestGUC_ChangePasskey_UserObjectPreserved(t *testing.T) {
	w := makeWorkerForUserTests()

	oldUser, _ := w.Users.Get(testPasskey)
	oldID := oldUser.ID

	newPK := freshPasskey("preserve1")
	q := url.Values{
		"old_passkey": {testPasskey},
		"new_passkey": {newPK},
	}
	if _, err := w.changePasskey(q); err != nil {
		t.Fatalf("changePasskey failed: %v", err)
	}

	newUser, ok := w.Users.Get(newPK)
	if !ok {
		t.Fatal("user not found under new passkey")
	}
	if newUser.ID != oldID {
		t.Errorf("expected user ID %d preserved under new passkey, got %d (identity invariant violated)", oldID, newUser.ID)
	}
}
