package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	slogs "github.com/winezer0/slogs"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	glog "gorm.io/gorm/logger"
)

func openDB(path, mode string) (*gorm.DB, *sql.DB, error) {
	journal, syncMode := "DELETE", "FULL"
	if mode == "wal" {
		journal, syncMode = "WAL", "NORMAL"
	}
	db, err := gorm.Open(sqlite.Open(path+"?_journal_mode="+journal+"&_synchronous="+syncMode+"&_busy_timeout=5000"), &gorm.Config{Logger: glog.Default.LogMode(glog.Silent)})
	if err != nil {
		return nil, nil, err
	}
	pool, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	fail := func(e error) (*gorm.DB, *sql.DB, error) { pool.Close(); return nil, nil, e }
	var actual string
	var syncValue int
	if err = db.Raw("PRAGMA journal_mode").Scan(&actual).Error; err != nil {
		return fail(err)
	}
	if err = db.Raw("PRAGMA synchronous").Scan(&syncValue).Error; err != nil {
		return fail(err)
	}
	want := 2
	if mode == "wal" {
		want = 1
	}
	if !strings.EqualFold(actual, journal) || syncValue != want {
		return fail(fmt.Errorf("unexpected PRAGMA %s/%d", actual, syncValue))
	}
	if err = db.AutoMigrate(&logRow{}); err != nil {
		return fail(err)
	}
	return db, pool, nil
}

// Per-worker histogram: 32 subdivisions per power of two, no shared hot lock.
type histogram [2048]uint64

func (h *histogram) add(ns uint64) {
	if ns == 0 {
		ns = 1
	}
	power := bits.Len64(ns) - 1
	base := uint64(1) << power
	sub := int((ns - base) * 32 / base)
	h[power*32+sub]++
}
func (h *histogram) percentile(q float64) float64 {
	var total uint64
	for _, n := range h {
		total += n
	}
	if total == 0 {
		return 0
	}
	rank := uint64(math.Ceil(float64(total) * q))
	var sum uint64
	for i, n := range h {
		sum += n
		if sum >= rank {
			return float64(uint64(1)<<uint(i/32)) * (1 + float64(i%32+1)/32) / 1000
		}
	}
	return 0
}

type workerResult struct {
	count uint64
	hist  histogram
}

func phase(logger *slogs.Logger, payload string, workers int, duration time.Duration, measure bool, rateCap ...int) (workerResult, time.Duration) {
	results := make([]workerResult, workers)
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(workers)
	done.Add(workers)
	var deadline time.Time
	for i := range results {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			ctx := context.Background()
			var ticks <-chan time.Time
			var stop <-chan time.Time
			if len(rateCap) != 0 && rateCap[0] > 0 {
				interval := time.Second * time.Duration(workers) / time.Duration(rateCap[0])
				if interval < time.Nanosecond {
					interval = time.Nanosecond
				}
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				ticks = ticker.C
				timer := time.NewTimer(time.Until(deadline))
				defer timer.Stop()
				stop = timer.C
			}
			// Use the same public Logger path as callers, with a transparent error tap.
			for time.Now().Before(deadline) {
				if ticks != nil {
					select {
					case <-ticks:
					case <-stop:
						return
					}
					if !time.Now().Before(deadline) {
						return
					}
				}
				var before int64
				if measure {
					before = preciseNow()
				}
				logger.InfoContext(ctx, "performance record", "payload", payload)
				results[i].count++
				if measure {
					results[i].hist.add(preciseElapsed(before))
				}
			}
		}(i)
	}
	ready.Wait()
	begin := time.Now()
	deadline = begin.Add(duration)
	close(start)
	done.Wait()
	var out workerResult
	for _, r := range results {
		out.count += r.count
		for i, n := range r.hist {
			out.hist[i] += n
		}
	}
	return out, time.Since(begin)
}

// Each worker may encounter errors: count them without hiding them behind slog.
type errorTap struct {
	slog.Handler
	state *errorState
}
type errorState struct {
	mu    sync.Mutex
	count uint64
	first string
}

func (h errorTap) Handle(ctx context.Context, r slog.Record) error {
	err := h.Handler.Handle(ctx, r)
	if err != nil {
		h.state.mu.Lock()
		h.state.count++
		if h.state.first == "" {
			h.state.first = err.Error()
		}
		h.state.mu.Unlock()
	}
	return err
}
func (h errorTap) WithAttrs(a []slog.Attr) slog.Handler {
	return errorTap{h.Handler.WithAttrs(a), h.state}
}
func (h errorTap) WithGroup(g string) slog.Handler { return errorTap{h.Handler.WithGroup(g), h.state} }

type nullHandler struct{}

