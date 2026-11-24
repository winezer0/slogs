package slogs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var (
	// ErrAsyncQueueFull means the new record was dropped without being written.
	ErrAsyncQueueFull = errors.New("slogs: async queue is full")
	// ErrAsyncClosed means the wrapper no longer accepts records.
	ErrAsyncClosed = errors.New("slogs: async handler is closed")
)

// AsyncOptions configures an explicitly asynchronous output target.
// Zero values select defaults. Negative sizes or durations are rejected.
type AsyncOptions struct {
	// QueueSize bounds waiting records, excluding the consumer's current batch
	// and at most one lookahead record. Zero defaults to 1024 records.
	QueueSize int
	// WriteTimeout applies to one Handle or HandleBatch call, starting when
	// writing begins. Zero defaults to 5 seconds. The target must honor context.
	WriteTimeout time.Duration
	// BatchSize is the maximum batch length. Zero defaults to 1 (no batching).
	// A target that does not implement BatchHandler always receives single writes.
	BatchSize int
	// BatchInterval bounds collection time from the first dequeued record.
	// Zero defaults to 10 milliseconds; ignored for single writes.
	BatchInterval time.Duration
	// OnError is called once per failed single write or batch, on the consumer.
	// It must return promptly and must not log through or close this wrapper.
	OnError func(error)
}

// BatchRecord preserves each record's context values, without the caller's
// cancellation or deadline. The HandleBatch context controls the whole write.
type BatchRecord struct {
	Context context.Context
	Record  slog.Record
}

// BatchHandler optionally writes records in one atomic operation. Implementations
// must commit all records or roll back on failure, honor ctx, and complete writes
// before returning. They must not retain the records slice after returning.
// WithAttrs/WithGroup must preserve this interface to keep batching on a view.
type BatchHandler interface {
	slog.Handler
	HandleBatch(ctx context.Context, records []BatchRecord) error
}

// AsyncStats is a concurrent sample, not an atomic snapshot of every field.
// After a completed Close, Enqueued == Written + Failed and Queued == InFlight == 0.
type AsyncStats struct {
	Enqueued       uint64
	Written        uint64
	Failed         uint64
	Dropped        uint64
	Rejected       uint64 // Direct Handle calls after closing; Enabled returns false.
	WrittenBatches uint64 // Successful HandleBatch calls, including partial batches.
	FailedBatches  uint64
	Queued         int
	InFlight       int // Collected/processing records, including one possible lookahead.
	LastError      error
}

// AsyncHandler queues records for a single background consumer. Only wrap the
// target that needs asynchronous writes; other fanout targets remain synchronous.
// Derived views share the queue, statistics and lifecycle. Batch boundaries are
// contiguous records from the very same view, not merely equal attributes.
//
// LogValuers and group structures are snapshotted before enqueueing (or binding
// WithAttrs). Any values retain their original types and references: callers must
// not subsequently mutate referenced maps, slices or pointers. Queueing is
// nonblocking, but attribute resolution and the target's Enabled/With methods run
// on the caller. This wrapper never closes the underlying handler or database.
type AsyncHandler struct {
	next  slog.Handler
	batch BatchHandler
	state *asyncState
}

type asyncEntry struct {
	view     *AsyncHandler
	ctx      context.Context
	record   slog.Record
	dequeued time.Time
}

type asyncState struct {
	opts  AsyncOptions
	queue chan asyncEntry
	done  chan struct{}
	// gate serializes closing the queue against nonblocking sends. No target or
	// user callback is invoked while holding it.
	gate   sync.RWMutex
	closed bool
	mu     sync.Mutex
	stats  AsyncStats
	first  error
}

var _ slog.Handler = (*AsyncHandler)(nil)

// NewAsyncHandler creates a caller-owned wrapper and starts one consumer.
// Ordinary slog logging methods discard queue errors; monitor Stats for loss.
func NewAsyncHandler(next slog.Handler, opts AsyncOptions) (*AsyncHandler, error) {
	if next == nil {
		return nil, errors.New("slogs: async target is nil")
	}
	if opts.QueueSize < 0 || opts.BatchSize < 0 || opts.WriteTimeout < 0 || opts.BatchInterval < 0 {
		return nil, errors.New("slogs: async sizes and durations must not be negative")
	}
	if opts.QueueSize == 0 {
		opts.QueueSize = 1024
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 5 * time.Second
	}
	if opts.BatchSize == 0 {
		opts.BatchSize = 1
	}
	if opts.BatchInterval == 0 {
		opts.BatchInterval = 10 * time.Millisecond
	}
	s := &asyncState{opts: opts, queue: make(chan asyncEntry, opts.QueueSize), done: make(chan struct{})}
	h := newAsyncView(next, s)
	go s.run()
	return h, nil
}

func newAsyncView(next slog.Handler, state *asyncState) *AsyncHandler {
	batch, _ := next.(BatchHandler)
	return &AsyncHandler{next: next, batch: batch, state: state}
}

// Enabled preserves this view's level policy, and returns false after closing.
func (h *AsyncHandler) Enabled(ctx context.Context, level slog.Level) bool {
	h.state.gate.RLock()
	closed := h.state.closed
	h.state.gate.RUnlock()
	return !closed && h.next.Enabled(asyncContext(ctx), level)
}

