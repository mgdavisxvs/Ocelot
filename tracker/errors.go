package tracker

import (
	"fmt"
	"net/http"
)

// TrackerError represents a structured error with HTTP status code
type TrackerError struct {
	Code    int    // HTTP status code
	Type    string // Error type (validation, authentication, database, internal)
	Message string // User-facing message
	Detail  string // Developer-facing detail
	Err     error  // Underlying error
}

// Error implements the error interface
func (e *TrackerError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Type, e.Message, e.Detail)
	}
	return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

// WithDetail creates a new error with formatted detail
func (e *TrackerError) WithDetail(format string, args ...interface{}) *TrackerError {
	return &TrackerError{
		Code:    e.Code,
		Type:    e.Type,
		Message: e.Message,
		Detail:  fmt.Sprintf(format, args...),
		Err:     e.Err,
	}
}

// WithError wraps an underlying error
func (e *TrackerError) WithError(err error) *TrackerError {
	return &TrackerError{
		Code:    e.Code,
		Type:    e.Type,
		Message: e.Message,
		Detail:  e.Detail,
		Err:     err,
	}
}

// Predefined errors
var (
	// Validation errors (400)
	ErrInvalidInfoHash = &TrackerError{
		Code:    http.StatusBadRequest,
		Type:    "validation",
		Message: "Invalid info_hash parameter",
		Detail:  "info_hash must be 20 or 40 characters",
	}

	ErrInvalidPasskey = &TrackerError{
		Code:    http.StatusForbidden,
		Type:    "authentication",
		Message: "Invalid passkey",
		Detail:  "User not found or passkey invalid",
	}

	ErrInvalidPeerID = &TrackerError{
		Code:    http.StatusBadRequest,
		Type:    "validation",
		Message: "Invalid peer_id parameter",
		Detail:  "peer_id must be exactly 20 characters",
	}

	ErrInvalidPort = &TrackerError{
		Code:    http.StatusBadRequest,
		Type:    "validation",
		Message: "Invalid port parameter",
		Detail:  "port must be between 1 and 65535",
	}

	ErrInvalidIP = &TrackerError{
		Code:    http.StatusBadRequest,
		Type:    "validation",
		Message: "Invalid IP address",
		Detail:  "IP address is private, reserved, or malformed",
	}

	ErrMissingParameter = &TrackerError{
		Code:    http.StatusBadRequest,
		Type:    "validation",
		Message: "Missing required parameter",
	}

	// Authentication errors (403)
	ErrUnauthorized = &TrackerError{
		Code:    http.StatusUnauthorized,
		Type:    "authentication",
		Message: "Authentication required",
		Detail:  "Valid API key or password required",
	}

	ErrForbidden = &TrackerError{
		Code:    http.StatusForbidden,
		Type:    "authorization",
		Message: "Access denied",
		Detail:  "Insufficient permissions for this operation",
	}

	ErrUserBanned = &TrackerError{
		Code:    http.StatusForbidden,
		Type:    "authorization",
		Message: "User is banned",
		Detail:  "User account has been disabled",
	}

	ErrClientNotWhitelisted = &TrackerError{
		Code:    http.StatusForbidden,
		Type:    "authorization",
		Message: "Client not whitelisted",
		Detail:  "BitTorrent client is not in whitelist",
	}

	// Not found errors (404)
	ErrTorrentNotFound = &TrackerError{
		Code:    http.StatusNotFound,
		Type:    "not_found",
		Message: "Torrent not registered",
		Detail:  "info_hash not in tracker database",
	}

	ErrUserNotFound = &TrackerError{
		Code:    http.StatusNotFound,
		Type:    "not_found",
		Message: "User not found",
	}

	// Rate limiting (429)
	ErrRateLimitExceeded = &TrackerError{
		Code:    http.StatusTooManyRequests,
		Type:    "rate_limit",
		Message: "Rate limit exceeded",
		Detail:  "Too many requests, please slow down",
	}

	// Database errors (500)
	ErrDatabaseQuery = &TrackerError{
		Code:    http.StatusInternalServerError,
		Type:    "database",
		Message: "Database query failed",
	}

	ErrDatabaseConnection = &TrackerError{
		Code:    http.StatusServiceUnavailable,
		Type:    "database",
		Message: "Database connection failed",
	}

	// Internal errors (500)
	ErrInternal = &TrackerError{
		Code:    http.StatusInternalServerError,
		Type:    "internal",
		Message: "Internal server error",
	}

	ErrCircuitOpen = &TrackerError{
		Code:    http.StatusServiceUnavailable,
		Type:    "circuit_breaker",
		Message: "Service temporarily unavailable",
		Detail:  "Circuit breaker is open, service degraded",
	}
)

// IsRetryable returns true if the error should be retried
func IsRetryable(err error) bool {
	if trackerErr, ok := err.(*TrackerError); ok {
		// Retry on database errors and circuit breaker
		return trackerErr.Type == "database" || trackerErr.Type == "circuit_breaker"
	}
	return false
}
