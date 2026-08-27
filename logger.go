package slogs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Logger wraps slog.Logger with configuration and lifecycle management.
type Logger struct {
	slog      *slog.Logger
	config    LogConfig
	lifecycle *loggerLifecycle
}

// loggerLifecycle owns resources shared by a root logger and its With views.
type loggerLifecycle struct {
	closers []io.Closer
	once    sync.Once
	err     error
}

// NewLogger creates a Logger from the given configuration.
// Console output goes to stdout; file output (if configured) uses rotation.
// A format value of "off" disables the corresponding target.
// If every target is disabled, the logger silently discards all records.
//
// Console level comes from ConsoleLevel (empty defaults to "info");
// file level comes from FileLevel (empty defaults to "debug").
func NewLogger(config LogConfig) (*Logger, error) {
	consoleLevel := parseLevel(config.ConsoleLevel)
	fileLevel := parseFileLevel(config.FileLevel)
	handlers := make([]slog.Handler, 0, 2)
	var closers []io.Closer

	// Console handler (stdout).
	if !isOff(config.ConsoleFormat) {
		handlers = append(handlers, newConsoleHandler(config.ConsoleFormat, consoleLevel))
	}

	// File handler (with rotation).
	if config.LogFilePath != "" && !isOff(config.LogFileFormat) {
		fileHandler, closer, err := newFileHandler(config, fileLevel)
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, fileHandler)
		closers = append(closers, closer)
	}

	handler := fanoutHandler(handlers)
	return &Logger{
		slog:      slog.New(handler),
		config:    config,
		lifecycle: &loggerLifecycle{closers: closers},
	}, nil
}

// isOff reports whether a format value disables its output target.
func isOff(v string) bool {
	return strings.EqualFold(v, "off")
}

// Slog returns the underlying *slog.Logger for direct use.
func (l *Logger) Slog() *slog.Logger {
	return l.slog
}

// With returns a Logger with the given attributes attached.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{slog: l.slog.With(args...), config: l.config, lifecycle: l.lifecycle}
}

// Enabled reports whether any configured output accepts the level.
func (l *Logger) Enabled(ctx context.Context, level slog.Level) bool {
	return l.slog.Enabled(ctx, level)
}

// Log records a message at the supplied level.
func (l *Logger) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	l.slog.Log(ctx, level, msg, args...)
}

// LogAttrs records a message with pre-built attributes at the supplied level.
func (l *Logger) LogAttrs(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	l.slog.LogAttrs(ctx, level, msg, attrs...)
}

// Debug logs at debug level.
func (l *Logger) Debug(msg string, args ...any) { l.slog.Debug(msg, args...) }

// DebugContext logs at debug level with the supplied context.
func (l *Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.slog.DebugContext(ctx, msg, args...)
}

// Info logs at info level.
func (l *Logger) Info(msg string, args ...any) { l.slog.Info(msg, args...) }

// InfoContext logs at info level with the supplied context.
func (l *Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.slog.InfoContext(ctx, msg, args...)
}

// Warn logs at warn level.
func (l *Logger) Warn(msg string, args ...any) { l.slog.Warn(msg, args...) }

// WarnContext logs at warn level with the supplied context.
func (l *Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.slog.WarnContext(ctx, msg, args...)
}

// Error logs at error level.
func (l *Logger) Error(msg string, args ...any) { l.slog.Error(msg, args...) }

// ErrorContext logs at error level with the supplied context.
func (l *Logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.slog.ErrorContext(ctx, msg, args...)
}

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
	if l == nil || l.lifecycle == nil {
		return nil
	}
	l.lifecycle.once.Do(func() {
		var errs []error
		for _, c := range l.lifecycle.closers {
			if err := c.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		l.lifecycle.err = errors.Join(errs...)
	})
	return l.lifecycle.err
}

// newConsoleHandler creates a stdout handler for the given format.
//   - "text" → slog text
//   - "json" → slog json
//   - "" → default mask "LCM"
//   - otherwise → mask format (T/L/C/M field selection)
func newConsoleHandler(format string, level slog.Level) slog.Handler {
	return formatHandler(format, level, os.Stdout, "LCM")
}

// formatHandler builds a handler for the given output target.
// defaultFormat is used when format is empty (e.g. "json" for files,
// "LCM" mask for console).
func formatHandler(format string, level slog.Level, w io.Writer, defaultFormat string) slog.Handler {
	if format == "" {
		format = defaultFormat
	}
	switch strings.ToLower(format) {
	case "json":
		return slog.NewJSONHandler(w, handlerOpts(level))
	case "text":
		return slog.NewTextHandler(w, handlerOpts(level))
	default:
		return newMaskHandler(format, level, w)
	}
}

// handlerOpts returns standard HandlerOptions with source shortening.
func handlerOpts(level slog.Level) *slog.HandlerOptions {
	return &slog.HandlerOptions{
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
}

// newFileHandler creates a file handler with built-in rotation.
// LogFileFormat controls the output format (same options as console,
// but empty defaults to "json").
func newFileHandler(config LogConfig, level slog.Level) (slog.Handler, io.Closer, error) {
	if err := ensureDir(config.LogFilePath); err != nil {
		return nil, nil, fmt.Errorf("logging: create log dir: %w", err)
	}
	rotator := &Rotator{
		Filename:   config.LogFilePath,
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}

	return formatHandler(config.LogFileFormat, level, rotator, "json"), rotator, nil
}

// fanoutHandler returns a single handler that dispatches to multiple handlers.
// An empty list yields a discard handler (all targets disabled).
func fanoutHandler(handlers []slog.Handler) slog.Handler {
	if len(handlers) == 0 {
		return discardHandler{}
	}
	if len(handlers) == 1 {
		return handlers[0]
	}
	return &multiHandler{handlers: handlers}
}

// discardHandler drops every log record; used when all targets are "off".
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return discardHandler{} }
func (discardHandler) WithGroup(string) slog.Handler             { return discardHandler{} }

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
// An empty or unknown value defaults to slog.LevelInfo.
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

// parseFileLevel converts a file level string to slog.Level.
// An empty value defaults to slog.LevelDebug; otherwise it
// falls back to parseLevel semantics (info for unknown values).
func parseFileLevel(level string) slog.Level {
	if level == "" {
		return slog.LevelDebug
	}
	return parseLevel(level)
}

// ensureDir creates the parent directory of a file path if it does not exist.
func ensureDir(filePath string) error {
	dir := filepath.Dir(filePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, 0o755)
	}
	return nil
}
