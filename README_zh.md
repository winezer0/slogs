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

## 自定义标准 Handler

`NewLogger(config, handlers...)` 可追加数据库等自定义 `slog.Handler`，复用现有分发器；各 Handler 自行控制日志级别，关闭控制台或文件输出不会关闭自定义目标。

```go
logger, err := slogs.NewLogger(config, taskHandler)
if err != nil {
    return err
}
defer logger.Close()
logger.Slog().InfoContext(ctx, "task started", "task_id", taskID)
```

传入的 Handler 由调用方管理生命周期，`Close` 不负责关闭它们；不允许传入 nil。分发器会尝试所有已启用目标，并返回聚合错误。标准 `slog.Logger` 会丢弃 Handler 返回的错误，持久化目标应自行记录失败状态或提供错误回调，避免静默丢失日志。

## 可选异步 Handler

只包装数据库等慢目标，控制台和文件保持现有同步行为：

```go
async, err := slogs.NewAsyncHandler(databaseHandler, slogs.AsyncOptions{
    QueueSize:     1024,
    WriteTimeout:  5 * time.Second,
    BatchSize:     100,
    BatchInterval: 10 * time.Millisecond,
    OnError:       func(err error) { /* 使用其他渠道报告错误 */ },
})
if err != nil {
    return err
}
logger, err := slogs.NewLogger(config, async)
if err != nil {
    _ = async.Close(context.Background())
    return err
}
// 使用 logger，退出时先停止产生新日志，再排空异步队列。
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
if err := async.Close(ctx); err != nil {
    // 等待超时后后台仍继续排空，保持数据库开启并再次调用 Close 等待。
    return err
}
// 数据库仍由调用方管理，此时可关闭；Logger.Close 只关闭库自有文件资源。
_ = logger.Close()
```

零值配置默认排队 1024 条、写入超时 5 秒、逐条消费。队列满时丢弃**新日志**并返回 `ErrAsyncQueueFull`，关闭后的直接调用返回 `ErrAsyncClosed`。常规日志方法会忽略这些返回错误，应通过 `Stats()` 查看丢弃、拒绝、写入失败、积压和批次计数。`OnError` 在消费者中每次写入或整批失败时调用一次，必须快速返回，不得通过同一包装器记录日志或调用其关闭方法。

底层实现 `slogs.BatchHandler` 且配置 `BatchSize > 1` 时启用批量提交。`HandleBatch(ctx, []slogs.BatchRecord)` 须保证整批提交或回滚，遵守写入 context，返回后不得保留传入切片；每条记录的 `Context` 单独保留原始上下文值，移除原请求取消和截止时间。单消费者只合并连续且属于**同一派生实例**的记录，达到条数、等待时间（默认 10ms）、实例切换或关闭时提交。派生实例未保留批量接口时退回逐条写入。批次失败按整批条数和一次失败批次计数，不重试、不回退逐条写入。独立 `perf` 模块提供 GORM/SQLite 事务适配，主库不依赖 GORM。

入队及绑定属性前解析 `LogValuer` 并复制属性、分组结构；`Any` 中 map、slice、指针保持原类型和引用，调用方之后不得修改。容量限制的是排队条数，不包含消费者持有的批次、预读记录，也不是字节上限。请求取消不影响已接收日志，底层须配合独立写入超时。派生实例共享队列、统计和关闭状态。`Close(ctx)` 停止接收并排空，完成后返回首次写入错误；context 仅限制本次等待，不取消后台排空。包装器不会关闭底层 handler 或数据库，队列丢弃通过统计体现，不作为关闭错误返回。

### 数据库配置建议与短测

支持批量接口的数据库 handler 可从 `BatchSize: 100`、`BatchInterval: 10*time.Millisecond`、`QueueSize: 1024`、`WriteTimeout: 5*time.Second` 开始。这些是配置起点，不是生产容量保证；库的零值配置仍然**逐条写入**。队列容量应结合允许的突发量及排空时间确定，持续溢出需要降低输入量或提高数据库处理能力。尽量复用派生 logger，频繁切换实例会拆散批次，应同时关注平均批次大小和丢弃、失败计数。SQLite 两种 journal/synchronous 配置的持久性语义不同。

