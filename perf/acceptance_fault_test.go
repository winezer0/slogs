package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	slogs "github.com/winezer0/slogs"
	"gorm.io/gorm"
)

func acceptanceRecord(message string) slog.Record {
	return slog.NewRecord(time.Now(), slog.LevelInfo, message, 0)
}

func acceptanceSend(t *testing.T, h slog.Handler, message string) {
	t.Helper()
	if err := h.Handle(context.Background(), acceptanceRecord(message)); err != nil {
		t.Fatal(err)
	}
}

func acceptanceFailure(t *testing.T, failures <-chan error) error {
	t.Helper()
	select {
	case err := <-failures:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for background failure")
		return nil
	}
}

func acceptanceIntegrity(t *testing.T, db *gorm.DB, expected int64) {
	t.Helper()
	var count int64
	if err := db.Model(&logRow{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("rows=%d expected=%d", count, expected)
	}
	var result string
	if err := db.Raw("PRAGMA integrity_check").Scan(&result).Error; err != nil || result != "ok" {
		t.Fatalf("integrity=%q err=%v", result, err)
	}
}

func TestAcceptanceSQLiteFullRecovery(t *testing.T) {
	for _, mode := range []string{"delete", "wal"} {
		t.Run(mode, func(t *testing.T) {
			db, pool, err := openDB(filepath.Join(t.TempDir(), "full.db"), mode)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			var pages, limit int
			if err := db.Raw("PRAGMA page_count").Scan(&pages).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Raw(fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Scan(&limit).Error; err != nil || limit != pages {
				t.Fatal(limit, err)
			}
			failures := make(chan error, 4)
			h, err := slogs.NewAsyncHandler(&dbHandler{db: db}, slogs.AsyncOptions{BatchSize: 2, BatchInterval: time.Hour, OnError: func(err error) { failures <- err }})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close(context.Background())
			acceptanceSend(t, h, strings.Repeat("a", 32768))
			acceptanceSend(t, h, strings.Repeat("b", 32768))
			failure := acceptanceFailure(t, failures)
			var sqliteErr sqlite3.Error
			if !errors.As(failure, &sqliteErr) || sqliteErr.Code != sqlite3.ErrFull {
				t.Fatal("expected SQLITE_FULL", failure)
			}
			acceptanceIntegrity(t, db, 0)
			if err := db.Raw("PRAGMA max_page_count=1048576").Scan(&limit).Error; err != nil {
				t.Fatal(err)
			}
			acceptanceSend(t, h, "recovered-one")
			acceptanceSend(t, h, "recovered-two")
			if err := h.Close(context.Background()); !errors.Is(err, failure) {
				t.Fatal("close lost first error", err)
			}
			stats := h.Stats()
			if stats.Failed != 2 || stats.Written != 2 || stats.FailedBatches != 1 || stats.WrittenBatches != 1 || stats.Enqueued != 4 || stats.Queued != 0 || stats.InFlight != 0 {
				t.Fatal(stats)
			}
			acceptanceIntegrity(t, db, 2)
			t.Logf("SQLITE_FULL: rollback and recovery verified: %+v", stats)
		})
	}
}

func TestAcceptancePersistentLockRecovery(t *testing.T) {
	for _, mode := range []string{"delete", "wal"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lock.db")
			db, pool, err := openDB(path, mode)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			_, blocker, err := openDB(path, mode)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Close()
			if err := db.Exec("PRAGMA busy_timeout=75").Error; err != nil {
				t.Fatal(err)
			}
			conn, err := blocker.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			failures := make(chan error, 16)
			h, err := slogs.NewAsyncHandler(&dbHandler{db: db}, slogs.AsyncOptions{QueueSize: 16, BatchSize: 2, BatchInterval: time.Hour, WriteTimeout: time.Second, OnError: func(err error) { failures <- err }})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close(context.Background())
			lockedAt := time.Now()
			for i := 0; i < 10; i++ {
				acceptanceSend(t, h, fmt.Sprintf("locked-%d", i))
			}
			var first error
			failedBatches := 0
			for i := 0; i < 5; i++ {
				err := acceptanceFailure(t, failures)
				var se sqlite3.Error
				if !errors.As(err, &se) || (se.Code != sqlite3.ErrBusy && se.Code != sqlite3.ErrLocked) {
					t.Fatal(err)
				}
				if first == nil {
					first = err
				}
				failedBatches++
			}
			for time.Since(lockedAt) < 2*time.Second {
				acceptanceSend(t, h, "still-locked-one")
				acceptanceSend(t, h, "still-locked-two")
				err := acceptanceFailure(t, failures)
				var se sqlite3.Error
				if !errors.As(err, &se) || (se.Code != sqlite3.ErrBusy && se.Code != sqlite3.ErrLocked) {
					t.Fatal(err)
				}
				failedBatches++
			}
			if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				t.Fatal(err)
			}
			acceptanceSend(t, h, "after-unlock-one")
			acceptanceSend(t, h, "after-unlock-two")
			if err := h.Close(context.Background()); !errors.Is(err, first) {
				t.Fatal(err)
			}
			stats := h.Stats()
			if stats.Failed != uint64(2*failedBatches) || stats.Written != 2 || stats.FailedBatches != uint64(failedBatches) || stats.WrittenBatches != 1 || stats.Dropped != 0 {
				t.Fatal(stats)
			}
			acceptanceIntegrity(t, db, 2)
			t.Logf("held write lock across %d failures (%s), subsequent batch recovered: %+v", failedBatches, time.Since(lockedAt), stats)
		})
	}
}

