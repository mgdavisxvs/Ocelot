package tracker

import (
	"context"
	"errors"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// spanRecorder captures every span the package emits during the test run.
// The global tracer provider can only be swapped in once, so it is installed
// here in TestMain rather than per-test.
var spanRecorder *tracetest.SpanRecorder

func TestMain(m *testing.M) {
	spanRecorder = tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(spanRecorder),
	))
	os.Exit(m.Run())
}

// spanMark records how many spans have finished so far. Pair it with
// spansSince to look at only the spans a single test produced, since the
// recorder has no Reset in this SDK version.
func spanMark() int {
	return len(spanRecorder.Ended())
}

func spansSince(mark int) []sdktrace.ReadOnlySpan {
	ended := spanRecorder.Ended()
	if mark > len(ended) {
		return nil
	}
	return ended[mark:]
}

func findSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("no span named %q among %d recorded spans", name, len(spans))
	return nil
}

func attrValue(t *testing.T, span sdktrace.ReadOnlySpan, key string) attribute.Value {
	t.Helper()
	for _, a := range span.Attributes() {
		if string(a.Key) == key {
			return a.Value
		}
	}
	t.Fatalf("span %q has no attribute %q", span.Name(), key)
	return attribute.Value{}
}

// Span creation

func TestStartSpanCreatesRecordingSpan(t *testing.T) {
	mark := spanMark()

	ctx, span := StartSpan(context.Background(), "test-operation")

	if !span.IsRecording() {
		t.Error("expected span to be recording")
	}

	sc := span.SpanContext()
	if !sc.TraceID().IsValid() {
		t.Error("expected a valid trace ID")
	}
	if !sc.SpanID().IsValid() {
		t.Error("expected a valid span ID")
	}
	if ctx == nil {
		t.Fatal("StartSpan returned a nil context")
	}

	span.End()

	recorded := findSpan(t, spansSince(mark), "test-operation")
	if recorded.SpanContext().SpanID() != sc.SpanID() {
		t.Error("recorded span ID does not match the returned span")
	}
}

func TestStartSpanWithAttributes(t *testing.T) {
	mark := spanMark()

	_, span := StartSpan(context.Background(), "attributed-operation",
		attribute.String("component", "tracker"),
		attribute.Int("retry_count", 3),
		attribute.Bool("cached", true),
	)
	span.End()

	recorded := findSpan(t, spansSince(mark), "attributed-operation")

	if got := attrValue(t, recorded, "component").AsString(); got != "tracker" {
		t.Errorf("component = %q, want %q", got, "tracker")
	}
	if got := attrValue(t, recorded, "retry_count").AsInt64(); got != 3 {
		t.Errorf("retry_count = %d, want 3", got)
	}
	if got := attrValue(t, recorded, "cached").AsBool(); !got {
		t.Error("cached = false, want true")
	}
}

func TestStartSpanWithoutAttributes(t *testing.T) {
	mark := spanMark()

	_, span := StartSpan(context.Background(), "bare-operation")
	span.End()

	recorded := findSpan(t, spansSince(mark), "bare-operation")
	if len(recorded.Attributes()) != 0 {
		t.Errorf("expected no attributes, got %d", len(recorded.Attributes()))
	}
}

// Context propagation

func TestSpanContextPropagation(t *testing.T) {
	mark := spanMark()

	parentCtx, parentSpan := StartSpan(context.Background(), "parent-operation")
	_, childSpan := StartSpan(parentCtx, "child-operation")

	childSpan.End()
	parentSpan.End()

	spans := spansSince(mark)
	parent := findSpan(t, spans, "parent-operation")
	child := findSpan(t, spans, "child-operation")

	if child.SpanContext().TraceID() != parent.SpanContext().TraceID() {
		t.Error("child span is not in the parent's trace")
	}
	if child.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Errorf("child parent span ID = %s, want %s",
			child.Parent().SpanID(), parent.SpanContext().SpanID())
	}
}

func TestSpanFromContextRetrieval(t *testing.T) {
	ctx, span := StartSpan(context.Background(), "retrievable-operation")
	defer span.End()

	fromCtx := trace.SpanFromContext(ctx)

	if fromCtx.SpanContext().SpanID() != span.SpanContext().SpanID() {
		t.Error("trace.SpanFromContext returned a different span than StartSpan")
	}
}

