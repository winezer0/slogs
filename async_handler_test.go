package slogs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type asyncTestKey struct{}

type asyncTestWrite struct {
	batch    bool
	records  []slog.Record
	values   []any
	err      []error
	deadline bool
	value    any
}

type asyncTestSink struct {
	mu            sync.Mutex
	writes        []asyncTestWrite
	output        bytes.Buffer
	entered       chan struct{}
	release       <-chan struct{}
	failure       error
	failureFor    func([]BatchRecord) error
	waitForCancel bool
}

type asyncTestHandler struct {
	sink   *asyncTestSink
	format slog.Handler
}

func newAsyncTestTarget(sink *asyncTestSink) *asyncTestHandler {
	return &asyncTestHandler{sink: sink, format: slog.NewJSONHandler(&sink.output, &slog.HandlerOptions{Level: slog.LevelWarn})}
}

func (h *asyncTestHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.format.Enabled(ctx, level)
}
func (h *asyncTestHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.write(ctx, []BatchRecord{{Context: ctx, Record: record}}, false)
}
func (h *asyncTestHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &asyncTestHandler{sink: h.sink, format: h.format.WithAttrs(attrs)}
}
func (h *asyncTestHandler) WithGroup(name string) slog.Handler {
	return &asyncTestHandler{sink: h.sink, format: h.format.WithGroup(name)}
}
func (h *asyncTestHandler) write(ctx context.Context, records []BatchRecord, batch bool) error {
	w := asyncTestWrite{batch: batch, value: ctx.Value(asyncTestKey{})}
	_, w.deadline = ctx.Deadline()
	for _, entry := range records {
		w.records = append(w.records, entry.Record.Clone())
		w.values = append(w.values, entry.Context.Value(asyncTestKey{}))
		w.err = append(w.err, entry.Context.Err())
	}
	h.sink.mu.Lock()
	h.sink.writes = append(h.sink.writes, w)
	h.sink.mu.Unlock()
	if h.sink.entered != nil {
		select {
		case h.sink.entered <- struct{}{}:
		default:
		}
	}
	if h.sink.release != nil {
		<-h.sink.release
	}
	if h.sink.waitForCancel {
		<-ctx.Done()
		return ctx.Err()
	}
	if h.sink.failure != nil {
		return h.sink.failure
	}
	if h.sink.failureFor != nil {
		if err := h.sink.failureFor(records); err != nil {
			return err
		}
	}
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	for _, entry := range records {
		if err := h.format.Handle(entry.Context, entry.Record); err != nil {
			return err
		}
	}
	return nil
}

type asyncTestBatchHandler struct{ *asyncTestHandler }

func (h *asyncTestBatchHandler) HandleBatch(ctx context.Context, records []BatchRecord) error {
	return h.write(ctx, records, true)
}
func (h *asyncTestBatchHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &asyncTestBatchHandler{h.asyncTestHandler.WithAttrs(attrs).(*asyncTestHandler)}
}
func (h *asyncTestBatchHandler) WithGroup(name string) slog.Handler {
	return &asyncTestBatchHandler{h.asyncTestHandler.WithGroup(name).(*asyncTestHandler)}
}

func asyncForTest(t *testing.T, next slog.Handler, opts AsyncOptions) *AsyncHandler {
	t.Helper()
	h, err := NewAsyncHandler(next, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := h.Close(ctx); errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
			t.Error("async consumer did not exit")
		}
	})
	return h
}

func asyncRecord(message string, attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Unix(1234, 0), slog.LevelWarn, message, 123)
	r.AddAttrs(attrs...)
	return r
}

func asyncSend(t *testing.T, handler slog.Handler, message string, attrs ...slog.Attr) {
	t.Helper()
	if err := handler.Handle(context.Background(), asyncRecord(message, attrs...)); err != nil {
		t.Fatal(err)
	}
}

func asyncAwait(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("consumer did not reach expected write")
	}
}

func asyncMessages(sink *asyncTestSink) [][]string {
	var batches [][]string
	for _, write := range sink.writes {
		var messages []string
		for _, record := range write.records {
			messages = append(messages, record.Message)
		}
		batches = append(batches, messages)
	}
	return batches
}