// Handle snapshots and queues one record, or returns ErrAsyncQueueFull or
// ErrAsyncClosed. Accepted records survive cancellation of the caller's context.
func (h *AsyncHandler) Handle(ctx context.Context, record slog.Record) error {
	s := h.state
	s.gate.RLock()
	closed := s.closed
	s.gate.RUnlock()
	if closed {
		s.mu.Lock()
		s.stats.Rejected++
		s.mu.Unlock()
		return ErrAsyncClosed
	}
	copy := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		copy.AddAttrs(snapshotAsyncAttr(attr))
		return true
	})
	entry := asyncEntry{view: h, ctx: context.WithoutCancel(asyncContext(ctx)), record: copy}
	s.gate.RLock()
	defer s.gate.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.stats.Rejected++
		return ErrAsyncClosed
	}
	select {
	case s.queue <- entry:
		s.stats.Enqueued++
		return nil
	default:
		s.stats.Dropped++
		return ErrAsyncQueueFull
	}
}

// WithAttrs resolves bound values now and creates a view sharing this queue.
func (h *AsyncHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	copy := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		copy[i] = snapshotAsyncAttr(attr)
	}
	return newAsyncView(h.next.WithAttrs(copy), h.state)
}

// WithGroup derives a target view while retaining the shared queue and lifecycle.
func (h *AsyncHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return newAsyncView(h.next.WithGroup(name), h.state)
}

func snapshotAsyncAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		copy := make([]slog.Attr, len(group))
		for i, child := range group {
			copy[i] = snapshotAsyncAttr(child)
		}
		attr.Value = slog.GroupValue(copy...)
	}
	return attr
}

func asyncContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// Stats returns counters and approximate current occupancy. Failed writes are
// separate from dropped/rejected records. LastError is the latest target failure.
func (h *AsyncHandler) Stats() AsyncStats {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	stats := h.state.stats
	stats.Queued = len(h.state.queue)
	return stats
}

// Close stops acceptance for every view and waits for all accepted records and
// callbacks. A context timeout only stops this wait; the consumer keeps draining.
// Call again to wait, keeping the target/database open until draining completes.
// On completion it returns the first write failure (not queue overflow errors).
func (h *AsyncHandler) Close(ctx context.Context) error {
	s := h.state
	s.gate.Lock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
	s.gate.Unlock()
	ctx = asyncContext(ctx)
	select {
	case <-s.done:
	default:
		select {
		case <-s.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.first
}

func (s *asyncState) read(entry asyncEntry) asyncEntry {
	entry.dequeued = time.Now()
	s.mu.Lock()
	s.stats.InFlight++
	s.mu.Unlock()
	return entry
}

func (s *asyncState) run() {
	defer close(s.done)
	var pending *asyncEntry
	var batch []BatchRecord
	for {
		var first asyncEntry
		if pending != nil {
			first, pending = *pending, nil
		} else {
			entry, ok := <-s.queue
			if !ok {
				return
			}
			first = s.read(entry)
		}
		if s.opts.BatchSize == 1 || first.view.batch == nil {
			ctx, cancel := context.WithTimeout(first.ctx, s.opts.WriteTimeout)
			err := first.view.next.Handle(ctx, first.record)
			cancel()
			s.finish(1, false, err)
			continue
		}
		batch = append(batch[:0], BatchRecord{Context: first.ctx, Record: first.record})
		deadline := first.dequeued.Add(s.opts.BatchInterval)
		timer := time.NewTimer(time.Until(deadline))
	collect:
		for len(batch) < s.opts.BatchSize {
			if !time.Now().Before(deadline) {
				break collect
			}
			// Check the timer first so a perpetually ready queue cannot prolong
			// collection past the interval through repeated select arbitration.
			select {
			case <-timer.C:
				break collect
			default:
			}
			select {
			case entry, ok := <-s.queue:
				if !ok {
					break collect
				}
				entry = s.read(entry)
				if entry.view != first.view {
					pending = &entry
					break collect
				}
				batch = append(batch, BatchRecord{Context: entry.ctx, Record: entry.record})
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), s.opts.WriteTimeout)
		err := first.view.batch.HandleBatch(ctx, batch)
		cancel()
		s.finish(len(batch), true, err)
		clear(batch) // Do not keep request contexts or payloads alive between batches.
	}
}

func (s *asyncState) finish(n int, batch bool, err error) {
	if err != nil {
		if batch {
			err = fmt.Errorf("slogs: async batch of %d records: %w", n, err)
		} else {
			err = fmt.Errorf("slogs: async write: %w", err)
		}
	}
	s.mu.Lock()
	s.stats.InFlight -= n
	if err == nil {
		s.stats.Written += uint64(n)
		if batch {
			s.stats.WrittenBatches++
		}
	} else {
		s.stats.Failed += uint64(n)
		s.stats.LastError = err
		if s.first == nil {
			s.first = err
		}
		if batch {
			s.stats.FailedBatches++
		}
	}
	s.mu.Unlock()
	if err != nil && s.opts.OnError != nil {
		s.opts.OnError(err)
	}
}
