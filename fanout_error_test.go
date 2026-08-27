package slogs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type failingHandler struct {
	err   error
	calls int
}

func (h *failingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *failingHandler) Handle(context.Context, slog.Record) error {
	h.calls++
	return h.err
}

func (h *failingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *failingHandler) WithGroup(string) slog.Handler      { return h }

type failingCloser struct {
	err   error
	calls int
}

func (c *failingCloser) Close() error {
	c.calls++
	return c.err
}

func TestMultiHandlerJoinsFailuresAfterTryingEveryTarget(t *testing.T) {
	firstErr := errors.New("first target failed")
	secondErr := errors.New("second target failed")
	first := &failingHandler{err: firstErr}
	second := &failingHandler{err: secondErr}
	handler := &multiHandler{handlers: []slog.Handler{first, second}}

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	err := handler.Handle(context.Background(), record)
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("handler calls = (%d, %d), want (1, 1)", first.calls, second.calls)
	}
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Handle() error = %v", err)
	}
}

func TestTargetHandlerPreservesTargetAcrossDerivation(t *testing.T) {
	want := errors.New("write failed")
	handler := targetHandler{name: "file", next: &failingHandler{err: want}}
	derived := handler.WithAttrs([]slog.Attr{slog.String("component", "test")}).WithGroup("group")

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	err := derived.Handle(context.Background(), record)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "file target") {
		t.Fatalf("Handle() error = %v", err)
	}
}

func TestLoggerCloseJoinsFailuresAndReturnsStableResult(t *testing.T) {
	firstErr := errors.New("first close failed")
	secondErr := errors.New("second close failed")
	first := &failingCloser{err: firstErr}
	second := &failingCloser{err: secondErr}
	logger := &Logger{lifecycle: &loggerLifecycle{closers: []io.Closer{first, second}}}

	firstResult := logger.Close()
	secondResult := logger.Close()
	if !errors.Is(firstResult, firstErr) || !errors.Is(firstResult, secondErr) {
		t.Fatalf("Close() error = %v", firstResult)
	}
	if firstResult != secondResult {
		t.Fatalf("Close() returned different errors: %v, %v", firstResult, secondResult)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("closer calls = (%d, %d), want (1, 1)", first.calls, second.calls)
	}
}
