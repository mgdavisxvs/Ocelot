package tracker

import (
	"errors"
	"testing"
	"time"
)

// ── DefaultRetryConfig ────────────────────────────────────────────────────────

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.InitialWait != 100*time.Millisecond {
		t.Errorf("InitialWait = %v, want 100ms", cfg.InitialWait)
	}
	if cfg.MaxWait != 5*time.Second {
		t.Errorf("MaxWait = %v, want 5s", cfg.MaxWait)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("Multiplier = %v, want 2.0", cfg.Multiplier)
	}
}

// ── RetryWithBackoff ──────────────────────────────────────────────────────────

func TestRetryWithBackoff_SuccessOnFirstAttempt(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(func() error {
		calls++
		return nil
	}, RetryConfig{MaxRetries: 3, InitialWait: 0, MaxWait: 0})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestRetryWithBackoff_NonRetryableError(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(func() error {
		calls++
		return ErrInvalidInfoHash // validation error, not retryable
	}, RetryConfig{MaxRetries: 5, InitialWait: 0, MaxWait: 0})
	if err == nil {
		t.Fatal("expected error")
	}
	// Non-retryable → stops immediately after 1 attempt.
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (non-retryable stops immediately)", calls)
	}
}

func TestRetryWithBackoff_RetryableError_MaxRetriesExceeded(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(func() error {
		calls++
		return ErrDatabaseQuery // database = retryable
	}, RetryConfig{MaxRetries: 2, InitialWait: 0, MaxWait: 0})
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	// MaxRetries=2: attempts 0, 1, 2 → 3 calls total.
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestRetryWithBackoff_SucceedsOnSecondAttempt(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(func() error {
		calls++
		if calls < 2 {
			return ErrDatabaseQuery
		}
		return nil
	}, RetryConfig{MaxRetries: 3, InitialWait: 0, MaxWait: 0})
	if err != nil {
		t.Fatalf("expected nil error on second attempt, got %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestRetryWithBackoff_MaxWaitCap(t *testing.T) {
	// With MaxWait=0 and InitialWait=time.Hour, cap should kick in.
	calls := 0
	err := RetryWithBackoff(func() error {
		calls++
		return ErrDatabaseQuery
	}, RetryConfig{MaxRetries: 1, InitialWait: time.Hour, MaxWait: 0})
	// MaxWait=0 means sleep is 0 (min(huge, 0) = 0); test finishes quickly.
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── isRetryableError ──────────────────────────────────────────────────────────

func TestIsRetryableError_TrackerDatabase(t *testing.T) {
	if !isRetryableError(ErrDatabaseQuery) {
		t.Error("database error should be retryable")
	}
}

func TestIsRetryableError_TrackerValidation(t *testing.T) {
	if isRetryableError(ErrInvalidInfoHash) {
		t.Error("validation error should not be retryable")
	}
}

func TestIsRetryableError_StringPatterns(t *testing.T) {
	retryable := []error{
		errors.New("database is locked"),
		errors.New("temporary failure occurred"),
		errors.New("connection reset by peer"),
		errors.New("context deadline exceeded"),
		errors.New("circuit breaker tripped"),
		errors.New("service temporarily unavailable"),
		errors.New("got a TIMEOUT"),
	}
	for _, e := range retryable {
		if !isRetryableError(e) {
			t.Errorf("expected %q to be retryable", e.Error())
		}
	}
}

func TestIsRetryableError_NonRetryable(t *testing.T) {
	if isRetryableError(errors.New("random non-matching error")) {
		t.Error("unknown error pattern should not be retryable")
	}
}

func TestIsRetryableError_Nil(t *testing.T) {
	if isRetryableError(nil) {
		t.Error("nil error should not be retryable")
	}
}

// ── Retry (convenience wrapper) ───────────────────────────────────────────────

func TestRetry_Success(t *testing.T) {
	err := Retry(func() error { return nil })
	if err != nil {
		t.Fatalf("Retry success: %v", err)
	}
}

func TestRetry_NonRetryableError(t *testing.T) {
	err := Retry(func() error { return ErrForbidden })
	if err == nil {
		t.Fatal("expected error from Retry on non-retryable")
	}
}

// ── RetryWithCustomBackoff ────────────────────────────────────────────────────

func TestRetryWithCustomBackoff_Success(t *testing.T) {
	err := RetryWithCustomBackoff(func() error { return nil }, 2, 0)
	if err != nil {
		t.Fatalf("RetryWithCustomBackoff: %v", err)
	}
}

func TestRetryWithCustomBackoff_MaxRetries(t *testing.T) {
	calls := 0
	err := RetryWithCustomBackoff(func() error {
		calls++
		return ErrDatabaseQuery
	}, 1, 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}
