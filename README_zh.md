# slogs

[English](README.md)

基于 Go 标准库 `log/slog`（Go 1.21+）的轻量级零外部依赖统一日志包，提供结构化日志、文件轮转、多目标输出和命名日志器管理能力。

## 特性

- **零外部依赖** — 基于 `log/slog`（Go 1.21+ 标准库），文件轮转内置（源自 lumberjack MIT 协议，已 vendor 化）
- **双目标输出** — 控制台（stdout）+ 文件，通过内置 `multiHandler` fanout 分发
- **文件轮转** — 内置 `Rotator`，支持按大小/数量/天数自动轮转 + gzip 压缩
- **结构化属性** — `With()` 附加 key-value 上下文，贯穿所有输出目标
- **命名日志器** — `Manager` 管理多个隔离的命名日志器实例
- **全局默认** — 懒初始化 + 包级便捷函数，开箱即用
- **并发安全** — 所有导出类型和函数均可安全并发使用

## 安装

```bash
go get github.com/winezer0/slogs
```

## 快速使用

### 包级函数（零配置）

```go
import "github.com/winezer0/slogs"

slogs.Info("server started", "port", 8080)
slogs.Errorf("connection failed: %v", err)
```

### 显式初始化

```go
cfg := slogs.NewConfig("debug", "logs/app.log", "json")
if err := slogs.Init(cfg); err != nil {
    log.Fatal(err)
}
slogs.Debug("initialized with file output")
```

### 独立 Logger 实例

```go
cfg := slogs.LogConfig{
    ConsoleLevel: "info",  // 控制台级别（空 = "info"）
    // FileLevel 为空时默认为 "debug" → 文件保留 debug 日志
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

// 结构化属性
reqLogger := logger.With("request_id", "abc-123")
reqLogger.Info("processing request", "method", "GET", "path", "/api/scan")

// Context 日志会把调用方 context 传递给 slog Handler
reqLogger.InfoContext(ctx, "request completed", "status", 200)
reqLogger.LogAttrs(ctx, slog.LevelDebug, "request details", slog.String("path", "/api/scan"))
```

### 命名日志器（多模块隔离）

```go
// 创建
scanLogger, _ := slogs.CreateLogger("scanengine", slogs.NewConfig("info", "logs/scan.log", "json"))
auditLogger, _ := slogs.CreateLogger("auditflow", slogs.NewConfig("debug", "logs/audit.log", "json"))

// 获取
logger, ok := slogs.GetLogger("scanengine")

// 程序退出时统一关闭
slogs.CloseAll()
```

### 获取底层 slog.Logger

```go
logger := slogs.Default()
slogLogger := logger.Slog() // *slog.Logger，可直接传递给 eino 等框架
```

实例 logger 同时提供 `DebugContext`、`InfoContext`、`WarnContext`、`ErrorContext`、`Log` 和 `LogAttrs`，参数语义与标准库 `slog.Logger` 一致。组合根可以继续使用 `Slog()` 传给只接受标准库 logger 的业务包。

## 配置说明

```yaml
logging:
  console_level: info   # 控制台：debug | info | warn | error（空 = info）
  file_level: ""        # 文件：debug | info | warn | error（空 = debug）
  console_format: ""    # 控制台："" | text | json | off | mask 字符串如 "TLCM"（空 = mask "LCM"）
  log_file_format: ""   # 文件："" | text | json | off | mask 字符串如 "CM"（空 = json）
  log_file_path: ""     # 日志文件路径，空 = 仅控制台
  max_size: 100         # 单文件最大 MB
  max_backups: 3        # 保留旧文件数
  max_age: 30           # 保留天数
  compress: true        # 轮转文件是否 gzip 压缩
```

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `ConsoleLevel` | string | `"info"` | 控制台最低日志级别：debug、info、warn、error |
| `FileLevel` | string | `"debug"` | 文件最低日志级别：debug、info、warn、error |
| `ConsoleFormat` | string | `""` | 控制台格式：`text`、`json`、`off` 或 mask 字符串（如 `"TLCM"`）；空 = mask `"LCM"` |
| `LogFileFormat` | string | `""` | 文件格式：`text`、`json`、`off` 或 mask 字符串（如 `"CM"`）；空 = json |
| `LogFilePath` | string | `""` | 日志文件路径；为空则禁用文件输出 |
| `MaxSize` | int | `100` | 单文件最大 MB，超出触发轮转 |
| `MaxBackups` | int | `3` | 保留旧文件最大数量 |
| `MaxAge` | int | `30` | 保留旧文件最大天数 |
| `Compress` | bool | `true` | 轮转文件是否 gzip 压缩 |

