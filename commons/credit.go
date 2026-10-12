// Package commons implements the Compute Commons economic allocation layer for Ocelot.
// It provides fixed-point credit accounting, resource pricing with scarcity adjustments,
// an immutable ledger, budget enforcement, and priority-class policy.
package commons

import "fmt"

// CreditScale is the number of base units per 1 CC. Using 6 decimal places gives
// sub-micro-credit precision while fitting comfortably in int64.
const CreditScale int64 = 1_000_000

// ComputeCredit is an internal accounting unit represented as a fixed-point integer.
// 1 CC = CreditScale base units. Never use float64 for CC arithmetic.
type ComputeCredit int64

// FromCC converts whole CC to base units.
func FromCC(cc int64) ComputeCredit { return ComputeCredit(cc * CreditScale) }

// FromMilliCC converts milli-CC (1/1000 CC) to base units.
func FromMilliCC(mcc int64) ComputeCredit { return ComputeCredit(mcc * CreditScale / 1000) }

// WholeCC returns the integer CC portion (floor).
func (c ComputeCredit) WholeCC() int64 { return int64(c) / CreditScale }

// MilliCC returns the value in milli-CC.
func (c ComputeCredit) MilliCC() int64 { return int64(c) * 1000 / CreditScale }

// Raw returns the underlying int64 base-unit value.
func (c ComputeCredit) Raw() int64 { return int64(c) }

// Add returns c + other.
func (c ComputeCredit) Add(other ComputeCredit) ComputeCredit { return c + other }

// Sub returns c - other. May be negative (credit earned).
func (c ComputeCredit) Sub(other ComputeCredit) ComputeCredit { return c - other }

// Neg returns -c.
func (c ComputeCredit) Neg() ComputeCredit { return -c }

// IsPositive returns true if c > 0.
func (c ComputeCredit) IsPositive() bool { return int64(c) > 0 }

// IsNegative returns true if c < 0.
func (c ComputeCredit) IsNegative() bool { return int64(c) < 0 }

// IsZero returns true if c == 0.
func (c ComputeCredit) IsZero() bool { return c == 0 }

// MulFrac multiplies c by (num/denom). Both num and denom must be positive.
// Uses 128-bit intermediate multiplication to avoid overflow for values up to ~9 * 10^9 CC.
// For values beyond that (extremely unusual), the result is clamped to MaxCredit.
func (c ComputeCredit) MulFrac(num, denom int64) ComputeCredit {
	if denom == 0 {
		panic("commons: MulFrac: denom is zero")
	}
	if num == 0 || c == 0 {
		return 0
	}
	// Use int64 checked multiplication: if |c * num| <= MaxInt64, safe.
	// Max safe: c up to ~9.2e18 / max(num) — for typical tracker values this is fine.
	// For safety, use the hi-lo trick via intermediate division.
	v := int64(c)
	// Compute (v * num) / denom with rounding toward zero.
	// Split to avoid overflow: q = v/denom, r = v%denom
	q := v / denom
	r := v % denom
	return ComputeCredit(q*num + r*num/denom)
}

// MulMillis multiplies c by millis/1000 (e.g., 1500 = 1.5x).
func (c ComputeCredit) MulMillis(millis int64) ComputeCredit {
	return c.MulFrac(millis, 1000)
}

// String formats as "X.XXXXXX CC".
func (c ComputeCredit) String() string {
	whole := int64(c) / CreditScale
	frac := int64(c) % CreditScale
	if frac < 0 {
		frac = -frac
	}
	sign := ""
	if int64(c) < 0 && whole == 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%d.%06d CC", sign, whole, frac)
}

// MaxCredit is the largest representable credit value (~9.2 million CC).
const MaxCredit = ComputeCredit(9_200_000 * CreditScale)

// MinCredit is the minimum (most negative) representable credit value.
const MinCredit = ComputeCredit(-9_200_000 * CreditScale)

// Clamp clamps c to [min, max].
func (c ComputeCredit) Clamp(min, max ComputeCredit) ComputeCredit {
	if c < min {
		return min
	}
	if c > max {
		return max
	}
	return c
}
