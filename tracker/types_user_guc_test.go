package tracker

// GUC (Gödel Unified Council) tests for UserList and User types.
// Lenses: Knuth (algorithmic), Turing (termination), Church (purity), Gödel (consistency).

import (
	"fmt"
	"sync"
	"testing"
)

// ── Knuth: algorithmic correctness, loop invariants, data structure invariants ──

// TestGUC_UserList_SetGetRoundTrip verifies that a Set followed by Get returns
// the exact same pointer — the map stores and retrieves the value without copying.
func TestGUC_UserList_SetGetRoundTrip(t *testing.T) {
	ul := NewUserList()
	u := NewUser(UserID(42), true, false)
	const pk = "roundtrip_passkey_aabbccdd11223344"
	ul.Set(pk, u)

	got, ok := ul.Get(pk)
	if !ok {
		t.Fatal("Get after Set returned ok=false")
	}
	if got != u {
		t.Fatalf("Get returned different pointer: got %p, want %p", got, u)
	}
	if got.ID != 42 {
		t.Errorf("ID round-tripped as %d, want 42", got.ID)
	}
}

// TestGUC_UserList_DeleteRemovesEntry verifies that Delete removes the entry so
// subsequent Get returns (nil, false) — not a stale pointer, not a zero-valued User.
func TestGUC_UserList_DeleteRemovesEntry(t *testing.T) {
	ul := NewUserList()
	const pk = "delete_test_passkey_aabbccdd11223344"
	ul.Set(pk, NewUser(UserID(1), true, false))
	ul.Delete(pk)

	got, ok := ul.Get(pk)
	if ok {
		t.Fatal("Get returned ok=true after Delete")
	}
	if got != nil {
		t.Fatalf("Get returned non-nil pointer after Delete: %p", got)
	}
}

