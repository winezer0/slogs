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