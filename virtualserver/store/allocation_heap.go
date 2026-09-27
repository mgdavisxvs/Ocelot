package store

import "container/heap"

// allocationHeapItem is an entry in the AllocationHeap priority queue.
type allocationHeapItem struct {
	alloc Allocation
	index int // maintained by heap.Interface for O(1) Remove
}

// allocationHeapImpl implements heap.Interface, ordered by RAMMiB ascending
// so heap[0] always holds the smallest active allocation.
type allocationHeapImpl []*allocationHeapItem

func (h allocationHeapImpl) Len() int            { return len(h) }
func (h allocationHeapImpl) Less(i, j int) bool  { return h[i].alloc.RAMMiB < h[j].alloc.RAMMiB }
func (h allocationHeapImpl) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *allocationHeapImpl) Push(x interface{}) {
	item := x.(*allocationHeapItem)
	item.index = len(*h)
	*h = append(*h, item)
}
func (h *allocationHeapImpl) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}

// AllocationHeap is a min-heap of active allocations ordered by RAM size.
// It provides O(log n) Push/Pop and O(1) Peek, enabling the scheduler to
// identify the node's smallest live allocation without scanning the full table.
type AllocationHeap struct {
	h    allocationHeapImpl
	byID map[int64]*allocationHeapItem // keyed by Allocation.ID for O(log n) remove
}

// NewAllocationHeap returns an empty AllocationHeap.
func NewAllocationHeap() *AllocationHeap {
	return &AllocationHeap{byID: make(map[int64]*allocationHeapItem)}
}

// Push adds an allocation to the heap.
func (ah *AllocationHeap) Push(a Allocation) {
	item := &allocationHeapItem{alloc: a}
	heap.Push(&ah.h, item)
	ah.byID[a.ID] = item
}

// Pop removes and returns the allocation with the smallest RAMMiB.
func (ah *AllocationHeap) Pop() (Allocation, bool) {
	if len(ah.h) == 0 {
		return Allocation{}, false
	}
	item := heap.Pop(&ah.h).(*allocationHeapItem)
	delete(ah.byID, item.alloc.ID)
	return item.alloc, true
}

// Peek returns the smallest allocation without removing it.
func (ah *AllocationHeap) Peek() (Allocation, bool) {
	if len(ah.h) == 0 {
		return Allocation{}, false
	}
	return ah.h[0].alloc, true
}

// Remove removes an allocation by ID in O(log n).
func (ah *AllocationHeap) Remove(id int64) bool {
	item, ok := ah.byID[id]
	if !ok {
		return false
	}
	heap.Remove(&ah.h, item.index)
	delete(ah.byID, id)
	return true
}

// Len returns the number of allocations in the heap.
func (ah *AllocationHeap) Len() int { return len(ah.h) }

// LoadFromStore populates the heap from the store's active allocations for
// a given node. Existing heap contents are discarded.
func (ah *AllocationHeap) LoadFromStore(allocs []Allocation) {
	ah.h = ah.h[:0]
	ah.byID = make(map[int64]*allocationHeapItem, len(allocs))
	for _, a := range allocs {
		item := &allocationHeapItem{alloc: a, index: len(ah.h)}
		ah.h = append(ah.h, item)
		ah.byID[a.ID] = item
	}
	heap.Init(&ah.h)
}
