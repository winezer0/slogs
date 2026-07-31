package slogs

import (
	"fmt"
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
func Init(config Config) error {
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
		l.Debug(msg, args...)
	}
}

// Info logs at info level using the default logger.
func Info(msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.Info(msg, args...)
	}
}

// Warn logs at warn level using the default logger.
func Warn(msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.Warn(msg, args...)
	}
}

// Error logs at error level using the default logger.
func Error(msg string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.Error(msg, args...)
	}
}

// Debugf logs a formatted message at debug level using the default logger.
func Debugf(template string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.Debugf(template, args...)
	}
}

// Infof logs a formatted message at info level using the default logger.
func Infof(template string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.Infof(template, args...)
	}
}

// Warnf logs a formatted message at warn level using the default logger.
func Warnf(template string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.Warnf(template, args...)
	}
}

// Errorf logs a formatted message at error level using the default logger.
func Errorf(template string, args ...any) {
	if l := ensureDefault(); l != nil {
		l.Errorf(template, args...)
	}
}