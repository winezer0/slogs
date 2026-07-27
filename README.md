# internal/logging

基于 Go 标准库 `log/slog` 的统一日志包，提供结构化日志、文件轮转、多目标输出和命名日志器管理能力。

A unified logging package built on Go's standard `log/slog`, providing structured logging, file rotation, multi-target output, and named logger management.

## 特性 / Features

- **零外部依赖** — 基于 `log/slog`（Go 1.21+ 标准库），文件轮转内置（源自 lumberjack MIT 协议，已 vendor 化）
- **双目标输出** — 控制台（stderr）+ 文件（JSON），通过 `multiHandler` fanout 分发
- **文件轮转** — 内置 `Rotator`，支持按大小/数量/天数自动轮转 + gzip 压缩
- **结构化属性** — `With()` 附加 key-value 上下文，贯穿所有输出目标
- **命名日志器** — `Manager` 管理多个隔离的命名日志器实例
- **全局默认** — 懒初始化 + 包级便捷函数，开箱即用

---

- **Zero external dependencies** — Built on `log/slog` (Go 1.21+ stdlib); file rotation is vendored internally (adapted from lumberjack, MIT License)
- **Dual-target output** — Console (stderr) + file (JSON), dispatched via `multiHandler` fanout
- **File rotation** — Built-in `Rotator` with size/count/age-based rolling + gzip compression
- **Structured attributes** — `With()` attaches key-value context across all output targets
- **Named loggers** — `Manager` manages multiple isolated named logger instances
- **Global default** — Lazy initialization + package-level convenience functions, ready out of the box

## 文件结构 / File Structure

| 文件 / File | 职责 / Responsibility |
|------|------|
| `config.go` | 配置结构体 + 默认值 + 工厂函数 / Config struct, defaults, factory functions |
| `logger.go` | Logger 封装、multiHandler、级别解析、文件轮转接入 / Logger wrapper, multiHandler, level parsing, file handler |
| `default.go` | 全局默认日志器 + 包级便捷函数 / Global default logger + package-level functions |
| `manager.go` | 命名日志器注册/获取/统一关闭 / Named logger registry (create/get/close-all) |
| `rotator.go` | 内置日志文件轮转器（源自 lumberjack） / Built-in log file rotator (adapted from lumberjack) |
| `chown.go` | 非 Linux 平台 chown 空实现 / No-op chown for non-Linux platforms |
| `chown_linux.go` | Linux 文件所有权保持 / Preserve file ownership on Linux |
| `logging_test.go` | 主单元测试 / Main unit tests |
| `rotator_test.go` | 轮转器单元测试 / Rotator unit tests |

## 快速使用 / Quick Start

### 包级函数（零配置） / Package-level Functions (Zero Config)

```go
import "github.com/winezer0/mgsast/internal/logging"

logging.Info("server started", "port", 8080)
logging.Errorf("connection failed: %v", err)
```

### 显式初始化 / Explicit Initialization

```go
cfg := logging.NewConfig("debug", "logs/app.log", "json")
if err := logging.Init(cfg); err != nil {
    log.Fatal(err)
}
logging.Debug("initialized with file output")
```

### 独立 Logger 实例 / Standalone Logger Instance

```go
cfg := logging.Config{
    Level:      "info",
    Format:     "text",
    FilePath:   "logs/audit.log",
    MaxSize:    50,
    MaxBackups: 5,
    MaxAge:     14,
    Compress:   true,
}
logger, err := logging.NewLogger(cfg)
if err != nil {
    log.Fatal(err)
}
defer logger.Close()

// Structured attributes
reqLogger := logger.With("request_id", "abc-123")
reqLogger.Info("processing request", "method", "GET", "path", "/api/scan")
```

### 命名日志器（多模块隔离） / Named Loggers (Multi-module Isolation)

```go
// Create
scanLogger, _ := logging.CreateLogger("scanengine", logging.NewConfig("info", "logs/scan.log", "json"))
auditLogger, _ := logging.CreateLogger("auditflow", logging.NewConfig("debug", "logs/audit.log", "json"))

// Retrieve
logger, ok := logging.GetLogger("scanengine")

// Close all on shutdown
logging.CloseAll()
```

### 获取底层 slog.Logger / Access Underlying slog.Logger

```go
logger := logging.Default()
slogLogger := logger.Slog() // *slog.Logger, can be passed directly to frameworks like eino
```

## 配置说明 / Configuration

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

## 日志级别 / Log Levels

| 级别 / Level | 值 / Value | 用途 / Purpose |
|------|-----|------|
| DEBUG | -4 | 开发调试信息 / Development debug info |
| INFO | 0 | 正常运行状态 / Normal operational status |
| WARN | 4 | 可恢复的异常/降级 / Recoverable anomalies / degradation |
| ERROR | 8 | 需要关注的错误 / Errors requiring attention |

