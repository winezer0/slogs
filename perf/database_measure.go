package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	slogs "github.com/winezer0/slogs"
	"gorm.io/gorm"
)

type databaseConfig struct {
	queueSize, batchSize int
	interval, timeout    time.Duration
	rate                 int
}

func defaultDatabaseConfig() databaseConfig {
	return databaseConfig{queueSize: 1024, batchSize: 100, interval: 10 * time.Millisecond, timeout: 5 * time.Second}
}

func (c databaseConfig) validate() error {
	if c.queueSize < 1 || c.batchSize < 1 || c.interval <= 0 || c.timeout <= 0 || c.rate < 0 {
		return errors.New("database queue/batch sizes and durations must be positive; rate must be nonnegative")
	}
	return nil
}

func databaseTarget(target string) (mode, journal string, ok bool) {
	parts := strings.Split(target, "-")
	if len(parts) != 2 || (parts[1] != "delete" && parts[1] != "wal") {
		return "", "", false
	}
	switch parts[0] {
	case "db":
		mode = "sync"
	case "async":
		mode = "async"
	case "batch":
		mode = "batch"
	default:
		return "", "", false
	}
	return mode, parts[1], true
}

type databasePhase struct {
	logger   *slogs.Logger
	async    *slogs.AsyncHandler
	failures *errorState
}

func newDatabasePhase(db *gorm.DB, mode string, config databaseConfig) (*databasePhase, error) {
	p := &databasePhase{failures: &errorState{}}
	var next slog.Handler = &dbHandler{db: db}
	if mode == "sync" {
		next = errorTap{Handler: next, state: p.failures}
	} else {
		size := 1
		if mode == "batch" {
			size = config.batchSize
		}
		var err error
		p.async, err = slogs.NewAsyncHandler(next, slogs.AsyncOptions{QueueSize: config.queueSize, BatchSize: size, BatchInterval: config.interval, WriteTimeout: config.timeout})
		if err != nil {
			return nil, err
		}
		next = p.async // Do not hide BatchHandler behind an ordinary handler wrapper.
	}
	logger, err := slogs.NewLogger(slogs.LogConfig{ConsoleFormat: "off", LogFileFormat: "off"}, next)
	if err != nil {
		if p.async != nil {
			_ = p.async.Close(context.Background())
		}
		return nil, err
	}
	p.logger = logger
	return p, nil
}

func (p *databasePhase) close() error {
	var err error
	if p.async != nil {
		err = p.async.Close(context.Background())
	}
	return errors.Join(err, p.logger.Close())
}

func (p *databasePhase) stats(calls uint64) slogs.AsyncStats {
	if p.async != nil {
		return p.async.Stats()
	}
	p.failures.mu.Lock()
	defer p.failures.mu.Unlock()
	stats := slogs.AsyncStats{Enqueued: calls, Written: calls - p.failures.count, Failed: p.failures.count}
	if p.failures.first != "" {
		stats.LastError = errors.New(p.failures.first)
	}
	return stats
}

