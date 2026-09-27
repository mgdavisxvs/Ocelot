package ml

import "math"

// WelfordAccum computes online mean and variance using Welford's algorithm.
// O(1) time and O(1) space per update; numerically stable.
//
// RULING-04 / F-05: replaces batch recompute with single-pass online estimator
// so the anomaly detector can track population statistics without buffering
// the full observation history.
type WelfordAccum struct {
	n    int64
	mean float64
	m2   float64
}

// Update incorporates a new observation x.
func (w *WelfordAccum) Update(x float64) {
	w.n++
	delta := x - w.mean
	w.mean += delta / float64(w.n)
	w.m2 += delta * (x - w.mean)
}

// Mean returns the current running mean.  Returns 0 for an empty accumulator.
func (w *WelfordAccum) Mean() float64 { return w.mean }

// Variance returns the sample variance (Bessel-corrected, n−1 denominator).
// Returns 0 for fewer than 2 observations.
func (w *WelfordAccum) Variance() float64 {
	if w.n < 2 {
		return 0
	}
	return w.m2 / float64(w.n-1)
}

// StdDev returns the sample standard deviation.
func (w *WelfordAccum) StdDev() float64 { return math.Sqrt(w.Variance()) }

// ZScore returns the z-score of x relative to the current distribution.
// Returns 0 when standard deviation is zero (all identical observations).
func (w *WelfordAccum) ZScore(x float64) float64 {
	sd := w.StdDev()
	if sd == 0 {
		return 0
	}
	return (x - w.mean) / sd
}

// Count returns the number of observations accumulated so far.
func (w *WelfordAccum) Count() int64 { return w.n }

// Reset clears all accumulated state.
func (w *WelfordAccum) Reset() { w.n = 0; w.mean = 0; w.m2 = 0 }
