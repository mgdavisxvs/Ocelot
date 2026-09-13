package tracker

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRetrySuccess(t *testing.T) {
	attempts := 0

	err := Retry(func() error {
		attempts++
		return nil
	})

	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}

	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestRetryEventualSuccess(t *testing.T) {
	attempts := 0
	maxAttempts := 3

	err := Retry(func() error {
		attempts++
		if attempts < maxAttempts {
			return errors.New("temporary failure")
		}
		return nil
	})

	if err != nil {
		t.Errorf("Expected eventual success, got error: %v", err)
	}

	if attempts != maxAttempts {
		t.Errorf("Expected %d attempts, got %d", maxAttempts, attempts)
	}
}

func TestRetryMaxRetriesExceeded(t *testing.T) {
	config := DefaultRetryConfig()
	attempts := 0

	err := RetryWithBackoff(func() error {
		attempts++
		return errors.New("temporary failure") // Use retryable error pattern
	}, config)

	if err == nil {
		t.Error("Expected error after max retries")
	}

	expectedAttempts := config.MaxRetries + 1 // Initial + retries
	if attempts != expectedAttempts {
		t.Errorf("Expected %d attempts, got %d", expectedAttempts, attempts)
	}

	if !strings.Contains(err.Error(), "max retries exceeded") {
		t.Errorf("Expected 'max retries exceeded' in error, got: %v", err)
	}
}

func TestRetryExponentialBackoff(t *testing.T) {
	config := RetryConfig{
		MaxRetries:  3,
		InitialWait: 10 * time.Millisecond,
		MaxWait:     200 * time.Millisecond,
		Multiplier:  2.0,
	}

	attempts := 0
	startTime := time.Now()

	RetryWithBackoff(func() error {
		attempts++
		return errors.New("temporary failure") // Use retryable error pattern
	}, config)

	elapsed := time.Since(startTime)

	// Expected delays: 10ms, 20ms, 40ms = 70ms minimum
	// (2^0)*10ms, (2^1)*10ms, (2^2)*10ms
	expectedMin := 70 * time.Millisecond

	if elapsed < expectedMin {
		t.Errorf("Expected at least %v total wait time, got %v", expectedMin, elapsed)
	}

	// Should not exceed a reasonable upper bound (account for execution time)
	expectedMax := 200 * time.Millisecond
	if elapsed > expectedMax {
		t.Errorf("Expected less than %v total time, got %v", expectedMax, elapsed)
	}
}

func TestRetryMaxWaitCap(t *testing.T) {
	config := RetryConfig{
		MaxRetries:  5,
		InitialWait: 100 * time.Millisecond,
		MaxWait:     150 * time.Millisecond, // Cap at 150ms
		Multiplier:  2.0,
	}

	attempts := 0
	waitTimes := []time.Duration{}
	lastTime := time.Now()

	RetryWithBackoff(func() error {
		now := time.Now()
		if attempts > 0 {
			waitTimes = append(waitTimes, now.Sub(lastTime))
		}
		lastTime = now
		attempts++
		return errors.New("temporary failure") // Use retryable error pattern
	}, config)

	// Later waits should be capped at MaxWait
	for i, wait := range waitTimes {
		// Account for timing variability
		if wait > config.MaxWait+50*time.Millisecond {
			t.Errorf("Wait %d exceeded MaxWait: %v > %v", i, wait, config.MaxWait)
		}
	}
}

func TestRetryNonRetryableError(t *testing.T) {
	attempts := 0

	// Create a non-retryable TrackerError (validation errors are not retryable)
	nonRetryableErr := &TrackerError{
		Type:    "validation",
		Message: "invalid input",
	}

	err := Retry(func() error {
		attempts++
		return nonRetryableErr
	})

	if err != nonRetryableErr {
		t.Errorf("Expected original error, got %v", err)
	}

	// Should only attempt once (no retries for non-retryable errors)
	if attempts != 1 {
		t.Errorf("Expected 1 attempt for non-retryable error, got %d", attempts)
	}
}

