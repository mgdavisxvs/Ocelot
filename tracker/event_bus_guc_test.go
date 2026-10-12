package tracker

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── Knuth: algorithmic correctness, loop invariants, data structure invariants

// TestGUC_Subscribe_BufferedChannelCreated verifies that Subscribe returns a
// channel whose buffer capacity exactly matches the requested bufSize (Knuth
// invariant: data structure is initialised with the specified capacity).
func TestGUC_Subscribe_BufferedChannelCreated(t *testing.T) {
	bus := NewEventBus()
	cases := []struct {
		id   string
		size int
	}{
		{"s1", 1},
		{"s10", 10},
		{"s100", 100},
	}
	for _, tc := range cases {
		ch := bus.Subscribe(tc.id, tc.size)
		if cap(ch) != tc.size {
			t.Errorf("Subscribe(%q, %d): channel capacity = %d, want %d",
				tc.id, tc.size, cap(ch), tc.size)
		}
	}
}

// TestGUC_Subscribe_DuplicateReturnsSameChannel verifies that re-subscribing
// with the same ID returns the identical channel object, even when a different
// bufSize is requested (Knuth invariant: idempotent map insertion).
func TestGUC_Subscribe_DuplicateReturnsSameChannel(t *testing.T) {
	bus := NewEventBus()
	ch1 := bus.Subscribe("dup", 4)
	ch2 := bus.Subscribe("dup", 8) // different bufSize — must still return the same ch
	if ch1 != ch2 {
		t.Fatal("Subscribe with duplicate ID returned a different channel")
	}
	if cap(ch1) != 4 {
		t.Errorf("channel capacity changed on duplicate Subscribe: got %d, want 4", cap(ch1))
	}
}

// TestGUC_SubscriberCount_AccuracyAfterOperations verifies that SubscriberCount
// tracks the map size exactly through Subscribe and Unsubscribe calls (Knuth
// loop invariant: count == len(subs) at every step).
func TestGUC_SubscriberCount_AccuracyAfterOperations(t *testing.T) {
	bus := NewEventBus()
	for i := 0; i < 5; i++ {
		bus.Subscribe(itoa(i), 1)
	}
	if got := bus.SubscriberCount(); got != 5 {
		t.Fatalf("after 5 subscribes, count = %d, want 5", got)
	}
	bus.Unsubscribe("2")
	bus.Unsubscribe("4")
	if got := bus.SubscriberCount(); got != 3 {
		t.Fatalf("after 2 unsubscribes, count = %d, want 3", got)
	}
}

// TestGUC_Publish_FanOutToAllSubscribers verifies that a single Publish
// delivers the event to every registered subscriber (Knuth: correctness of the
// fan-out loop — every map entry must be visited exactly once).
func TestGUC_Publish_FanOutToAllSubscribers(t *testing.T) {
	bus := NewEventBus()
	const n = 5
	chs := make([]chan BusEvent, n)
	for i := 0; i < n; i++ {
		chs[i] = bus.Subscribe(itoa(i), 1)
	}
	evt := BusEvent{Type: EventAnnounce, Time: time.Now()}
	bus.Publish(evt)
	for i, ch := range chs {
		select {
		case got := <-ch:
			if got.Type != EventAnnounce {
				t.Errorf("sub %d: got event type %q, want %q", i, got.Type, EventAnnounce)
			}
		default:
			t.Errorf("sub %d: no event received after Publish", i)
		}
	}
}

// TestGUC_BusEvent_JSONRoundTrip verifies that a BusEvent serialised with
// JSON() can be unmarshalled back to a document containing the correct type
// field (Knuth: correctness of the JSON encoding algorithm).
func TestGUC_BusEvent_JSONRoundTrip(t *testing.T) {
	original := BusEvent{
		Type:    EventSnatch,
		Payload: SnatchEventPayload{InfoHash: "abc123", UserID: 42},
		Time:    time.Unix(1700000000, 0).UTC(),
	}
	b := original.JSON()
	if b == nil {
		t.Fatal("JSON() returned nil for a valid event")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v", err)
	}
	got, ok := decoded["type"].(string)
	if !ok || got != string(EventSnatch) {
		t.Errorf("decoded type = %q, want %q", got, EventSnatch)
	}
}

