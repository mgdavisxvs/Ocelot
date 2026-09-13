package chain

import (
	"math"
	"sync"
)

// Chain is a discrete-time Markov chain with online Bayesian learning
// and exponential temporal decay. Thread-safe.
type Chain struct {
	mu       sync.RWMutex
	n        int
	counts   [][]float64
	decay    float64  // multiplied into counts on each Decay() call
	smoothing float64 // Laplace/Bayesian smoothing α (prior pseudo-count per cell)
}

// New creates a Chain with n states, Laplace-smoothed prior (α=1.0), and
// the given per-epoch decay factor.
func New(n int, decay float64) *Chain {
	return NewWithSmoothing(n, decay, 1.0)
}

// NewWithSmoothing creates a Chain with configurable Bayesian smoothing α.
// α is the prior pseudo-count added to each cell (Laplace: α=1.0; weaker prior: α<1.0).
// α=0 disables smoothing — not recommended for sparse data.
func NewWithSmoothing(n int, decay float64, alpha float64) *Chain {
	if alpha < 0 {
		alpha = 0
	}
	counts := make([][]float64, n)
	for i := range counts {
		counts[i] = make([]float64, n)
		for j := range counts[i] {
			counts[i][j] = alpha
		}
	}
	return &Chain{n: n, counts: counts, decay: decay, smoothing: alpha}
}

func (c *Chain) N() int { return c.n }

// Smoothing returns the configured prior α value.
func (c *Chain) Smoothing() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.smoothing
}

// Observe records a single state transition from → to.
func (c *Chain) Observe(from, to int) {
	if from < 0 || from >= c.n || to < 0 || to >= c.n {
		return
	}
	c.mu.Lock()
	c.counts[from][to]++
	c.mu.Unlock()
}

// P returns the normalized n×n row-stochastic transition matrix.
func (c *Chain) P() [][]float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p := make([][]float64, c.n)
	for i := range p {
		p[i] = make([]float64, c.n)
		var sum float64
		for j := range c.counts[i] {
			sum += c.counts[i][j]
		}
		for j := range c.counts[i] {
			p[i][j] = c.counts[i][j] / sum
		}
	}
	return p
}

// EffectiveSampleCount returns the effective number of real observations for
// state i: Σ_j C[i][j] - n×α. Values < 0 indicate only prior has been seen.
func (c *Chain) EffectiveSampleCount(state int) float64 {
	if state < 0 || state >= c.n {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	var sum float64
	for _, v := range c.counts[state] {
		sum += v
	}
	return sum - float64(c.n)*c.smoothing
}

// EffectiveSampleCounts returns the effective sample count for every state.
func (c *Chain) EffectiveSampleCounts() []float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	esc := make([]float64, c.n)
	prior := float64(c.n) * c.smoothing
	for i := range c.counts {
		var sum float64
		for _, v := range c.counts[i] {
			sum += v
		}
		esc[i] = sum - prior
	}
	return esc
}

// Step advances distribution pi by k steps: returns pi · Pᵏ.
func (c *Chain) Step(pi []float64, k int) []float64 {
	p := c.P()
	cur := make([]float64, c.n)
	copy(cur, pi)
	tmp := make([]float64, c.n)
	for step := 0; step < k; step++ {
		for j := range tmp {
			tmp[j] = 0
		}
		for i := 0; i < c.n; i++ {
			if cur[i] == 0 {
				continue
			}
			for j := 0; j < c.n; j++ {
				tmp[j] += cur[i] * p[i][j]
			}
		}
		cur, tmp = tmp, cur
	}
	return cur
}

// Forecast computes π at each of the given step horizons (e.g. [4, 24, 96, 288]).
// Returns one distribution slice per horizon.
func (c *Chain) Forecast(pi []float64, horizons []int) [][]float64 {
	if len(horizons) == 0 {
		return nil
	}
	// Sort not assumed; compute incrementally by stepping from prior horizon.
	// We reuse the Step machinery, stepping from the previous horizon.
	// Make a sorted copy to step incrementally.
	sorted := make([]int, len(horizons))
	idx := make([]int, len(horizons))
	copy(sorted, horizons)
	for i := range idx {
		idx[i] = i
	}
	// Insertion sort (small n).
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}

	out := make([][]float64, len(horizons))
	cur := make([]float64, len(pi))
	copy(cur, pi)
	prev := 0
	for rank, h := range sorted {
		steps := h - prev
		if steps < 0 {
			steps = 0
		}
		cur = c.Step(cur, steps)
		cp := make([]float64, len(cur))
		copy(cp, cur)
		out[idx[rank]] = cp
		prev = h
	}
	return out
}

// Entropy computes Shannon entropy H(π) in bits.
func Entropy(pi []float64) float64 {
	var h float64
	for _, p := range pi {
		if p > 1e-12 {
			h -= p * math.Log2(p)
		}
	}
	return h
}

// PathLogLikelihood returns the negative log-likelihood of observing the
// given path sequence under the current transition matrix. A higher value
// indicates a more anomalous trajectory. Returns +∞ for impossible paths.
func (c *Chain) PathLogLikelihood(path []int) float64 {
	if len(path) < 2 {
		return 0
	}
	p := c.P()
	var nll float64
	for t := 0; t < len(path)-1; t++ {
		prob := p[path[t]][path[t+1]]
		if prob < 1e-12 {
			return math.Inf(1)
		}
		nll -= math.Log(prob)
	}
	return nll
}