type acceptanceStartedHandler struct {
	slog.Handler
	started chan struct{}
	once    sync.Once
}

func (h *acceptanceStartedHandler) Handle(ctx context.Context, r slog.Record) error {
	h.once.Do(func() { close(h.started) })
	return h.Handler.Handle(ctx, r)
}

// Executed only as a disposable child. The parent kills it with an open write
// transaction, one committed row, and accepted records still in memory.
func TestAcceptanceCrashHelper(t *testing.T) {
	if os.Getenv("SLOGS_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	db, pool, err := openDB(os.Getenv("SLOGS_CRASH_DB"), os.Getenv("SLOGS_CRASH_MODE"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	h, err := slogs.NewAsyncHandler(&dbHandler{db: db}, slogs.AsyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	acceptanceSend(t, h, "committed")
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Force dirty pages onto disk before killing the process, rather than only
	// discarding an uncommitted transaction that still fits in SQLite's cache.
	for _, pragma := range []string{"PRAGMA cache_size=1", "PRAGMA cache_spill=ON"} {
		if _, err := conn.ExecContext(context.Background(), pragma); err != nil {
			t.Fatal(err)
		}
	}
	sidecar := os.Getenv("SLOGS_CRASH_DB") + "-journal"
	if os.Getenv("SLOGS_CRASH_MODE") == "wal" {
		sidecar = os.Getenv("SLOGS_CRASH_DB") + "-wal"
	}
	sidecarSize := func() int64 {
		info, err := os.Stat(sidecar)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	before := sidecarSize()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "INSERT INTO log_rows (message,attributes) VALUES ('uncommitted',?)", strings.Repeat("x", 256<<10)); err != nil {
		t.Fatal(err)
	}
	recoveryBytes := sidecarSize() - before
	if recoveryBytes <= 0 {
		t.Fatal("uncommitted transaction did not spill pages to its recovery file")
	}
	started := &acceptanceStartedHandler{Handler: &dbHandler{db: db}, started: make(chan struct{})}
	pending, err := slogs.NewAsyncHandler(started, slogs.AsyncOptions{QueueSize: 16, WriteTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	acceptanceSend(t, pending, "pending-0")
	select {
	case <-started.started:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not start")
	}
	for i := 1; i < 8; i++ {
		acceptanceSend(t, pending, fmt.Sprintf("pending-%d", i))
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"ready": true, "accepted": pending.Stats().Enqueued, "recovery_bytes": recoveryBytes}); err != nil {
		t.Fatal(err)
	}
	select {} // No Close/deferred cleanup before Process.Kill.
}

func TestAcceptanceProcessKillRecovery(t *testing.T) {
	for _, mode := range []string{"delete", "wal"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crash.db")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestAcceptanceCrashHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "SLOGS_CRASH_HELPER=1", "SLOGS_CRASH_DB="+path, "SLOGS_CRASH_MODE="+mode)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if !waited {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			type response struct {
				Ready         bool   `json:"ready"`
				Accepted      uint64 `json:"accepted"`
				RecoveryBytes int64  `json:"recovery_bytes"`
				Err           error  `json:"-"`
			}
			ready := make(chan response, 1)
			go func() {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					if !strings.HasPrefix(scanner.Text(), "{") {
						continue
					}
					var r response
					if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
						ready <- response{Err: err}
						return
					}
					if r.Ready {
						ready <- r
						return
					}
				}
				ready <- response{Err: fmt.Errorf("helper exited before ready: %v", scanner.Err())}
			}()
			var status response
			select {
			case status = <-ready:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if status.Err != nil {
				t.Fatal(status.Err)
			}
			if status.Accepted != 8 || status.RecoveryBytes <= 0 {
				t.Fatal(status)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			if err == nil {
				t.Fatal("helper was not forcibly terminated")
			}
			db, pool, err := openDB(path, mode)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			acceptanceIntegrity(t, db, 1)
			var rows []logRow
			if err := db.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].Message != "committed" {
				t.Fatal(rows, err)
			}
			h, err := slogs.NewAsyncHandler(&dbHandler{db: db}, slogs.AsyncOptions{})
			if err != nil {
				t.Fatal(err)
			}
			acceptanceSend(t, h, "after-restart")
			if err := h.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			acceptanceIntegrity(t, db, 2)
			t.Logf("committed row retained, spilled transaction rolled back (%d recovery bytes), %d accepted in-memory records lost as expected; restart writable", status.RecoveryBytes, status.Accepted)
		})
	}
}
