package tracker

import (
	"context"
	"log/slog"
	"os"
)

// Logger provides structured logging with JSON output
type Logger struct {
	logger *slog.Logger
	level  slog.Level
}

// NewLogger creates a new structured logger
func NewLogger(level string) *Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     lvl,
		AddSource: true,
	})

	return &Logger{
		logger: slog.New(handler),
		level:  lvl,
	}
}

// Debug logs a debug message with structured fields
func (l *Logger) Debug(msg string, args ...any) {
	l.logger.Debug(msg, args...)
}

// Info logs an info message with structured fields
func (l *Logger) Info(msg string, args ...any) {
	l.logger.Info(msg, args...)
}

// Warn logs a warning message with structured fields
func (l *Logger) Warn(msg string, args ...any) {
	l.logger.Warn(msg, args...)
}

// Error logs an error message with structured fields
func (l *Logger) Error(msg string, err error, args ...any) {
	if err != nil {
		args = append(args, "error", err.Error())
	}
	l.logger.Error(msg, args...)
}

// With returns a new logger with the given attributes
func (l *Logger) With(args ...any) *Logger {
	return &Logger{
		logger: l.logger.With(args...),
		level:  l.level,
	}
}

// WithContext returns a new logger with context values
func (l *Logger) WithContext(ctx context.Context) *Logger {
	// Extract common context values if present
	return l
}

// Global logger instance
var defaultLogger = NewLogger("info")

// SetDefaultLogger sets the global default logger
func SetDefaultLogger(logger *Logger) {
	defaultLogger = logger
}

// GetDefaultLogger returns the global default logger
func GetDefaultLogger() *Logger {
	return defaultLogger
}