// NormalizedPathNLL returns the per-transition NLL: PathLogLikelihood / (len-1).
// This normalizes for path length, enabling fair comparison across users.
// Returns 0 for paths shorter than 2.
func (c *Chain) NormalizedPathNLL(path []int) float64 {
	if len(path) < 2 {
		return 0
	}
	nll := c.PathLogLikelihood(path)
	if math.IsInf(nll, 1) {
		return nll
	}
	return nll / float64(len(path)-1)
}

// BrierScore returns the Brier score for a single forecast.
// pi is the predicted distribution; actual is the observed state index.
// BrierScore = Σ_i (p_i - o_i)² where o_i = 1 iff i == actual.
func BrierScore(pi []float64, actual int) float64 {
	var score float64
	for i, p := range pi {
		o := 0.0
		if i == actual {
			o = 1.0
		}
		d := p - o
		score += d * d
	}
	return score
}

// LogLoss returns the log loss for a single forecast.
// LogLoss = -log(p[actual]), clamped to prevent log(0).
func LogLoss(pi []float64, actual int) float64 {
	if actual < 0 || actual >= len(pi) {
		return math.Inf(1)
	}
	p := math.Max(1e-15, pi[actual])
	return -math.Log(p)
}

// Decay multiplies all counts by the decay factor to down-weight old observations.
// The smoothing prior cells are not re-floored — they decay proportionally to
// the observation mass, preserving the relative prior weight.
func (c *Chain) Decay() {
	c.mu.Lock()
	for i := range c.counts {
		for j := range c.counts[i] {
			c.counts[i][j] *= c.decay
		}
	}
	c.mu.Unlock()
}

// Counts returns a deep copy of the raw counts matrix.
func (c *Chain) Counts() [][]float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([][]float64, c.n)
	for i := range out {
		out[i] = make([]float64, c.n)
		copy(out[i], c.counts[i])
	}
	return out
}

// LoadCounts replaces the counts matrix from persisted storage.
func (c *Chain) LoadCounts(counts [][]float64) {
	c.mu.Lock()
	for i := range counts {
		if i >= c.n {
			break
		}
		for j := range counts[i] {
			if j >= c.n {
				break
			}
			c.counts[i][j] = counts[i][j]
		}
	}
	c.mu.Unlock()
}

// ExpectedAbsorptionSteps returns, for each transient state, the expected
// number of steps until the chain reaches the absorbing state abs.
// Uses the fundamental matrix N = (I − Q)⁻¹; result[abs] = 0.
func (c *Chain) ExpectedAbsorptionSteps(abs int) []float64 {
	p := c.P()
	transient := make([]int, 0, c.n-1)
	for i := range p {
		if i != abs {
			transient = append(transient, i)
		}
	}
	m := len(transient)
	if m == 0 {
		return make([]float64, c.n)
	}

	// Q = sub-matrix of transient states
	q := make([][]float64, m)
	for i := range q {
		q[i] = make([]float64, m)
		for j := range q[i] {
			q[i][j] = p[transient[i]][transient[j]]
		}
	}

	// Solve (I − Q) · t = 1 via Gaussian elimination
	t := solveLinear(identityMinus(q, m), ones(m), m)

	result := make([]float64, c.n)
	for i, ti := range transient {
		result[ti] = t[i]
	}
	return result
}

func identityMinus(q [][]float64, m int) [][]float64 {
	a := make([][]float64, m)
	for i := range a {
		a[i] = make([]float64, m)
		for j := range a[i] {
			if i == j {
				a[i][j] = 1 - q[i][j]
			} else {
				a[i][j] = -q[i][j]
			}
		}
	}
	return a
}

func ones(m int) []float64 {
	v := make([]float64, m)
	for i := range v {
		v[i] = 1
	}
	return v
}

// solveLinear solves A·x = b via Gaussian elimination with partial pivoting.
func solveLinear(a [][]float64, b []float64, m int) []float64 {
	// Augment [A|b]
	aug := make([][]float64, m)
	for i := range aug {
		aug[i] = make([]float64, m+1)
		copy(aug[i], a[i])
		aug[i][m] = b[i]
	}
	for col := 0; col < m; col++ {
		pivot := col
		for row := col + 1; row < m; row++ {
			if math.Abs(aug[row][col]) > math.Abs(aug[pivot][col]) {
				pivot = row
			}
		}
		aug[col], aug[pivot] = aug[pivot], aug[col]
		if math.Abs(aug[col][col]) < 1e-12 {
			continue
		}
		scale := 1.0 / aug[col][col]
		for j := col; j <= m; j++ {
			aug[col][j] *= scale
		}
		for row := 0; row < m; row++ {
			if row == col || aug[row][col] == 0 {
				continue
			}
			factor := aug[row][col]
			for j := col; j <= m; j++ {
				aug[row][j] -= factor * aug[col][j]
			}
		}
	}
	x := make([]float64, m)
	for i := range x {
		x[i] = aug[i][m]
	}
	return x
}
