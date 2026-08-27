package slogs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type contextCaptureHandler struct {
	ctx context.Context
}

func (h *contextCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *contextCaptureHandler) Handle(ctx context.Context, _ slog.Record) error {
	h.ctx = ctx
	return nil
}
func (h *contextCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *contextCaptureHandler) WithGroup(string) slog.Handler      { return h }

type countingCloser struct {
	count int
}

func (c *countingCloser) Close() error {
	c.count++
	return nil
}

type contextKey string

func TestLogger_ContextMethodsForwardContext(t *testing.T) {
	handler := &contextCaptureHandler{}
	logger := &Logger{slog: slog.New(handler)}
	ctx := context.WithValue(context.Background(), contextKey("request_id"), "req-1")

	logger.InfoContext(ctx, "context info")
	if handler.ctx == nil || handler.ctx.Value(contextKey("request_id")) != "req-1" {
		t.Fatal("InfoContext did not forward the caller context")
	}
	logger.Log(ctx, slog.LevelWarn, "context warning")
	if handler.ctx == nil || handler.ctx.Value(contextKey("request_id")) != "req-1" {
		t.Fatal("Log did not forward the caller context")
	}
	logger.LogAttrs(ctx, slog.LevelError, "context error", slog.String("component", "test"))
	if handler.ctx == nil || handler.ctx.Value(contextKey("request_id")) != "req-1" {
		t.Fatal("LogAttrs did not forward the caller context")
	}
}

func TestLoggerWithSharesCloseLifecycle(t *testing.T) {
	closer := &countingCloser{}
	root := &Logger{slog: slog.New(discardHandler{}), lifecycle: &loggerLifecycle{closers: []io.Closer{closer}}}
	child := root.With("component", "child")

	if err := child.Close(); err != nil {
		t.Fatalf("child close: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("root close: %v", err)
	}
	if closer.count != 1 {
		t.Fatalf("shared logger lifecycle closed %d times, want 1", closer.count)
	}
}

func TestNewLoggerRejectsUnopenableLogFile(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger, err := NewLogger(LogConfig{
		ConsoleFormat: "off", LogFileFormat: "json",
		LogFilePath: filepath.Join(parent, "runtime.jsonl"),
	})
	if err == nil {
		_ = logger.Close()
		t.Fatal("NewLogger() error = nil")
	}
}

// --- Config tests ---

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ConsoleLevel != "info" {
		t.Errorf("expected console_level=info, got %q", cfg.ConsoleLevel)
	}
	if cfg.FileLevel != "debug" {
		t.Errorf("expected file_level=debug, got %q", cfg.FileLevel)
	}
	if cfg.ConsoleFormat != "LCM" {
		t.Errorf("expected empty console_format, got %q", cfg.ConsoleFormat)
	}
	if cfg.LogFilePath != "" {
		t.Errorf("expected empty file_path, got %q", cfg.LogFilePath)
	}
	if cfg.MaxSize != 100 {
		t.Errorf("expected max_size=100, got %d", cfg.MaxSize)
	}
	if cfg.MaxBackups != 3 {
		t.Errorf("expected max_backups=3, got %d", cfg.MaxBackups)
	}
	if cfg.MaxAge != 30 {
		t.Errorf("expected max_age=30, got %d", cfg.MaxAge)
	}
	if !cfg.Compress {
		t.Error("expected compress=true")
	}
}

func TestNewConfig(t *testing.T) {
	tests := []struct {
		name          string
		level         string
		filePath      string
		format        string
		wantLevel     string
		wantCF        string // ConsoleFormat
		wantFileLevel string
	}{
		{"empty format", "", "", "", "info", "", "debug"},
		{"text format", "debug", "/tmp/app.log", "text", "debug", "text", "debug"},
		{"json format", "warn", "", "json", "warn", "json", "debug"},
		{"off format", "error", "", "off", "error", "off", "debug"},
		{"mask string", "debug", "/tmp/app.log", "TLCM", "debug", "TLCM", "debug"},
		{"lowercase mask", "debug", "/tmp/app.log", "cm", "debug", "cm", "debug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig(tt.level, tt.filePath, tt.format)
			if cfg.ConsoleLevel != tt.wantLevel {
				t.Errorf("console_level: want %q, got %q", tt.wantLevel, cfg.ConsoleLevel)
			}
			if cfg.ConsoleFormat != tt.wantCF {
				t.Errorf("console_format: want %q, got %q", tt.wantCF, cfg.ConsoleFormat)
			}
			if cfg.LogFilePath != tt.filePath {
				t.Errorf("file_path: want %q, got %q", tt.filePath, cfg.LogFilePath)
			}
			if cfg.FileLevel != tt.wantFileLevel {
				t.Errorf("file_level: want %q, got %q", tt.wantFileLevel, cfg.FileLevel)
			}
		})
	}
}

