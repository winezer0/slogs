package slogs

import (
	"fmt"
	"sync"
)

var (
	defaultLogger *Logger
	defaultMu     sync.Mutex
)

// Init initializes the global default logger with the given configuration.
// Once the default logger has been initialized (either via Init or lazy init),
// subsequent calls are no-ops and return nil.
func Init(config Config) error {
	defaultMu.Lock()
	defer defaultMu.Unlock()

	if defaultLogger != nil {
		return nil
	}

	logger, err := NewLogger(config)
	if err != nil {
		return err
	}
	defaultLogger = logger
	return nil
}

// ensureDefault lazily initializes the default logger with DefaultConfig
// and returns the current default logger (nil if initialization failed).
func ensureDefault() *Logger {
	defaultMu.Lock()
	defer defaultMu.Unlock()

	if defaultLogger == nil {
		logger, err := NewLogger(DefaultConfig())
		if err != nil {
			fmt.Printf("logging: init default logger failed: %v\n", err)
			return nil
		}
		defaultLogger = logger
	}
	return defaultLogger
}

// Default returns the global default logger, initializing it if necessary.
func Default() *Logger {
	return ensureDefault()
}

// SetDefault replaces the global default logger (useful for testing or late configuration).
func SetDefault(logger *Logger) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultLogger = logger
}

// closeDefault closes the default logger and resets it to nil.
// Only callers that hold defaultMu should call this.
func closeDefault() error {
	if defaultLogger == nil {
		return nil
	}
	err := defaultLogger.Close()
	defaultLogger = nil
	return err
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