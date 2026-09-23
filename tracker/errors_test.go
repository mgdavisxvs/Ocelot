package tracker

import (
	"net/http"
	"testing"
)

func TestTrackerError_Error_WithDetail(t *testing.T) {
	e := &TrackerError{Code: 400, Type: "validation", Message: "Bad input", Detail: "field x missing"}
	got := e.Error()
	if got != "[validation] Bad input: field x missing" {
		t.Errorf("Error() = %q", got)
	}
}

func TestTrackerError_Error_NoDetail(t *testing.T) {
	e := &TrackerError{Code: 403, Type: "auth", Message: "Denied"}
	got := e.Error()
	if got != "[auth] Denied" {
		t.Errorf("Error() = %q", got)
	}
}

func TestTrackerError_WithDetail(t *testing.T) {
	base := ErrInvalidInfoHash
	derived := base.WithDetail("got length %d", 10)
	if derived.Code != base.Code {
		t.Errorf("WithDetail changed code: %d", derived.Code)
	}
	if derived.Detail != "got length 10" {
		t.Errorf("WithDetail Detail = %q", derived.Detail)
	}
}

func TestTrackerError_WithError(t *testing.T) {
	import_err := ErrDatabaseQuery
	wrapped := import_err.WithError(ErrInternal)
	if wrapped.Err == nil {
		t.Error("WithError: Err should not be nil")
	}
}

func TestSentinelErrors_HTTPCodes(t *testing.T) {
	cases := []struct {
		err  *TrackerError
		code int
	}{
		{ErrInvalidInfoHash, http.StatusBadRequest},
		{ErrInvalidPasskey, http.StatusForbidden},
		{ErrInvalidPeerID, http.StatusBadRequest},
		{ErrInvalidPort, http.StatusBadRequest},
		{ErrMissingParameter, http.StatusBadRequest},
		{ErrUnauthorized, http.StatusUnauthorized},
		{ErrForbidden, http.StatusForbidden},
		{ErrUserBanned, http.StatusForbidden},
		{ErrClientNotWhitelisted, http.StatusForbidden},
		{ErrTorrentNotFound, http.StatusNotFound},
		{ErrUserNotFound, http.StatusNotFound},
		{ErrRateLimitExceeded, http.StatusTooManyRequests},
		{ErrDatabaseQuery, http.StatusInternalServerError},
		{ErrDatabaseConnection, http.StatusServiceUnavailable},
		{ErrInternal, http.StatusInternalServerError},
		{ErrCircuitOpen, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		if c.err.Code != c.code {
			t.Errorf("%s: Code = %d, want %d", c.err.Message, c.err.Code, c.code)
		}
	}
}

func TestIsRetryable_DatabaseError(t *testing.T) {
	if !IsRetryable(ErrDatabaseQuery) {
		t.Error("database error should be retryable")
	}
}

func TestIsRetryable_CircuitBreaker(t *testing.T) {
	if !IsRetryable(ErrCircuitOpen) {
		t.Error("circuit breaker error should be retryable")
	}
}

func TestIsRetryable_AuthError(t *testing.T) {
	if IsRetryable(ErrUnauthorized) {
		t.Error("auth error must not be retryable")
	}
}

func TestIsRetryable_PlainError(t *testing.T) {
	if IsRetryable(ErrTorrentNotFound) {
		t.Error("not-found error must not be retryable")
	}
}
