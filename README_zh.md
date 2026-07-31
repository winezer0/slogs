# slogs

[English](README.md)

基于 Go 标准库 `log/slog`（Go 1.21+）的轻量级零外部依赖统一日志包，提供结构化日志、文件轮转、多目标输出和命名日志器管理能力。

## 特性

- **零外部依赖** — 基于 `log/slog`（Go 1.21+ 标准库），文件轮转内置（源自 lumberjack MIT 协议，已 vendor 化）
- **双目标输出** — 控制台（stderr）+ 文件，通过内置 `multiHandler` fanout 分发
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

// 结构化属性
reqLogger := logger.With("request_id", "abc-123")
reqLogger.Info("processing request", "method", "GET", "path", "/api/scan")
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

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `Level` | string | `"info"` | 最低日志级别：debug、info、warn、error |
| `Format` | string | `"text"` | 控制台和文件输出格式：text 或 json |
| `FilePath` | string | `""` | 日志文件路径；为空则禁用文件输出 |
| `MaxSize` | int | `100` | 单文件最大 MB，超出触发轮转 |
| `MaxBackups` | int | `3` | 保留旧文件最大数量 |
| `MaxAge` | int | `30` | 保留旧文件最大天数 |
| `Compress` | bool | `true` | 轮转文件是否 gzip 压缩 |

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

**文件输出（格式由 `Config.Format` 决定）：**
```json
{"time":"2026-07-27T18:00:00.000+08:00","level":"INFO","source":{"function":"main.main","file":"main.go","line":42},"msg":"server started","port":8080}
```

## 文件结构

| 文件 | 职责 |
|------|------|
| `doc.go` | 包级文档 |
| `config.go` | 配置结构体 + 默认值 + 工厂函数 |
| `logger.go` | Logger 封装、multiHandler、级别解析、文件轮转接入 |
| `default.go` | 全局默认日志器 + 包级便捷函数 |
| `manager.go` | 命名日志器注册/获取/统一关闭 |
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
