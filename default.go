package slogs

import (
	"fmt"
	"sync"
)

var (
	defaultLogger *Logger
	defaultOnce   sync.Once
)

// Init initializes the global default logger with the given configuration.
// Subsequent calls are no-ops (first call wins).
func Init(config Config) error {
	var initErr error
	defaultOnce.Do(func() {
		logger, err := NewLogger(config)
		if err != nil {
			initErr = err
			return
		}
		defaultLogger = logger
	})
	return initErr
}

// ensureDefault lazily initializes the default logger with DefaultConfig.
func ensureDefault() {
	if defaultLogger == nil {
		defaultOnce.Do(func() {
			logger, err := NewLogger(DefaultConfig())
			if err != nil {
				fmt.Printf("logging: init default logger failed: %v\n", err)
				return
			}
			defaultLogger = logger
		})
	}
}

// Default returns the global default logger, initializing it if necessary.
func Default() *Logger {
	ensureDefault()
	return defaultLogger
}

// SetDefault replaces the global default logger (useful for testing or late configuration).
func SetDefault(logger *Logger) {
	defaultLogger = logger
}

// Debug logs at debug level using the default logger.
func Debug(msg string, args ...any) { ensureDefault(); defaultLogger.Debug(msg, args...) }

// Info logs at info level using the default logger.
func Info(msg string, args ...any) { ensureDefault(); defaultLogger.Info(msg, args...) }

// Warn logs at warn level using the default logger.
func Warn(msg string, args ...any) { ensureDefault(); defaultLogger.Warn(msg, args...) }

// Error logs at error level using the default logger.
func Error(msg string, args ...any) { ensureDefault(); defaultLogger.Error(msg, args...) }

// Debugf logs a formatted message at debug level using the default logger.
func Debugf(template string, args ...any) { ensureDefault(); defaultLogger.Debugf(template, args...) }

// Infof logs a formatted message at info level using the default logger.
func Infof(template string, args ...any) { ensureDefault(); defaultLogger.Infof(template, args...) }

// Warnf logs a formatted message at warn level using the default logger.
func Warnf(template string, args ...any) { ensureDefault(); defaultLogger.Warnf(template, args...) }

// Errorf logs a formatted message at error level using the default logger.
func Errorf(template string, args ...any) { ensureDefault(); defaultLogger.Errorf(template, args...) }
