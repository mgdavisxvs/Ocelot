package commons

import (
	"testing"
)

func TestFromCC(t *testing.T) {
	c := FromCC(100)
	if c.WholeCC() != 100 {
		t.Fatalf("expected 100 CC, got %d", c.WholeCC())
	}
	if c.Raw() != 100*CreditScale {
		t.Fatalf("expected raw %d, got %d", 100*CreditScale, c.Raw())
	}
}

func TestFromMilliCC(t *testing.T) {
	c := FromMilliCC(1500) // 1.5 CC
	if c.WholeCC() != 1 {
		t.Fatalf("expected whole 1, got %d", c.WholeCC())
	}
	if c.MilliCC() != 1500 {
		t.Fatalf("expected 1500 mCC, got %d", c.MilliCC())
	}
}

func TestCreditAddSub(t *testing.T) {
	a := FromCC(100)
	b := FromCC(40)
	if (a + b).WholeCC() != 140 {
		t.Fatal("add wrong")
	}
	if (a - b).WholeCC() != 60 {
		t.Fatal("sub wrong")
	}
}

func TestCreditNeg(t *testing.T) {
	c := FromCC(50)
	neg := c.Neg()
	if neg.Raw() != -50*CreditScale {
		t.Fatalf("expected -%d, got %d", 50*CreditScale, neg.Raw())
	}
	if !neg.IsNegative() {
		t.Fatal("neg should be negative")
	}
}

func TestMulFrac(t *testing.T) {
	cases := []struct {
		value int64
		num   int64
		denom int64
		want  int64 // whole CC result
	}{
		{100 * CreditScale, 1, 2, 50},     // 100 * 1/2 = 50
		{100 * CreditScale, 3, 4, 75},     // 100 * 3/4 = 75
		{1 * CreditScale, 1, 3, 0},        // 1 * 1/3 = 0.333... CC (truncated whole)
		{1000 * CreditScale, 2, 1, 2000},  // 1000 * 2 = 2000
		{0, 5, 10, 0},                     // 0 * anything = 0
	}
	for _, tc := range cases {
		got := ComputeCredit(tc.value).MulFrac(tc.num, tc.denom)
		if got.WholeCC() != tc.want {
			t.Errorf("MulFrac(%d, %d/%d): want %d CC, got %d CC",
				tc.value/CreditScale, tc.num, tc.denom, tc.want, got.WholeCC())
		}
	}
}

func TestMulMillis(t *testing.T) {
	c := FromCC(100)
	// 100 CC * 1500 millis = 150 CC
	got := c.MulMillis(1500)
	if got.WholeCC() != 150 {
		t.Fatalf("expected 150 CC, got %d", got.WholeCC())
	}
}

func TestClamp(t *testing.T) {
	min := FromCC(10)
	max := FromCC(100)
	if FromCC(5).Clamp(min, max) != min {
		t.Fatal("below min should clamp to min")
	}
	if FromCC(200).Clamp(min, max) != max {
		t.Fatal("above max should clamp to max")
	}
	if FromCC(50).Clamp(min, max) != FromCC(50) {
		t.Fatal("in-range should not change")
	}
}

func TestCreditString(t *testing.T) {
	c := FromCC(42)
	s := c.String()
	if s != "42.000000 CC" {
		t.Fatalf("expected '42.000000 CC', got %q", s)
	}

	neg := FromCC(-7)
	if neg.IsPositive() {
		t.Fatal("negative CC should not be positive")
	}
}

func TestNoFloat(t *testing.T) {
	// Verify that 1 CC / 3 does not lose precision beyond what int64 allows.
	c := FromCC(1)
	third := c.MulFrac(1, 3)
	// Should be 333333 base units (0.333333 CC)
	if third.Raw() != 333333 {
		t.Fatalf("expected 333333 base units, got %d", third.Raw())
	}
}

func TestMulFracPanicOnZeroDenom(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for zero denom")
		}
	}()
	FromCC(10).MulFrac(1, 0)
}

func TestAddSub(t *testing.T) {
	a := FromCC(100)
	b := FromCC(40)
	if got := a.Add(b); got != FromCC(140) {
		t.Errorf("Add = %s, want 140 CC", got)
	}
	if got := a.Sub(b); got != FromCC(60) {
		t.Errorf("Sub = %s, want 60 CC", got)
	}
}

func TestIsZero(t *testing.T) {
	if !ComputeCredit(0).IsZero() {
		t.Fatal("zero should be zero")
	}
	if FromCC(1).IsZero() {
		t.Fatal("non-zero should not be zero")
	}
}
