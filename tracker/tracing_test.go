package tracker

import (
	"context"
	"errors"
	"testing"
)

func TestStartSpan_NoOp(t *testing.T) {
	ctx, span := StartSpan(context.Background(), "test-span")
	if ctx == nil {
		t.Error("StartSpan returned nil context")
	}
	span.End()
}

func TestStartSpan_WithAttrs(t *testing.T) {
	ctx, span := StartSpan(context.Background(), "test-with-attrs")
	if ctx == nil {
		t.Error("StartSpan returned nil context")
	}
	span.End()
}

func TestTraceAnnounce(t *testing.T) {
	ctx, span := TraceAnnounce(context.Background(), "abc123", "peer-1")
	if ctx == nil {
		t.Error("TraceAnnounce returned nil context")
	}
	span.End()
}

func TestTraceScrape(t *testing.T) {
	ctx, span := TraceScrape(context.Background(), []string{"hash1", "hash2"})
	if ctx == nil {
		t.Error("TraceScrape returned nil context")
	}
	span.End()
}

func TestTraceDBQuery(t *testing.T) {
	ctx, span := TraceDBQuery(context.Background(), "select")
	if ctx == nil {
		t.Error("TraceDBQuery returned nil context")
	}
	span.End()
}

func TestTraceRedisOp(t *testing.T) {
	ctx, span := TraceRedisOp(context.Background(), "get")
	if ctx == nil {
		t.Error("TraceRedisOp returned nil context")
	}
	span.End()
}

func TestTracePeerSelection(t *testing.T) {
	ctx, span := TracePeerSelection(context.Background(), "torrent-1", 50)
	if ctx == nil {
		t.Error("TracePeerSelection returned nil context")
	}
	span.End()
}

func TestAddSpanEvent(t *testing.T) {
	ctx := context.Background()
	// Must not panic — span from background context is a no-op span.
	AddSpanEvent(ctx, "peer-selected")
}

func TestAddSpanError(t *testing.T) {
	ctx := context.Background()
	// Must not panic with a real error.
	AddSpanError(ctx, errors.New("test error"))
}

func TestAddSpanError_NilError(t *testing.T) {
	ctx := context.Background()
	// Must not panic with a nil error.
	AddSpanError(ctx, nil)
}
