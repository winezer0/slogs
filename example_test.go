package slogs_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/winezer0/slogs"
)

// ExampleInit demonstrates initializing the global default logger with file output.
func ExampleInit() {
	dir, _ := os.MkdirTemp("", "slogs-example")
	defer os.RemoveAll(dir)

	cfg := slogs.NewConfig("debug", filepath.Join(dir, "app.log"), "json")
	if err := slogs.Init(cfg); err != nil {
		fmt.Println("init error:", err)
		return
	}
	slogs.Debug("initialized with file output")
	// Output is written to stderr and the log file.
}

// ExampleDefault demonstrates using the global default logger with zero configuration.
func ExampleDefault() {
	logger := slogs.Default()
	logger.Info("using default logger", "version", "1.0.0")
}

// ExampleNewLogger demonstrates creating a standalone logger instance.
func ExampleNewLogger() {
	dir, _ := os.MkdirTemp("", "slogs-example")
	defer os.RemoveAll(dir)

	cfg := slogs.Config{
		Level:      "info",
		Format:     "text",
		FilePath:   filepath.Join(dir, "audit.log"),
		MaxSize:    50,
		MaxBackups: 5,
		MaxAge:     14,
		Compress:   true,
	}
	logger, err := slogs.NewLogger(cfg)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer logger.Close()

	logger.Info("audit event", "user", "admin", "action", "login")
}

// ExampleLogger_With demonstrates attaching structured attributes to a logger.
func ExampleLogger_With() {
	logger := slogs.Default()
	reqLogger := logger.With("request_id", "abc-123", "service", "gateway")
	reqLogger.Info("processing request", "method", "GET", "path", "/api/users")
}

// ExampleCreateLogger demonstrates creating and retrieving named loggers.
func ExampleCreateLogger() {
	dir, _ := os.MkdirTemp("", "slogs-example")
	defer os.RemoveAll(dir)
	defer slogs.CloseAll()

	_, err := slogs.CreateLogger("scan", slogs.NewConfig("info", filepath.Join(dir, "scan.log"), "json"))
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	logger, ok := slogs.GetLogger("scan")
	fmt.Println("retrieved:", ok)
	logger.Info("scan completed", "targets", 42)
	// Output: retrieved: true
}

// ExampleSetDefault demonstrates replacing the global default logger.
func ExampleSetDefault() {
	dir, _ := os.MkdirTemp("", "slogs-example")
	defer os.RemoveAll(dir)

	cfg := slogs.NewConfig("warn", filepath.Join(dir, "warn.log"), "json")
	logger, err := slogs.NewLogger(cfg)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer logger.Close()

	slogs.SetDefault(logger)
	// Now package-level functions use the new logger.
	slogs.Warn("disk usage high", "percent", 92)
}

// ExampleDefaultConfig demonstrates obtaining the default configuration.
func ExampleDefaultConfig() {
	cfg := slogs.DefaultConfig()
	fmt.Println(cfg.Level)
	fmt.Println(cfg.Format)
	fmt.Println(cfg.MaxSize)
	// Output:
	// info
	// text
	// 100
}

// ExampleNewConfig demonstrates creating a configuration with custom parameters.
func ExampleNewConfig() {
	cfg := slogs.NewConfig("error", "/var/log/myapp.log", "json")
	fmt.Println(cfg.Level)
	fmt.Println(cfg.FilePath)
	fmt.Println(cfg.Compress)
	// Output:
	// error
	// /var/log/myapp.log
	// true
}
