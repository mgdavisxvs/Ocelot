package tracker

import (
	"context"
	"errors"
	"testing"
)

func TestLogger_Error_WithError(t *testing.T) {
	l := NewLogger("info")
	// Should not panic; no assertion on output
	l.Error("something failed", errors.New("oops"))
}

func TestLogger_Error_NilError(t *testing.T) {
	l := NewLogger("info")
	l.Error("no-op failure", nil)
}

func TestLogger_With_ReturnsNewLogger(t *testing.T) {
	l := NewLogger("info")
	l2 := l.With("key", "value")
	if l2 == nil {
		t.Fatal("With returned nil")
	}
	if l2 == l {
		t.Error("With should return a distinct logger instance")
	}
}

func TestLogger_WithContext_ReturnsLogger(t *testing.T) {
	l := NewLogger("info")
	ctx := context.WithValue(context.Background(), "request_id", "abc123")
	l2 := l.WithContext(ctx)
	if l2 == nil {
		t.Fatal("WithContext returned nil")
	}
}

func TestSetDefaultLogger(t *testing.T) {
	orig := GetDefaultLogger()
	newL := NewLogger("warn")
	SetDefaultLogger(newL)
	if GetDefaultLogger() != newL {
		t.Error("SetDefaultLogger did not update the default logger")
	}
	// Restore
	SetDefaultLogger(orig)
}