独立 `perf` 模块默认运行短时数据库矩阵：1KiB 负载、1／16 并发、预热 1 秒、测量 5 秒、重复三次。在 `perf` 目录执行，需启用 CGO 并安装 C 编译器：

```powershell
$env:CGO_ENABLED = "1"
go run . -targets db-delete,async-delete,batch-delete,db-wal,async-wal,batch-wal
go run . -targets batch-delete,batch-wal -workers 16 -rate 1000
```

`db-*` 为同步逐条、`async-*` 为异步逐条、`batch-*` 为异步批量。可通过 `-queue-size`、`-batch-size`、`-batch-interval`、`-write-timeout` 调整参数。`-rate` 限制每秒总调用尝试数，实际发送可能更低；零值刻意让生产者不限速，可能大量丢弃。预热使用独立包装器，完全排空并清除数据后才开始正式测量。输出分别报告调用延迟／速率、接收／写入／丢弃数量、丢弃率、测量期间近似完成写入数、**包含最终排空时间**的有效落库速率、平均批次大小、排空时间和数据库关闭时间。内存分配包含测量及排空阶段，按调用尝试数分摊。排空后校验“调用数＝接收＋丢弃＋拒绝”“接收＝成功＋失败”，并核对数据库行数等于成功写入数。高调用速率伴随丢弃不代表数据库吞吐提升；不限速结果包含生产者竞争，也不是数据库自身的独立容量极限。文件及轮转目标仍可显式选择，默认矩阵不包含它们。

### 完整回归、故障注入与预验收

仓库包含两个 Go 模块，应启用 CGO，分别在根目录和 `perf` 目录执行 `go test -race ./... -count=1`。`perf` 回归包含 SQLite `SQLITE_FULL` 容量限制、至少持续两秒且反复失败的写锁、释放容量／锁后的恢复，以及未提交脏页已写入恢复文件时强杀子进程。强杀测试核对已提交数据保留、未提交事务回滚、数据库完整性及重启写入，并明确验证内存队列中的已接收记录会丢失；这不是断电测试。

真实文件系统满测试仅限 Linux，需要 root 和 `unshare`。它只填满**独立挂载命名空间内的 8MiB tmpfs**，验证真实 `ENOSPC`，再释放空间并检查恢复。普通用户回归会跳过此项，Windows／Linux 仍执行 SQLite 页数上限测试。在 Linux 的 `perf` 目录执行：

```sh
CGO_ENABLED=1 go test -race -c -o /tmp/slogs-perf-acceptance.test .
sudo /tmp/slogs-perf-acceptance.test -test.run TestAcceptanceFilesystemFull -test.v
```

持续运行测试默认跳过，需显式开启。在 Windows 的 `perf` 目录执行：

```powershell
$env:CGO_ENABLED = "1"
$env:SLOGS_SOAK_DURATION = "10m"
go test . -run TestAcceptanceSoak -count=1 -timeout=12m -v
Remove-Item Env:SLOGS_SOAK_DURATION
```

Linux 使用 `CGO_ENABLED=1 SLOGS_SOAK_DURATION=10m go test . -run TestAcceptanceSoak -count=1 -timeout=12m -v`。两种 SQLite 配置各使用 16 个生产者，模拟每目标约 500 条／秒、每分钟前五秒约 5000 条／秒的突发，负载 1KiB，并包含请求取消和派生实例切换。每 30 秒采样 GC 后堆内存／对象数、进程 RSS／私有内存（包含原生分配）、goroutine、积压及写入／丢弃计数；排空后核对账目和数据库完整性。突发丢弃允许发生并如实报告，不算写入失败，也不冒充成功写入。

预热后比较首尾各三个样本的中位数，增长阈值为 Go 堆 32MiB、RSS／私有内存各 128MiB、goroutine 八个。这些宽松边界仅用于预验收，不是不存在泄漏的证明。Windows 私有内存为私有提交量，Linux 为私有干净／脏页，绝对值不能直接跨系统对比。十分钟模拟不能替代数小时／数天稳定性，也不等于真实业务 handler 和负载验证。macOS 只验证纯 Go 主库 amd64／arm64 交叉编译，未验证运行行为或 CGO SQLite。

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