// --- parseLevel tests ---

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLevel(tt.input)
			if got != tt.want {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- parseFileLevel tests ---

func TestParseFileLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseFileLevel(tt.input)
			if got != tt.want {
				t.Errorf("parseFileLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- Logger tests ---

func TestNewLogger_WithFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	cfg := NewConfig("debug", logFile, "json")
	cfg.LogFileFormat = "json"

	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger with file failed: %v", err)
	}
	logger.Info("file log entry", "key", "value")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "file log entry") {
		t.Errorf("log file should contain message, got: %s", string(data))
	}
	// Verify JSON structure.
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &record); err != nil {
		t.Fatalf("log file should be valid JSON: %v", err)
	}
	if record["msg"] != "file log entry" {
		t.Errorf("expected msg='file log entry', got %v", record["msg"])
	}
}

func TestNewLogger_FileCreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "sub", "dir", "app.log")
	cfg := NewConfig("info", logFile, "json")

	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger should create parent dirs: %v", err)
	}
	logger.Info("nested dir test")
	logger.Close()

	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Error("log file should exist after writing")
	}
}

func TestLogger_With(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "with.log")
	cfg := NewConfig("info", logFile, "json")
	cfg.LogFileFormat = "json"

	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	child := logger.With("component", "test")
	child.Info("with attrs")
	logger.Close()

	data, _ := os.ReadFile(logFile)
	if !strings.Contains(string(data), "component") {
		t.Errorf("log should contain 'component' attr, got: %s", string(data))
	}
	if !strings.Contains(string(data), "test") {
		t.Errorf("log should contain 'test' value, got: %s", string(data))
	}
}

func TestLogger_LevelFiltering(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "level.log")
	cfg := NewConfig("error", logFile, "json")
	cfg.LogFileFormat = "json"
	cfg.FileLevel = "error" // explicit file level to test filtering

	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	logger.Debug("should not appear")
	logger.Info("should not appear")
	logger.Warn("should not appear")
	logger.Error("should appear")
	logger.Close()

	data, _ := os.ReadFile(logFile)
	content := string(data)
	if strings.Contains(content, "should not appear") {
		t.Error("messages below error level should be filtered")
	}
	if !strings.Contains(content, "should appear") {
		t.Error("error message should be present")
	}
}

// TestLogger_FileLevelDefaultsToDebug verifies that an empty FileLevel
// records debug logs in the file even when the console level is higher.
func TestLogger_FileLevelDefaultsToDebug(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "filedebug.log")
	cfg := NewConfig("info", logFile, "json") // console=info, FileLevel="" → debug
	cfg.LogFileFormat = "json"
	cfg.ConsoleFormat = "off" // avoid console noise

	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	logger.Debug("debug should appear in file")
	logger.Info("info should appear in file")
	logger.Close()

	data, _ := os.ReadFile(logFile)
	content := string(data)
	if !strings.Contains(content, "debug should appear in file") {
		t.Error("debug message should appear in file when FileLevel is empty (defaults to debug)")
	}
	if !strings.Contains(content, "info should appear in file") {
		t.Error("info message should appear in file")
	}
}

// --- multiHandler tests ---

func TestMultiHandler_Enabled(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelError})
	h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelDebug})

	mh := &multiHandler{handlers: []slog.Handler{h1, h2}}
	ctx := context.Background()

	if !mh.Enabled(ctx, slog.LevelDebug) {
		t.Error("multiHandler should be enabled if any handler is enabled")
	}
	if !mh.Enabled(ctx, slog.LevelError) {
		t.Error("multiHandler should be enabled for error level")
	}
}

