package tracker

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// EventType identifies the kind of tracker event.
type EventType string

const (
	EventAnnounce        EventType = "announce"
	EventSnatch          EventType = "snatch"
	EventPeerJoined      EventType = "peer_joined"
	EventPeerLeft        EventType = "peer_left"
	EventTorrentAdded    EventType = "torrent_added"
	EventTorrentRemoved  EventType = "torrent_removed"
	EventTorrentUpdated  EventType = "torrent_updated"
	EventUserAdded       EventType = "user_added"
	EventUserRemoved     EventType = "user_removed"
	EventPasskeyChanged  EventType = "passkey_changed"
	EventWhitelistAdded   EventType = "whitelist_added"
	EventWhitelistRemoved EventType = "whitelist_removed"
	EventTorrentHealth   EventType = "torrent_health"
)

// Event is a single tracker event emitted to the bus.
type Event struct {
	Type    EventType   `json:"type"`
	Payload interface{} `json:"payload"`
	Time    time.Time   `json:"time"`
}

// JSON serializes the event to JSON. Returns nil on marshal error.
func (e Event) JSON() []byte {
	b, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	return b
}

// ── Payload types ─────────────────────────────────────────────────────────────

type AnnounceEventPayload struct {
	InfoHash   string `json:"info_hash"`
	UserID     uint32 `json:"user_id"`
	Event      string `json:"event"`
	Left       int64  `json:"left"`
	Uploaded   int64  `json:"uploaded"`
	Downloaded int64  `json:"downloaded"`
}

type SnatchEventPayload struct {
	InfoHash string `json:"info_hash"`
	UserID   uint32 `json:"user_id"`
}

type PeerEventPayload struct {
	InfoHash string `json:"info_hash"`
	UserID   uint32 `json:"user_id"`
	IP       string `json:"ip"`
	Port     uint16 `json:"port"`
	Seeder   bool   `json:"seeder"`
}

type TorrentEventPayload struct {
	InfoHash string `json:"info_hash"`
	ID       uint32 `json:"id"`
	FreeType uint8  `json:"free_type,omitempty"`
}

type UserEventPayload struct {
	UserID  uint32 `json:"user_id"`
	Passkey string `json:"passkey,omitempty"`
}

type PasskeyChangedPayload struct {
	UserID     uint32 `json:"user_id"`
	OldPasskey string `json:"old_passkey"`
	NewPasskey string `json:"new_passkey"`
}

type WhitelistEventPayload struct {
	Prefix string `json:"prefix"`
}

// ── EventBus ──────────────────────────────────────────────────────────────────

// EventBus is an in-process pub/sub bus. Each subscriber gets an independent
// buffered channel; slow readers are dropped (non-blocking send) rather than
// blocking the announce hot-path.
type EventBus struct {
	mu   sync.RWMutex
	subs map[string]chan Event
}

func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[string]chan Event)}
}

// Subscribe registers a new subscriber with id and returns its event channel.
// If id already exists the existing channel is returned.
func (b *EventBus) Subscribe(id string, bufSize int) chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		return ch
	}
	ch := make(chan Event, bufSize)
	b.subs[id] = ch
	return ch
}

// Unsubscribe removes the subscriber and closes its channel.
func (b *EventBus) Unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		close(ch)
		delete(b.subs, id)
	}
}

// Publish fans out e to every registered subscriber. Non-blocking: a subscriber
// whose buffer is full is skipped with a log warning.
func (b *EventBus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for id, ch := range b.subs {
		select {
		case ch <- e:
		default:
			log.Printf("event_bus: subscriber %q too slow, dropping %s event", id, e.Type)
		}
	}
}

// SubscriberCount returns the number of currently registered subscribers.
func (b *EventBus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
