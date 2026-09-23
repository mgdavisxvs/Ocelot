package tracker

import (
	"testing"
	"time"
)

func TestEventBus_SubscribeAndPublish(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe("sub1", 8)

	e := BusEvent{Type: EventAnnounce, Payload: nil, Time: time.Now()}
	bus.Publish(e)

	select {
	case got := <-ch:
		if got.Type != EventAnnounce {
			t.Fatalf("want EventAnnounce, got %s", got.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := NewEventBus()
	bus.Subscribe("sub1", 8)

	if bus.SubscriberCount() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", bus.SubscriberCount())
	}
	bus.Unsubscribe("sub1")
	if bus.SubscriberCount() != 0 {
		t.Fatalf("expected 0 subscribers, got %d", bus.SubscriberCount())
	}
}

func TestEventBus_SlowSubscriberDrops(t *testing.T) {
	bus := NewEventBus()
	bus.Subscribe("slow", 1) // buffer of 1

	// Fill the buffer
	bus.Publish(BusEvent{Type: EventAnnounce})
	// This second publish must not block even though the buffer is full
	done := make(chan struct{})
	go func() {
		bus.Publish(BusEvent{Type: EventSnatch})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Publish blocked on slow subscriber")
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	bus := NewEventBus()
	ch1 := bus.Subscribe("s1", 8)
	ch2 := bus.Subscribe("s2", 8)
	defer bus.Unsubscribe("s1")
	defer bus.Unsubscribe("s2")

	bus.Publish(BusEvent{Type: EventTorrentAdded})

	for _, ch := range []chan BusEvent{ch1, ch2} {
		select {
		case evt := <-ch:
			if evt.Type != EventTorrentAdded {
				t.Fatalf("want EventTorrentAdded, got %s", evt.Type)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestEventBus_NilBusPublish(t *testing.T) {
	// Worker.publish must not panic when Bus is nil
	w := &Worker{}
	w.publish(BusEvent{Type: EventAnnounce}) // should be a no-op
}

func TestEvent_JSON(t *testing.T) {
	e := BusEvent{Type: EventSnatch, Payload: SnatchEventPayload{InfoHash: "abc", UserID: 42}, Time: time.Now()}
	data := e.JSON()
	if data == nil {
		t.Fatal("expected non-nil JSON")
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}
}
