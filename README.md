# slogs

[中文文档](README_zh.md)

A lightweight, zero-external-dependency unified logging package built on Go's standard `log/slog` (Go 1.21+), providing structured logging, file rotation, multi-target output, and named logger management.

## Features

- **Zero external dependencies** — Built on `log/slog` (Go 1.21+ stdlib); file rotation is vendored internally (adapted from lumberjack, MIT License)
- **Dual-target output** — Console (stdout) + file, dispatched via a built-in fan-out `multiHandler`
- **File rotation** — Built-in `Rotator` with size/count/age-based rolling + gzip compression
- **Structured attributes** — `With()` attaches key-value context across all output targets
- **Named loggers** — `Manager` manages multiple isolated named logger instances
- **Global default** — Lazy initialization + package-level convenience functions, ready out of the box
- **Thread-safe** — All exported types and functions are safe for concurrent use

## Custom standard handlers

`NewLogger(config, handlers...)` accepts additional caller-owned `slog.Handler` targets, including database adapters. Each target keeps its own level policy; disabling console/file output does not disable custom targets. Nil handlers are rejected. `Close` only closes resources opened by slogs.

The fan-out attempts every enabled target and aggregates failures. Standard `slog.Logger` discards Handler errors, so durable handlers must retain failures or expose an error callback. Use `logger.Slog()` to inject the resulting standard logger into business code.

## Optional asynchronous handlers

Wrap only the slow target; console/file outputs and existing callers stay synchronous.

```go
async, err := slogs.NewAsyncHandler(databaseHandler, slogs.AsyncOptions{
    QueueSize:     1024,
    WriteTimeout:  5 * time.Second,
    BatchSize:     100,
    BatchInterval: 10 * time.Millisecond,
    OnError:       func(err error) { /* report outside this logger */ },
})
if err != nil {
    return err
}
logger, err := slogs.NewLogger(config, async)
if err != nil {
    _ = async.Close(context.Background())
    return err
}
// Use logger, then stop producers before shutting down.
// logger.Close() closes only slogs-owned file resources.
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
if err := async.Close(ctx); err != nil {
    // On a wait timeout, keep the database open and call Close again to wait.
    return err
}
// The database remains caller-owned; it can now be closed.
_ = logger.Close()
```

Zero options default to 1024 queued records, a 5-second write timeout, and single writes. A full queue drops the **new** record (`ErrAsyncQueueFull`); direct calls after closing return `ErrAsyncClosed`. Normal logging discards these errors: monitor `Stats()` for drops, rejections, write failures, occupancy, and batch counters. `OnError` runs once per failed write/batch on the consumer; it must return promptly and must not log through or close the same wrapper.

Targets implementing `slogs.BatchHandler` can opt into batching with `BatchSize > 1`. `HandleBatch(ctx, []slogs.BatchRecord)` must atomically commit or roll back the whole batch, honor the write context, and not retain the supplied slice. Each entry also carries its original context values without cancellation/deadlines. A single consumer batches only consecutive records from the **same derived handler instance**, flushing on size, interval (default 10ms), a view change, or shutdown. Derived targets that lose the batch interface fall back to single writes. Failed batches are counted by record and batch, with no retry or single-write fallback. The isolated `perf` module contains a GORM/SQLite transaction adapter; GORM is not a dependency of slogs.

`LogValuer`s and attribute/group structures are snapshotted on the caller; referenced `Any` maps, slices and pointers are **not deep-copied** and must remain immutable after logging/binding. Queue capacity counts waiting records, not bytes or the consumer's current batch/lookahead. Request cancellation does not cancel accepted records; the target must honor its independent write timeout. All derived views share the queue, statistics and close state. `Close(ctx)` drains accepted records and returns the first write failure; its context only limits waiting. It never closes the underlying handler/database, and queue loss is reported through statistics rather than the close result.

### Database configuration and short checks

