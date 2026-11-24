package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	slogs "github.com/winezer0/slogs"
)

func TestHandlerAttributesGroupsAndConcurrency(t *testing.T) {
	for _, mode := range []string{"delete", "wal"} {
		t.Run(mode, func(t *testing.T) {
			db, pool, err := openDB(filepath.Join(t.TempDir(), "test.db"), mode)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			root := &dbHandler{db: db}
			handler := root.WithAttrs([]slog.Attr{slog.String("service", "test")}).WithGroup("request").WithAttrs([]slog.Attr{slog.Int("id", 7)}).WithGroup("detail")
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < 10; j++ {
						r := slog.NewRecord(time.Now(), slog.LevelWarn, "record", 0)
						r.AddAttrs(slog.Group("nested", slog.Bool("ok", true)), slog.String("value", "hello"))
						if err := handler.Handle(context.Background(), r); err != nil {
							t.Error(err)
						}
					}
				}()
			}
			wg.Wait()
			var rows []logRow
			if err := db.Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 160 {
				t.Fatalf("rows=%d", len(rows))
			}
			for _, row := range rows {
				var attrs map[string]any
				if err := json.Unmarshal([]byte(row.Attributes), &attrs); err != nil {
					t.Fatal(err)
				}
				request := attrs["request"].(map[string]any)
				detail := request["detail"].(map[string]any)
				if attrs["service"] != "test" || request["id"] != float64(7) || detail["value"] != "hello" || detail["nested"].(map[string]any)["ok"] != true || row.Level != "WARN" || row.Message != "record" || row.Time.IsZero() {
					t.Fatalf("unexpected row: %+v", row)
				}
			}
			if len(root.attrs) != 0 || len(root.groups) != 0 {
				t.Fatal("derived handler mutated root")
			}
		})
	}
}

func TestHandlerWriteFailures(t *testing.T) {
	db, pool, err := openDB(filepath.Join(t.TempDir(), "test.db"), "wal")
	if err != nil {
		t.Fatal(err)
	}
	handler := &dbHandler{db: db}
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "record", 0)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handler.Handle(canceled, r); err == nil {
		t.Fatal("canceled write accepted")
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), r); err == nil {
		t.Fatal("closed database write accepted")
	}
}

func TestMeasurementVerification(t *testing.T) {
	for _, target := range []string{"null", "file", "db-delete", "db-wal", "both-delete", "both-wal", "rotate"} {
		t.Run(target, func(t *testing.T) {
			r, err := run(target, 4, 256, 1, 10*time.Millisecond, 30*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if r.Calls == 0 || r.P99US <= 0 || r.Errors != 0 {
				t.Fatalf("invalid result: %+v", r)
			}
		})
	}
}

func TestHistogram(t *testing.T) {
	var h histogram
	for _, n := range []uint64{1, 1000, 10000, 100000, 1000000} {
		h.add(n)
	}
	if h.percentile(.5) < 10 || h.percentile(.5) > 10.4 || h.percentile(.99) < 1000 || h.percentile(.99) > 1040 {
		t.Fatalf("incorrect histogram %f %f", h.percentile(.5), h.percentile(.99))
	}
}

func TestHandlerBatchAtomic(t *testing.T) {
	for _, mode := range []string{"delete", "wal"} {
		t.Run(mode, func(t *testing.T) {
			db, pool, err := openDB(filepath.Join(t.TempDir(), "batch.db"), mode)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			h := &dbHandler{db: db}
			makeRecord := func(message string) slog.Record { return slog.NewRecord(time.Now(), slog.LevelInfo, message, 0) }
			if err := h.Handle(context.Background(), makeRecord("existing")); err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(`CREATE TRIGGER reject_log BEFORE INSERT ON log_rows WHEN NEW.message = 'fail' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`).Error; err != nil {
				t.Fatal(err)
			}
			batch := []slogs.BatchRecord{
				{Context: context.Background(), Record: makeRecord("good")},
				{Context: context.Background(), Record: makeRecord("fail")},
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := h.HandleBatch(canceled, batch); !errors.Is(err, context.Canceled) {
				t.Fatal("batch ignored canceled transaction context", err)
			}
			if err := h.HandleBatch(context.Background(), batch); err == nil {
				t.Fatal("batch failure not reported")
			}
			var count int64
			if err := db.Model(&logRow{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("partial batch committed: rows=%d", count)
			}
			batch[1].Record = makeRecord("another")
			if err := h.HandleBatch(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&logRow{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 3 {
				t.Fatalf("rows=%d", count)
			}
		})
	}
}

func TestHandlerAsyncBatchIntegration(t *testing.T) {
	for _, mode := range []string{"delete", "wal"} {
		t.Run(mode, func(t *testing.T) {
			db, pool, err := openDB(filepath.Join(t.TempDir(), "async.db"), mode)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			h, err := slogs.NewAsyncHandler(&dbHandler{db: db}, slogs.AsyncOptions{BatchSize: 3, BatchInterval: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close(context.Background())
			a := h.WithAttrs([]slog.Attr{slog.String("view", "a")})
			b := h.WithGroup("detail").WithAttrs([]slog.Attr{slog.String("view", "b")})
			for _, item := range []struct {
				handler slog.Handler
				message string
			}{
				{a, "a1"}, {a, "a2"}, {a, "a3"}, {b, "b1"}, {b, "b2"}, {a, "a4"}, {a, "a5"},
			} {
				r := slog.NewRecord(time.Now(), slog.LevelInfo, item.message, 0)
				r.AddAttrs(slog.Int("number", 7))
				if err := item.handler.Handle(context.Background(), r); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			stats := h.Stats()
			if stats.Enqueued != 7 || stats.Written != 7 || stats.WrittenBatches != 3 || stats.Failed != 0 || stats.Dropped != 0 || stats.InFlight != 0 {
				t.Fatal(stats)
			}
			var rows []logRow
			if err := db.Order("id").Find(&rows).Error; err != nil {
				t.Fatal(err)
			} // Wrapper must leave DB open.
			if len(rows) != 7 {
				t.Fatal(len(rows))
			}
			for i, message := range []string{"a1", "a2", "a3", "b1", "b2", "a4", "a5"} {
				if rows[i].Message != message {
					t.Fatalf("order: %+v", rows)
				}
				var attrs map[string]any
				if err := json.Unmarshal([]byte(rows[i].Attributes), &attrs); err != nil {
					t.Fatal(err)
				}
				if message[0] == 'b' {
					attrs = attrs["detail"].(map[string]any)
				}
				if attrs["view"] != string(message[0]) || attrs["number"] != float64(7) {
					t.Fatal(attrs)
				}
			}
		})
	}
}