func TestMultiHandler_Handle(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
	h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelWarn})

	mh := &multiHandler{handlers: []slog.Handler{h1, h2}}

	// Use slog.New to properly dispatch records through multiHandler.
	logger := slog.New(mh)
	logger.Info("info message")
	logger.Debug("debug message")

	if !strings.Contains(buf1.String(), "info message") {
		t.Error("h1 (info level) should receive info message")
	}
	if strings.Contains(buf1.String(), "debug message") {
		t.Error("h1 (info level) should not receive debug message")
	}
	if strings.Contains(buf2.String(), "info message") {
		t.Error("h2 (warn level) should not receive info message")
	}
}

func TestMultiHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, nil)
	mh := &multiHandler{handlers: []slog.Handler{h}}

	mh2 := mh.WithAttrs([]slog.Attr{slog.String("k", "v")})
	logger := slog.New(mh2)
	logger.Info("attrs test")

	if !strings.Contains(buf.String(), `"k":"v"`) {
		t.Errorf("expected attr k=v in output, got: %s", buf.String())
	}
}

func TestMultiHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, nil)
	mh := &multiHandler{handlers: []slog.Handler{h}}

	mh2 := mh.WithGroup("grp")
	logger := slog.New(mh2)
	logger.Info("group test", "key", "val")

	if !strings.Contains(buf.String(), "grp") {
		t.Errorf("expected group in output, got: %s", buf.String())
	}
}

func TestFanoutHandler_Single(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, nil)
	result := fanoutHandler([]slog.Handler{h})
	// Single handler should be returned directly (not wrapped).
	if _, ok := result.(*multiHandler); ok {
		t.Error("fanoutHandler with single handler should not wrap in multiHandler")
	}
}

func TestFanoutHandler_Multiple(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)
	result := fanoutHandler([]slog.Handler{h1, h2})
	if _, ok := result.(*multiHandler); !ok {
		t.Error("fanoutHandler with multiple handlers should return multiHandler")
	}
}

// --- Manager tests ---

func TestManager_CreateAndGet(t *testing.T) {
	m := &Manager{loggers: make(map[string]*Logger)}
	cfg := DefaultConfig()

	logger, err := m.Create("test-logger", cfg)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if logger == nil {
		t.Fatal("created logger should not be nil")
	}

	got, ok := m.Get("test-logger")
	if !ok {
		t.Fatal("Get should find created logger")
	}
	if got != logger {
		t.Error("Get should return the same logger instance")
	}
}

func TestManager_CreateDuplicate(t *testing.T) {
	m := &Manager{loggers: make(map[string]*Logger)}
	cfg := DefaultConfig()

	_, err := m.Create("dup", cfg)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}
	_, err = m.Create("dup", cfg)
	if err == nil {
		t.Error("duplicate Create should return error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %v", err)
	}
}

func TestManager_CreateEmptyName(t *testing.T) {
	m := &Manager{loggers: make(map[string]*Logger)}
	_, err := m.Create("", DefaultConfig())
	if err == nil {
		t.Error("empty name should return error")
	}
	if !strings.Contains(err.Error(), "cannot be empty") {
		t.Errorf("error should mention 'cannot be empty', got: %v", err)
	}
}

func TestManager_GetNotFound(t *testing.T) {
	m := &Manager{loggers: make(map[string]*Logger)}
	_, ok := m.Get("nonexistent")
	if ok {
		t.Error("Get for nonexistent logger should return false")
	}
}

func TestManager_CloseAll(t *testing.T) {
	tmpDir := t.TempDir()
	m := &Manager{loggers: make(map[string]*Logger)}

	cfg := NewConfig("info", filepath.Join(tmpDir, "a.log"), "json")
	_, err := m.Create("a", cfg)
	if err != nil {
		t.Fatalf("Create a failed: %v", err)
	}

	cfg2 := NewConfig("info", filepath.Join(tmpDir, "b.log"), "json")
	_, err = m.Create("b", cfg2)
	if err != nil {
		t.Fatalf("Create b failed: %v", err)
	}

	if err := m.CloseAll(); err != nil {
		t.Fatalf("CloseAll failed: %v", err)
	}

	// After CloseAll, registry should be empty.
	_, ok := m.Get("a")
	if ok {
		t.Error("registry should be empty after CloseAll")
	}
}

// --- Default logger tests ---

