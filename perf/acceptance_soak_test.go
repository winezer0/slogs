package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	slogs "github.com/winezer0/slogs"
	"gorm.io/gorm"
)

type acceptanceSoakTarget struct {
	mode      string
	db        *gorm.DB
	handler   *slogs.AsyncHandler
	base      slog.Handler
	jobs      chan uint64
	attempted atomic.Uint64
}

type acceptanceMemorySample struct {
	Seconds    float64 `json:"seconds"`
	Heap       uint64  `json:"heap_bytes_after_gc"`
	Objects    uint64  `json:"heap_objects"`
	RSS        uint64  `json:"rss_bytes"`
	Private    uint64  `json:"private_bytes"`
	Goroutines int     `json:"goroutines"`
}

func acceptanceSample(t *testing.T, started time.Time, targets []*acceptanceSoakTarget) acceptanceMemorySample {
	t.Helper()
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	rss, private, err := acceptanceProcessMemory()
	if err != nil {
		t.Fatal(err)
	}
	sample := acceptanceMemorySample{Seconds: time.Since(started).Seconds(), Heap: memory.HeapAlloc, Objects: memory.HeapObjects, RSS: rss, Private: private, Goroutines: runtime.NumGoroutine()}
	stats := map[string]slogs.AsyncStats{}
	for _, target := range targets {
		stats[target.mode] = target.handler.Stats()
	}
	encoded, err := json.Marshal(struct {
		Memory  acceptanceMemorySample      `json:"memory"`
		Targets map[string]slogs.AsyncStats `json:"targets"`
	}{sample, stats})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("soak_sample %s", encoded)
	return sample
}

