package agent

import (
	"sync"

	"github.com/mgdavisxvs/Ocelot/compute/node"
)

const maxBufferedEvents = 100

// healthBuffer is a bounded event queue (§4.6).
// Events are drained by the health push loop and replaced by the executor.
type healthBuffer struct {
	mu       sync.Mutex
	events   []node.HealthEvent
	overflow bool
}

func newHealthBuffer() *healthBuffer {
	return &healthBuffer{
		events: make([]node.HealthEvent, 0, maxBufferedEvents),
	}
}

// push adds an event to the buffer. If the buffer is full, the oldest event
// is dropped and the overflow flag is set per §4.6.
func (hb *healthBuffer) push(ev node.HealthEvent) {
	hb.mu.Lock()
	defer hb.mu.Unlock()
	if len(hb.events) >= maxBufferedEvents {
		hb.events = hb.events[1:] // drop oldest
		hb.overflow = true
	}
	hb.events = append(hb.events, ev)
}

// drain atomically removes and returns all buffered events plus the overflow flag.
func (hb *healthBuffer) drain() (events []node.HealthEvent, overflow bool) {
	hb.mu.Lock()
	defer hb.mu.Unlock()
	if len(hb.events) == 0 {
		return nil, false
	}
	events = hb.events
	overflow = hb.overflow
	hb.events = make([]node.HealthEvent, 0, maxBufferedEvents)
	hb.overflow = false
	return events, overflow
}