func TestDefault_LazyInit(t *testing.T) {
	// Reset global state for test isolation.
	oldLogger := defaultLogger.Load()
	defaultLogger.Store(nil)
	defer func() {
		// Restore old logger; close the lazy-init one created by this test.
		if l := defaultLogger.Swap(oldLogger); l != nil {
			l.Close()
		}
	}()

	logger := Default()
	if logger == nil {
		t.Fatal("Default() should never return nil")
	}
	// Should not panic.
	logger.Info("default logger works")
}

func TestSetDefault(t *testing.T) {
	oldLogger := defaultLogger.Load()
	defer func() {
		// Restore old logger; close the test's custom logger.
		if l := defaultLogger.Swap(oldLogger); l != nil {
			l.Close()
		}
	}()

	custom, err := NewLogger(NewConfig("debug", "", "json"))
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	SetDefault(custom)
	if Default() != custom {
		t.Error("SetDefault should replace the default logger")
	}
}

// --- ensureDir tests ---

func TestEnsureDir_CreatesNested(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "a", "b", "c", "file.log")
	if err := ensureDir(target); err != nil {
		t.Fatalf("ensureDir failed: %v", err)
	}
	dir := filepath.Dir(target)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("path should be a directory")
	}
}

func TestEnsureDir_ExistingDir(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "file.log")
	if err := ensureDir(target); err != nil {
		t.Fatalf("ensureDir on existing dir should not fail: %v", err)
	}
}

// --- Manager Remove tests ---

func TestManager_Remove(t *testing.T) {
	m := &Manager{loggers: make(map[string]*Logger)}
	cfg := DefaultConfig()

	logger, err := m.Create("removable", cfg)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Close the logger ourselves to avoid file leak (it's console-only, fine).
	if err := m.Remove("removable"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	_, ok := m.Get("removable")
	if ok {
		t.Error("logger should be removed from registry")
	}
	_ = logger // already closed by Remove
}

func TestManager_RemoveNotFound(t *testing.T) {
	m := &Manager{loggers: make(map[string]*Logger)}
	err := m.Remove("nonexistent")
	if err == nil {
		t.Error("Remove nonexistent should return error")
	}
}

func TestManager_CloseAllWithDefaultLogger(t *testing.T) {
	tmpDir := t.TempDir()

	// Init a default logger with file output.
	oldLogger := defaultLogger.Load()
	defaultLogger.Store(nil)
	defer func() {
		if l := defaultLogger.Swap(oldLogger); l != nil {
			l.Close()
		}
	}()

	cfg := NewConfig("info", filepath.Join(tmpDir, "default.log"), "json")
	if err := Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create a managed logger too.
	_, err := CreateLogger("managed", NewConfig("info", filepath.Join(tmpDir, "managed.log"), "json"))
	if err != nil {
		t.Fatalf("CreateLogger failed: %v", err)
	}

	// CloseAll should close both managed and default loggers.
	if err := CloseAll(); err != nil {
		t.Fatalf("CloseAll failed: %v", err)
	}

	// After CloseAll, default logger should be nil (re-initializable).
	if defaultLogger.Load() != nil {
		t.Error("defaultLogger should be nil after CloseAll")
	}

	// Default logger should be re-initializable on next use.
	Info("re-initialized after closeall")
}

func TestPackageLevel_RemoveLogger(t *testing.T) {
	// Clean up after test.
	defer CloseAll()

	cfg := NewConfig("debug", "", "text")
	_, err := CreateLogger("test-remove", cfg)
	if err != nil {
		t.Fatalf("CreateLogger failed: %v", err)
	}

	if err := RemoveLogger("test-remove"); err != nil {
		t.Fatalf("RemoveLogger failed: %v", err)
	}

	_, ok := GetLogger("test-remove")
	if ok {
		t.Error("logger should be removed")
	}
}

// --- Mask handler tests ---

func TestMaskHandler_Format(t *testing.T) {
	tests := []struct {
		format string
		check  []string // substrings that should appear
		not    []string // substrings that should NOT appear
	}{
		{"TLCM", []string{"INFO", "logging_test.go", "test message"}, nil},
		{"CM", []string{"logging_test.go", "test message"}, nil},
		{"M", []string{"test message"}, []string{"INFO", "logging_test.go"}},
		{"L", []string{"INFO"}, []string{"test message"}},
		{"tlm", []string{"INFO", "test message"}, nil}, // lowercase "tlm" → uppercased "TLM" (T, L, M, no caller)
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			var buf bytes.Buffer
			h := newMaskHandler(tt.format, slog.LevelDebug, &buf)
			logger := slog.New(h)
			logger.Info("test message", "key", "val")

			output := buf.String()
			for _, s := range tt.check {
				if !strings.Contains(output, s) {
					t.Errorf("output %q should contain %q", output, s)
				}
			}
			for _, s := range tt.not {
				if strings.Contains(output, s) {
					t.Errorf("output %q should NOT contain %q", output, s)
				}
			}
		})
	}
}

