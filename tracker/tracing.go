package tracker

import (
	"context"
	"log"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Tracer provides distributed tracing functionality
var tracer = otel.Tracer("ocelot-tracker")

// InitTracerProvider registers an OTLP HTTP exporter when endpoint is non-empty
// and returns a shutdown function the caller must invoke on exit.
// When endpoint is empty the global tracer remains a no-op and the returned
// function is a no-op.
func InitTracerProvider(endpoint string) func() {
	if endpoint == "" {
		return func() {}
	}
	exp, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		log.Printf("tracing: failed to create OTLP exporter: %v", err)
		return func() {}
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	tracer = tp.Tracer("ocelot-tracker")
	return func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("tracing: shutdown error: %v", err)
		}
	}
}

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