func TestAsyncDropNewAndDrain(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	sink := &asyncTestSink{entered: make(chan struct{}, 8), release: release}
	h := asyncForTest(t, newAsyncTestTarget(sink), AsyncOptions{QueueSize: 2, BatchSize: 8})
	t.Cleanup(unblock)
	asyncSend(t, h, "first")
	asyncAwait(t, sink.entered)
	asyncSend(t, h, "second")
	asyncSend(t, h, "third")
	if err := h.Handle(context.Background(), asyncRecord("dropped")); !errors.Is(err, ErrAsyncQueueFull) {
		t.Fatal(err)
	}
	stats := h.Stats()
	if stats.Enqueued != 3 || stats.Dropped != 1 || stats.Queued != 2 || stats.InFlight != 1 {
		t.Fatal(stats)
	}
	unblock()
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := asyncMessages(sink); !reflect.DeepEqual(got, [][]string{{"first"}, {"second"}, {"third"}}) {
		t.Fatal(got)
	}
	stats = h.Stats()
	if stats.Written != 3 || stats.Failed != 0 || stats.Queued != 0 || stats.InFlight != 0 || stats.WrittenBatches != 0 {
		t.Fatal(stats)
	}
}

func TestAsyncBatchSizeAndClosePartial(t *testing.T) {
	sink := &asyncTestSink{entered: make(chan struct{}, 8)}
	h := asyncForTest(t, &asyncTestBatchHandler{newAsyncTestTarget(sink)}, AsyncOptions{BatchSize: 2, BatchInterval: time.Hour})
	asyncSend(t, h, "one")
	asyncSend(t, h, "two")
	asyncAwait(t, sink.entered) // Must flush at the size limit before Close.
	asyncSend(t, h, "three")
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := asyncMessages(sink); !reflect.DeepEqual(got, [][]string{{"one", "two"}, {"three"}}) {
		t.Fatal(got)
	}
	if stats := h.Stats(); stats.Written != 3 || stats.WrittenBatches != 2 || stats.InFlight != 0 {
		t.Fatal(stats)
	}
}

func TestAsyncBatchInterval(t *testing.T) {
	sink := &asyncTestSink{entered: make(chan struct{}, 8)}
	h := asyncForTest(t, &asyncTestBatchHandler{newAsyncTestTarget(sink)}, AsyncOptions{BatchSize: 10, BatchInterval: 20 * time.Millisecond})
	asyncSend(t, h, "only")
	asyncAwait(t, sink.entered) // Neither size limit nor Close can trigger this write.
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := asyncMessages(sink); !reflect.DeepEqual(got, [][]string{{"only"}}) {
		t.Fatal(got)
	}
}

func TestAsyncBatchIntervalIncludesLookaheadWait(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	sink := &asyncTestSink{entered: make(chan struct{}, 8), release: release}
	h := asyncForTest(t, &asyncTestBatchHandler{newAsyncTestTarget(sink)}, AsyncOptions{BatchSize: 8, BatchInterval: 20 * time.Millisecond})
	t.Cleanup(unblock)
	view := h.WithGroup("other")
	asyncSend(t, h, "root")
	asyncSend(t, view, "other1")
	asyncAwait(t, sink.entered) // The different view is now held as lookahead.
	<-time.After(40 * time.Millisecond)
	asyncSend(t, view, "other2")
	unblock()
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := asyncMessages(sink); !reflect.DeepEqual(got, [][]string{{"root"}, {"other1"}, {"other2"}}) {
		t.Fatal(got)
	}
}

func TestAsyncBatchViewBoundaries(t *testing.T) {
	sink := &asyncTestSink{}
	h := asyncForTest(t, &asyncTestBatchHandler{newAsyncTestTarget(sink)}, AsyncOptions{BatchSize: 10, BatchInterval: time.Hour})
	a := h.WithAttrs([]slog.Attr{slog.String("view", "same")})
	b := h.WithAttrs([]slog.Attr{slog.String("view", "same")})
	c := h.WithGroup("detail")
	asyncSend(t, a, "a1")
	asyncSend(t, a, "a2")
	asyncSend(t, b, "b1")
	asyncSend(t, c, "c1", slog.String("field", "value"))
	asyncSend(t, a, "a3")
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := asyncMessages(sink); !reflect.DeepEqual(got, [][]string{{"a1", "a2"}, {"b1"}, {"c1"}, {"a3"}}) {
		t.Fatal(got)
	}
	decoder := json.NewDecoder(&sink.output)
	for _, message := range []string{"a1", "a2", "b1", "c1", "a3"} {
		var data map[string]any
		if err := decoder.Decode(&data); err != nil {
			t.Fatal(err)
		}
		if data["msg"] != message {
			t.Fatal(data)
		}
		if message == "c1" {
			if data["view"] != nil || data["detail"].(map[string]any)["field"] != "value" {
				t.Fatal(data)
			}
		} else if data["view"] != "same" || data["detail"] != nil {
			t.Fatal(data)
		}
	}
}