func TestNestedSpanHierarchy(t *testing.T) {
	mark := spanMark()

	ctx, root := StartSpan(context.Background(), "level-0")
	ctx, mid := StartSpan(ctx, "level-1")
	_, leaf := StartSpan(ctx, "level-2")

	leaf.End()
	mid.End()
	root.End()

	spans := spansSince(mark)
	l0 := findSpan(t, spans, "level-0")
	l1 := findSpan(t, spans, "level-1")
	l2 := findSpan(t, spans, "level-2")

	traceID := l0.SpanContext().TraceID()
	if l1.SpanContext().TraceID() != traceID || l2.SpanContext().TraceID() != traceID {
		t.Error("nested spans do not share a single trace ID")
	}
	if l1.Parent().SpanID() != l0.SpanContext().SpanID() {
		t.Error("level-1 is not parented to level-0")
	}
	if l2.Parent().SpanID() != l1.SpanContext().SpanID() {
		t.Error("level-2 is not parented to level-1")
	}
}

func TestRemoteSpanContextPropagation(t *testing.T) {
	mark := spanMark()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("bad trace ID fixture: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("bad span ID fixture: %v", err)
	}

	remote := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), remote)

	_, span := StartSpan(ctx, "continues-remote-trace")
	span.End()

	recorded := findSpan(t, spansSince(mark), "continues-remote-trace")

	if recorded.SpanContext().TraceID() != traceID {
		t.Errorf("trace ID = %s, want %s", recorded.SpanContext().TraceID(), traceID)
	}
	if recorded.Parent().SpanID() != spanID {
		t.Errorf("parent span ID = %s, want %s", recorded.Parent().SpanID(), spanID)
	}
	if !recorded.Parent().IsRemote() {
		t.Error("expected the parent span context to be marked remote")
	}
}

// Tracker-specific span helpers

func TestTraceAnnounceAttributes(t *testing.T) {
	mark := spanMark()

	_, span := TraceAnnounce(context.Background(), "abcdef0123456789abcd", "-qB4380-abcdefghijkl")
	span.End()

	recorded := findSpan(t, spansSince(mark), "announce")

	if got := attrValue(t, recorded, "info_hash").AsString(); got != "abcdef0123456789abcd" {
		t.Errorf("info_hash = %q", got)
	}
	if got := attrValue(t, recorded, "peer_id").AsString(); got != "-qB4380-abcdefghijkl" {
		t.Errorf("peer_id = %q", got)
	}
}

func TestTraceScrapeAttributes(t *testing.T) {
	mark := spanMark()

	hashes := []string{"hash1", "hash2", "hash3"}
	_, span := TraceScrape(context.Background(), hashes)
	span.End()

	recorded := findSpan(t, spansSince(mark), "scrape")

	if got := attrValue(t, recorded, "torrents_count").AsInt64(); got != 3 {
		t.Errorf("torrents_count = %d, want 3", got)
	}
}

func TestTraceScrapeEmptyHashes(t *testing.T) {
	mark := spanMark()

	_, span := TraceScrape(context.Background(), nil)
	span.End()

	recorded := findSpan(t, spansSince(mark), "scrape")
	if got := attrValue(t, recorded, "torrents_count").AsInt64(); got != 0 {
		t.Errorf("torrents_count = %d, want 0", got)
	}
}

func TestTraceDBQueryAttributes(t *testing.T) {
	mark := spanMark()

	_, span := TraceDBQuery(context.Background(), "select_peers")
	span.End()

	recorded := findSpan(t, spansSince(mark), "db.query")
	if got := attrValue(t, recorded, "query_type").AsString(); got != "select_peers" {
		t.Errorf("query_type = %q, want %q", got, "select_peers")
	}
}

func TestTraceRedisOpAttributes(t *testing.T) {
	mark := spanMark()

	_, span := TraceRedisOp(context.Background(), "HGETALL")
	span.End()

	recorded := findSpan(t, spansSince(mark), "redis.op")
	if got := attrValue(t, recorded, "operation").AsString(); got != "HGETALL" {
		t.Errorf("operation = %q, want %q", got, "HGETALL")
	}
}

func TestTracePeerSelectionAttributes(t *testing.T) {
	mark := spanMark()

	_, span := TracePeerSelection(context.Background(), "torrent-42", 50)
	span.End()

	recorded := findSpan(t, spansSince(mark), "peer.selection")

	if got := attrValue(t, recorded, "torrent_id").AsString(); got != "torrent-42" {
		t.Errorf("torrent_id = %q", got)
	}
	if got := attrValue(t, recorded, "num_want").AsInt64(); got != 50 {
		t.Errorf("num_want = %d, want 50", got)
	}
}

