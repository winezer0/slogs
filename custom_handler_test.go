package slogs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestCustomHandlerComposition checks public composition, levels and caller ownership.
func TestCustomHandlerComposition(t *testing.T) {
	var output bytes.Buffer
	handler := slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger, err := NewLogger(LogConfig{ConsoleFormat: "off", LogFileFormat: "off"}, handler)
	if err != nil {
		t.Fatal(err)
	}
	logger.With("task", "one").Slog().WithGroup("details").Debug("record", "files", 6)
	if !strings.Contains(output.String(), `"task":"one","details":{"files":6}`) {
		t.Fatal(output.String())
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	before := output.Len()
	if err := handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "caller still owns handler", 0)); err != nil || output.Len() == before {
		t.Fatal(err, output.String())
	}
	if _, err := NewLogger(LogConfig{}, nil); err == nil {
		t.Fatal("nil handler accepted")
	}
}

// TestCustomHandlerErrors checks all custom targets and error propagation after derivation.
func TestCustomHandlerErrors(t *testing.T) {
	want := errors.New("custom persistence failed")
	first, second := &failingHandler{err: want}, &failingHandler{}
	logger, err := NewLogger(LogConfig{ConsoleFormat: "off"}, first, second)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	err = logger.Slog().With("task", "one").WithGroup("details").Handler().Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "record", 0))
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "custom[0]") || first.calls != 1 || second.calls != 1 {
		t.Fatal(err, first.calls, second.calls)
	}
}