// ── Turing: termination conditions, halting behaviour, decidability ───────────

// TestGUC_Unsubscribe_ChannelClosed verifies that Unsubscribe closes the
// subscriber channel so that consumers can detect the termination signal
// (Turing: subscriber goroutine halts cleanly on close).
func TestGUC_Unsubscribe_ChannelClosed(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe("close-me", 4)
	bus.Unsubscribe("close-me")
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel read returned ok=true after Unsubscribe — channel was not closed")
		}
	default:
		t.Fatal("Unsubscribe did not close the channel (select fell through to default)")
	}
}

// TestGUC_Unsubscribe_NonexistentSafe verifies that calling Unsubscribe for an
// ID that was never registered terminates without panicking (Turing: safe
// halting on empty-domain input).
func TestGUC_Unsubscribe_NonexistentSafe(t *testing.T) {
	bus := NewEventBus()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Unsubscribe on nonexistent ID panicked: %v", r)
		}
	}()
	bus.Unsubscribe("never-registered")
}

// TestGUC_Publish_ZeroSubscribersNoOp verifies that publishing to an empty bus
// terminates immediately without blocking or panicking (Turing: halting on the
// empty domain).
func TestGUC_Publish_ZeroSubscribersNoOp(t *testing.T) {
	bus := NewEventBus()
	done := make(chan struct{})
	go func() {
		defer close(done)
		bus.Publish(BusEvent{Type: EventAnnounce})
	}()
	select {
	case <-done:
		// terminated — correct
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Publish on empty bus did not terminate in time")
	}
}

// TestGUC_SlowSubscriber_DropsEventNonBlocking verifies that when a subscriber
// channel is full the publish returns immediately (event dropped) instead of
// blocking the caller (Turing: bounded termination regardless of downstream
// reader speed).
func TestGUC_SlowSubscriber_DropsEventNonBlocking(t *testing.T) {
	bus := NewEventBus()
	_ = bus.Subscribe("slow", 1)
	// Fill the buffer so the next publish must drop.
	bus.Publish(BusEvent{Type: EventAnnounce})

	done := make(chan struct{})
	go func() {
		defer close(done)
		bus.Publish(BusEvent{Type: EventAnnounce}) // should drop, not block
	}()
	select {
	case <-done:
		// non-blocking drop — correct
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Publish blocked when the subscriber channel was full")
	}
}

// TestGUC_Publish_CompletesDespiteSlowSubscribers verifies that a burst of
// publishes to a permanently full subscriber completes in bounded wall time
// (Turing: worst-case termination bound on N non-blocking sends).
func TestGUC_Publish_CompletesDespiteSlowSubscribers(t *testing.T) {
	bus := NewEventBus()
	_ = bus.Subscribe("blocked", 1)
	bus.Publish(BusEvent{Type: EventAnnounce}) // fill the single slot

	start := time.Now()
	const burst = 200
	for i := 0; i < burst; i++ {
		bus.Publish(BusEvent{Type: EventAnnounce})
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("publishing %d events to a blocked subscriber took %v (want <1s)", burst, elapsed)
	}
}

// ── Church: functional purity, side-effect isolation, referential transparency

// TestGUC_BusEvent_JSONDeterministic verifies that JSON() returns identical
// bytes on every call for the same receiver value (Church: referential
// transparency — same input yields same output).
func TestGUC_BusEvent_JSONDeterministic(t *testing.T) {
	evt := BusEvent{
		Type:    EventPeerJoined,
		Payload: PeerEventPayload{InfoHash: "xyz", UserID: 7, IP: "10.0.0.1", Port: 6881},
		Time:    time.Unix(0, 0).UTC(),
	}
	b1 := evt.JSON()
	b2 := evt.JSON()
	if string(b1) != string(b2) {
		t.Errorf("JSON() not deterministic:\n  call 1: %s\n  call 2: %s", b1, b2)
	}
}