## 输出格式 / Output Format

**控制台 text 模式（stderr） / Console text mode (stderr):**
```
time=2026-07-27T18:00:00.000+08:00 level=INFO source=main.go:42 msg="server started" port=8080
```

**控制台 json 模式（stderr） / Console json mode (stderr):**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

**文件输出（始终 JSON） / File output (always JSON):**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

## 依赖 / Dependencies

- `log/slog` — Go 标准库 / Go standard library
- 无第三方依赖 / No third-party dependencies（文件轮转已内置于 `rotator.go`，源自 lumberjack MIT 协议 / file rotation is vendored in `rotator.go`, adapted from lumberjack MIT License）

## 测试 / Testing

```bash
go test ./internal/logging/... -v -cover
```

覆盖率 ≥ 84% / Coverage ≥ 84%.
# internal/logging

基于 Go 标准库 `log/slog` 的统一日志包，提供结构化日志、文件轮转、多目标输出和命名日志器管理能力。

## 特性

- **零框架依赖** — 基于 `log/slog`（Go 1.21+ 标准库），无第三方日志框架
- **双目标输出** — 控制台（stderr）+ 文件（JSON），通过 `multiHandler` fanout 分发
- **文件轮转** — 集成 `lumberjack.v2`，支持按大小/数量/天数自动轮转 + 压缩
- **结构化属性** — `With()` 附加 key-value 上下文，贯穿所有输出目标
- **命名日志器** — `Manager` 管理多个隔离的命名日志器实例
- **全局默认** — 懒初始化 + 包级便捷函数，开箱即用

## 文件结构

| 文件 | 职责 |
|------|------|
| `config.go` | 配置结构体 + 默认值 + 工厂函数 |
| `logger.go` | Logger 封装、multiHandler、级别解析、文件轮转 |
| `default.go` | 全局默认日志器 + 包级便捷函数 |
| `manager.go` | 命名日志器注册/获取/统一关闭 |

## 快速使用

### 包级函数（零配置）

```go
import "github.com/winezer0/mgsast/internal/logging"

logging.Info("server started", "port", 8080)
logging.Errorf("connection failed: %v", err)
```

### 显式初始化

```go
cfg := logging.NewConfig("debug", "logs/app.log", "json")
if err := logging.Init(cfg); err != nil {
    log.Fatal(err)
}
logging.Debug("initialized with file output")
```

### 独立 Logger 实例

```go
cfg := logging.Config{
    Level:      "info",
    Format:     "text",
    FilePath:   "logs/audit.log",
    MaxSize:    50,
    MaxBackups: 5,
    MaxAge:     14,
    Compress:   true,
}
logger, err := logging.NewLogger(cfg)
if err != nil {
    log.Fatal(err)
}
defer logger.Close()

// 结构化属性
reqLogger := logger.With("request_id", "abc-123")
reqLogger.Info("processing request", "method", "GET", "path", "/api/scan")
```

### 命名日志器（多模块隔离）

```go
// 创建
scanLogger, _ := logging.CreateLogger("scanengine", logging.NewConfig("info", "logs/scan.log", "json"))
auditLogger, _ := logging.CreateLogger("auditflow", logging.NewConfig("debug", "logs/audit.log", "json"))

// 获取
logger, ok := logging.GetLogger("scanengine")

// 程序退出时统一关闭
logging.CloseAll()
```

### 获取底层 slog.Logger

```go
logger := logging.Default()
slogLogger := logger.Slog() // *slog.Logger，可直接传递给 eino 等框架
```

## 配置说明

```yaml
logging:
  level: info          # debug | info | warn | error
  format: text         # text（人类可读）| json（结构化）
  file_path: ""        # 日志文件路径，空 = 仅控制台
  max_size: 100        # 单文件最大 MB
  max_backups: 3       # 保留旧文件数
  max_age: 30          # 保留天数
  compress: true       # 轮转文件是否 gzip 压缩
```

## 日志级别

| 级别 | 值 | 用途 |
|------|-----|------|
| DEBUG | -4 | 开发调试信息 |
| INFO | 0 | 正常运行状态 |
| WARN | 4 | 可恢复的异常/降级 |
| ERROR | 8 | 需要关注的错误 |

## 输出格式

**控制台 text 模式（stderr）：**
```
time=2026-07-27T18:00:00.000+08:00 level=INFO source=main.go:42 msg="server started" port=8080
```

**控制台 json 模式（stderr）：**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

**文件输出（始终 JSON）：**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

## 依赖

- `log/slog` — Go 标准库
- `gopkg.in/natefinch/lumberjack.v2` — 文件轮转

## 测试

```bash
go test ./internal/logging/... -v -cover
```

覆盖率 ≥ 84%。
