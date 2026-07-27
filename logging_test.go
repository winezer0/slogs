package slogs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- Config tests ---

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Level != "info" {
		t.Errorf("expected level=info, got %q", cfg.Level)
	}
	if cfg.Format != "text" {
		t.Errorf("expected format=text, got %q", cfg.Format)
	}
	if cfg.FilePath != "" {
		t.Errorf("expected empty file_path, got %q", cfg.FilePath)
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

func TestNewConfig_Defaults(t *testing.T) {
	tests := []struct {
		name       string
		level      string
		filePath   string
		format     string
		wantLevel  string
		wantFormat string
	}{
		{"all empty", "", "", "", "info", "text"},
		{"custom level", "debug", "/tmp/app.log", "json", "debug", "json"},
		{"warn level", "warn", "", "text", "warn", "text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig(tt.level, tt.filePath, tt.format)
			if cfg.Level != tt.wantLevel {
				t.Errorf("level: want %q, got %q", tt.wantLevel, cfg.Level)
			}
			if cfg.Format != tt.wantFormat {
				t.Errorf("format: want %q, got %q", tt.wantFormat, cfg.Format)
			}
			if cfg.FilePath != tt.filePath {
				t.Errorf("file_path: want %q, got %q", tt.filePath, cfg.FilePath)
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

// --- Logger tests ---

func TestNewLogger_ConsoleOnly(t *testing.T) {
	logger, err := NewLogger(DefaultConfig())
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	defer logger.Close()

	if logger.Slog() == nil {
		t.Fatal("Slog() should not be nil")
	}
	// Should not panic on log calls.
	logger.Debug("debug message", "key", "value")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")
	logger.Debugf("debug %s", "formatted")
	logger.Infof("info %d", 42)
	logger.Warnf("warn %v", true)
	logger.Errorf("error %v", nil)
}

func TestNewLogger_WithFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	cfg := NewConfig("debug", logFile, "json")

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
	cfg := NewConfig("info", logFile, "text")

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
	oldLogger := defaultLogger
	oldOnce := defaultOnce
	defer func() {
		defaultLogger = oldLogger
		defaultOnce = oldOnce
	}()
	defaultLogger = nil
	defaultOnce = sync.Once{}

	logger := Default()
	if logger == nil {
		t.Fatal("Default() should never return nil")
	}
	// Should not panic.
	logger.Info("default logger works")
}

func TestSetDefault(t *testing.T) {
	oldLogger := defaultLogger
	defer func() { defaultLogger = oldLogger }()

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

// --- Package-level convenience functions ---

func TestPackageLevelFunctions(t *testing.T) {
	// Reset for isolation.
	oldLogger := defaultLogger
	oldOnce := defaultOnce
	defer func() {
		defaultLogger = oldLogger
		defaultOnce = oldOnce
	}()
	defaultLogger = nil
	defaultOnce = sync.Once{}

	// These should not panic (lazy init).
	Debug("pkg debug")
	Info("pkg info")
	Warn("pkg warn")
	Error("pkg error")
	Debugf("pkg %s", "debugf")
	Infof("pkg %s", "infof")
	Warnf("pkg %s", "warnf")
	Errorf("pkg %s", "errorf")
}