// TestGUC_AllPayloadTypes_Serialize verifies that every payload type defined in
// event_bus.go marshals to non-nil JSON and round-trips without error (Church:
// pure serialisation over all algebraic data constructors).
func TestGUC_AllPayloadTypes_Serialize(t *testing.T) {
	type tc struct {
		name    string
		payload interface{}
	}
	cases := []tc{
		{"AnnounceEventPayload", AnnounceEventPayload{InfoHash: "h1", UserID: 1, Event: "started", Left: 0, Uploaded: 100, Downloaded: 200}},
		{"SnatchEventPayload", SnatchEventPayload{InfoHash: "h2", UserID: 2}},
		{"PeerEventPayload", PeerEventPayload{InfoHash: "h3", UserID: 3, IP: "1.2.3.4", Port: 6881, Seeder: true}},
		{"TorrentEventPayload", TorrentEventPayload{InfoHash: "h4", ID: 4, FreeType: 1}},
		{"UserEventPayload", UserEventPayload{UserID: 5, Passkey: "pk5"}},
		{"PasskeyChangedPayload", PasskeyChangedPayload{UserID: 6, OldPasskey: "old", NewPasskey: "new"}},
		{"WhitelistEventPayload", WhitelistEventPayload{Prefix: "uTorrent"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			evt := BusEvent{Type: EventAnnounce, Payload: tc.payload, Time: time.Now()}
			b := evt.JSON()
			if b == nil {
				t.Fatalf("%s: JSON() returned nil", tc.name)
			}
			var out map[string]interface{}
			if err := json.Unmarshal(b, &out); err != nil {
				t.Fatalf("%s: unmarshal error: %v", tc.name, err)
			}
			if out["payload"] == nil {
				t.Errorf("%s: payload field missing in JSON output", tc.name)
			}
		})
	}
}

// TestGUC_Publish_DoesNotMutateEvent verifies that Publish leaves the BusEvent
// passed by the caller unchanged (Church: side-effect isolation — publish is a
// read-only observer of the event value).
func TestGUC_Publish_DoesNotMutateEvent(t *testing.T) {
	bus := NewEventBus()
	_ = bus.Subscribe("obs", 2)
	evt := BusEvent{
		Type:    EventTorrentAdded,
		Payload: TorrentEventPayload{InfoHash: "immutable", ID: 99},
		Time:    time.Unix(42, 0),
	}
	typeSnapshot := evt.Type
	timeSnapshot := evt.Time
	bus.Publish(evt)
	if evt.Type != typeSnapshot {
		t.Errorf("Publish mutated Type: was %q, now %q", typeSnapshot, evt.Type)
	}
	if evt.Time != timeSnapshot {
		t.Errorf("Publish mutated Time: was %v, now %v", timeSnapshot, evt.Time)
	}
}

// TestGUC_Subscribe_ChannelsAreIndependent verifies that two distinct
// subscribers receive independent channels so that draining one does not affect
// the other (Church: side-effect isolation between independent subscriptions).
func TestGUC_Subscribe_ChannelsAreIndependent(t *testing.T) {
	bus := NewEventBus()
	ch1 := bus.Subscribe("a", 2)
	ch2 := bus.Subscribe("b", 2)
	if ch1 == ch2 {
		t.Fatal("distinct subscriber IDs returned the same channel")
	}
	bus.Publish(BusEvent{Type: EventUserAdded})
	if len(ch1) != 1 {
		t.Errorf("ch1 has %d events, want 1", len(ch1))
	}
	if len(ch2) != 1 {
		t.Errorf("ch2 has %d events, want 1", len(ch2))
	}
	<-ch1 // drain only ch1
	if len(ch2) != 1 {
		t.Errorf("draining ch1 affected ch2 length: got %d, want 1", len(ch2))
	}
}