> 控制台与文件级别相互独立。例如 `ConsoleLevel: "info"` 且 `FileLevel` 为空（默认 `"debug"`）时，控制台保持安静，而文件仍会记录 debug 日志。

> **"off" 语义：** format 字段设为 `"off"` 即关闭对应目标输出（控制台或文件）。即使 `LogFilePath` 非空，文件目标也会被 `"off"` 关闭。若所有目标都被关闭，logger 静默丢弃所有日志记录。

### `NewConfig` 便捷映射

`NewConfig(level, filePath, format)` 保留旧的三参数签名，将 `format` 参数原样存入 `ConsoleFormat`：

| `format` 参数 | 结果 |
|---------------|------|
| `"text"`、`"json"`、`"off"` | 控制台 text / json / 关闭 |
| mask 字符串，如 `"TLCM"`、`"CM"` | 控制台 mask 格式 |
| `""`（空） | 控制台默认 mask `"LCM"` |

文件输出默认为 `LogFileFormat = ""` → 设置文件路径时使用 json。

## 日志级别

| 级别 | 值 | 用途 |
|------|-----|------|
| DEBUG | -4 | 开发调试信息 |
| INFO | 0 | 正常运行状态 |
| WARN | 4 | 可恢复的异常/降级 |
| ERROR | 8 | 需要关注的错误 |

## 输出格式

**控制台 text 模式（stdout）** — 各级别实际输出：
```
time=2026-08-01T03:12:20.090+08:00 level=DEBUG source=logger.go:69 msg="debug message" db=users slow=true
time=2026-08-01T03:12:20.116+08:00 level=INFO source=logger.go:72 msg="server started" port=8080
time=2026-08-01T03:12:20.116+08:00 level=WARN source=logger.go:75 msg="disk low" free_gb=1.5
time=2026-08-01T03:12:20.116+08:00 level=ERROR source=logger.go:78 msg="connection failed" err=timeout
```
每行依次为：`time`（ISO8601 含毫秒）、`level`、`source`（basename:行号，经 `ReplaceAttr` 缩短）、`msg`，其后是 `key=value` 属性。

**控制台 json 模式（stdout）：**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

**文件输出（格式由 `LogFileFormat` 决定）：**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

### Mask 格式

Mask 格式只渲染选中的字段，输出紧凑单行，由 mask 字符串控制：`T`=时间、`L`=级别、`C`=调用者、`M`=消息（任意组合，如 `"TLCM"`、`"CM"`、`"M"`）。直接在 `ConsoleFormat`/`LogFileFormat` 上设置 mask —— 任何不是 `text`/`json`/`off`/空 的值都会被当作 mask 字符串。

```go
cfg := slogs.LogConfig{
    ConsoleLevel:  "info",
    ConsoleFormat: "TLC",   // 控制台：时间 + 级别 + 调用者
    LogFileFormat: "json",  // 文件：普通 json，与控制台互不影响
    LogFilePath:   "logs/app.log",
}
```

**控制台 `ConsoleFormat: "TLCM"`（stdout）：**
```
2026-07-27T18:00:00+08:00 INFO main.go:42 server started
```

**控制台 `ConsoleFormat: "M"`（stdout）：**
```
server started
```

## 文件结构

| 文件 | 职责 |
|------|------|
| `doc.go` | 包级文档 |
| `config.go` | 配置结构体 + 默认值 + 工厂函数 |
| `logger.go` | Logger 封装、multiHandler、级别解析、文件轮转接入 |
| `default.go` | 全局默认日志器 + 包级便捷函数 |
| `manager.go` | 命名日志器注册/获取/统一关闭 |
| `mask_handler.go` | Mask 格式处理器（T/L/C/M 字段选择） |
| `rotator.go` | 内置日志文件轮转器（源自 lumberjack） |
| `chown.go` | 非 Linux 平台 chown 空实现 |
| `chown_linux.go` | Linux 文件所有权保持 |

## 依赖

- `log/slog` — Go 标准库
- 无第三方依赖（文件轮转已内置于 `rotator.go`，源自 lumberjack MIT 协议）

## 测试

```bash
go test ./... -v -cover
```

## API 文档

完整 API 文档请参阅 [pkg.go.dev/github.com/winezer0/slogs](https://pkg.go.dev/github.com/winezer0/slogs)。

## 许可证

[MIT](LICENSE)
