package tracker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ── Knuth: algorithmic correctness, loop invariants, data-structure invariants ─

// TestGUC_Knuth_OtelSampleRateDefault verifies that otelSampleRate returns the
// documented default of 0.1 when OTEL_SAMPLE_RATE is unset.
func TestGUC_Knuth_OtelSampleRateDefault(t *testing.T) {
	t.Setenv("OTEL_SAMPLE_RATE", "")
	got := otelSampleRate()
	if got != 0.1 {
		t.Errorf("otelSampleRate() = %v, want 0.1 (default)", got)
	}
}

// TestGUC_Knuth_OtelSampleRateValidValues verifies correct parsing for all
// valid float strings in [0, 1].
func TestGUC_Knuth_OtelSampleRateValidValues(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"0.0", 0.0},
		{"0.5", 0.5},
		{"1.0", 1.0},
		{"0.25", 0.25},
		{"0.9999", 0.9999},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Setenv("OTEL_SAMPLE_RATE", tc.input)
			got := otelSampleRate()
			if got != tc.want {
				t.Errorf("otelSampleRate(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestGUC_Knuth_OtelSampleRateInvalidValues verifies that out-of-range and
// non-numeric inputs fall back to the 0.1 default.
func TestGUC_Knuth_OtelSampleRateInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"negative", "-0.1"},
		{"greater_than_one", "1.1"},
		{"non_numeric", "abc"},
		{"large_integer", "999"},
		{"only_spaces", "   "},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_SAMPLE_RATE", tc.input)
			got := otelSampleRate()
			if got != 0.1 {
				t.Errorf("otelSampleRate(%q) = %v, want default 0.1", tc.input, got)
			}
		})
	}
}