func (nullHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (nullHandler) Handle(context.Context, slog.Record) error { return nil }
func (h nullHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h nullHandler) WithGroup(string) slog.Handler           { return h }

type result struct {
	Target                   string  `json:"target"`
	Workers                  int     `json:"workers"`
	PayloadBytes             int     `json:"payload_bytes"`
	Repeat                   int     `json:"repeat"`
	Calls                    uint64  `json:"calls"`
	Seconds                  float64 `json:"seconds"`
	PerSecond                float64 `json:"per_second"`
	P50US                    float64 `json:"p50_us"`
	P95US                    float64 `json:"p95_us"`
	P99US                    float64 `json:"p99_us"`
	BytesPerCall             float64 `json:"bytes_per_call"`
	AllocsPerCall            float64 `json:"allocs_per_call"`
	Errors                   uint64  `json:"errors"`
	FirstError               string  `json:"first_error,omitempty"`
	Expected                 uint64  `json:"expected_rows"`
	DBRows                   int64   `json:"db_rows"`
	FileRows                 uint64  `json:"file_rows"`
	RotatedFiles             int     `json:"rotated_files"`
	CloseMS                  float64 `json:"close_ms"`
	Mode                     string  `json:"mode,omitempty"`
	WarmupCalls              uint64  `json:"warmup_calls,omitempty"`
	Accepted                 uint64  `json:"accepted"`
	Written                  uint64  `json:"written"`
	Failed                   uint64  `json:"failed"`
	Dropped                  uint64  `json:"dropped"`
	Rejected                 uint64  `json:"rejected"`
	DropPercent              float64 `json:"drop_percent"`
	WrittenDuringMeasurement uint64  `json:"written_during_measurement"`
	SteadyPersistedPerSecond float64 `json:"steady_persisted_per_second"`
	PersistedPerSecond       float64 `json:"persisted_per_second_including_drain"`
	DrainMS                  float64 `json:"drain_ms"`
	QueuedAtEnd              int     `json:"queued_at_end"`
	InFlightAtEnd            int     `json:"in_flight_at_end"`
	WrittenBatches           uint64  `json:"written_batches"`
	FailedBatches            uint64  `json:"failed_batches"`
	AverageBatch             float64 `json:"average_batch"`
	QueueSize                int     `json:"queue_size"`
	BatchSize                int     `json:"batch_size"`
	BatchIntervalMS          float64 `json:"batch_interval_ms"`
	RateCap                  int     `json:"rate_cap"`
}

func countLines(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var reader io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		z, e := gzip.NewReader(f)
		if e != nil {
			return 0, e
		}
		defer z.Close()
		reader = z
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 65536), 1024*1024)
	var n uint64
	for scanner.Scan() {
		n++
	}
	return n, scanner.Err()
}

