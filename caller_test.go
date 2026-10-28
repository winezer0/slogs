package slogs

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"testing"
)

type callerHandler struct {
	record slog.Record
	ctx    context.Context
}

func (h *callerHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *callerHandler) Handle(ctx context.Context, r slog.Record) error {
	h.ctx = ctx
	h.record = r.Clone()
	return nil
}
func (h *callerHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *callerHandler) WithGroup(string) slog.Handler      { return h }

func TestPublicEntryPointsPreserveCaller(t *testing.T) {
	h := &callerHandler{}
	logger := &Logger{slog: slog.New(h)}
	old := defaultLogger.Swap(logger)
	t.Cleanup(func() { defaultLogger.Store(old) })
	ctx := context.WithValue(context.Background(), contextKey("caller"), "value")
	tests := []struct {
		name       string
		call       func() int
		hasContext bool
		formatted  bool
	}{
		{name: "logger.Debug", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.Debug("message", "key", "value")
			return line + 1
		}, hasContext: false, formatted: false},
		{name: "logger.DebugContext", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.DebugContext(ctx, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "logger.Debugf", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.Debugf("message %s", "value")
			return line + 1
		}, hasContext: false, formatted: true},
		{name: "logger.Info", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.Info("message", "key", "value")
			return line + 1
		}, hasContext: false, formatted: false},
		{name: "logger.InfoContext", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.InfoContext(ctx, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "logger.Infof", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.Infof("message %s", "value")
			return line + 1
		}, hasContext: false, formatted: true},
		{name: "logger.Warn", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.Warn("message", "key", "value")
			return line + 1
		}, hasContext: false, formatted: false},
		{name: "logger.WarnContext", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.WarnContext(ctx, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "logger.Warnf", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.Warnf("message %s", "value")
			return line + 1
		}, hasContext: false, formatted: true},
		{name: "logger.Error", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.Error("message", "key", "value")
			return line + 1
		}, hasContext: false, formatted: false},
		{name: "logger.ErrorContext", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.ErrorContext(ctx, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "logger.Errorf", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.Errorf("message %s", "value")
			return line + 1
		}, hasContext: false, formatted: true},
		{name: "logger.Log", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.Log(ctx, slog.LevelInfo, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "logger.LogAttrs", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			logger.LogAttrs(ctx, slog.LevelInfo, "message", slog.String("key", "value"))
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "Debug", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			Debug("message", "key", "value")
			return line + 1
		}, hasContext: false, formatted: false},
		{name: "DebugContext", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			DebugContext(ctx, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "Debugf", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			Debugf("message %s", "value")
			return line + 1
		}, hasContext: false, formatted: true},
		{name: "Info", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			Info("message", "key", "value")
			return line + 1
		}, hasContext: false, formatted: false},
		{name: "InfoContext", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			InfoContext(ctx, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "Infof", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			Infof("message %s", "value")
			return line + 1
		}, hasContext: false, formatted: true},
		{name: "Warn", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			Warn("message", "key", "value")
			return line + 1
		}, hasContext: false, formatted: false},
		{name: "WarnContext", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			WarnContext(ctx, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "Warnf", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			Warnf("message %s", "value")
			return line + 1
		}, hasContext: false, formatted: true},
		{name: "Error", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			Error("message", "key", "value")
			return line + 1
		}, hasContext: false, formatted: false},
		{name: "ErrorContext", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			ErrorContext(ctx, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "Errorf", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			Errorf("message %s", "value")
			return line + 1
		}, hasContext: false, formatted: true},
		{name: "LogContext", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			LogContext(ctx, slog.LevelInfo, "message", "key", "value")
			return line + 1
		}, hasContext: true, formatted: false},
		{name: "LogAttrs", call: func() int {
			_, _, line, _ := runtime.Caller(0)
			LogAttrs(ctx, slog.LevelInfo, "message", slog.String("key", "value"))
			return line + 1
		}, hasContext: true, formatted: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := tt.call()
			frame, _ := runtime.CallersFrames([]uintptr{h.record.PC}).Next()
			if !strings.HasSuffix(frame.File, "caller_test.go") || frame.Line != line {
				t.Fatalf("caller = %s:%d, want caller_test.go:%d", frame.File, frame.Line, line)
			}
			if tt.hasContext && h.ctx != ctx {
				t.Fatal("context was not preserved")
			}
			if tt.formatted {
				if h.record.Message != "message value" {
					t.Fatalf("message = %q", h.record.Message)
				}
			} else {
				found := false
				h.record.Attrs(func(a slog.Attr) bool { found = a.Key == "key" && a.Value.String() == "value"; return !found })
				if !found {
					t.Fatal("missing structured attribute")
				}
			}
		})
	}
}