func TestAcceptanceSoak(t *testing.T) {
	text := os.Getenv("SLOGS_SOAK_DURATION")
	if text == "" {
		t.Skip("opt-in: set SLOGS_SOAK_DURATION=10m for the pre-acceptance soak")
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("memory sampling implemented only for Windows/Linux")
	}
	duration, err := time.ParseDuration(text)
	if err != nil || duration < 2*time.Second {
		t.Fatal("soak duration must be at least 2s", err)
	}
	var targets []*acceptanceSoakTarget
	for _, mode := range []string{"delete", "wal"} {
		db, pool, err := openDB(filepath.Join(t.TempDir(), mode+".db"), mode)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		h, err := slogs.NewAsyncHandler(&dbHandler{db: db}, slogs.AsyncOptions{QueueSize: 1024, BatchSize: 100, BatchInterval: 10 * time.Millisecond, WriteTimeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close(context.Background())
		targets = append(targets, &acceptanceSoakTarget{mode: mode, db: db, handler: h, base: h.WithAttrs([]slog.Attr{slog.String("service", mode)}), jobs: make(chan uint64, 64)})
	}
	unexpected := make(chan error, 1)
	var workers sync.WaitGroup
	payload := strings.Repeat("x", 1024)
	for _, target := range targets {
		for id := 0; id < 16; id++ {
			workers.Add(1)
			go func(target *acceptanceSoakTarget, id int) {
				defer workers.Done()
				view := target.base.WithGroup("diagnostic").WithAttrs([]slog.Attr{slog.Int("worker", id)})
				for sequence := range target.jobs {
					handler := target.base
					if sequence%100 == 0 {
						handler = view
					}
					r := slog.NewRecord(time.Now(), slog.LevelInfo, "simulated request", 0)
					r.AddAttrs(slog.Uint64("sequence", sequence), slog.String("payload", payload), slog.Group("request", slog.String("route", []string{"/read", "/write", "/status"}[sequence%3]), slog.Int("status", 200)))
					ctx, cancel := context.WithCancel(context.Background())
					if sequence%10 == 0 {
						cancel()
					}
					err := handler.Handle(ctx, r)
					cancel()
					target.attempted.Add(1)
					if err != nil && !errors.Is(err, slogs.ErrAsyncQueueFull) {
						select {
						case unexpected <- err:
						default:
						}
					}
				}
			}(target, id)
		}
	}
	started := time.Now()
	ctx, stop := context.WithTimeout(context.Background(), duration)
	defer stop()
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer func() {
			for _, target := range targets {
				close(target.jobs)
			}
		}()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		var sequence uint64
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				n := 5 // About 500 records/s per target, with 5s bursts at about 5000/s.
				if now.Sub(started)%time.Minute < 5*time.Second {
					n = 50
				}
				for i := 0; i < n; i++ {
					sequence++
					for _, target := range targets {
						select {
						case target.jobs <- sequence:
						case <-ctx.Done():
							return
						}
					}
				}
			}
		}
	}()
	// Cleanup order guarantees jobs are closed, workers finish, and queues drain
	// before the caller-owned databases are closed, including on test failure.
	defer func() { stop(); <-producerDone; workers.Wait() }()
	interval := 30 * time.Second
	if duration/5 < interval {
		interval = duration / 5
	}
	warm := time.Minute
	if duration/5 < warm {
		warm = duration / 5
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var baseline, tail []acceptanceMemorySample
	collect := func() {
		sample := acceptanceSample(t, started, targets)
		if time.Duration(sample.Seconds*float64(time.Second)) >= warm {
			if len(baseline) < 3 {
				baseline = append(baseline, sample)
			}
			tail = append(tail, sample)
			if len(tail) > 3 {
				tail = tail[1:]
			}
		}
	}
	running := true
	for running {
		select {
		case <-ticker.C:
			collect()
		case <-ctx.Done():
			running = false
		}
	}
	collect()
	<-producerDone
	workers.Wait()
	for _, target := range targets {
		closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := target.handler.Close(closeCtx)
		cancel()
		if err != nil {
			t.Fatal("soak drain", target.mode, err)
		}
		stats := target.handler.Stats()
		if stats.Enqueued+stats.Dropped+stats.Rejected != target.attempted.Load() || stats.Enqueued != stats.Written+stats.Failed || stats.Failed != 0 || stats.Rejected != 0 || stats.Queued != 0 || stats.InFlight != 0 || stats.Written == 0 {
			t.Fatal(target.mode, stats)
		}
		acceptanceIntegrity(t, target.db, int64(stats.Written))
		t.Logf("soak_final mode=%s attempts=%d stats=%+v", target.mode, target.attempted.Load(), stats)
	}
	select {
	case err := <-unexpected:
		t.Fatal(err)
	default:
	}
	if len(baseline) < 3 || len(tail) < 3 {
		t.Fatal("insufficient memory samples")
	}
	median := func(samples []acceptanceMemorySample, field func(acceptanceMemorySample) uint64) uint64 {
		values := make([]uint64, len(samples))
		for i, s := range samples {
			values[i] = field(s)
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		return values[len(values)/2]
	}
	for _, metric := range []struct {
		name  string
		field func(acceptanceMemorySample) uint64
		limit uint64
	}{
		{"heap", func(s acceptanceMemorySample) uint64 { return s.Heap }, 32 << 20},
		{"rss", func(s acceptanceMemorySample) uint64 { return s.RSS }, 128 << 20},
		{"private", func(s acceptanceMemorySample) uint64 { return s.Private }, 128 << 20},
		{"goroutines", func(s acceptanceMemorySample) uint64 { return uint64(s.Goroutines) }, 8},
	} {
		before, after := median(baseline, metric.field), median(tail, metric.field)
		t.Logf("soak_growth metric=%s baseline=%d tail=%d allowed_growth=%d", metric.name, before, after, metric.limit)
		if after > before && after-before > metric.limit {
			t.Errorf("%s growth exceeded pre-acceptance bound: %d -> %d", metric.name, before, after)
		}
	}
	post := acceptanceSample(t, started, targets)
	if uint64(post.Goroutines) > median(baseline, func(s acceptanceMemorySample) uint64 { return uint64(s.Goroutines) })+4 {
		t.Fatal("goroutines remained after drain", post)
	}
	t.Logf("%s simulated soak completed; bounds are a short pre-acceptance heuristic, not proof against long-term leaks", duration)
}
