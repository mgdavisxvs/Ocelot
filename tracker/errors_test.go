package tracker

import (
	"errors"
	"testing"
)

// ── TrackerError.Error ────────────────────────────────────────────────────────

func TestTrackerError_Error_WithDetail(t *testing.T) {
	e := &TrackerError{
		Type:    "validation",
		Message: "bad input",
		Detail:  "missing field x",
	}
	got := e.Error()
	if got != "[validation] bad input: missing field x" {
		t.Errorf("Error() = %q, want \"[validation] bad input: missing field x\"", got)
	}
}

func TestTrackerError_Error_WithoutDetail(t *testing.T) {
	e := &TrackerError{
		Type:    "not_found",
		Message: "torrent not found",
	}
	got := e.Error()
	if got != "[not_found] torrent not found" {
		t.Errorf("Error() = %q, want \"[not_found] torrent not found\"", got)
	}
}

func TestTrackerError_PredefinedErrors_HaveNonEmptyMessages(t *testing.T) {
	errs := []*TrackerError{
		ErrInvalidInfoHash, ErrInvalidPasskey, ErrInvalidPeerID,
		ErrInvalidPort, ErrInvalidIP, ErrMissingParameter,
		ErrUnauthorized, ErrForbidden, ErrUserBanned,
		ErrClientNotWhitelisted, ErrTorrentNotFound, ErrUserNotFound,
		ErrRateLimitExceeded, ErrDatabaseQuery, ErrDatabaseConnection,
		ErrInternal, ErrCircuitOpen,
	}
	for _, e := range errs {
		if e.Error() == "" {
			t.Errorf("predefined error %T has empty Error() string", e)
		}
	}
}

// ── IsRetryable ───────────────────────────────────────────────────────────────

func TestIsRetryable_DatabaseError(t *testing.T) {
	if !IsRetryable(ErrDatabaseQuery) {
		t.Error("database error should be retryable")
	}
	if !IsRetryable(ErrDatabaseConnection) {
		t.Error("database connection error should be retryable")
	}
}

func TestIsRetryable_CircuitBreakerError(t *testing.T) {
	if !IsRetryable(ErrCircuitOpen) {
		t.Error("circuit breaker error should be retryable")
	}
}

func TestIsRetryable_ValidationError(t *testing.T) {
	if IsRetryable(ErrInvalidInfoHash) {
		t.Error("validation error should not be retryable")
	}
	if IsRetryable(ErrInvalidPasskey) {
		t.Error("authentication error should not be retryable")
	}
}

func TestIsRetryable_NonTrackerError(t *testing.T) {
	if IsRetryable(errors.New("some random error")) {
		t.Error("plain error should not be retryable via IsRetryable")
	}
}
