package tracker

import (
	"encoding/json"
	"sync"
	"time"
)

// LogEntry is one record in the causal replay log.
type LogEntry struct {
	Seq     uint64          `json:"seq"`
	Topic   string          `json:"topic"`
	TraceID string          `json:"trace_id"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// CausalLog (C-02) is a bounded ring buffer of events ordered by Lamport
// sequence numbers.  It integrates with NodeGossip via Merge to provide
// causal replay for anti-entropy replication: a sibling tracker can call
// Since(seq) to receive all entries it missed and replay them in order.
//
// Merge implements last-writer-wins per Seq so duplicate deliveries are idempotent.
//
// CausalLog is safe for concurrent use.
type CausalLog struct {
	mu      sync.RWMutex
	ring    []LogEntry
	cap     int
	head    int // next write position (ring[head] is oldest when full)
	size    int
	highSeq uint64 // highest Seq ever stored
}

// NewCausalLog creates a log with the given ring-buffer capacity.
// Minimum capacity is 64; default is 4096.
func NewCausalLog(capacity int) *CausalLog {
	if capacity < 64 {
		capacity = 64
	}
	return &CausalLog{
		ring: make([]LogEntry, capacity),
		cap:  capacity,
	}
}

// Append adds an event to the log.  The entry's Payload is serialised from the
// concrete event value via JSON; marshal failures are silently dropped.
func (l *CausalLog) Append(e Event) {
	payload, _ := json.Marshal(e)
	entry := LogEntry{
		Seq:     e.Seq(),
		Topic:   e.Topic(),
		TraceID: e.TraceID(),
		At:      e.OccurredAt(),
		Payload: payload,
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring[l.head] = entry
	l.head = (l.head + 1) % l.cap
	if l.size < l.cap {
		l.size++
	}
	if entry.Seq > l.highSeq {
		l.highSeq = entry.Seq
	}
}

// Since returns all entries with Seq > afterSeq in causal order.
// Returns an empty slice when all known entries are already covered.
func (l *CausalLog) Since(afterSeq uint64) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.size == 0 || l.highSeq <= afterSeq {
		return nil
	}
	out := make([]LogEntry, 0, l.size)
	start := (l.head - l.size + l.cap) % l.cap
	for i := 0; i < l.size; i++ {
		e := l.ring[(start+i)%l.cap]
		if e.Seq > afterSeq {
			out = append(out, e)
		}
	}
	return out
}

// Merge applies remote entries from a gossip peer.  Uses last-writer-wins per
// Seq so re-delivered entries are idempotent.
func (l *CausalLog) Merge(remote []LogEntry) {
	if len(remote) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Build a set of Seqs already in the ring to skip duplicates.
	known := make(map[uint64]struct{}, l.size)
	start := (l.head - l.size + l.cap) % l.cap
	for i := 0; i < l.size; i++ {
		known[l.ring[(start+i)%l.cap].Seq] = struct{}{}
	}
	for _, e := range remote {
		if _, dup := known[e.Seq]; dup {
			continue
		}
		l.ring[l.head] = e
		l.head = (l.head + 1) % l.cap
		if l.size < l.cap {
			l.size++
		}
		if e.Seq > l.highSeq {
			l.highSeq = e.Seq
		}
		known[e.Seq] = struct{}{}
	}
}

// HighSeq returns the highest Lamport sequence number in the log.
func (l *CausalLog) HighSeq() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.highSeq
}

// Len returns the number of entries currently stored.
func (l *CausalLog) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.size
}

// BusSubscriber returns a func(Event) that appends every event to this log.
// Pass it to Bus.Subscribe to wire the log into the event bus automatically.
func (l *CausalLog) BusSubscriber() func(Event) {
	return func(e Event) { l.Append(e) }
}