// TestGUC_BusEvent_JSONReturnsNilOnError verifies that JSON() returns nil when
// the payload cannot be marshalled (Church: total function handling — every
// input must produce a defined output, including the error branch).
func TestGUC_BusEvent_JSONReturnsNilOnError(t *testing.T) {
	// Channels are not JSON-serialisable; json.Marshal returns an error for them.
	evt := BusEvent{Type: EventAnnounce, Payload: make(chan struct{})}
	b := evt.JSON()
	if b != nil {
		t.Errorf("JSON() should return nil for an unmarshalable payload, got %q", b)
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state detection

// TestGUC_Concurrent_RaceFree exercises Subscribe, Publish, and Unsubscribe
// concurrently from multiple goroutines to expose data races when run with
// -race (Gödel: internal state remains consistent under concurrent mutation).
func TestGUC_Concurrent_RaceFree(t *testing.T) {
	bus := NewEventBus()
	const workers = 8
	const ops = 50
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := itoa(i)
			for j := 0; j < ops; j++ {
				ch := bus.Subscribe(id, 4)
				bus.Publish(BusEvent{Type: EventAnnounce})
				select {
				case <-ch:
				default:
				}
				if j%10 == 0 {
					bus.Unsubscribe(id)
				}
			}
			bus.Unsubscribe(id)
		}()
	}
	wg.Wait()
}

// TestGUC_AuditEvent_DroppedLogsError verifies that dropping an audit-class
// event emits a log line containing "ERROR", making the data loss observable
// to operators (Gödel: invariant — audit trail violations must surface).
func TestGUC_AuditEvent_DroppedLogsError(t *testing.T) {
	bus := NewEventBus()
	_ = bus.Subscribe("slow-audit", 1)
	bus.Publish(BusEvent{Type: EventSnatch}) // fill the single buffer slot

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	bus.Publish(BusEvent{Type: EventSnatch}) // must drop and log at ERROR level

	if !strings.Contains(buf.String(), "ERROR") {
		t.Errorf("dropped audit event did not log ERROR; log output: %q", buf.String())
	}
}

// TestGUC_NonAuditEvent_DroppedLogsLessVerbose verifies that dropping a
// non-audit event does NOT emit an ERROR log line, confirming that log severity
// is assigned proportionally to event importance (Gödel: no false escalation of
// non-critical events to error severity).
func TestGUC_NonAuditEvent_DroppedLogsLessVerbose(t *testing.T) {
	bus := NewEventBus()
	_ = bus.Subscribe("slow-noaudit", 1)
	bus.Publish(BusEvent{Type: EventAnnounce}) // fill the buffer

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	bus.Publish(BusEvent{Type: EventAnnounce}) // non-audit drop

	if strings.Contains(buf.String(), "ERROR") {
		t.Errorf("non-audit drop incorrectly logged at ERROR severity; log output: %q", buf.String())
	}
	if buf.Len() == 0 {
		t.Error("expected a log line for non-audit drop, got none")
	}
}

// TestGUC_Subscribe_CapacityInvariantPreserved verifies that the channel
// capacity set on the first Subscribe call is never silently overwritten by
// subsequent re-subscriptions using a different bufSize (Gödel: configuration
// invariant — once established, subscriber capacity is immutable).
func TestGUC_Subscribe_CapacityInvariantPreserved(t *testing.T) {
	bus := NewEventBus()
	const firstBuf = 16
	ch := bus.Subscribe("stable", firstBuf)
	for attempt := 0; attempt < 5; attempt++ {
		returned := bus.Subscribe("stable", attempt+1) // intentionally different sizes
		if cap(returned) != firstBuf {
			t.Fatalf("attempt %d: channel capacity changed from %d to %d after re-subscribe",
				attempt, firstBuf, cap(returned))
		}
		if returned != ch {
			t.Fatalf("attempt %d: re-subscribe returned a different channel object", attempt)
		}
	}
}

// TestGUC_SubscriberCount_ConsistencyUnderConcurrency verifies that
// SubscriberCount never reports a value outside [0, N] while N goroutines
// concurrently subscribe and unsubscribe (Gödel: global consistency invariant
// must hold at every observable moment, not just at quiescence).
func TestGUC_SubscriberCount_ConsistencyUnderConcurrency(t *testing.T) {
	bus := NewEventBus()
	const N = 20
	var anomalies atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := itoa(i)
			bus.Subscribe(id, 2)
			if c := bus.SubscriberCount(); c < 0 || c > N {
				anomalies.Add(1)
			}
			bus.Unsubscribe(id)
			if c := bus.SubscriberCount(); c < 0 || c > N {
				anomalies.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := anomalies.Load(); n > 0 {
		t.Errorf("SubscriberCount returned out-of-range value %d time(s)", n)
	}
}
