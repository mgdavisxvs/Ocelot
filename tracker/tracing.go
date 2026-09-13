package tracker

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Tracer provides distributed tracing functionality
var tracer = otel.Tracer("ocelot-tracker")

// StartSpan starts a new trace span
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	ctx, span := tracer.Start(ctx, name)
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, span
}

// TraceAnnounce traces an announce request
func TraceAnnounce(ctx context.Context, infoHash, peerID string) (context.Context, trace.Span) {
	return StartSpan(ctx, "announce",
		attribute.String("info_hash", infoHash),
		attribute.String("peer_id", peerID),
	)
}

// TraceScrape traces a scrape request
func TraceScrape(ctx context.Context, infoHashes []string) (context.Context, trace.Span) {
	return StartSpan(ctx, "scrape",
		attribute.Int("torrents_count", len(infoHashes)),
	)
}

// TraceDBQuery traces a database query
func TraceDBQuery(ctx context.Context, queryType string) (context.Context, trace.Span) {
	return StartSpan(ctx, "db.query",
		attribute.String("query_type", queryType),
	)
}

// TraceRedisOp traces a Redis operation
func TraceRedisOp(ctx context.Context, operation string) (context.Context, trace.Span) {
	return StartSpan(ctx, "redis.op",
		attribute.String("operation", operation),
	)
}

// TracePeerSelection traces peer selection algorithm
func TracePeerSelection(ctx context.Context, torrentID string, numWant int) (context.Context, trace.Span) {
	return StartSpan(ctx, "peer.selection",
		attribute.String("torrent_id", torrentID),
		attribute.Int("num_want", numWant),
	)
}

// AddSpanEvent adds an event to the current span
func AddSpanEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span != nil {
		span.AddEvent(name, trace.WithAttributes(attrs...))
	}
}

// AddSpanError records an error on the current span
func AddSpanError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	if span != nil && err != nil {
		span.RecordError(err)
	}
}
