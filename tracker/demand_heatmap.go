package tracker

import (
	"sync"
	"time"
)

// DemandHeatmap tracks recent announce demand per artifact.
// It maintains a fixed-size ring buffer of the last N announce records,
// and an ASN-prefix histogram for rapid modal network computation.
//
// The ring buffer rolls over oldest entries when full; the histogram
// is updated atomically on each record and decremented on eviction.
//
// Safe for concurrent use.
type DemandHeatmap struct {
	mu   sync.Mutex
	ring []demandRecord
	head int
	size int

	// netCount maps a uint16 /16 network key to the count of recent announces.
	// Key = (ip[0]<<8 | ip[1]) — the first two octets of the source IPv4.
	netCount map[uint16]int
	total    int
}

type demandRecord struct {
	net uint16 // first 2 octets of announce source IP
	at  time.Time
}

// NewDemandHeatmap creates a ring buffer of capacity cap.
// cap = 0 disables recording (noop heatmap).
func NewDemandHeatmap(cap int) *DemandHeatmap {
	if cap <= 0 {
		cap = 0
	}
	return &DemandHeatmap{
		ring:     make([]demandRecord, cap),
		netCount: make(map[uint16]int),
	}
}

// Record registers one announce from clientIP. Non-blocking path: lock is held
// briefly. Callers on the hot announce path should call this in a goroutine.
func (h *DemandHeatmap) Record(clientIP []byte) {
	if len(h.ring) == 0 || len(clientIP) < 2 {
		return
	}
	net := uint16(clientIP[0])<<8 | uint16(clientIP[1])
	now := time.Now()

	h.mu.Lock()
	defer h.mu.Unlock()

	// Evict the slot being overwritten (if the ring is full).
	if h.size == len(h.ring) {
		old := h.ring[h.head]
		if h.netCount[old.net] > 1 {
			h.netCount[old.net]--
		} else {
			delete(h.netCount, old.net)
		}
		h.total--
	} else {
		h.size++
	}

	h.ring[h.head] = demandRecord{net: net, at: now}
	h.head = (h.head + 1) % len(h.ring)
	h.netCount[net]++
	h.total++
}

// ModalNet returns the most frequently seen /16 network prefix in the window,
// and true if at least one record exists. Returns 0, false when empty.
func (h *DemandHeatmap) ModalNet() (uint16, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total == 0 {
		return 0, false
	}
	var best uint16
	var bestCount int
	for net, count := range h.netCount {
		if count > bestCount {
			bestCount = count
			best = net
		}
	}
	return best, true
}

// TotalCount returns the number of records currently in the window.
func (h *DemandHeatmap) TotalCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total
}

// NetFraction returns the fraction of demand from the given /16 net key.
func (h *DemandHeatmap) NetFraction(net uint16) float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total == 0 {
		return 0
	}
	return float64(h.netCount[net]) / float64(h.total)
}

// NetAffinity returns a [0,1] proximity score between a node /16 key and the
// modal demand network.
//
//	1.0 = same /16 network
//	0.5 = same /8 (first octet only)
//	0.0 = no match
func NetAffinity(nodeNet, demandNet uint16) float64 {
	if nodeNet == demandNet {
		return 1.0
	}
	if nodeNet>>8 == demandNet>>8 {
		return 0.5
	}
	return 0.0
}

// NetKeyFromIP converts the first four bytes of an IPv4 address to the /16 key.
func NetKeyFromIP(ip []byte) uint16 {
	if len(ip) < 2 {
		return 0
	}
	return uint16(ip[0])<<8 | uint16(ip[1])
}
