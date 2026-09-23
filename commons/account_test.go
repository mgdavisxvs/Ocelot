package commons

import (
	"testing"
)

func TestAccountAvailable(t *testing.T) {
	a := &Account{
		Balance:  FromCC(1000),
		Reserved: FromCC(200),
	}
	if a.Available() != FromCC(800) {
		t.Fatalf("expected 800 CC available, got %s", a.Available())
	}
}

func TestAccountAvailableNeverNegative(t *testing.T) {
	a := &Account{
		Balance:  FromCC(100),
		Reserved: FromCC(500), // over-reserved (shouldn't happen but must be safe)
	}
	if a.Available() != 0 {
		t.Fatalf("available should be 0 when reserved > balance, got %s", a.Available())
	}
}

func TestAccountCanAffordProtected(t *testing.T) {
	a := &Account{
		Balance:       0,
		PriorityClass: P0Critical,
	}
	// P0 can always afford anything.
	if !a.CanAfford(FromCC(1_000_000)) {
		t.Fatal("P0 should always be able to afford")
	}
}

func TestAccountCanAffordP2(t *testing.T) {
	a := &Account{
		Balance:       FromCC(100),
		PriorityClass: P2Standard,
	}
	if !a.CanAfford(FromCC(50)) {
		t.Fatal("P2 with sufficient balance should afford")
	}
	if a.CanAfford(FromCC(200)) {
		t.Fatal("P2 with insufficient balance should not afford")
	}
}

func TestBudgetRemaining(t *testing.T) {
	b := &Budget{
		MaxCredits: FromCC(1000),
		Consumed:   FromCC(300),
	}
	if b.Remaining() != FromCC(700) {
		t.Fatalf("expected 700 CC remaining, got %s", b.Remaining())
	}
}

func TestBudgetExhausted(t *testing.T) {
	b := &Budget{
		MaxCredits: FromCC(100),
		Consumed:   FromCC(100),
	}
	if !b.IsExhausted() {
		t.Fatal("budget should be exhausted")
	}
	if b.CanAccept(FromCC(1)) {
		t.Fatal("exhausted budget should not accept more charges")
	}
}

func TestBudgetCanAccept(t *testing.T) {
	b := &Budget{
		MaxCredits: FromCC(100),
		Consumed:   FromCC(80),
	}
	if !b.CanAccept(FromCC(20)) {
		t.Fatal("should accept exactly remaining")
	}
	if b.CanAccept(FromCC(21)) {
		t.Fatal("should not accept more than remaining")
	}
}

func TestReputationDiscount(t *testing.T) {
	cases := []struct {
		reputation int64
		wantMillis int64
	}{
		{ReputationDefault, 1000}, // no change at default
		{ReputationMax, 800},      // 20% discount at perfect reputation
		{0, 1200},                 // 20% surcharge at zero reputation
		{5000, 1000},              // midpoint → 1000
	}
	for _, tc := range cases {
		got := ReputationDiscount(tc.reputation)
		if got != tc.wantMillis {
			t.Errorf("ReputationDiscount(%d) = %d, want %d", tc.reputation, got, tc.wantMillis)
		}
	}
}

func TestPriorityClassIsValid(t *testing.T) {
	for _, pc := range []PriorityClass{P0Critical, P1Guaranteed, P2Standard, P3Opportunistic} {
		if !pc.IsValid() {
			t.Errorf("%s should be valid", pc)
		}
	}
	if PriorityClass(99).IsValid() {
		t.Fatal("out-of-range priority class should not be valid")
	}
}

func TestPriorityClassOutranks(t *testing.T) {
	if !P0Critical.Outranks(P1Guaranteed) {
		t.Fatal("P0 should outrank P1")
	}
	if P2Standard.Outranks(P1Guaranteed) {
		t.Fatal("P2 should not outrank P1")
	}
}

func TestErrBudgetExhaustedMessage(t *testing.T) {
	tid := uint32(42)
	e := &ErrBudgetExhausted{
		UserID:    1,
		TorrentID: &tid,
		Requested: FromCC(100),
		Remaining: FromCC(0),
	}
	msg := e.Error()
	if msg == "" {
		t.Fatal("error message should not be empty")
	}
}

func TestAccountLoadUpsert(t *testing.T) {
	db := newTestDB(t)

	a := &Account{
		UserID:        77,
		Balance:       FromCC(500),
		Allocated:     FromCC(1000),
		Reserved:      0,
		Reputation:    ReputationDefault,
		PriorityClass: P2Standard,
		CreatedAt:     1000,
		UpdatedAt:     1000,
	}
	if err := UpsertAccount(db, a); err != nil {
		t.Fatalf("upsert account: %v", err)
	}

	loaded, err := LoadAccount(db, 77)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected account, got nil")
	}
	if loaded.Balance != FromCC(500) {
		t.Errorf("expected balance 500 CC, got %s", loaded.Balance)
	}
	if loaded.PriorityClass != P2Standard {
		t.Errorf("expected P2Standard, got %s", loaded.PriorityClass)
	}
}

func TestLoadAccountNotFound(t *testing.T) {
	db := newTestDB(t)
	a, err := LoadAccount(db, 99999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a != nil {
		t.Fatal("expected nil for non-existent account")
	}
}

func TestErrInsufficientBalance_Message(t *testing.T) {
	err := &ErrInsufficientBalance{
		UserID:    42,
		Required:  FromCC(100),
		Available: FromCC(10),
	}
	msg := err.Error()
	if msg == "" {
		t.Error("expected non-empty error message")
	}
	if err.Error() == err.Error()[:0] {
		t.Error("Error() returned empty string")
	}
}

func TestDefaultWorkloadEconomics(t *testing.T) {
	we := DefaultWorkloadEconomics(77)
	if we.AccountUserID != 77 {
		t.Errorf("AccountUserID = %d, want 77", we.AccountUserID)
	}
	if we.PriorityClass != P2Standard {
		t.Errorf("PriorityClass = %s, want P2Standard", we.PriorityClass)
	}
	if !we.Preemptible {
		t.Error("default should be preemptible")
	}
}

func TestWouldAcceptPrice_WithinCap(t *testing.T) {
	we := DefaultWorkloadEconomics(1)
	// Default cap is 200 CC/GB for downloads.
	if !we.WouldAcceptPrice(ResourceDownload, FromCC(100)) {
		t.Error("100 CC should be within 200 CC cap")
	}
	if we.WouldAcceptPrice(ResourceDownload, FromCC(201)) {
		t.Error("201 CC should exceed 200 CC cap")
	}
}

func TestWouldAcceptPrice_NoCap(t *testing.T) {
	we := &WorkloadEconomics{
		AccountUserID: 2,
		PriorityClass: P2Standard,
	}
	// No cap set → always accept.
	if !we.WouldAcceptPrice(ResourceDownload, FromCC(99999)) {
		t.Error("no-cap workload should accept any price")
	}
}