func TestMaskHandler_Attrs(t *testing.T) {
	var buf bytes.Buffer
	h := newMaskHandler("TLCM", slog.LevelDebug, &buf)
	logger := slog.New(h)
	logger.Info("msg", "key1", "val1", "key2", 42)

	output := buf.String()
	if !strings.Contains(output, "key1=val1") {
		t.Errorf("output should contain key1=val1, got: %s", output)
	}
	if !strings.Contains(output, "key2=42") {
		t.Errorf("output should contain key2=42, got: %s", output)
	}
}

func TestMaskHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := newMaskHandler("TLCM", slog.LevelDebug, &buf)
	logger := slog.New(h).With("component", "test")
	logger.Info("with attrs")

	output := buf.String()
	if !strings.Contains(output, "component=test") {
		t.Errorf("output should contain component=test, got: %s", output)
	}
}

func TestMaskHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := newMaskHandler("TLCM", slog.LevelDebug, &buf)
	logger := slog.New(h).WithGroup("request").With("id", "abc")
	logger.Info("grouped")

	output := buf.String()
	if !strings.Contains(output, "request.id=abc") {
		t.Errorf("output should contain request.id=abc, got: %s", output)
	}
}

func TestMaskHandler_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	h := newMaskHandler("M", slog.LevelWarn, &buf)
	logger := slog.New(h)
	logger.Debug("should not appear")
	logger.Info("should not appear either")
	logger.Warn("should appear")

	output := buf.String()
	if strings.Contains(output, "should not appear") {
		t.Error("debug messages should be filtered")
	}
	if !strings.Contains(output, "should appear") {
		t.Error("warn message should be present")
	}
}

// --- "off" disabling tests ---

func TestNewLogger_ConsoleOff(t *testing.T) {
	// ConsoleFormat "off" disables console output; file output still works.
	dir := t.TempDir()
	logFile := filepath.Join(dir, "console-off.log")
	cfg := LogConfig{
		ConsoleLevel:  "debug",
		ConsoleFormat: "off",
		LogFilePath:   logFile,
	}
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger with console off failed: %v", err)
	}
	defer logger.Close()
	logger.Info("console off, file on")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "console off, file on") {
		t.Errorf("file should contain message despite console off, got: %s", string(data))
	}
}

func TestNewLogger_FileOff(t *testing.T) {
	// LogFileFormat "off" disables file output even with a non-empty path.
	dir := t.TempDir()
	logFile := filepath.Join(dir, "file-off.log")
	cfg := LogConfig{
		ConsoleLevel:  "debug",
		ConsoleFormat: "off", // avoid console noise
		LogFileFormat: "off",
		LogFilePath:   logFile,
	}
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger with file off failed: %v", err)
	}
	defer logger.Close()
	logger.Info("should not hit the file")

	if _, err := os.Stat(logFile); !os.IsNotExist(err) {
		t.Errorf("log file should not exist when file format is off, got stat err: %v", err)
	}
}

func TestNewLogger_AllOffSilent(t *testing.T) {
	// All targets disabled: logger is silent, no error.
	cfg := LogConfig{
		ConsoleLevel:  "debug",
		ConsoleFormat: "off",
	}
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger with all targets off should succeed: %v", err)
	}
	defer logger.Close()
	logger.Info("silently discarded")
	logger.Debug("also silently discarded")
	logger.Error("errors are discarded too")
}

// captureStdout redirects os.Stdout to a pipe, runs fn, and returns
// everything written to stdout during fn.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	_ = r.Close()
	return string(data)
}

// --- Merged format field regression tests ---