For a batch-capable database handler, start with `BatchSize: 100`, `BatchInterval: 10*time.Millisecond`, `QueueSize: 1024`, and `WriteTimeout: 5*time.Second`. These are starting settings, not a production capacity guarantee. The library's zero-value options still use **single writes**. Size the queue against tolerated bursts and drain time; sustained overflow needs lower input volume or more database write capacity. Reuse derived logger instances where possible: frequent view changes split batches, so monitor average batch length as well as drop/failure counters. The two SQLite journal/synchronous configurations have different durability semantics.

The isolated `perf` module defaults to a short database-only matrix (1KiB payload, 1/16 producers, 1-second warmup, 5-second measurement, three repetitions). Run from `perf` with CGO enabled and a C compiler available:

```powershell
$env:CGO_ENABLED = "1"
go run . -targets db-delete,async-delete,batch-delete,db-wal,async-wal,batch-wal
go run . -targets batch-delete,batch-wal -workers 16 -rate 1000
```

`db-*` means synchronous single writes, `async-*` asynchronous single writes, and `batch-*` asynchronous batches. Options include `-queue-size`, `-batch-size`, `-batch-interval`, and `-write-timeout`. `-rate` caps aggregate attempted calls per second (actual delivery can be lower); zero deliberately saturates producers and may cause substantial loss. Warmup uses a separate drained wrapper and its rows are removed before measurement. Output separates call latency/rate, accepted/written/dropped counts, drop percentage, approximate writes completed during measurement, effective persisted rate **including final drain**, average batch length, drain time, and database close time. Allocations include measurement plus drain and are divided by attempted calls. After draining, the tool checks `calls = accepted + dropped + rejected`, `accepted = written + failed`, and database rows equal successful writes. A high call rate with drops is not database throughput; saturation results include producer contention and are not an isolated database capacity limit. File/rotation targets remain available explicitly and are not in the default matrix.

### Regression, fault injection, and pre-acceptance soak

The repository has two Go modules: run `go test -race ./... -count=1` in both the root and `perf` directories with CGO enabled. The `perf` regression suite includes SQLite `SQLITE_FULL` limits, a write lock held across repeated failures for at least two seconds, recovery after capacity/lock restoration, and actual subprocess termination with spilled uncommitted pages. Crash checks retain committed data, roll back the open transaction, verify database integrity/restart writes, and explicitly confirm that accepted in-memory records are lost on termination. These are process-crash tests, not power-loss tests.

The real filesystem-full test is Linux-only and requires root plus `unshare`. It fills only an **8MiB tmpfs inside a private mount namespace**, verifies real `ENOSPC`, then frees space and checks database recovery. Unprivileged runs skip this case; SQLite page-limit tests still run on Windows/Linux. From `perf` on Linux:

```sh
CGO_ENABLED=1 go test -race -c -o /tmp/slogs-perf-acceptance.test .
sudo /tmp/slogs-perf-acceptance.test -test.run TestAcceptanceFilesystemFull -test.v
```

The soak test is opt-in and skipped by default. From `perf` on Windows:

```powershell
$env:CGO_ENABLED = "1"
$env:SLOGS_SOAK_DURATION = "10m"
go test . -run TestAcceptanceSoak -count=1 -timeout=12m -v
Remove-Item Env:SLOGS_SOAK_DURATION
```

On Linux use `CGO_ENABLED=1 SLOGS_SOAK_DURATION=10m go test . -run TestAcceptanceSoak -count=1 -timeout=12m -v`. Both SQLite configurations run with 16 producer workers each, approximately 500 records/s per target and five-second bursts at approximately 5000/s each minute, 1KiB payloads, request cancellation, and derived-view changes. The sampler reports post-GC heap/objects, process RSS/private memory (including native allocations), goroutines, queue occupancy and write/drop counters every 30 seconds. Accounting and integrity are checked after draining. Burst drops are allowed and reported, not classified as write failures or hidden as successful writes.

After warmup, the first/last three sample medians are compared: permitted increases are 32MiB Go heap, 128MiB RSS/private memory, and eight goroutines. These coarse bounds are pre-acceptance heuristics, not proof against leaks. Windows private memory is committed private bytes; Linux uses private clean/dirty pages, so absolute values are not cross-platform equivalents. A ten-minute simulated run does not validate hours/days of stability or a real business handler/workload. macOS validation is limited to pure-Go root-module amd64/arm64 cross-compilation, not runtime or CGO SQLite execution.