// TestGUC_Knuth_TailSamplerDescriptionFormat verifies the exact string format
// produced by Description() for various okRate values.
func TestGUC_Knuth_TailSamplerDescriptionFormat(t *testing.T) {
	tests := []struct {
		okRate float64
		want   string
	}{
		{0.1, "TailSampler{errorRate=1.0,okRate=0.100}"},
		{0.5, "TailSampler{errorRate=1.0,okRate=0.500}"},
		{1.0, "TailSampler{errorRate=1.0,okRate=1.000}"},
		{0.0, "TailSampler{errorRate=1.0,okRate=0.000}"},
		{0.333, "TailSampler{errorRate=1.0,okRate=0.333}"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(fmt.Sprintf("rate_%.3f", tc.okRate), func(t *testing.T) {
			s := tailSampler{okRate: tc.okRate}
			got := s.Description()
			if got != tc.want {
				t.Errorf("Description() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGUC_Knuth_TailSamplerOkRateField verifies that the okRate field is stored
// with bitwise equality after struct construction.
func TestGUC_Knuth_TailSamplerOkRateField(t *testing.T) {
	rates := []float64{0.0, 0.01, 0.1, 0.5, 0.999, 1.0}
	for _, r := range rates {
		r := r
		s := tailSampler{okRate: r}
		if s.okRate != r {
			t.Errorf("tailSampler{okRate:%v}.okRate = %v, want %v", r, s.okRate, r)
		}
	}
}

// ── Turing: termination conditions, halting behavior ─────────────────────────

// TestGUC_Turing_InitTracingNoEndpointTerminates verifies InitTracing returns
// promptly when OTEL_ENDPOINT is unset (no blocking network dial).
func TestGUC_Turing_InitTracingNoEndpointTerminates(t *testing.T) {
	t.Setenv("OTEL_ENDPOINT", "")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = InitTracing("test-service")
	}()
	select {
	case <-done:
		// halted as expected
	case <-time.After(2 * time.Second):
		t.Fatal("InitTracing did not terminate within 2s when OTEL_ENDPOINT is unset")
	}
}

// TestGUC_Turing_ShutdownCallableMultipleTimes verifies the no-op shutdown
// function terminates without error or deadlock across repeated calls.
func TestGUC_Turing_ShutdownCallableMultipleTimes(t *testing.T) {
	t.Setenv("OTEL_ENDPOINT", "")
	shutdown, err := InitTracing("multi-shutdown-test")
	if err != nil {
		t.Fatalf("InitTracing unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown func must not be nil")
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if shutErr := shutdown(ctx); shutErr != nil {
			t.Errorf("shutdown call %d returned error: %v", i+1, shutErr)
		}
	}
}

// TestGUC_Turing_OtelSampleRateEmptyEnv verifies the empty-string fast path
// terminates with the correct default value.
func TestGUC_Turing_OtelSampleRateEmptyEnv(t *testing.T) {
	t.Setenv("OTEL_SAMPLE_RATE", "")
	got := otelSampleRate()
	if got != 0.1 {
		t.Errorf("otelSampleRate() with empty env = %v, want 0.1", got)
	}
}

// TestGUC_Turing_OtelSampleRateNaNInput verifies that NaN input is rejected:
// fmt.Sscanf parses "nan" as IEEE NaN; f >= 0 && f <= 1 is false for NaN, so
// the function must fall through to the 0.1 default.
func TestGUC_Turing_OtelSampleRateNaNInput(t *testing.T) {
	t.Setenv("OTEL_SAMPLE_RATE", "nan")
	got := otelSampleRate()
	if got != 0.1 {
		t.Errorf("otelSampleRate(\"nan\") = %v, want 0.1 (NaN must not escape sampler)", got)
	}
}

// TestGUC_Turing_InitTracingShutdownWithCanceledContext verifies the no-op
// shutdown function does not block when supplied an already-canceled context.
func TestGUC_Turing_InitTracingShutdownWithCanceledContext(t *testing.T) {
	t.Setenv("OTEL_ENDPOINT", "")
	shutdown, err := InitTracing("cancel-ctx-test")
	if err != nil {
		t.Fatalf("InitTracing error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before calling shutdown
	done := make(chan error, 1)
	go func() { done <- shutdown(ctx) }()
	select {
	case shutErr := <-done:
		if shutErr != nil {
			t.Errorf("shutdown(canceled ctx) = %v, want nil", shutErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown blocked on a pre-canceled context")
	}
}

// ── Church: functional purity, side-effect isolation, referential transparency ─

// TestGUC_Church_OtelSampleRatePure verifies that repeated calls with identical
// env state return the same value (no internal mutable accumulator).
func TestGUC_Church_OtelSampleRatePure(t *testing.T) {
	t.Setenv("OTEL_SAMPLE_RATE", "0.42")
	first := otelSampleRate()
	for i := 0; i < 5; i++ {
		got := otelSampleRate()
		if got != first {
			t.Errorf("call %d: otelSampleRate() = %v, want %v (non-deterministic)", i+1, got, first)
		}
	}
}

// TestGUC_Church_TailSamplerDescriptionDeterministic verifies Description() is
// a pure function: same receiver → same output, no mutation of the struct.
func TestGUC_Church_TailSamplerDescriptionDeterministic(t *testing.T) {
	s := tailSampler{okRate: 0.75}
	first := s.Description()
	for i := 0; i < 10; i++ {
		got := s.Description()
		if got != first {
			t.Errorf("call %d: Description() = %q, want %q", i+1, got, first)
		}
	}
	if s.okRate != 0.75 {
		t.Errorf("tailSampler.okRate mutated after Description(): got %v, want 0.75", s.okRate)
	}
}

// TestGUC_Church_InitTracingNoopIdempotent verifies that calling InitTracing
// multiple times without an endpoint is idempotent (no cumulative side effects).
func TestGUC_Church_InitTracingNoopIdempotent(t *testing.T) {
	t.Setenv("OTEL_ENDPOINT", "")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		shutdown, err := InitTracing("idempotent-test")
		if err != nil {
			t.Errorf("call %d: InitTracing error: %v", i+1, err)
		}
		if shutdown == nil {
			t.Errorf("call %d: shutdown func is nil", i+1)
			continue
		}
		if shutErr := shutdown(ctx); shutErr != nil {
			t.Errorf("call %d: shutdown error: %v", i+1, shutErr)
		}
	}
}

// TestGUC_Church_NewTailSamplerIndependentInstances verifies that two samplers
// with different rates carry completely independent state (no aliasing).
func TestGUC_Church_NewTailSamplerIndependentInstances(t *testing.T) {
	s1 := tailSampler{okRate: 0.1}
	s2 := tailSampler{okRate: 0.9}
	d1 := s1.Description()
	d2 := s2.Description()
	if d1 == d2 {
		t.Errorf("distinct samplers produced identical descriptions: %q", d1)
	}
	// Changing s2 must not affect s1.
	originalRate := s1.okRate
	s2.okRate = 0.1
	if s1.okRate != originalRate {
		t.Errorf("s1.okRate changed after s2 mutation: %v", s1.okRate)
	}
}

// TestGUC_Church_ShouldSampleRequiresOtelImport documents that calling
// ShouldSample directly requires sdktrace.SamplingParameters, which is outside
// the allowed standard-library-only import set for this test file.
func TestGUC_Church_ShouldSampleRequiresOtelImport(t *testing.T) {
	t.Skip("not yet implemented: direct ShouldSample call requires sdktrace.SamplingParameters import (OTEL SDK)")
}

// ── Gödel: formal consistency, invariant preservation, impossible-state detection ─

// TestGUC_Godel_OtelSampleRateNeverNegative asserts the invariant rate >= 0
// across all possible env inputs, ruling out the impossible negative-rate state.
func TestGUC_Godel_OtelSampleRateNeverNegative(t *testing.T) {
	inputs := []string{"", "-1", "-0.001", "abc", "0.0", "0.5", "1.0", "-999"}
	for _, v := range inputs {
		v := v
		t.Setenv("OTEL_SAMPLE_RATE", v)
		got := otelSampleRate()
		if got < 0 {
			t.Errorf("OTEL_SAMPLE_RATE=%q produced negative rate %v", v, got)
		}
	}
}

// TestGUC_Godel_OtelSampleRateNeverExceedsOne asserts the invariant rate <= 1
// across all possible env inputs, ruling out the impossible over-sampling state.
func TestGUC_Godel_OtelSampleRateNeverExceedsOne(t *testing.T) {
	inputs := []string{"", "1.1", "2", "999", "abc", "0.0", "0.5", "1.0", "1.0001"}
	for _, v := range inputs {
		v := v
		t.Setenv("OTEL_SAMPLE_RATE", v)
		got := otelSampleRate()
		if got > 1.0 {
			t.Errorf("OTEL_SAMPLE_RATE=%q produced rate > 1.0: %v", v, got)
		}
	}
}

// TestGUC_Godel_DescriptionNonEmpty asserts that Description() never returns an
// empty string, ruling out the impossible state of an unnamed sampler.
func TestGUC_Godel_DescriptionNonEmpty(t *testing.T) {
	rates := []float64{0.0, 0.001, 0.1, 0.5, 0.999, 1.0}
	for _, r := range rates {
		r := r
		s := tailSampler{okRate: r}
		desc := s.Description()
		if desc == "" {
			t.Errorf("tailSampler{okRate=%v}.Description() returned empty string", r)
		}
	}
}

// TestGUC_Godel_ShutdownNonNil asserts that InitTracing never returns a nil
// shutdown function, preventing nil-dereference on deferred shutdown calls.
func TestGUC_Godel_ShutdownNonNil(t *testing.T) {
	t.Setenv("OTEL_ENDPOINT", "")
	shutdown, err := InitTracing("non-nil-shutdown-test")
	if err != nil {
		t.Fatalf("InitTracing error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("InitTracing returned nil shutdown function — callers cannot safely defer it")
	}
}

// TestGUC_Godel_ConcurrentDescriptionCalls verifies that concurrent reads of
// tailSampler.Description() produce consistent, contradiction-free results and
// do not race (the struct is immutable after construction).
func TestGUC_Godel_ConcurrentDescriptionCalls(t *testing.T) {
	s := tailSampler{okRate: 0.2}
	expected := s.Description()
	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errors := make([]string, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			got := s.Description()
			if got != expected {
				errors[i] = fmt.Sprintf("goroutine %d: Description() = %q, want %q", i, got, expected)
			}
		}()
	}
	wg.Wait()
	for _, e := range errors {
		if e != "" {
			t.Error(e)
		}
	}
}