func TestNewLogger_DefaultConsoleMask(t *testing.T) {
	// Empty ConsoleFormat defaults to mask "LCM": level + caller + message.
	out := captureStdout(t, func() {
		logger, err := NewLogger(LogConfig{ConsoleLevel: "info"})
		if err != nil {
			t.Fatalf("NewLogger failed: %v", err)
		}
		defer logger.Close()
		logger.Info("default mask output")
	})

	if !strings.Contains(out, "INFO") {
		t.Errorf("mask output should contain level INFO, got: %q", out)
	}
	if !strings.Contains(out, "default mask output") {
		t.Errorf("mask output should contain message, got: %q", out)
	}
	// Mask format must NOT look like text or json.
	if strings.Contains(out, "level=INFO") {
		t.Errorf("expected mask format, got text format: %q", out)
	}
	if strings.Contains(out, `"level":"INFO"`) {
		t.Errorf("expected mask format, got json format: %q", out)
	}
}

func TestNewLogger_DefaultFileFormatJSON(t *testing.T) {
	// Empty LogFileFormat defaults to json in the file.
	dir := t.TempDir()
	logFile := filepath.Join(dir, "default.log")
	cfg := LogConfig{
		ConsoleLevel:  "info",
		ConsoleFormat: "off", // avoid console noise
		LogFilePath:   logFile,
	}
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	logger.Info("default file format")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("file should be valid JSON with empty LogFileFormat, got %q: %v", string(data), err)
	}
	if record["msg"] != "default file format" {
		t.Errorf("expected msg='default file format', got %v", record["msg"])
	}
}

func TestNewLogger_FormatCaseInsensitive(t *testing.T) {
	// Uppercase format values behave identically to lowercase ones.

	outJSON := captureStdout(t, func() {
		logger, err := NewLogger(LogConfig{ConsoleLevel: "info", ConsoleFormat: "JSON"})
		if err != nil {
			t.Fatalf("NewLogger with JSON failed: %v", err)
		}
		defer logger.Close()
		logger.Info("uppercase json")
	})
	if !strings.Contains(outJSON, `"msg":"uppercase json"`) {
		t.Errorf("ConsoleFormat \"JSON\" should produce json output, got: %q", outJSON)
	}

	outText := captureStdout(t, func() {
		logger, err := NewLogger(LogConfig{ConsoleLevel: "info", ConsoleFormat: "TEXT"})
		if err != nil {
			t.Fatalf("NewLogger with TEXT failed: %v", err)
		}
		defer logger.Close()
		logger.Info("uppercase text")
	})
	if !strings.Contains(outText, "level=INFO") {
		t.Errorf("ConsoleFormat \"TEXT\" should produce text output, got: %q", outText)
	}

	outOff := captureStdout(t, func() {
		logger, err := NewLogger(LogConfig{ConsoleLevel: "info", ConsoleFormat: "OFF"})
		if err != nil {
			t.Fatalf("NewLogger with OFF failed: %v", err)
		}
		defer logger.Close()
		logger.Info("uppercase off")
	})
	if outOff != "" {
		t.Errorf("ConsoleFormat \"OFF\" should disable console, got output: %q", outOff)
	}
}

func TestNewLogger_ConsoleMaskFormat(t *testing.T) {
	// ConsoleFormat set to a mask string produces mask output on stdout.
	out := captureStdout(t, func() {
		logger, err := NewLogger(LogConfig{ConsoleLevel: "debug", ConsoleFormat: "TLCM"})
		if err != nil {
			t.Fatalf("NewLogger with mask format failed: %v", err)
		}
		defer logger.Close()
		logger.Info("mask console test", "key", "val")
	})

	if !strings.Contains(out, "INFO") {
		t.Errorf("mask output should contain level, got: %q", out)
	}
	if !strings.Contains(out, "mask console test") {
		t.Errorf("mask output should contain message, got: %q", out)
	}
	if !strings.Contains(out, "key=val") {
		t.Errorf("mask output should contain attributes, got: %q", out)
	}
	// Mask format must NOT look like text or json.
	if strings.Contains(out, "level=INFO") {
		t.Errorf("expected mask format, got text format: %q", out)
	}
	if strings.Contains(out, `"level":"INFO"`) {
		t.Errorf("expected mask format, got json format: %q", out)
	}
}