## Installation

```bash
go get github.com/winezer0/slogs
```

## Quick Start

### Package-level Functions (Zero Config)

```go
import "github.com/winezer0/slogs"

slogs.Info("server started", "port", 8080)
slogs.Errorf("connection failed: %v", err)
```

### Explicit Initialization

```go
cfg := slogs.NewConfig("debug", "logs/app.log", "json")
if err := slogs.Init(cfg); err != nil {
    log.Fatal(err)
}
slogs.Debug("initialized with file output")
```

### Standalone Logger Instance

```go
cfg := slogs.LogConfig{
    ConsoleLevel: "info",  // console level (empty defaults to "info")
    // FileLevel defaults to "debug" when empty → files capture debug logs
    LogFilePath:  "logs/audit.log",
    MaxSize:      50,
    MaxBackups:   5,
    MaxAge:       14,
    Compress:     true,
}
logger, err := slogs.NewLogger(cfg)
if err != nil {
    log.Fatal(err)
}
defer logger.Close()

// Structured attributes
reqLogger := logger.With("request_id", "abc-123")
reqLogger.Info("processing request", "method", "GET", "path", "/api/scan")

// Context-aware methods forward ctx to the underlying slog.Handler.
reqLogger.InfoContext(ctx, "request completed", "status", 200)
reqLogger.LogAttrs(ctx, slog.LevelDebug, "request details", slog.String("path", "/api/scan"))
```

### Named Loggers (Multi-module Isolation)

```go
// Create
scanLogger, _ := slogs.CreateLogger("scanengine", slogs.NewConfig("info", "logs/scan.log", "json"))
auditLogger, _ := slogs.CreateLogger("auditflow", slogs.NewConfig("debug", "logs/audit.log", "json"))

// Retrieve
logger, ok := slogs.GetLogger("scanengine")

// Close all on shutdown
slogs.CloseAll()
```

### Access Underlying slog.Logger

```go
logger := slogs.Default()
slogLogger := logger.Slog() // *slog.Logger, can be passed to frameworks like eino
```

The instance logger also exposes `DebugContext`, `InfoContext`, `WarnContext`, `ErrorContext`, `Log`, and `LogAttrs` with the same calling conventions as the standard library `slog.Logger`. Use `Slog()` when a business package explicitly depends on the standard logger interface.

## Configuration

```yaml
logging:
  console_level: info   # console: debug | info | warn | error (empty = info)
  file_level: ""        # file: debug | info | warn | error (empty = debug)
  console_format: ""    # console: "" | text | json | off | mask string like "TLCM" (empty = mask "LCM")
  log_file_format: ""   # file: "" | text | json | off | mask string like "CM" (empty = json)
  log_file_path: ""     # log file path; empty = console only
  max_size: 100         # max megabytes per file before rotation
  max_backups: 3        # max number of old files to retain
  max_age: 30           # max days to retain old files
  localtime: false      # local time in rotated filenames; false keeps UTC
  compress: true        # gzip compress rotated files
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ConsoleLevel` | string | `"info"` | Minimum log level for console: debug, info, warn, error |
| `FileLevel` | string | `"debug"` | Minimum log level for file: debug, info, warn, error |
| `ConsoleFormat` | string | `""` | Console format: `text`, `json`, `off`, or a mask string (e.g. `"TLCM"`); empty = mask `"LCM"` |
| `LogFileFormat` | string | `""` | File format: `text`, `json`, `off`, or a mask string (e.g. `"CM"`); empty = json |
| `LogFilePath` | string | `""` | Log file path; empty disables file output |
| `MaxSize` | int | `100` | Max MB per file before rotation |
| `MaxBackups` | int | `3` | Max old files to retain |
| `MaxAge` | int | `30` | Max days to retain old files |
| `LocalTime` | bool | `false` | Use local time in rotated filenames; does not change record timestamps |
| `Compress` | bool | `true` | Gzip compress rotated files |