func TestTraceHelpersNestUnderAnnounce(t *testing.T) {
	mark := spanMark()

	ctx, announce := TraceAnnounce(context.Background(), "info", "peer")
	_, query := TraceDBQuery(ctx, "record_peer")
	query.End()
	announce.End()

	spans := spansSince(mark)
	parent := findSpan(t, spans, "announce")
	child := findSpan(t, spans, "db.query")

	if child.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Error("db.query span is not nested under the announce span")
	}
}

// Events and errors

func TestAddSpanEvent(t *testing.T) {
	mark := spanMark()

	ctx, span := StartSpan(context.Background(), "event-operation")
	AddSpanEvent(ctx, "peer_list_built", attribute.Int("peer_count", 25))
	span.End()

	recorded := findSpan(t, spansSince(mark), "event-operation")

	if len(recorded.Events()) != 1 {
		t.Fatalf("expected 1 event, got %d", len(recorded.Events()))
	}

	event := recorded.Events()[0]
	if event.Name != "peer_list_built" {
		t.Errorf("event name = %q, want %q", event.Name, "peer_list_built")
	}

	var found bool
	for _, a := range event.Attributes {
		if string(a.Key) == "peer_count" {
			found = true
			if a.Value.AsInt64() != 25 {
				t.Errorf("peer_count = %d, want 25", a.Value.AsInt64())
			}
		}
	}
	if !found {
		t.Error("event is missing the peer_count attribute")
	}
}

func TestAddSpanEventMultiple(t *testing.T) {
	mark := spanMark()

	ctx, span := StartSpan(context.Background(), "multi-event-operation")
	AddSpanEvent(ctx, "first")
	AddSpanEvent(ctx, "second")
	AddSpanEvent(ctx, "third")
	span.End()

	recorded := findSpan(t, spansSince(mark), "multi-event-operation")
	if len(recorded.Events()) != 3 {
		t.Fatalf("expected 3 events, got %d", len(recorded.Events()))
	}

	want := []string{"first", "second", "third"}
	for i, name := range want {
		if recorded.Events()[i].Name != name {
			t.Errorf("event %d = %q, want %q", i, recorded.Events()[i].Name, name)
		}
	}
}

func TestAddSpanError(t *testing.T) {
	mark := spanMark()

	ctx, span := StartSpan(context.Background(), "failing-operation")
	AddSpanError(ctx, errors.New("database is locked"))
	span.End()

	recorded := findSpan(t, spansSince(mark), "failing-operation")

	if len(recorded.Events()) != 1 {
		t.Fatalf("expected 1 exception event, got %d", len(recorded.Events()))
	}

	event := recorded.Events()[0]
	if event.Name != "exception" {
		t.Errorf("event name = %q, want %q", event.Name, "exception")
	}

	var message string
	for _, a := range event.Attributes {
		if string(a.Key) == "exception.message" {
			message = a.Value.AsString()
		}
	}
	if message != "database is locked" {
		t.Errorf("exception.message = %q, want %q", message, "database is locked")
	}
}

func TestAddSpanErrorIgnoresNil(t *testing.T) {
	mark := spanMark()

	ctx, span := StartSpan(context.Background(), "clean-operation")
	AddSpanError(ctx, nil)
	span.End()

	recorded := findSpan(t, spansSince(mark), "clean-operation")
	if len(recorded.Events()) != 0 {
		t.Errorf("nil error should record nothing, got %d events", len(recorded.Events()))
	}
}

func TestAddSpanErrorWithTrackerError(t *testing.T) {
	mark := spanMark()

	ctx, span := StartSpan(context.Background(), "tracker-error-operation")
	AddSpanError(ctx, &TrackerError{Type: "database", Message: "connection refused"})
	span.End()

	recorded := findSpan(t, spansSince(mark), "tracker-error-operation")
	if len(recorded.Events()) != 1 {
		t.Fatalf("expected 1 exception event, got %d", len(recorded.Events()))
	}
}

func TestSpanEventsWithoutActiveSpan(t *testing.T) {
	// A context with no span yields a no-op span; these must not panic.
	ctx := context.Background()

	AddSpanEvent(ctx, "orphan_event", attribute.String("key", "value"))
	AddSpanError(ctx, errors.New("orphan error"))
}