// TestGUC_UserList_LenAccuracyAfterMixedOps verifies that Size() equals the number
// of live entries through a sequence of Set and Delete operations.
func TestGUC_UserList_LenAccuracyAfterMixedOps(t *testing.T) {
	tests := []struct {
		name    string
		sets    int
		deletes int
		want    int
	}{
		{"empty", 0, 0, 0},
		{"add3", 3, 0, 3},
		{"add5_del2", 5, 2, 3},
		{"add10_del10", 10, 10, 0},
		{"add1_del0", 1, 0, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ul := NewUserList()
			keys := make([]string, tc.sets)
			for i := 0; i < tc.sets; i++ {
				keys[i] = fmt.Sprintf("pk_%s_%04d", tc.name, i)
				ul.Set(keys[i], NewUser(UserID(i+1), true, false))
			}
			for i := 0; i < tc.deletes && i < len(keys); i++ {
				ul.Delete(keys[i])
			}
			if got := ul.Size(); got != tc.want {
				t.Errorf("Size() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestGUC_UserList_LargeList10000Entries verifies that the UserList handles 10 000
// entries correctly: Size equals 10 000, and a sample of lookups succeed.
func TestGUC_UserList_LargeList10000Entries(t *testing.T) {
	const N = 10000
	ul := NewUserList()
	for i := 0; i < N; i++ {
		pk := fmt.Sprintf("large_pk_%08d", i)
		ul.Set(pk, NewUser(UserID(i+1), i%2 == 0, false))
	}

	if got := ul.Size(); got != N {
		t.Fatalf("Size() = %d after inserting %d entries, want %d", got, N, N)
	}

	// Sample a handful of entries at known positions.
	for _, idx := range []int{0, 1, 9999, 5000, 42} {
		pk := fmt.Sprintf("large_pk_%08d", idx)
		u, ok := ul.Get(pk)
		if !ok {
			t.Errorf("Get(%q) = ok=false, want true", pk)
			continue
		}
		want := UserID(idx + 1)
		if u.ID != want {
			t.Errorf("Get(%q).ID = %d, want %d", pk, u.ID, want)
		}
	}
}

// TestGUC_UserList_MultipleUsersDistinctPasskeys verifies that each of N users
// stored under distinct passkeys is retrievable and returns the correct UserID.
func TestGUC_UserList_MultipleUsersDistinctPasskeys(t *testing.T) {
	tests := []struct {
		passkey string
		id      UserID
		canLeech bool
	}{
		{"pk_alpha_aaaa1111bbbb2222cccc", UserID(1), true},
		{"pk_beta__aaaa1111bbbb2222cccc", UserID(2), false},
		{"pk_gamma_aaaa1111bbbb2222cccc", UserID(3), true},
		{"pk_delta_aaaa1111bbbb2222cccc", UserID(4), false},
	}

	ul := NewUserList()
	for _, tc := range tests {
		ul.Set(tc.passkey, NewUser(tc.id, tc.canLeech, false))
	}

	for _, tc := range tests {
		t.Run(tc.passkey[:8], func(t *testing.T) {
			u, ok := ul.Get(tc.passkey)
			if !ok {
				t.Fatalf("Get(%q) ok=false", tc.passkey)
			}
			if u.ID != tc.id {
				t.Errorf("ID = %d, want %d", u.ID, tc.id)
			}
			if u.CanLeech.Load() != tc.canLeech {
				t.Errorf("CanLeech = %v, want %v", u.CanLeech.Load(), tc.canLeech)
			}
		})
	}
}

// ── Turing: termination conditions, halting behavior ─────────────────────────

// TestGUC_UserList_GetMissingReturnsNilFalse verifies the contract that a Get on
// a key never inserted returns exactly (nil, false) — never a zero-value User.
func TestGUC_UserList_GetMissingReturnsNilFalse(t *testing.T) {
	ul := NewUserList()
	// Populate a couple of entries to rule out the trivially-empty case.
	ul.Set("existing_pk_aaaa1111bbbb2222cc", NewUser(UserID(10), true, false))

	got, ok := ul.Get("nonexistent_passkey_0000000000000000")
	if ok {
		t.Fatal("Get on missing key returned ok=true")
	}
	if got != nil {
		t.Fatalf("Get on missing key returned non-nil: %+v", got)
	}
}

// TestGUC_UserList_ForEachIteratesAllEntries verifies that ForEach visits every
// entry exactly once when the callback always returns true.
func TestGUC_UserList_ForEachIteratesAllEntries(t *testing.T) {
	const N = 7
	ul := NewUserList()
	want := make(map[string]bool, N)
	for i := 0; i < N; i++ {
		pk := fmt.Sprintf("foreach_pk_%04d", i)
		want[pk] = true
		ul.Set(pk, NewUser(UserID(i+1), true, false))
	}

	seen := make(map[string]bool, N)
	ul.ForEach(func(passkey string, _ *User) bool {
		seen[passkey] = true
		return true
	})

	if len(seen) != N {
		t.Errorf("ForEach visited %d entries, want %d", len(seen), N)
	}
	for pk := range want {
		if !seen[pk] {
			t.Errorf("ForEach missed entry %q", pk)
		}
	}
}

// TestGUC_UserList_ForEachTerminatesOnFalseReturn verifies that ForEach halts
// iteration as soon as the callback returns false, visiting no more entries.
func TestGUC_UserList_ForEachTerminatesOnFalseReturn(t *testing.T) {
	ul := NewUserList()
	for i := 0; i < 10; i++ {
		ul.Set(fmt.Sprintf("halt_pk_%04d", i), NewUser(UserID(i+1), true, false))
	}

	stopAfter := 3
	count := 0
	ul.ForEach(func(_ string, _ *User) bool {
		count++
		return count < stopAfter
	})

	if count != stopAfter {
		t.Errorf("ForEach visited %d entries after false return, want exactly %d", count, stopAfter)
	}
}

// TestGUC_UserList_StalePasskeyNotReturnedAfterDelete verifies that a passkey
// removed with Delete never reappears during a subsequent ForEach pass.
func TestGUC_UserList_StalePasskeyNotReturnedAfterDelete(t *testing.T) {
	ul := NewUserList()
	const stalePK = "stale_pk_aaaa1111bbbb2222cccc333"
	ul.Set(stalePK, NewUser(UserID(99), true, false))
	ul.Set("live_pk__aaaa1111bbbb2222cccc333", NewUser(UserID(100), true, false))

	ul.Delete(stalePK)

	ul.ForEach(func(passkey string, _ *User) bool {
		if passkey == stalePK {
			t.Errorf("deleted passkey %q reappeared in ForEach", stalePK)
		}
		return true
	})

	if ul.Size() != 1 {
		t.Errorf("Size() = %d after deleting one of two entries, want 1", ul.Size())
	}
}

// TestGUC_UserList_ResetClearsAllEntries verifies that Reset brings Size to zero
// and that previously inserted entries are no longer retrievable.
func TestGUC_UserList_ResetClearsAllEntries(t *testing.T) {
	ul := NewUserList()
	keys := []string{
		"reset_pk1_aaaa1111bbbb2222cccc33",
		"reset_pk2_aaaa1111bbbb2222cccc33",
		"reset_pk3_aaaa1111bbbb2222cccc33",
	}
	for i, pk := range keys {
		ul.Set(pk, NewUser(UserID(i+1), true, false))
	}
	ul.Reset()

	if got := ul.Size(); got != 0 {
		t.Errorf("Size() = %d after Reset, want 0", got)
	}
	for _, pk := range keys {
		if _, ok := ul.Get(pk); ok {
			t.Errorf("Get(%q) returned ok=true after Reset", pk)
		}
	}
}

// ── Church: functional purity, side-effect isolation, immutability ────────────

// TestGUC_NewUser_DefaultsSetCorrectly verifies that NewUser initialises all
// fields from its arguments and leaves zero-value atomics at their defaults.
func TestGUC_NewUser_DefaultsSetCorrectly(t *testing.T) {
	tests := []struct {
		id        UserID
		canLeech  bool
		protectIP bool
	}{
		{UserID(1), true, false},
		{UserID(2), false, true},
		{UserID(3), false, false},
		{UserID(4), true, true},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("id%d", tc.id), func(t *testing.T) {
			u := NewUser(tc.id, tc.canLeech, tc.protectIP)
			if u.ID != tc.id {
				t.Errorf("ID = %d, want %d", u.ID, tc.id)
			}
			if u.CanLeech.Load() != tc.canLeech {
				t.Errorf("CanLeech = %v, want %v", u.CanLeech.Load(), tc.canLeech)
			}
			if u.ProtectIP.Load() != tc.protectIP {
				t.Errorf("ProtectIP = %v, want %v", u.ProtectIP.Load(), tc.protectIP)
			}
			if u.Deleted.Load() {
				t.Error("Deleted should be false on a freshly created User")
			}
			if u.Leeching.Load() != 0 {
				t.Errorf("Leeching = %d, want 0", u.Leeching.Load())
			}
			if u.Seeding.Load() != 0 {
				t.Errorf("Seeding = %d, want 0", u.Seeding.Load())
			}
		})
	}
}

// TestGUC_User_UploadedAtomicIncrement documents that User has no Uploaded field.
// Upload accounting lives on the Peer struct, not User.
func TestGUC_User_UploadedAtomicIncrement(t *testing.T) {
	t.Skip("not yet implemented: User.Uploaded — upload accounting is on Peer, not User")
}

// TestGUC_User_DownloadedAtomicIncrement documents that User has no Downloaded field.
// Download accounting lives on the Peer struct, not User.
func TestGUC_User_DownloadedAtomicIncrement(t *testing.T) {
	t.Skip("not yet implemented: User.Downloaded — download accounting is on Peer, not User")
}

// TestGUC_UserList_PasskeyLookupCorrect verifies that each passkey resolves to its
// assigned user, not to any other user stored in the same list.
func TestGUC_UserList_PasskeyLookupCorrect(t *testing.T) {
	tests := []struct {
		passkey string
		wantID  UserID
	}{
		{"lookup_pk_a_aaaa1111bbbb2222cc33", UserID(11)},
		{"lookup_pk_b_aaaa1111bbbb2222cc33", UserID(22)},
		{"lookup_pk_c_aaaa1111bbbb2222cc33", UserID(33)},
	}

	ul := NewUserList()
	for _, tc := range tests {
		ul.Set(tc.passkey, NewUser(tc.wantID, true, false))
	}

	for _, tc := range tests {
		t.Run(tc.passkey[:10], func(t *testing.T) {
			u, ok := ul.Get(tc.passkey)
			if !ok {
				t.Fatalf("Get(%q) ok=false", tc.passkey)
			}
			if u.ID != tc.wantID {
				t.Errorf("ID = %d, want %d (passkey collision?)", u.ID, tc.wantID)
			}
		})
	}
}

// TestGUC_UserList_GetDoesNotMutateSize verifies that repeated Get calls are
// read-only: they never increase or decrease Size().
func TestGUC_UserList_GetDoesNotMutateSize(t *testing.T) {
	ul := NewUserList()
	const pk = "immutable_pk_aaaa1111bbbb2222cccc"
	ul.Set(pk, NewUser(UserID(7), true, false))
	before := ul.Size()

	for i := 0; i < 100; i++ {
		ul.Get(pk)
		ul.Get("missing_key_that_does_not_exist")
	}

	if after := ul.Size(); after != before {
		t.Errorf("Size changed from %d to %d after Get-only operations", before, after)
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state ───────

// TestGUC_UserList_ConcurrentSafe exercises the UserList under concurrent reads,
// writes, and deletes. Run with -race to surface any data races.
func TestGUC_UserList_ConcurrentSafe(t *testing.T) {
	ul := NewUserList()
	const workers = 20
	const itersEach = 50

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < itersEach; i++ {
				pk := fmt.Sprintf("race_pk_w%03d_i%03d", workerID, i)
				ul.Set(pk, NewUser(UserID(workerID*1000+i), true, false))
				ul.Get(pk)
				ul.Size()
				if i%3 == 0 {
					ul.Delete(pk)
				}
			}
			ul.ForEach(func(_ string, _ *User) bool { return true })
		}(w)
	}
	wg.Wait()

	// After all goroutines finish, Size must not be negative (impossible but testable).
	if s := ul.Size(); s < 0 {
		t.Errorf("Size() = %d after concurrent ops, want >= 0", s)
	}
}

// TestGUC_UserID_TypeSafety verifies the UserID type's arithmetic identity
// properties and that distinct values are never equal.
func TestGUC_UserID_TypeSafety(t *testing.T) {
	var zero UserID
	if zero != 0 {
		t.Errorf("zero-value UserID = %d, want 0", zero)
	}

	tests := []struct {
		a, b  UserID
		equal bool
	}{
		{UserID(0), UserID(0), true},
		{UserID(1), UserID(1), true},
		{UserID(1), UserID(2), false},
		{UserID(^uint32(0)), UserID(^uint32(0)), true}, // max uint32
		{UserID(^uint32(0)), UserID(0), false},
	}

	for _, tc := range tests {
		got := tc.a == tc.b
		if got != tc.equal {
			t.Errorf("UserID(%d) == UserID(%d): got %v, want %v", tc.a, tc.b, got, tc.equal)
		}
	}
}

// TestGUC_UserList_DeleteNonExistentIsNoop verifies that calling Delete on a key
// that was never inserted neither panics nor changes Size.
func TestGUC_UserList_DeleteNonExistentIsNoop(t *testing.T) {
	ul := NewUserList()
	ul.Set("existing_noop_aaaa1111bbbb2222cc", NewUser(UserID(1), true, false))
	before := ul.Size()

	// Must not panic.
	ul.Delete("key_that_was_never_inserted_0000")

	if after := ul.Size(); after != before {
		t.Errorf("Size changed from %d to %d after Delete of absent key", before, after)
	}
}

// TestGUC_UserList_SizeInvariantThroughOperations verifies that Size always equals
// the count of live keys by cross-checking with a manual ForEach count.
func TestGUC_UserList_SizeInvariantThroughOperations(t *testing.T) {
	ul := NewUserList()
	pks := make([]string, 8)
	for i := range pks {
		pks[i] = fmt.Sprintf("inv_pk_%04d_aaaa1111bbbb2222cc", i)
		ul.Set(pks[i], NewUser(UserID(i+1), true, false))
	}

	checkInvariant := func(label string) {
		t.Helper()
		reported := ul.Size()
		counted := 0
		ul.ForEach(func(_ string, _ *User) bool {
			counted++
			return true
		})
		if reported != counted {
			t.Errorf("[%s] Size()=%d but ForEach counted %d — invariant broken", label, reported, counted)
		}
	}

	checkInvariant("after 8 inserts")
	ul.Delete(pks[0])
	ul.Delete(pks[7])
	checkInvariant("after 2 deletes")
	ul.Set(pks[0], NewUser(UserID(100), false, true)) // re-insert
	checkInvariant("after re-insert")
	ul.Reset()
	checkInvariant("after Reset")
}

// TestGUC_UserList_GetByIDAfterDelete verifies that GetByID finds no match once
// the owning passkey has been removed from the list.
func TestGUC_UserList_GetByIDAfterDelete(t *testing.T) {
	ul := NewUserList()
	const pk = "getbyid_pk_aaaa1111bbbb2222cccc3"
	targetID := UserID(77)
	ul.Set(pk, NewUser(targetID, true, false))

	// Sanity: must be found before Delete.
	if u, ok := ul.GetByID(targetID); !ok || u == nil {
		t.Fatal("GetByID could not find user before Delete")
	}

	ul.Delete(pk)

	// Must not be found after Delete.
	got, ok := ul.GetByID(targetID)
	if ok {
		t.Errorf("GetByID returned ok=true after Delete of the owning passkey")
	}
	if got != nil {
		t.Errorf("GetByID returned non-nil user after Delete: %+v", got)
	}
}