> Console and file levels are independent. For example, `ConsoleLevel: "info"` with an empty `FileLevel` (defaults to `"debug"`) keeps the console quiet while files capture debug logs.

> **"off" semantics:** a format field set to `"off"` disables its target output (console or file). A file target is disabled even when `LogFilePath` is non-empty. If all targets are disabled, the logger silently discards every record.

### `NewConfig` Convenience Mapping

`NewConfig(level, filePath, format)` keeps the legacy 3-argument signature and stores the `format` argument verbatim in `ConsoleFormat`:

| `format` argument | Result |
|-------------------|--------|
| `"text"`, `"json"`, `"off"` | Console text / json / disabled |
| a mask string, e.g. `"TLCM"`, `"CM"` | Console mask format |
| `""` (empty) | Console defaults to mask `"LCM"` |

The file output defaults to `LogFileFormat = ""` → json when a file path is set.

## Log Levels

| Level | Value | Purpose |
|-------|-------|---------|
| DEBUG | -4 | Development debug info |
| INFO | 0 | Normal operational status |
| WARN | 4 | Recoverable anomalies / degradation |
| ERROR | 8 | Errors requiring attention |

## Output Format

**Console text mode (stdout)** — actual output for each level:
```
time=2026-08-01T03:12:20.090+08:00 level=DEBUG source=logger.go:69 msg="debug message" db=users slow=true
time=2026-08-01T03:12:20.116+08:00 level=INFO source=logger.go:72 msg="server started" port=8080
time=2026-08-01T03:12:20.116+08:00 level=WARN source=logger.go:75 msg="disk low" free_gb=1.5
time=2026-08-01T03:12:20.116+08:00 level=ERROR source=logger.go:78 msg="connection failed" err=timeout
```
Each line: `time` (ISO8601 with milliseconds), `level`, `source` (basename:line, shortened by `ReplaceAttr`), `msg`, then any attributes as `key=value`.

**Console json mode (stdout):**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

**File output (format follows `LogFileFormat`):**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

### Mask Format

Mask format renders only the selected fields in a compact single line, controlled by a mask string: `T`=time, `L`=level, `C`=caller, `M`=message (any combination, e.g. `"TLCM"`, `"CM"`, `"M"`). Set the mask directly on `ConsoleFormat`/`LogFileFormat` — any value that is not `text`/`json`/`off`/empty is treated as a mask string.

```go
cfg := slogs.LogConfig{
    ConsoleLevel:  "info",
    ConsoleFormat: "TLC",   // console: time + level + caller only
    LogFileFormat: "json",  // file: normal json, independent of console
    LogFilePath:   "logs/app.log",
}
```

**Console with `ConsoleFormat: "TLCM"` (stdout):**
```
2026-07-27T18:00:00+08:00 INFO main.go:42 server started
```

**Console with `ConsoleFormat: "M"` (stdout):**
```
server started
```

## File Structure

| File | Responsibility |
|------|----------------|
| `doc.go` | Package-level documentation |
| `config.go` | Config struct, defaults, factory functions |
| `logger.go` | Logger wrapper, multiHandler, level parsing, file handler |
| `default.go` | Global default logger + package-level functions |
| `manager.go` | Named logger registry (create/get/close-all) |
| `mask_handler.go` | Mask format handler (T/L/C/M field selection) |
| `rotator.go` | Built-in log file rotator (adapted from lumberjack) |
| `chown.go` | No-op chown for non-Linux platforms |
| `chown_linux.go` | Preserve file ownership on Linux |

## Dependencies

- `log/slog` — Go standard library
- No third-party dependencies (file rotation is vendored in `rotator.go`, adapted from lumberjack MIT License)

## Testing

```bash
go test ./... -v -cover
```

## API Documentation

Full API documentation is available at [pkg.go.dev/github.com/winezer0/slogs](https://pkg.go.dev/github.com/winezer0/slogs).

## License

[MIT](LICENSE)
