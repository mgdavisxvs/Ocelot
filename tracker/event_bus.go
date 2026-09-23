package tracker

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// BusEventType identifies the kind of tracker event on the internal event bus.
type BusEventType string

const (
	EventAnnounce        BusEventType = "announce"
	EventSnatch          BusEventType = "snatch"
	EventPeerJoined      BusEventType = "peer_joined"
	EventPeerLeft        BusEventType = "peer_left"
	EventTorrentAdded    BusEventType = "torrent_added"
	EventTorrentRemoved  BusEventType = "torrent_removed"
	EventTorrentUpdated  BusEventType = "torrent_updated"
	EventUserAdded       BusEventType = "user_added"
	EventUserRemoved     BusEventType = "user_removed"
	EventPasskeyChanged  BusEventType = "passkey_changed"
	EventWhitelistAdded   BusEventType = "whitelist_added"
	EventWhitelistRemoved BusEventType = "whitelist_removed"
	EventTorrentHealth   BusEventType = "torrent_health"
)

// BusEvent is a single tracker event emitted to the internal pub/sub bus.
type BusEvent struct {
	Type    BusEventType `json:"type"`
	Payload interface{}  `json:"payload"`
	Time    time.Time    `json:"time"`
}

// JSON serializes the event to JSON. Returns nil on marshal error.
func (e BusEvent) JSON() []byte {
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
	subs map[string]chan BusEvent
}

func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[string]chan BusEvent)}
}

// Subscribe registers a new subscriber with id and returns its event channel.
// If id already exists the existing channel is returned.
func (b *EventBus) Subscribe(id string, bufSize int) chan BusEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		return ch
	}
	ch := make(chan BusEvent, bufSize)
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
func (b *EventBus) Publish(e BusEvent) {
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
