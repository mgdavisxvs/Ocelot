package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// captureStdout swaps os.Stdout for a pipe while fn runs and returns whatever
// was written. The logger binds os.Stdout at construction time, so loggers
// under test must be built inside fn.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w

	// Drain concurrently so a large volume of log lines cannot fill the pipe
	// buffer and deadlock the writer.
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stdout = orig
	w.Close()
	out := <-done
	r.Close()

	return out
}

func parseLogLines(t *testing.T, output string) []map[string]any {
	t.Helper()

	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}

	var entries []map[string]any
	for _, line := range strings.Split(trimmed, "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not valid JSON: %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

// Level filtering

func TestLoggerLevelFiltering(t *testing.T) {
	tests := []struct {
		level      string
		wantLevels []string
	}{
		{"debug", []string{"DEBUG", "INFO", "WARN", "ERROR"}},
		{"info", []string{"INFO", "WARN", "ERROR"}},
		{"warn", []string{"WARN", "ERROR"}},
		{"error", []string{"ERROR"}},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			output := captureStdout(t, func() {
				logger := NewLogger(tt.level)
				logger.Debug("debug message")
				logger.Info("info message")
				logger.Warn("warn message")
				logger.Error("error message", nil)
			})

			entries := parseLogLines(t, output)
			if len(entries) != len(tt.wantLevels) {
				t.Fatalf("level %q emitted %d entries, want %d", tt.level, len(entries), len(tt.wantLevels))
			}

			for i, want := range tt.wantLevels {
				if got := entries[i]["level"]; got != want {
					t.Errorf("entry %d level = %v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestLoggerInvalidLevelDefaultsToInfo(t *testing.T) {
	output := captureStdout(t, func() {
		logger := NewLogger("not-a-level")
		logger.Debug("suppressed")
		logger.Info("emitted")
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry at the default level, got %d", len(entries))
	}
	if entries[0]["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", entries[0]["level"])
	}
}

func TestLoggerSuppressedLevelEmitsNothing(t *testing.T) {
	output := captureStdout(t, func() {
		logger := NewLogger("error")
		for i := 0; i < 1000; i++ {
			logger.Debug("suppressed", "iteration", i)
			logger.Info("suppressed", "iteration", i)
		}
	})

	if entries := parseLogLines(t, output); len(entries) != 0 {
		t.Errorf("expected no output below the threshold, got %d entries", len(entries))
	}
}

// JSON structure

func TestLoggerJSONStructure(t *testing.T) {
	output := captureStdout(t, func() {
		NewLogger("info").Info("structured message")
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	for _, key := range []string{"time", "level", "msg", "source"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("log entry is missing the %q key", key)
		}
	}

	if entry["msg"] != "structured message" {
		t.Errorf("msg = %v, want %q", entry["msg"], "structured message")
	}
}

func TestLoggerSourceLocation(t *testing.T) {
	output := captureStdout(t, func() {
		NewLogger("info").Info("locate me")
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	source, ok := entries[0]["source"].(map[string]any)
	if !ok {
		t.Fatalf("source is not an object: %#v", entries[0]["source"])
	}

	file, _ := source["file"].(string)
	if !strings.HasSuffix(file, "logger.go") {
		t.Errorf("source file = %q, want it to point at the logger wrapper", file)
	}
	if _, ok := source["line"]; !ok {
		t.Error("source is missing a line number")
	}
}

func TestLoggerStructuredFieldTypes(t *testing.T) {
	output := captureStdout(t, func() {
		NewLogger("info").Info("typed fields",
			"peer_id", "-qB4380-abcdefghijkl",
			"port", 6881,
			"compact", true,
			"ratio", 1.75,
		)
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry["peer_id"] != "-qB4380-abcdefghijkl" {
		t.Errorf("peer_id = %v", entry["peer_id"])
	}
	if entry["port"] != float64(6881) {
		t.Errorf("port = %v, want 6881", entry["port"])
	}
	if entry["compact"] != true {
		t.Errorf("compact = %v, want true", entry["compact"])
	}
	if entry["ratio"] != 1.75 {
		t.Errorf("ratio = %v, want 1.75", entry["ratio"])
	}
}

func TestLoggerFieldsWithSpecialCharacters(t *testing.T) {
	output := captureStdout(t, func() {
		NewLogger("info").Info("escaping",
			"quoted", `he said "hi"`,
			"newline", "line1\nline2",
			"unicode", "日本語",
		)
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry["quoted"] != `he said "hi"` {
		t.Errorf("quoted = %v", entry["quoted"])
	}
	if entry["newline"] != "line1\nline2" {
		t.Errorf("newline field did not round-trip: %v", entry["newline"])
	}
	if entry["unicode"] != "日本語" {
		t.Errorf("unicode = %v", entry["unicode"])
	}
}

// With() attribute chaining

func TestLoggerWithAttributes(t *testing.T) {
	output := captureStdout(t, func() {
		logger := NewLogger("info").With("component", "announce")
		logger.Info("first")
		logger.Info("second")
	})

	entries := parseLogLines(t, output)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	for i, entry := range entries {
		if entry["component"] != "announce" {
			t.Errorf("entry %d is missing the inherited component attribute: %v", i, entry["component"])
		}
	}
}

func TestLoggerWithChaining(t *testing.T) {
	output := captureStdout(t, func() {
		NewLogger("info").
			With("component", "announce").
			With("torrent_id", 42).
			Info("chained")
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry["component"] != "announce" {
		t.Errorf("component = %v", entry["component"])
	}
	if entry["torrent_id"] != float64(42) {
		t.Errorf("torrent_id = %v, want 42", entry["torrent_id"])
	}
}

func TestLoggerWithDoesNotMutateParent(t *testing.T) {
	output := captureStdout(t, func() {
		parent := NewLogger("info")
		child := parent.With("scoped", "child-only")

		child.Info("from child")
		parent.Info("from parent")
	})

	entries := parseLogLines(t, output)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0]["scoped"] != "child-only" {
		t.Error("child logger lost its scoped attribute")
	}
	if _, present := entries[1]["scoped"]; present {
		t.Error("With() leaked an attribute onto the parent logger")
	}
}

func TestLoggerWithPreservesLevel(t *testing.T) {
	output := captureStdout(t, func() {
		logger := NewLogger("warn").With("component", "batch")
		logger.Info("suppressed")
		logger.Warn("emitted")
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0]["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", entries[0]["level"])
	}
}

// Error logging

func TestLoggerErrorAttachesError(t *testing.T) {
	output := captureStdout(t, func() {
		NewLogger("info").Error("write failed", errors.New("database is locked"))
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", entry["level"])
	}
	if entry["error"] != "database is locked" {
		t.Errorf("error = %v, want %q", entry["error"], "database is locked")
	}
}

func TestLoggerErrorWithNilError(t *testing.T) {
	output := captureStdout(t, func() {
		NewLogger("info").Error("failed without a cause", nil)
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if _, present := entries[0]["error"]; present {
		t.Error("a nil error should not add an error field")
	}
}

func TestLoggerErrorWithExtraFields(t *testing.T) {
	output := captureStdout(t, func() {
		NewLogger("info").Error("announce rejected",
			errors.New("invalid peer ID"),
			"peer_id", "short",
			"info_hash", "abcd",
		)
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry["error"] != "invalid peer ID" {
		t.Errorf("error = %v", entry["error"])
	}
	if entry["peer_id"] != "short" {
		t.Errorf("peer_id = %v", entry["peer_id"])
	}
	if entry["info_hash"] != "abcd" {
		t.Errorf("info_hash = %v", entry["info_hash"])
	}
}

func TestLoggerErrorWithTrackerError(t *testing.T) {
	output := captureStdout(t, func() {
		NewLogger("info").Error("db call failed", &TrackerError{
			Type:    "database",
			Message: "connection refused",
		})
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	msg, _ := entries[0]["error"].(string)
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("error = %q, want it to contain the TrackerError message", msg)
	}
}

// Context and default logger

func TestLoggerWithContext(t *testing.T) {
	output := captureStdout(t, func() {
		logger := NewLogger("info").WithContext(context.Background())
		if logger == nil {
			t.Fatal("WithContext returned nil")
		}
		logger.Info("context-scoped")
	})

	entries := parseLogLines(t, output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0]["msg"] != "context-scoped" {
		t.Errorf("msg = %v", entries[0]["msg"])
	}
}

func TestDefaultLoggerRoundTrip(t *testing.T) {
	original := GetDefaultLogger()
	defer SetDefaultLogger(original)

	if original == nil {
		t.Fatal("the package default logger is nil")
	}

	replacement := NewLogger("debug")
	SetDefaultLogger(replacement)

	if GetDefaultLogger() != replacement {
		t.Error("GetDefaultLogger did not return the logger set by SetDefaultLogger")
	}
}

// Concurrency

func TestLoggerConcurrentWrites(t *testing.T) {
	const goroutines = 50
	const perGoroutine = 10

	output := captureStdout(t, func() {
		logger := NewLogger("info")

		var wg sync.WaitGroup
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < perGoroutine; j++ {
					logger.Info("concurrent", "goroutine", id, "iteration", j)
				}
			}(i)
		}
		wg.Wait()
	})

	// Every line must still be individually parseable — interleaved writes
	// would produce corrupt JSON.
	entries := parseLogLines(t, output)
	if len(entries) != goroutines*perGoroutine {
		t.Errorf("got %d entries, want %d", len(entries), goroutines*perGoroutine)
	}
}

// Performance

func BenchmarkLoggerInfo(b *testing.B) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatalf("failed to open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	orig := os.Stdout
	os.Stdout = devNull
	logger := NewLogger("info")
	os.Stdout = orig

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("announce handled", "torrent_id", i, "peers", 50)
	}
}

func BenchmarkLoggerSuppressedDebug(b *testing.B) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatalf("failed to open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	orig := os.Stdout
	os.Stdout = devNull
	logger := NewLogger("error")
	os.Stdout = orig

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Debug("announce handled", "torrent_id", i, "peers", 50)
	}
}
