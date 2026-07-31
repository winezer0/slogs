package slogs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Logger wraps slog.Logger with configuration and lifecycle management.
type Logger struct {
	slog    *slog.Logger
	config  Config
	closers []io.Closer
}

// NewLogger creates a Logger from the given configuration.
// Console output goes to stderr; file output (if configured) uses JSON with rotation.
func NewLogger(config Config) (*Logger, error) {
	level := parseLevel(config.Level)
	handlers := make([]slog.Handler, 0, 2)
	var closers []io.Closer

	// Console handler (stderr).
	consoleHandler := newConsoleHandler(config.Format, level)
	handlers = append(handlers, consoleHandler)

	// File handler (JSON, with rotation).
	if config.FilePath != "" {
		fileHandler, closer, err := newFileHandler(config, level)
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, fileHandler)
		closers = append(closers, closer)
	}

	handler := fanoutHandler(handlers)
	return &Logger{
		slog:    slog.New(handler),
		config:  config,
		closers: closers,
	}, nil
}

// Slog returns the underlying *slog.Logger for direct use.
func (l *Logger) Slog() *slog.Logger {
	return l.slog
}

// With returns a Logger with the given attributes attached.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{slog: l.slog.With(args...), config: l.config, closers: l.closers}
}

// Debug logs at debug level.
func (l *Logger) Debug(msg string, args ...any) { l.slog.Debug(msg, args...) }

// Info logs at info level.
func (l *Logger) Info(msg string, args ...any) { l.slog.Info(msg, args...) }

// Warn logs at warn level.
func (l *Logger) Warn(msg string, args ...any) { l.slog.Warn(msg, args...) }

// Error logs at error level.
func (l *Logger) Error(msg string, args ...any) { l.slog.Error(msg, args...) }

// Debugf logs a formatted message at debug level.
func (l *Logger) Debugf(template string, args ...any) {
	l.slog.Debug(fmt.Sprintf(template, args...))
}

// Infof logs a formatted message at info level.
func (l *Logger) Infof(template string, args ...any) {
	l.slog.Info(fmt.Sprintf(template, args...))
}

// Warnf logs a formatted message at warn level.
func (l *Logger) Warnf(template string, args ...any) {
	l.slog.Warn(fmt.Sprintf(template, args...))
}

// Errorf logs a formatted message at error level.
func (l *Logger) Errorf(template string, args ...any) {
	l.slog.Error(fmt.Sprintf(template, args...))
}

// Close flushes and releases file resources.
func (l *Logger) Close() error {
	var errs []error
	for _, c := range l.closers {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("logging close: %v", errs)
	}
	return nil
}

// newConsoleHandler creates a stderr handler with the specified format and level.
func newConsoleHandler(format string, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				if src, ok := a.Value.Any().(*slog.Source); ok {
					src.File = filepath.Base(src.File)
				}
			}
			return a
		},
	}
	if strings.EqualFold(format, "json") {
		return slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.NewTextHandler(os.Stderr, opts)
}

// newFileHandler creates a file handler with built-in rotation.
// Format is "text" (human-readable) or "json" (structured).
func newFileHandler(config Config, level slog.Level) (slog.Handler, io.Closer, error) {
	if err := ensureDir(config.FilePath); err != nil {
		return nil, nil, fmt.Errorf("logging: create log dir: %w", err)
	}
	rotator := &Rotator{
		Filename:   config.FilePath,
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}
	opts := &slog.HandlerOptions{Level: level, AddSource: true}
	if strings.EqualFold(config.Format, "text") {
		return slog.NewTextHandler(rotator, opts), rotator, nil
	}
	return slog.NewJSONHandler(rotator, opts), rotator, nil
}

// fanoutHandler returns a single handler that dispatches to multiple handlers.
func fanoutHandler(handlers []slog.Handler) slog.Handler {
	if len(handlers) == 1 {
		return handlers[0]
	}
	return &multiHandler{handlers: handlers}
}

// multiHandler dispatches log records to all underlying handlers.
type multiHandler struct {
	handlers []slog.Handler
}

// Enabled returns true if any underlying handler is enabled for the level.
func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle dispatches the record to all enabled handlers.
func (m *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, record.Level) {
			if err := h.Handle(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

// WithAttrs returns a new multiHandler with attributes added to all handlers.
func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

// WithGroup returns a new multiHandler with a group added to all handlers.
func (m *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
}

// parseLevel converts a level string to slog.Level.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ensureDir creates the parent directory of a file path if it does not exist.
func ensureDir(filePath string) error {
	dir := filepath.Dir(filePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, 0o755)
	}
	return nil
}