// The root can batch, but its inherited With methods return ordinary handlers.
type asyncTestLostBatch struct{ *asyncTestHandler }

func (h *asyncTestLostBatch) HandleBatch(ctx context.Context, records []BatchRecord) error {
	return h.write(ctx, records, true)
}

func TestAsyncDerivedBatchFallback(t *testing.T) {
	sink := &asyncTestSink{}
	h := asyncForTest(t, &asyncTestLostBatch{newAsyncTestTarget(sink)}, AsyncOptions{BatchSize: 10, BatchInterval: time.Hour})
	view := h.WithAttrs([]slog.Attr{slog.String("key", "value")})
	asyncSend(t, view, "one")
	asyncSend(t, view, "two")
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := asyncMessages(sink); !reflect.DeepEqual(got, [][]string{{"one"}, {"two"}}) {
		t.Fatal(got)
	}
	if stats := h.Stats(); stats.WrittenBatches != 0 || stats.Written != 2 {
		t.Fatal(stats)
	}
}

type asyncTestLogValuer struct {
	value atomic.Value
	calls atomic.Int32
}

func (v *asyncTestLogValuer) LogValue() slog.Value {
	v.calls.Add(1)
	return slog.StringValue(v.value.Load().(string))
}

func TestAsyncAttributeSnapshot(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	sink := &asyncTestSink{entered: make(chan struct{}, 8), release: release}
	h := asyncForTest(t, newAsyncTestTarget(sink), AsyncOptions{})
	t.Cleanup(unblock)
	asyncSend(t, h, "block")
	asyncAwait(t, sink.entered)
	v := &asyncTestLogValuer{}
	v.value.Store("bound-before")
	view := h.WithAttrs([]slog.Attr{slog.Group("bound", slog.Any("value", v))}).WithGroup("detail")
	if v.calls.Load() != 1 {
		t.Fatal("WithAttrs must resolve nested LogValuer synchronously")
	}
	v.value.Store("record-before")
	group := slog.Group("group", slog.Any("value", v), slog.String("stable", "before"))
	immutable := &struct{ Name string }{"retained"}
	r := asyncRecord("snapshot", group, slog.Any("object", immutable))
	if err := view.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if v.calls.Load() != 2 {
		t.Fatal("Handle must resolve nested LogValuer before returning")
	}
	v.value.Store("after")
	group.Value.Group()[1] = slog.String("stable", "after")
	unblock()
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v.calls.Load() != 2 {
		t.Fatal("consumer resolved LogValuer again")
	}
	got := sink.writes[1].records[0]
	if got.Time != r.Time || got.Level != r.Level || got.PC != r.PC {
		t.Fatal("record metadata changed")
	}
	got.Attrs(func(a slog.Attr) bool {
		if a.Key == "object" && a.Value.Any() != immutable {
			t.Error("Any reference/type changed")
		}
		return true
	})
	decoder := json.NewDecoder(&sink.output)
	var data map[string]any
	if err := decoder.Decode(&data); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&data); err != nil {
		t.Fatal(err)
	}
	detail := data["detail"].(map[string]any)
	if data["bound"].(map[string]any)["value"] != "bound-before" || detail["group"].(map[string]any)["value"] != "record-before" || detail["group"].(map[string]any)["stable"] != "before" {
		t.Fatal(data)
	}
}

