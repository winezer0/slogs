# slogs

[中文文档](README_zh.md)

A lightweight, zero-external-dependency unified logging package built on Go's standard `log/slog` (Go 1.21+), providing structured logging, file rotation, multi-target output, and named logger management.

## Features

- **Zero external dependencies** — Built on `log/slog` (Go 1.21+ stdlib); file rotation is vendored internally (adapted from lumberjack, MIT License)
- **Dual-target output** — Console (stderr) + file, dispatched via a built-in fan-out `multiHandler`
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
cfg := slogs.Config{
    Level:      "info",
    Format:     "text",
    FilePath:   "logs/audit.log",
    MaxSize:    50,
    MaxBackups: 5,
    MaxAge:     14,
    Compress:   true,
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
  level: info          # debug | info | warn | error
  format: text         # text (human-readable) | json (structured)
  file_path: ""        # log file path; empty = console only
  max_size: 100        # max megabytes per file before rotation
  max_backups: 3       # max number of old files to retain
  max_age: 30          # max days to retain old files
  compress: true       # gzip compress rotated files
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Level` | string | `"info"` | Minimum log level: debug, info, warn, error |
| `Format` | string | `"text"` | Console and file output format: text or json |
| `FilePath` | string | `""` | Log file path; empty disables file output |
| `MaxSize` | int | `100` | Max MB per file before rotation |
| `MaxBackups` | int | `3` | Max old files to retain |
| `MaxAge` | int | `30` | Max days to retain old files |
| `Compress` | bool | `true` | Gzip compress rotated files |

## Log Levels

| Level | Value | Purpose |
|-------|-------|---------|
| DEBUG | -4 | Development debug info |
| INFO | 0 | Normal operational status |
| WARN | 4 | Recoverable anomalies / degradation |
| ERROR | 8 | Errors requiring attention |

## Output Format

**Console text mode (stderr):**
```
time=2026-07-27T18:00:00.000+08:00 level=INFO source=main.go:42 msg="server started" port=8080
```

**Console json mode (stderr):**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

**File output (format follows `Config.Format`):**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

## File Structure

| File | Responsibility |
|------|----------------|
| `doc.go` | Package-level documentation |
| `config.go` | Config struct, defaults, factory functions |
| `logger.go` | Logger wrapper, multiHandler, level parsing, file handler |
| `default.go` | Global default logger + package-level functions |
| `manager.go` | Named logger registry (create/get/close-all) |
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