func run(target string, workers, size, repeat int, warm, duration time.Duration) (out result, err error) {
	dir, err := os.MkdirTemp("", "slogs-perf-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)
	config := slogs.LogConfig{ConsoleFormat: "off", LogFileFormat: "off"}
	hasFile := target == "file" || target == "rotate" || strings.HasPrefix(target, "both-")
	hasDB := strings.HasPrefix(target, "db-") || strings.HasPrefix(target, "both-")
	if hasFile {
		config.LogFilePath = filepath.Join(dir, "events.log")
		config.LogFileFormat = "json"
		config.MaxSize = 1048576
	}
	if target == "rotate" {
		config.MaxSize = 100
		config.Compress = true
	}
	var db *gorm.DB
	var pool *sql.DB
	var extra []slog.Handler
	if hasDB {
		mode := "delete"
		if strings.HasSuffix(target, "wal") {
			mode = "wal"
		}
		db, pool, err = openDB(filepath.Join(dir, "events.db"), mode)
		if err != nil {
			return out, err
		}
		defer pool.Close()
		extra = append(extra, &dbHandler{db: db})
	}
	if target == "null" {
		extra = append(extra, nullHandler{})
	}
	logger, err := slogs.NewLogger(config, extra...)
	if err != nil {
		return out, err
	}
	defer logger.Close()
	state := &errorState{}
	tapped := slog.New(errorTap{logger.Slog().Handler(), state})
	// Reuse public Logger methods while wrapping the entire fanout for error counts.
	measured, err := slogs.NewLogger(slogs.LogConfig{ConsoleFormat: "off", LogFileFormat: "off"}, tapped.Handler())
	if err != nil {
		return out, err
	}
	defer measured.Close()
	payload := strings.Repeat("x", size)
	pre, _ := phase(measured, payload, workers, warm, false)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, elapsed := phase(measured, payload, workers, duration, true)
	runtime.ReadMemStats(&after)
	out = result{Target: target, Workers: workers, PayloadBytes: size, Repeat: repeat, Calls: got.count, Seconds: elapsed.Seconds(), PerSecond: float64(got.count) / elapsed.Seconds(), P50US: got.hist.percentile(.5), P95US: got.hist.percentile(.95), P99US: got.hist.percentile(.99), Errors: state.count, FirstError: state.first, Expected: pre.count + got.count}
	if got.count == 0 {
		return out, errors.New("no measured calls")
	}
	out.BytesPerCall = float64(after.TotalAlloc-before.TotalAlloc) / float64(got.count)
	out.AllocsPerCall = float64(after.Mallocs-before.Mallocs) / float64(got.count)
	if db != nil {
		if err = db.Model(&logRow{}).Count(&out.DBRows).Error; err != nil {
			return out, err
		}
	}
	closeStart := time.Now()
	if err = logger.Close(); err != nil {
		return out, err
	}
	if pool != nil {
		if err = pool.Close(); err != nil {
			return out, err
		}
	}
	out.CloseMS = float64(time.Since(closeStart).Nanoseconds()) / 1e6
	if hasFile {
		files, e := filepath.Glob(filepath.Join(dir, "*.log*"))
		if e != nil {
			return out, e
		}
		out.RotatedFiles = len(files) - 1
		for _, path := range files {
			n, e := countLines(path)
			if e != nil {
				return out, e
			}
			out.FileRows += n
		}
	}
	if out.Errors != 0 || (hasDB && uint64(out.DBRows) != out.Expected) || (hasFile && out.FileRows != out.Expected) {
		return out, fmt.Errorf("write verification failed: %+v", out)
	}
	return out, nil
}

func main() {
	warm := flag.Duration("warmup", time.Second, "warmup per case (database warmup is drained and removed)")
	duration := flag.Duration("duration", 5*time.Second, "measurement per case, excluding drain")
	repeats := flag.Int("repeats", 3, "repetitions")
	targetFlag := flag.String("targets", "db-delete,async-delete,batch-delete,db-wal,async-wal,batch-wal", "database sync/async/batch targets; legacy null,file,rotate,both-delete,both-wal also available")
	skip := flag.Int("skip", 0, "skip completed cases in the selected matrix when resuming")
	workerFlag := flag.String("workers", "1,16", "comma-separated worker counts")
	sizeFlag := flag.String("sizes", "1024", "payload sizes, excluding record metadata")
	defaults := defaultDatabaseConfig()
	queueSize := flag.Int("queue-size", defaults.queueSize, "async waiting-record capacity")
	batchSize := flag.Int("batch-size", defaults.batchSize, "maximum batch size for batch targets")
	batchInterval := flag.Duration("batch-interval", defaults.interval, "maximum batch collection interval")
	writeTimeout := flag.Duration("write-timeout", defaults.timeout, "timeout per database write/batch")
	rateCap := flag.Int("rate", 0, "aggregate producer rate cap for database targets; 0 saturates the target")
	flag.Parse()
	parseInts := func(s string) []int {
		var values []int
		for _, part := range strings.Split(s, ",") {
			var n int
			if _, err := fmt.Sscanf(part, "%d", &n); err != nil || n < 1 {
				panic("invalid positive integer: " + part)
			}
			values = append(values, n)
		}
		return values
	}
	if *duration <= 0 || *warm < 0 || *repeats < 1 || *skip < 0 {
		panic("invalid measurement duration/repeats")
	}
	config := databaseConfig{queueSize: *queueSize, batchSize: *batchSize, interval: *batchInterval, timeout: *writeTimeout, rate: *rateCap}
	if err := config.validate(); err != nil {
		panic(err)
	}
	fmt.Printf("environment: go=%s os=%s arch=%s cpus=%d GOMAXPROCS=%d; CGO SQLite; GORM default transactions; histogram upper bounds ~3%%; allocations include worker setup and database drain; payload excludes metadata\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0))
	var timerHist histogram
	for i := 0; i < 100000; i++ {
		timerHist.add(preciseElapsed(preciseNow()))
	}
	fmt.Printf("timer calibration: median_us=%.6f p99_us=%.6f (included, not subtracted)\n", timerHist.percentile(.5), timerHist.percentile(.99))
	encoder := json.NewEncoder(os.Stdout)
	caseIndex := 0
	for _, target := range strings.Split(*targetFlag, ",") {
		switch target {
		case "null", "file", "db-delete", "db-wal", "async-delete", "async-wal", "batch-delete", "batch-wal", "both-delete", "both-wal", "rotate":
		default:
			panic("unknown target: " + target)
		}
		for _, size := range parseInts(*sizeFlag) {
			for _, workers := range parseInts(*workerFlag) {
				for repeat := 1; repeat <= *repeats; repeat++ {
					caseIndex++
					if caseIndex <= *skip {
						continue
					}
					var r result
					var err error
					if _, _, ok := databaseTarget(target); ok {
						r, err = runDatabase(target, workers, size, repeat, *warm, *duration, config)
					} else {
						r, err = run(target, workers, size, repeat, *warm, *duration)
					}
					if err != nil {
						fmt.Fprintln(os.Stderr, err)
						os.Exit(1)
					}
					if err := encoder.Encode(r); err != nil {
						panic(err)
					}
				}
			}
		}
	}
}
