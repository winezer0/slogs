package slogs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
)

var (
	defaultLogger atomic.Pointer[Logger]
	defaultMu     sync.Mutex
)

// Init initializes the global default logger with the given configuration.
// Once the default logger has been initialized (either via Init or lazy init),
// subsequent calls are no-ops and return nil.
func Init(config LogConfig) error {
	if defaultLogger.Load() != nil {
		return nil
	}

	defaultMu.Lock()
	defer defaultMu.Unlock()

	if defaultLogger.Load() != nil {
		return nil
	}

	logger, err := NewLogger(config)
	if err != nil {
		return err
	}
	defaultLogger.Store(logger)
	return nil
}

// ensureDefault lazily initializes the default logger with DefaultConfig
// and returns the current default logger (nil if initialization failed).
func ensureDefault() *Logger {
	if l := defaultLogger.Load(); l != nil {
		return l
	}

	defaultMu.Lock()
	defer defaultMu.Unlock()

	if l := defaultLogger.Load(); l != nil {
		return l
	}

	logger, err := NewLogger(DefaultConfig())
	if err != nil {
		fmt.Printf("logging: init default logger failed: %v\n", err)
		return nil
	}
	defaultLogger.Store(logger)
	return logger
}

// Default returns the global default logger, initializing it if necessary.
func Default() *Logger {
	return ensureDefault()
}

// SetDefault replaces the global default logger (useful for testing or late configuration).
// The previous default logger is closed to prevent resource leaks.
func SetDefault(logger *Logger) {
	old := defaultLogger.Swap(logger)
	if old != nil {
		old.Close()
	}
}

// closeDefault atomically replaces the default logger with nil and closes it.
// Safe to call without holding defaultMu.
func closeDefault() error {
	l := defaultLogger.Swap(nil)
	if l == nil {
		return nil
	}
	return l.Close()
}

// Debug logs at debug level using the default logger.
func Debug(msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(context.Background(), slog.LevelDebug, msg, args, nil)
	}
}

// DebugContext logs at debug level using the default logger and context.
func DebugContext(ctx context.Context, msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(ctx, slog.LevelDebug, msg, args, nil)
	}
}

// Info logs at info level using the default logger.
func Info(msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(context.Background(), slog.LevelInfo, msg, args, nil)
	}
}

// InfoContext logs at info level using the default logger and context.
func InfoContext(ctx context.Context, msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(ctx, slog.LevelInfo, msg, args, nil)
	}
}

// Warn logs at warn level using the default logger.
func Warn(msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(context.Background(), slog.LevelWarn, msg, args, nil)
	}
}

// WarnContext logs at warn level using the default logger and context.
func WarnContext(ctx context.Context, msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(ctx, slog.LevelWarn, msg, args, nil)
	}
}

// Error logs at error level using the default logger.
func Error(msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(context.Background(), slog.LevelError, msg, args, nil)
	}
}

// ErrorContext logs at error level using the default logger and context.
func ErrorContext(ctx context.Context, msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(ctx, slog.LevelError, msg, args, nil)
	}
}

// LogContext logs at the supplied level using the default logger and context.
func LogContext(ctx context.Context, level slog.Level, msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(ctx, level, msg, args, nil)
	}
}

// LogAttrs logs pre-built attributes at the supplied level using the default logger.
func LogAttrs(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	if l := ensureDefault(); l != nil {
		l.log(ctx, level, msg, nil, attrs)
	}
}

// Debugf logs a formatted message at debug level using the default logger.
func Debugf(template string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(context.Background(), slog.LevelDebug, fmt.Sprintf(template, args...), nil, nil)
	}
}

// Infof logs a formatted message at info level using the default logger.
func Infof(template string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(context.Background(), slog.LevelInfo, fmt.Sprintf(template, args...), nil, nil)
	}
}

// Warnf logs a formatted message at warn level using the default logger.
func Warnf(template string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(context.Background(), slog.LevelWarn, fmt.Sprintf(template, args...), nil, nil)
	}
}

// Errorf logs a formatted message at error level using the default logger.
func Errorf(template string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.log(context.Background(), slog.LevelError, fmt.Sprintf(template, args...), nil, nil)
	}
}
