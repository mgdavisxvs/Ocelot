package tracker

import (
	"fmt"
	"strings"
	"time"
)

// RetryConfig holds retry configuration
type RetryConfig struct {
	MaxRetries  int
	InitialWait time.Duration
	MaxWait     time.Duration
	Multiplier  float64
}

// DefaultRetryConfig returns default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:  3,
		InitialWait: 100 * time.Millisecond,
		MaxWait:     5 * time.Second,
		Multiplier:  2.0,
	}
}

// RetryWithBackoff retries a function with exponential backoff
func RetryWithBackoff(fn func() error, config RetryConfig) error {
	var lastErr error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Check if error is retryable
		if !isRetryableError(lastErr) {
			return lastErr
		}

		// Don't wait after last attempt
		if attempt >= config.MaxRetries {
			break
		}

		// Calculate backoff duration
		wait := time.Duration(float64(config.InitialWait) * float64(uint(1)<<uint(attempt)))
		if wait > config.MaxWait {
			wait = config.MaxWait
		}

		logger := GetDefaultLogger()
		logger.Warn("operation failed, retrying",
			"attempt", attempt+1,
			"max_retries", config.MaxRetries,
			"wait", wait,
			"error", lastErr,
		)

		time.Sleep(wait)
	}

	return fmt.Errorf("max retries exceeded (%d): %w", config.MaxRetries, lastErr)
}

// isRetryableError determines if an error should be retried
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check if it's a TrackerError
	if trackerErr, ok := err.(*TrackerError); ok {
		return IsRetryable(trackerErr)
	}

	// Check for common retryable error patterns
	errStr := strings.ToLower(err.Error())

	retryablePatterns := []string{
		"database is locked",
		"temporary failure",
		"connection reset",
		"timeout",
		"deadline exceeded",
		"temporarily unavailable",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// RetryableFunc wraps a function with retry logic
type RetryableFunc func() error

// Retry is a convenience function for retrying with default config
func Retry(fn RetryableFunc) error {
	return RetryWithBackoff(fn, DefaultRetryConfig())
}

// RetryWithCustomBackoff allows custom retry configuration
func RetryWithCustomBackoff(fn RetryableFunc, maxRetries int, initialWait time.Duration) error {
	config := RetryConfig{
		MaxRetries:  maxRetries,
		InitialWait: initialWait,
		MaxWait:     10 * time.Second,
		Multiplier:  2.0,
	}
	return RetryWithBackoff(fn, config)
}