func TestAsyncContextsAndTimeout(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "batch"}[batch], func(t *testing.T) {
			sink := &asyncTestSink{}
			var target slog.Handler = newAsyncTestTarget(sink)
			opts := AsyncOptions{WriteTimeout: time.Second}
			if batch {
				target = &asyncTestBatchHandler{target.(*asyncTestHandler)}
				opts.BatchSize = 2
				opts.BatchInterval = time.Hour
			}
			h := asyncForTest(t, target, opts)
			for _, value := range []string{"first", "second"} {
				ctx, cancel := context.WithCancel(context.WithValue(context.Background(), asyncTestKey{}, value))
				cancel()
				if err := h.Handle(ctx, asyncRecord(value)); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			var values []any
			for _, write := range sink.writes {
				if !write.deadline {
					t.Fatal("write has no independent timeout")
				}
				for _, err := range write.err {
					if err != nil {
						t.Fatal("caller cancellation leaked", err)
					}
				}
				if batch && write.value != nil {
					t.Fatal("batch context must not impersonate the first record")
				}
				if !batch && write.value != write.values[0] {
					t.Fatal("single write lost context values")
				}
				values = append(values, write.values...)
			}
			if !reflect.DeepEqual(values, []any{"first", "second"}) {
				t.Fatal(values)
			}
		})
	}
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "single-timeout", true: "batch-timeout"}[batch], func(t *testing.T) {
			sink := &asyncTestSink{waitForCancel: true}
			var target slog.Handler = newAsyncTestTarget(sink)
			opts := AsyncOptions{WriteTimeout: 10 * time.Millisecond}
			if batch {
				target = &asyncTestBatchHandler{target.(*asyncTestHandler)}
				opts.BatchSize = 2
				opts.BatchInterval = time.Hour
			}
			h := asyncForTest(t, target, opts)
			asyncSend(t, h, "one")
			if batch {
				asyncSend(t, h, "two")
			}
			if err := h.Close(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			stats := h.Stats()
			if stats.Failed != stats.Enqueued || stats.Written != 0 || !errors.Is(stats.LastError, context.DeadlineExceeded) {
				t.Fatal(stats)
			}
		})
	}
}

func TestAsyncFailuresAndCallback(t *testing.T) {
	want := errors.New("database failed")
	sink := &asyncTestSink{failure: want}
	var callbacks atomic.Int32
	var h *AsyncHandler
	h = asyncForTest(t, &asyncTestBatchHandler{newAsyncTestTarget(sink)}, AsyncOptions{BatchSize: 3, BatchInterval: time.Hour, OnError: func(err error) {
		if !errors.Is(err, want) {
			t.Error(err)
		}
		if h.Stats().Failed == 0 {
			t.Error("callback must observe recorded failure")
		}
		callbacks.Add(1)
	}})
	for _, message := range []string{"one", "two", "three", "four"} {
		asyncSend(t, h, message)
	}
	for i := 0; i < 2; i++ {
		if err := h.Close(context.Background()); !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
	stats := h.Stats()
	if callbacks.Load() != 2 || stats.Failed != 4 || stats.FailedBatches != 2 || stats.Written != 0 || stats.InFlight != 0 || !errors.Is(stats.LastError, want) {
		t.Fatal(stats, callbacks.Load())
	}
	if got := asyncMessages(sink); !reflect.DeepEqual(got, [][]string{{"one", "two", "three"}, {"four"}}) {
		t.Fatal("unexpected retry/fallback", got)
	}
}

func TestAsyncCloseTimeoutAndSharedLifecycle(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	sink := &asyncTestSink{entered: make(chan struct{}, 8), release: release}
	h := asyncForTest(t, newAsyncTestTarget(sink), AsyncOptions{QueueSize: 2})
	t.Cleanup(unblock)
	view := h.WithGroup("group").(*AsyncHandler)
	asyncSend(t, view, "first")
	asyncAwait(t, sink.entered)
	asyncSend(t, h, "second")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := view.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if h.Enabled(context.Background(), slog.LevelError) || view.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("closed view still enabled")
	}
	if err := h.Handle(context.Background(), asyncRecord("rejected")); !errors.Is(err, ErrAsyncClosed) {
		t.Fatal(err)
	}
	if stats := h.Stats(); stats.Rejected != 1 || stats.Enqueued != 2 {
		t.Fatal(stats)
	}
	unblock()
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := asyncMessages(sink); !reflect.DeepEqual(got, [][]string{{"first"}, {"second"}}) {
		t.Fatal(got)
	}
	if stats := h.Stats(); stats.Written != 2 || stats.InFlight != 0 || stats.Queued != 0 {
		t.Fatal(stats)
	}
}

