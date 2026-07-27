// Package slogs provides a lightweight, zero-external-dependency unified logging
// solution built on Go's standard [log/slog] package (Go 1.21+).
//
// # Features
//
//   - Dual-target output: console (stderr) and file (JSON), dispatched via a
//     built-in fan-out multiHandler.
//   - File rotation: built-in Rotator (adapted from lumberjack, MIT License)
//     supports size-based rolling, backup retention, age-based cleanup, and
//     gzip compression.
//   - Structured attributes: [Logger.With] attaches key-value context that
//     propagates to all output targets.
//   - Named loggers: package-level [CreateLogger]/[GetLogger] manage multiple
//     isolated logger instances via a singleton Manager.
//   - Global default: lazy-initialized default logger with package-level
//     convenience functions ([Info], [Debug], [Error], etc.).
//
// # Quick Start
//
// Zero-configuration usage with the global default logger:
//
//	slogs.Info("server started", "port", 8080)
//	slogs.Errorf("connection failed: %v", err)
//
// Explicit initialization with file output:
//
//	cfg := slogs.NewConfig("debug", "logs/app.log", "json")
//	if err := slogs.Init(cfg); err != nil {
//	    log.Fatal(err)
//	}
//
// Standalone logger instance:
//
//	logger, err := slogs.NewLogger(slogs.Config{
//	    Level:    "info",
//	    FilePath: "logs/audit.log",
//	    MaxSize:  50,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer logger.Close()
//	logger.Info("audit event", "user", "admin")
//
// Named loggers for multi-module isolation:
//
//	scanLog, _ := slogs.CreateLogger("scan", slogs.NewConfig("info", "logs/scan.log", "json"))
//	auditLog, _ := slogs.CreateLogger("audit", slogs.NewConfig("debug", "logs/audit.log", "json"))
//	defer slogs.CloseAll()
//
// # Configuration
//
// The [Config] struct controls logging behavior:
//
//   - Level: minimum log level ("debug", "info", "warn", "error").
//   - Format: console format ("text" or "json").
//   - FilePath: log file path; empty disables file output.
//   - MaxSize: max megabytes per file before rotation (default 100).
//   - MaxBackups: max old files to retain (default 3).
//   - MaxAge: max days to retain old files (default 30).
//   - Compress: gzip-compress rotated files (default true).
//
// # Thread Safety
//
// All exported types and functions are safe for concurrent use. The Manager
// uses a read-write mutex; Logger and Rotator use internal synchronization.
package slogs
