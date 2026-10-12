package tracker

import (
	"strings"
	"sync"
	"sync/atomic"
)

const defaultBusBuffer = 8192

// Handler is a function that receives a published event. It must not block.
type Handler func(Event)

// subscription holds a topic pattern and its handler.
type subscription struct {
	pattern string
	handler Handler
}

// Bus is the in-process event bus. All methods are goroutine-safe.
// Publish never blocks — if the internal channel is full, the event is dropped
// and the drop counter is incremented.
type Bus struct {
	mu    sync.RWMutex
	subs  []subscription
	ch    chan Event
	drops atomic.Uint64
	quit  chan struct{}
	wg    sync.WaitGroup
}

// NewBus creates and starts an in-process event bus with the given buffer size.
// If bufSize <= 0 the default (8192) is used.
func NewBus(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = defaultBusBuffer
	}
	b := &Bus{
		ch:   make(chan Event, bufSize),
		quit: make(chan struct{}),
	}
	b.wg.Add(1)
	go b.dispatch()
	return b
}

// Subscribe registers a handler for events whose topic matches the given pattern.
// Pattern supports exact match or a trailing ".*" wildcard, e.g. "anomaly.*".
func (b *Bus) Subscribe(pattern string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, subscription{pattern: pattern, handler: handler})
}

// Publish enqueues an event for dispatch. Non-blocking: drops on full channel.
func (b *Bus) Publish(e Event) {
	select {
	case b.ch <- e:
	default:
		drops := b.drops.Add(1)
		// Best-effort: try to enqueue a drop notification without blocking.
		select {
		case b.ch <- NewBusDropEvent(e.Topic(), drops):
		default:
		}
	}
}

// Drops returns the total number of events dropped since startup.
func (b *Bus) Drops() uint64 { return b.drops.Load() }

// Stop drains the channel and waits for the dispatch goroutine to exit.
func (b *Bus) Stop() {
	close(b.quit)
	b.wg.Wait()
}

func (b *Bus) dispatch() {
	defer b.wg.Done()
	for {
		select {
		case e := <-b.ch:
			b.deliver(e)
		case <-b.quit:
			// Drain remaining events before exit.
			for {
				select {
				case e := <-b.ch:
					b.deliver(e)
				default:
					return
				}
			}
		}
	}
}

func (b *Bus) deliver(e Event) {
	b.mu.RLock()
	subs := b.subs
	b.mu.RUnlock()
	topic := e.Topic()
	for _, s := range subs {
		if topicMatches(s.pattern, topic) {
			s.handler(e)
		}
	}
}

// topicMatches returns true when pattern matches topic.
// Supports exact match and trailing-wildcard "prefix.*".
func topicMatches(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		prefix := pattern[:len(pattern)-2]
		return strings.HasPrefix(topic, prefix+".")
	}
	return false
}