func TestAsyncConcurrentCloseAndProducers(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	sink := &asyncTestSink{entered: make(chan struct{}, 1), release: release}
	h := asyncForTest(t, &asyncTestBatchHandler{newAsyncTestTarget(sink)}, AsyncOptions{QueueSize: 64, BatchSize: 8})
	t.Cleanup(unblock)
	// Start a partial batch by crossing a view boundary, then keep it in flight.
	view := h.WithGroup("queued")
	asyncSend(t, h, "in-flight")
	asyncSend(t, view, "queued-one")
	asyncAwait(t, sink.entered)
	asyncSend(t, view, "queued-two")
	if stats := h.Stats(); stats.InFlight < 1 || stats.Queued < 1 {
		t.Fatal(stats)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				if err := h.Handle(context.Background(), asyncRecord("record")); err != nil && !errors.Is(err, ErrAsyncQueueFull) && !errors.Is(err, ErrAsyncClosed) {
					t.Error(err)
				}
				_ = h.Stats()
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := h.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := h.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	unblock()
	wg.Wait()
	stats := h.Stats()
	if stats.Enqueued < 3 || stats.Enqueued+stats.Dropped+stats.Rejected != 1603 || stats.Enqueued != stats.Written || stats.Queued != 0 || stats.InFlight != 0 {
		t.Fatal(stats)
	}
}

func TestAsyncBatchFailureRecoveryAndErrorHistory(t *testing.T) {
	first, last := errors.New("first batch failed"), errors.New("second batch failed")
	sink := &asyncTestSink{failureFor: func(records []BatchRecord) error {
		switch records[0].Record.Message {
		case "first":
			return first
		case "second":
			return last
		default:
			return nil
		}
	}}
	var callbacks atomic.Int32
	h := asyncForTest(t, &asyncTestBatchHandler{newAsyncTestTarget(sink)}, AsyncOptions{BatchSize: 2, BatchInterval: time.Hour, OnError: func(error) { callbacks.Add(1) }})
	for _, message := range []string{"first", "first-tail", "second", "second-tail", "success", "success-tail"} {
		asyncSend(t, h, message)
	}
	for i := 0; i < 2; i++ {
		if err := h.Close(context.Background()); !errors.Is(err, first) {
			t.Fatal(err)
		}
	}
	stats := h.Stats()
	if stats.Enqueued != 6 || stats.Written != 2 || stats.Failed != 4 || stats.WrittenBatches != 1 || stats.FailedBatches != 2 || !errors.Is(stats.LastError, last) || callbacks.Load() != 2 {
		t.Fatal(stats)
	}
	if got := asyncMessages(sink); !reflect.DeepEqual(got, [][]string{{"first", "first-tail"}, {"second", "second-tail"}, {"success", "success-tail"}}) {
		t.Fatal("unexpected retry or missing recovery", got)
	}
}

func TestAsyncBlockedDatabaseKeepsFileSynchronous(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	sink := &asyncTestSink{entered: make(chan struct{}, 1), release: release}
	h := asyncForTest(t, newAsyncTestTarget(sink), AsyncOptions{QueueSize: 2})
	t.Cleanup(unblock)
	path := filepath.Join(t.TempDir(), "output.log")
	logger, err := NewLogger(LogConfig{ConsoleFormat: "off", LogFilePath: path, LogFileFormat: "json"}, h)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	logger.Warn("first")
	asyncAwait(t, sink.entered)
	logger.Warn("second")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"msg":"first"`) || !strings.Contains(string(data), `"msg":"second"`) {
		t.Fatal(string(data))
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if stats := h.Stats(); stats.Written != 0 || stats.Enqueued != 2 || !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal(stats)
	}
	unblock()
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stats := h.Stats(); stats.Written != 2 {
		t.Fatal(stats)
	}
}

func TestAsyncValidationAndLoggerOwnership(t *testing.T) {
	if _, err := NewAsyncHandler(nil, AsyncOptions{}); err == nil {
		t.Fatal("nil target accepted")
	}
	for _, opts := range []AsyncOptions{{QueueSize: -1}, {BatchSize: -1}, {WriteTimeout: -1}, {BatchInterval: -1}} {
		if _, err := NewAsyncHandler(newAsyncTestTarget(&asyncTestSink{}), opts); err == nil {
			t.Fatal("negative option accepted")
		}
	}
	sink := &asyncTestSink{}
	h := asyncForTest(t, newAsyncTestTarget(sink), AsyncOptions{})
	if h.Enabled(nil, slog.LevelInfo) || !h.Enabled(nil, slog.LevelWarn) {
		t.Fatal("level policy changed")
	}
	logger, err := NewLogger(LogConfig{ConsoleFormat: "off", LogFileFormat: "off"}, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if !h.Enabled(nil, slog.LevelWarn) {
		t.Fatal("Logger.Close closed caller-owned handler")
	}
	if err := h.Handle(nil, asyncRecord("still open")); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(nil); err != nil {
		t.Fatal(err)
	}
	if stats := h.Stats(); stats.Written != 1 || stats.WrittenBatches != 0 {
		t.Fatal(stats)
	}
}
