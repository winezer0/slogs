package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDatabaseMeasurementAccounting(t *testing.T) {
	for _, target := range []string{"db-delete", "async-delete", "batch-delete", "db-wal", "async-wal", "batch-wal"} {
		t.Run(target, func(t *testing.T) {
			config := defaultDatabaseConfig()
			config.queueSize, config.batchSize, config.interval = 4, 3, time.Millisecond
			r, err := runDatabase(target, 4, 256, 1, 10*time.Millisecond, 30*time.Millisecond, config)
			if err != nil {
				t.Fatal(err)
			}
			if r.WarmupCalls == 0 || r.Calls == 0 || r.Calls != r.Accepted+r.Dropped+r.Rejected || r.Accepted != r.Written+r.Failed || uint64(r.DBRows) != r.Written || r.Expected != r.Written || r.Failed != 0 || r.PersistedPerSecond <= 0 || r.P99US <= 0 {
				t.Fatalf("incorrect measured-only accounting: %+v", r)
			}
			if r.Mode == "batch" && (r.WrittenBatches == 0 || r.AverageBatch < 1 || r.AverageBatch > 3) {
				t.Fatal(r)
			}
			if r.Mode != "batch" && (r.WrittenBatches != 0 || r.AverageBatch != 1) {
				t.Fatal(r)
			}
		})
	}
}

func TestDatabaseMeasurementRateCap(t *testing.T) {
	config := defaultDatabaseConfig()
	config.rate = 100
	r, err := runDatabase("batch-wal", 1, 256, 1, 0, 60*time.Millisecond, config)
	if err != nil {
		t.Fatal(err)
	}
	if r.WarmupCalls != 0 || r.Calls == 0 || r.Calls > 7 || r.Dropped != 0 || r.RateCap != 100 {
		t.Fatal(r)
	}
}

func TestDatabasePhaseReportsFailure(t *testing.T) {
	for _, mode := range []string{"sync", "async", "batch"} {
		t.Run(mode, func(t *testing.T) {
			db, pool, err := openDB(filepath.Join(t.TempDir(), "failure.db"), "wal")
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			p, err := newDatabasePhase(db, mode, defaultDatabaseConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			if err := pool.Close(); err != nil {
				t.Fatal(err)
			}
			p.logger.InfoContext(context.Background(), "failed write")
			_ = p.close()
			stats := p.stats(1)
			if stats.Enqueued != 1 || stats.Failed != 1 || stats.Written != 0 || stats.LastError == nil {
				t.Fatal(stats)
			}
		})
	}
}

func TestDatabaseMeasurementValidation(t *testing.T) {
	for _, config := range []databaseConfig{{}, {queueSize: -1}, {rate: -1}} {
		if err := config.validate(); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if _, err := runDatabase("unknown", 1, 256, 1, 0, time.Millisecond, defaultDatabaseConfig()); err == nil {
		t.Fatal("unknown target accepted")
	}
}