// runDatabase separates warmup from measurement using two wrappers and a drained,
// cleared table. Call throughput is not database throughput: drops are expected
// under saturation, and effective persisted throughput includes the final drain.
func runDatabase(target string, workers, size, repeat int, warm, duration time.Duration, config databaseConfig) (out result, err error) {
	mode, journal, ok := databaseTarget(target)
	if !ok {
		return out, fmt.Errorf("unknown database target: %s", target)
	}
	if err := config.validate(); err != nil {
		return out, err
	}
	if workers < 1 || size < 1 || warm < 0 || duration <= 0 {
		return out, errors.New("invalid database measurement arguments")
	}
	dir, err := os.MkdirTemp("", "slogs-perf-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)
	db, pool, err := openDB(filepath.Join(dir, "events.db"), journal)
	if err != nil {
		return out, err
	}
	defer pool.Close()
	payload := strings.Repeat("x", size)
	var warmCalls uint64
	if warm > 0 {
		pre, err := newDatabasePhase(db, mode, config)
		if err != nil {
			return out, err
		}
		got, _ := phase(pre.logger, payload, workers, warm, false, config.rate)
		if err := pre.close(); err != nil {
			return out, fmt.Errorf("warmup drain: %w", err)
		}
		stats := pre.stats(got.count)
		var rows int64
		if err := db.Model(&logRow{}).Count(&rows).Error; err != nil {
			return out, err
		}
		if uint64(rows) != stats.Written || stats.Failed != 0 {
			return out, errors.New("warmup persistence verification failed")
		}
		warmCalls = got.count
		if err := db.Exec("DELETE FROM log_rows").Error; err != nil {
			return out, err
		}
	}
	p, err := newDatabasePhase(db, mode, config)
	if err != nil {
		return out, err
	}
	defer p.close() // Always drain before the caller-owned DB and temp directory close.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	got, elapsed := phase(p.logger, payload, workers, duration, true, config.rate)
	atEnd := p.stats(got.count)
	drainStart := time.Now()
	closeErr := p.close()
	drain := time.Since(drainStart)
	total := time.Since(started)
	runtime.ReadMemStats(&after)
	stats := p.stats(got.count)
	out = result{Target: target, Mode: mode, Workers: workers, PayloadBytes: size, Repeat: repeat, Calls: got.count, Seconds: elapsed.Seconds(), WarmupCalls: warmCalls, P50US: got.hist.percentile(.5), P95US: got.hist.percentile(.95), P99US: got.hist.percentile(.99), Accepted: stats.Enqueued, Written: stats.Written, Failed: stats.Failed, Dropped: stats.Dropped, Rejected: stats.Rejected, Errors: stats.Failed, Expected: stats.Written, WrittenDuringMeasurement: atEnd.Written, QueuedAtEnd: atEnd.Queued, InFlightAtEnd: atEnd.InFlight, WrittenBatches: stats.WrittenBatches, FailedBatches: stats.FailedBatches, DrainMS: float64(drain.Nanoseconds()) / 1e6, RateCap: config.rate, BatchSize: 1}
	if mode != "sync" {
		out.QueueSize = config.queueSize
	}
	if mode == "batch" {
		out.BatchSize = config.batchSize
		out.BatchIntervalMS = float64(config.interval.Nanoseconds()) / 1e6
	}
	if got.count == 0 {
		return out, errors.New("no measured calls; increase duration or rate")
	}
	out.PerSecond = float64(got.count) / elapsed.Seconds()
	out.PersistedPerSecond = float64(stats.Written) / total.Seconds()
	out.SteadyPersistedPerSecond = float64(atEnd.Written) / elapsed.Seconds()
	out.DropPercent = 100 * float64(stats.Dropped) / float64(got.count)
	out.BytesPerCall = float64(after.TotalAlloc-before.TotalAlloc) / float64(got.count)
	out.AllocsPerCall = float64(after.Mallocs-before.Mallocs) / float64(got.count)
	out.AverageBatch = 1
	if batches := stats.WrittenBatches + stats.FailedBatches; batches != 0 {
		out.AverageBatch = float64(stats.Written+stats.Failed) / float64(batches)
	}
	if closeErr != nil {
		out.FirstError = closeErr.Error()
	} else if stats.LastError != nil {
		out.FirstError = stats.LastError.Error()
	}
	if err := db.Model(&logRow{}).Count(&out.DBRows).Error; err != nil {
		return out, err
	}
	closeStart := time.Now()
	if err := pool.Close(); err != nil {
		return out, err
	}
	out.CloseMS = float64(time.Since(closeStart).Nanoseconds()) / 1e6
	if closeErr != nil {
		return out, fmt.Errorf("measurement drain: %w", closeErr)
	}
	if got.count != stats.Enqueued+stats.Dropped+stats.Rejected || stats.Enqueued != stats.Written+stats.Failed || uint64(out.DBRows) != stats.Written || stats.Queued != 0 || stats.InFlight != 0 || stats.Failed != 0 {
		return out, fmt.Errorf("database persistence/accounting verification failed: %+v", out)
	}
	return out, nil
}
