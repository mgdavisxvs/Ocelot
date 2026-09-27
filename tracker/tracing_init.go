package tracker

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

// InitTracing initialises the global OpenTelemetry trace provider.
// When OTEL_ENDPOINT is set (e.g. "http://collector:4318") an OTLP/HTTP
// exporter is created; otherwise the function is a no-op and the existing
// no-op tracer in tracing.go continues to handle all spans.
// The returned shutdown function must be called on process exit.
func InitTracing(serviceName string) (shutdown func(context.Context) error, err error) {
	endpoint := os.Getenv("OTEL_ENDPOINT")
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	exp, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(newTailSampler(otelSampleRate()))),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// otelSampleRate reads OTEL_SAMPLE_RATE (0.0–1.0, default 0.1).
func otelSampleRate() float64 {
	if v := os.Getenv("OTEL_SAMPLE_RATE"); v != "" {
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err == nil && f >= 0 && f <= 1 {
			return f
		}
	}
	return 0.1
}

// ── Tail-based sampler ────────────────────────────────────────────────────────
//
// newTailSampler returns a Sampler that always samples spans carrying an error
// attribute (100%) and samples all other spans at okRate.
//
// RULING-07 mitigation: head-based uniform sampling at 10% loses all error
// traces.  A tail sampler keeps every error for post-mortem analysis while
// keeping the overall trace volume proportional to okRate for success paths.
func newTailSampler(okRate float64) sdktrace.Sampler {
	return tailSampler{okRate: okRate}
}

type tailSampler struct{ okRate float64 }

func (s tailSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	// Always sample if any attribute signals an error.
	for _, attr := range p.Attributes {
		if string(attr.Key) == "error" && attr.Value.AsBool() {
			return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
		}
	}
	// Delegate to ratio-based sampler for non-error spans.
	return sdktrace.TraceIDRatioBased(s.okRate).ShouldSample(p)
}

func (s tailSampler) Description() string {
	return fmt.Sprintf("TailSampler{errorRate=1.0,okRate=%.3f}", s.okRate)
}
