package tracker

import (
	"math/rand"
	"sync"
)

// F-K2: IntervalSkipList replaces the flat map in IntervalCache with an
// ordered skip list, providing O(log n) average Get/Set/Delete and O(k) ordered
// range iteration without a full-table sort.
//
// Skip list parameters follow Knuth vol. 3 §6.2.2:
//   - Maximum level: 16 (handles 4.3×10⁹ entries at level-skip probability 0.25)
//   - Promotion probability p = 0.25 (balances memory vs. search efficiency)

const (
	skipListMaxLevel = 16
	skipListP        = 0.25
)

type skipNode struct {
	key     TorrentID
	value   int
	forward [skipListMaxLevel]*skipNode
}

// IntervalSkipList is a concurrent, ordered key-value structure for announce
// intervals. Keys are TorrentIDs (uint32); values are interval seconds.
type IntervalSkipList struct {
	mu      sync.RWMutex
	head    *skipNode
	level   int // current highest level in use
	len     int
	rng     *rand.Rand
}

// NewIntervalSkipList creates an empty IntervalSkipList.
func NewIntervalSkipList() *IntervalSkipList {
	head := &skipNode{key: 0, value: 0}
	return &IntervalSkipList{
		head: head,
		rng:  rand.New(rand.NewSource(42)), //nolint:gosec
	}
}

// Set inserts or updates the interval for torrentID. O(log n) average.
func (sl *IntervalSkipList) Set(id TorrentID, interval int) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	update := [skipListMaxLevel]*skipNode{}
	cur := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for cur.forward[i] != nil && cur.forward[i].key < id {
			cur = cur.forward[i]
		}
		update[i] = cur
	}
	next := cur.forward[0]
	if next != nil && next.key == id {
		next.value = interval
		return
	}

	lvl := sl.randomLevel()
	if lvl > sl.level {
		for i := sl.level; i < lvl; i++ {
			update[i] = sl.head
		}
		sl.level = lvl
	}

	n := &skipNode{key: id, value: interval}
	for i := 0; i < lvl; i++ {
		n.forward[i] = update[i].forward[i]
		update[i].forward[i] = n
	}
	sl.len++
}

// Get returns (interval, true) when torrentID is present. O(log n) average.
func (sl *IntervalSkipList) Get(id TorrentID) (int, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	cur := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for cur.forward[i] != nil && cur.forward[i].key < id {
			cur = cur.forward[i]
		}
	}
	cur = cur.forward[0]
	if cur != nil && cur.key == id {
		return cur.value, true
	}
	return 0, false
}

// Delete removes the entry for torrentID. O(log n) average. No-op if absent.
func (sl *IntervalSkipList) Delete(id TorrentID) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	update := [skipListMaxLevel]*skipNode{}
	cur := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for cur.forward[i] != nil && cur.forward[i].key < id {
			cur = cur.forward[i]
		}
		update[i] = cur
	}
	target := cur.forward[0]
	if target == nil || target.key != id {
		return
	}
	for i := 0; i < sl.level; i++ {
		if update[i].forward[i] != target {
			break
		}
		update[i].forward[i] = target.forward[i]
	}
	for sl.level > 1 && sl.head.forward[sl.level-1] == nil {
		sl.level--
	}
	sl.len--
}

// Len returns the number of entries. O(1).
func (sl *IntervalSkipList) Len() int {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.len
}

// RangeByInterval returns all TorrentIDs whose interval ≤ maxSec, in ascending
// TorrentID order. This enables the scheduler to prioritise short-interval
// torrents without sorting the entire cache. O(n) traversal.
func (sl *IntervalSkipList) RangeByInterval(maxSec int) []TorrentID {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	var out []TorrentID
	cur := sl.head.forward[0]
	for cur != nil {
		if cur.value <= maxSec {
			out = append(out, cur.key)
		}
		cur = cur.forward[0]
	}
	return out
}

// InOrderIter calls fn for each (TorrentID, interval) in ascending key order.
// Iteration stops when fn returns false. Held under RLock — fn must not call
// Set/Delete on this list.
func (sl *IntervalSkipList) InOrderIter(fn func(TorrentID, int) bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	cur := sl.head.forward[0]
	for cur != nil {
		if !fn(cur.key, cur.value) {
			return
		}
		cur = cur.forward[0]
	}
}

func (sl *IntervalSkipList) randomLevel() int {
	lvl := 1
	for lvl < skipListMaxLevel && sl.rng.Float64() < skipListP {
		lvl++
	}
	return lvl
}