func TestRetryRetryableErrorPatterns(t *testing.T) {
	tests := []struct {
		name      string
		errorMsg  string
		retryable bool
	}{
		{
			name:      "Database locked",
			errorMsg:  "database is locked",
			retryable: true,
		},
		{
			name:      "Temporary failure",
			errorMsg:  "temporary failure in name resolution",
			retryable: true,
		},
		{
			name:      "Connection reset",
			errorMsg:  "connection reset by peer",
			retryable: true,
		},
		{
			name:      "Timeout",
			errorMsg:  "i/o timeout",
			retryable: true,
		},
		{
			name:      "Deadline exceeded",
			errorMsg:  "context deadline exceeded",
			retryable: true,
		},
		{
			name:      "Temporarily unavailable",
			errorMsg:  "service temporarily unavailable",
			retryable: true,
		},
		{
			name:      "Validation error",
			errorMsg:  "validation failed: invalid format",
			retryable: false,
		},
		{
			name:      "Authentication error",
			errorMsg:  "authentication required",
			retryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.errorMsg)
			result := isRetryableError(err)

			if result != tt.retryable {
				t.Errorf("isRetryableError(%q) = %v, expected %v",
					tt.errorMsg, result, tt.retryable)
			}
		})
	}
}

func TestRetryWithCustomBackoff(t *testing.T) {
	attempts := 0

	err := RetryWithCustomBackoff(func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary failure")
		}
		return nil
	}, 5, 5*time.Millisecond)

	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestRetryDefaultConfig(t *testing.T) {
	config := DefaultRetryConfig()

	if config.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries 3, got %d", config.MaxRetries)
	}

	if config.InitialWait != 100*time.Millisecond {
		t.Errorf("Expected InitialWait 100ms, got %v", config.InitialWait)
	}

	if config.MaxWait != 5*time.Second {
		t.Errorf("Expected MaxWait 5s, got %v", config.MaxWait)
	}

	if config.Multiplier != 2.0 {
		t.Errorf("Expected Multiplier 2.0, got %f", config.Multiplier)
	}
}

func TestRetryTrackerErrorRetryable(t *testing.T) {
	attempts := 0

	// Database errors are retryable based on IsRetryable() function
	retryableErr := &TrackerError{
		Type:    "database",
		Message: "database is locked",
	}

	err := RetryWithBackoff(func() error {
		attempts++
		if attempts < 2 {
			return retryableErr
		}
		return nil
	}, RetryConfig{
		MaxRetries:  3,
		InitialWait: 5 * time.Millisecond,
		MaxWait:     100 * time.Millisecond,
		Multiplier:  2.0,
	})

	if err != nil {
		t.Errorf("Expected eventual success, got error: %v", err)
	}

	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
}

func TestRetryZeroRetries(t *testing.T) {
	config := RetryConfig{
		MaxRetries:  0,
		InitialWait: 10 * time.Millisecond,
		MaxWait:     100 * time.Millisecond,
		Multiplier:  2.0,
	}

	attempts := 0

	err := RetryWithBackoff(func() error {
		attempts++
		return errors.New("failure")
	}, config)

	if err == nil {
		t.Error("Expected error with zero retries")
	}

	// Should only run once (no retries)
	if attempts != 1 {
		t.Errorf("Expected 1 attempt with MaxRetries=0, got %d", attempts)
	}
}

func TestRetryNilErrorReturnsImmediately(t *testing.T) {
	attempts := 0

	err := Retry(func() error {
		attempts++
		return nil
	})

	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should only run once when successful
	if attempts != 1 {
		t.Errorf("Expected 1 attempt on immediate success, got %d", attempts)
	}
}

func TestRetryAlternatingSuccessFailure(t *testing.T) {
	attempts := 0

	err := Retry(func() error {
		attempts++
		// Succeed on even attempts, fail on odd
		if attempts%2 == 0 {
			return nil
		}
		return errors.New("temporary failure")
	})

	// Should succeed on attempt 2
	if err != nil {
		t.Errorf("Expected eventual success, got error: %v", err)
	}

	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
}

func TestRetryErrorWrapping(t *testing.T) {
	// Use a retryable error pattern
	originalErr := errors.New("database is locked")

	err := Retry(func() error {
		return originalErr
	})

	if err == nil {
		t.Fatal("Expected error")
	}

	// Check that error mentions retries (should retry and eventually fail)
	if !strings.Contains(err.Error(), "max retries exceeded") {
		t.Errorf("Expected error to mention max retries, got: %v", err)
	}

	// Should also contain original error message
	if !strings.Contains(err.Error(), "database is locked") {
		t.Errorf("Expected wrapped error to contain original message, got: %v", err)
	}
}
