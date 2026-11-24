package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	slogs "github.com/winezer0/slogs"
)

// The privileged child gets a private mount namespace. Only its 8MiB tmpfs is
// filled, never the host volume; mounts disappear even if the child is killed.
func TestAcceptanceFilesystemFull(t *testing.T) {
	if os.Getenv("SLOGS_TMPFS_CHILD") != "1" {
		if os.Geteuid() != 0 {
			t.Skip("real ENOSPC requires root and a private mount namespace")
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", executable, "-test.run=^TestAcceptanceFilesystemFull$", "-test.v")
		cmd.Env = append(os.Environ(), "SLOGS_TMPFS_CHILD=1", "SLOGS_TMPFS_PATH="+t.TempDir())
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated ENOSPC test: %v\n%s", err, output)
		}
		t.Logf("private namespace result:\n%s", output)
		return
	}
	dir := os.Getenv("SLOGS_TMPFS_PATH")
	if dir == "" {
		t.Fatal("missing isolated mount path")
	}
	if err := syscall.Mount("tmpfs", dir, "tmpfs", 0, "size=8m"); err != nil {
		t.Fatal(err)
	}
	defer syscall.Unmount(dir, 0)
	db, pool, err := openDB(filepath.Join(dir, "full.db"), "delete")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fillerPath := filepath.Join(dir, "filler")
	filler, err := os.Create(fillerPath)
	if err != nil {
		t.Fatal(err)
	}
	block := bytes.Repeat([]byte("x"), 65536)
	for {
		_, err = filler.Write(block)
		if err != nil {
			break
		}
	}
	_ = filler.Close()
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatal("filesystem was not actually full", err)
	}
	failures := make(chan error, 4)
	h, err := slogs.NewAsyncHandler(&dbHandler{db: db}, slogs.AsyncOptions{BatchSize: 2, BatchInterval: time.Hour, OnError: func(err error) { failures <- err }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	for i := 0; i < 2; i++ {
		r := acceptanceRecord("disk-full")
		r.AddAttrs(slog.String("payload", string(block)))
		if err := h.Handle(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	failure := acceptanceFailure(t, failures)
	if err := os.Remove(fillerPath); err != nil {
		t.Fatal(err)
	}
	acceptanceSend(t, h, "recovered-one")
	acceptanceSend(t, h, "recovered-two")
	if err := h.Close(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	stats := h.Stats()
	if stats.Failed != 2 || stats.Written != 2 || stats.FailedBatches != 1 || stats.WrittenBatches != 1 {
		t.Fatal(stats)
	}
	acceptanceIntegrity(t, db, 2)
	t.Logf("real ENOSPC, rollback and post-free recovery verified; error=%v stats=%+v", failure, stats)
}
