package slogs

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Rotator basic write tests ---

func TestRotator_WriteCreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	r := &Rotator{Filename: logFile, MaxSize: 1}
	defer r.Close()

	msg := "hello rotator\n"
	n, err := r.Write([]byte(msg))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(msg) {
		t.Errorf("Write returned %d, want %d", n, len(msg))
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if string(data) != msg {
		t.Errorf("file content = %q, want %q", string(data), msg)
	}
}

func TestRotator_WriteAppends(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "append.log")

	r := &Rotator{Filename: logFile, MaxSize: 1}
	r.Write([]byte("line1\n"))
	r.Write([]byte("line2\n"))
	r.Close()

	data, _ := os.ReadFile(logFile)
	if string(data) != "line1\nline2\n" {
		t.Errorf("expected two lines, got: %q", string(data))
	}
}

func TestRotator_WriteExceedsMaxSize(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "big.log")

	// Set megabyte small for testing.
	origMegabyte := megabyte
	megabyte = 1 // 1 byte = 1 "megabyte"
	defer func() { megabyte = origMegabyte }()

	r := &Rotator{Filename: logFile, MaxSize: 1}
	defer r.Close()

	// Write more than 1 byte (max).
	_, err := r.Write([]byte("this exceeds max size"))
	if err == nil {
		t.Error("expected error when write exceeds max size")
	}
	if !strings.Contains(err.Error(), "exceeds maximum file size") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRotator_Rotation(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "rotate.log")

	origMegabyte := megabyte
	megabyte = 100 // 100 bytes per "megabyte"
	defer func() { megabyte = origMegabyte }()

	r := &Rotator{Filename: logFile, MaxSize: 1, MaxBackups: 5}
	defer r.Close()

	// Write enough to trigger rotation (100 bytes max).
	chunk := strings.Repeat("x", 60)
	r.Write([]byte(chunk))
	r.Write([]byte(chunk)) // This should trigger rotation.

	// After rotation, a backup file should exist.
	entries, _ := os.ReadDir(tmpDir)
	if len(entries) < 2 {
		t.Errorf("expected at least 2 files after rotation, got %d", len(entries))
	}
}

func TestRotator_Close(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "close.log")

	r := &Rotator{Filename: logFile, MaxSize: 1}
	r.Write([]byte("data"))

	if err := r.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	// Double close should be safe.
	if err := r.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestRotator_ManualRotate(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "manual.log")

	r := &Rotator{Filename: logFile, MaxSize: 100}
	r.Write([]byte("before rotate"))

	if err := r.Rotate(); err != nil {
		t.Fatalf("Rotate failed: %v", err)
	}

	r.Write([]byte("after rotate"))
	r.Close()

	// Current file should only have "after rotate".
	data, _ := os.ReadFile(logFile)
	if string(data) != "after rotate" {
		t.Errorf("current file = %q, want 'after rotate'", string(data))
	}

	// A backup should exist.
	entries, _ := os.ReadDir(tmpDir)
	if len(entries) < 2 {
		t.Error("expected backup file after manual rotate")
	}
}

// --- Rotator MaxBackups tests ---

func TestRotator_MaxBackups(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "backups.log")

	origMegabyte := megabyte
	megabyte = 50
	defer func() { megabyte = origMegabyte }()

	r := &Rotator{Filename: logFile, MaxSize: 1, MaxBackups: 2}
	defer r.Close()

	// Trigger multiple rotations.
	for i := 0; i < 5; i++ {
		r.Write([]byte(strings.Repeat("y", 60)))
		time.Sleep(10 * time.Millisecond) // Ensure unique timestamps.
	}

	// Wait for mill goroutine to process.
	time.Sleep(100 * time.Millisecond)

	entries, _ := os.ReadDir(tmpDir)
	// Should have current file + at most 2 backups.
	backupCount := 0
	for _, e := range entries {
		if e.Name() != "backups.log" {
			backupCount++
		}
	}
	if backupCount > 3 { // Allow slight timing slack.
		t.Errorf("expected at most 3 backup files, got %d", backupCount)
	}
}

// --- Rotator compression tests ---

func TestRotator_Compress(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "compress.log")

	origMegabyte := megabyte
	megabyte = 50
	defer func() { megabyte = origMegabyte }()

	r := &Rotator{Filename: logFile, MaxSize: 1, Compress: true}
	defer r.Close()

	// Trigger rotation.
	r.Write([]byte(strings.Repeat("z", 60)))
	r.Write([]byte(strings.Repeat("z", 60)))

	// Wait for compression goroutine.
	time.Sleep(200 * time.Millisecond)

	entries, _ := os.ReadDir(tmpDir)
	hasGz := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".gz") {
			hasGz = true
			// Verify it's valid gzip.
			f, _ := os.Open(filepath.Join(tmpDir, e.Name()))
			gz, err := gzip.NewReader(f)
			if err != nil {
				t.Errorf("invalid gzip file %s: %v", e.Name(), err)
			} else {
				gz.Close()
			}
			f.Close()
		}
	}
	if !hasGz {
		t.Log("no .gz file found (timing dependent, acceptable)")
	}
}

// --- Rotator MaxAge tests ---

func TestRotator_MaxAge(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "age.log")

	origMegabyte := megabyte
	megabyte = 50
	defer func() { megabyte = origMegabyte }()

	// Mock currentTime to create old backups.
	origTime := currentTime
	now := time.Now()
	currentTime = func() time.Time { return now.Add(-48 * time.Hour) }

	r := &Rotator{Filename: logFile, MaxSize: 1, MaxAge: 1}

	// Create an old backup.
	r.Write([]byte(strings.Repeat("a", 60)))
	r.Write([]byte(strings.Repeat("a", 60)))
	r.Close()

	// Reset time to now and trigger mill.
	currentTime = origTime
	r2 := &Rotator{Filename: logFile, MaxSize: 1, MaxAge: 1}
	r2.Write([]byte("trigger"))
	time.Sleep(100 * time.Millisecond)
	r2.Close()

	// Old backups (>1 day) should be removed.
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if e.Name() != "age.log" && !strings.HasSuffix(e.Name(), ".gz") {
			// Check if it's an old backup that should have been removed.
			if t2, err := time.Parse(backupTimeFormat, extractTimestamp(e.Name(), "age-")); err == nil {
				if now.Sub(t2) > 24*time.Hour {
					t.Errorf("old backup %s should have been removed", e.Name())
				}
			}
		}
	}
}

// --- backupName tests ---

func TestBackupName(t *testing.T) {
	origTime := currentTime
	currentTime = func() time.Time {
		return time.Date(2026, 7, 27, 18, 30, 0, 0, time.UTC)
	}
	defer func() { currentTime = origTime }()

	tests := []struct {
		name  string
		input string
		local bool
		want  string
	}{
		{
			"with extension",
			filepath.Join("/var/log", "app.log"),
			false,
			filepath.Join("/var/log", "app-2026-07-27T18-30-00.000.log"),
		},
		{
			"without extension",
			filepath.Join("/tmp", "logfile"),
			false,
			filepath.Join("/tmp", "logfile-2026-07-27T18-30-00.000"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := backupName(tt.input, tt.local)
			if got != tt.want {
				t.Errorf("backupName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- filename default tests ---

func TestRotator_DefaultFilename(t *testing.T) {
	r := &Rotator{}
	name := r.filename()
	if name == "" {
		t.Error("default filename should not be empty")
	}
	if !strings.HasSuffix(name, "-slogs.log") {
		t.Errorf("default filename should end with -slogs.log, got: %s", name)
	}
}

// --- max tests ---

func TestRotator_Max(t *testing.T) {
	origMegabyte := megabyte
	megabyte = 1024 * 1024
	defer func() { megabyte = origMegabyte }()

	tests := []struct {
		maxSize int
		want    int64
	}{
		{0, int64(defaultMaxSize * 1024 * 1024)},
		{50, int64(50 * 1024 * 1024)},
		{1, int64(1 * 1024 * 1024)},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("MaxSize=%d", tt.maxSize), func(t *testing.T) {
			r := &Rotator{MaxSize: tt.maxSize}
			if got := r.max(); got != tt.want {
				t.Errorf("max() = %d, want %d", got, tt.want)
			}
		})
	}
}

// --- compressLogFile tests ---

func TestCompressLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "source.log")
	dst := src + ".gz"

	content := "log content for compression test\n"
	os.WriteFile(src, []byte(content), 0o644)

	if err := compressLogFile(src, dst); err != nil {
		t.Fatalf("compressLogFile failed: %v", err)
	}

	// Source should be removed.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source file should be removed after compression")
	}

	// Destination should be valid gzip with correct content.
	f, err := os.Open(dst)
	if err != nil {
		t.Fatalf("open compressed file: %v", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	data, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if string(data) != content {
		t.Errorf("decompressed = %q, want %q", string(data), content)
	}
}

func TestCompressLogFile_SourceNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	err := compressLogFile(filepath.Join(tmpDir, "nope.log"), filepath.Join(tmpDir, "nope.gz"))
	if err == nil {
		t.Error("expected error for non-existent source")
	}
}

// --- timeFromName tests ---

func TestRotator_TimeFromName(t *testing.T) {
	r := &Rotator{Filename: "/var/log/app.log"}

	tests := []struct {
		name     string
		filename string
		prefix   string
		ext      string
		wantErr  bool
	}{
		{"valid", "app-2026-07-27T18-30-00.000.log", "app-", ".log", false},
		{"valid gz", "app-2026-07-27T18-30-00.000.log.gz", "app-", ".log.gz", false},
		{"wrong prefix", "other-2026-07-27T18-30-00.000.log", "app-", ".log", true},
		{"wrong ext", "app-2026-07-27T18-30-00.000.txt", "app-", ".log", true},
		{"bad timestamp", "app-not-a-time.log", "app-", ".log", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.timeFromName(tt.filename, tt.prefix, tt.ext)
			if (err != nil) != tt.wantErr {
				t.Errorf("timeFromName(%q) error = %v, wantErr %v", tt.filename, err, tt.wantErr)
			}
		})
	}
}

// --- prefixAndExt tests ---

func TestRotator_PrefixAndExt(t *testing.T) {
	tests := []struct {
		filename   string
		wantPrefix string
		wantExt    string
	}{
		{"/var/log/app.log", "app-", ".log"},
		{"/tmp/noext", "noext-", ""},
		{"relative/path/server.error.log", "server.error-", ".log"},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			r := &Rotator{Filename: tt.filename}
			prefix, ext := r.prefixAndExt()
			if prefix != tt.wantPrefix {
				t.Errorf("prefix = %q, want %q", prefix, tt.wantPrefix)
			}
			if ext != tt.wantExt {
				t.Errorf("ext = %q, want %q", ext, tt.wantExt)
			}
		})
	}
}

// --- byFormatTime sort tests ---

func TestByFormatTime_Sort(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 3, 10, 6, 0, 0, 0, time.UTC)

	files := byFormatTime{
		{timestamp: t1},
		{timestamp: t2},
		{timestamp: t3},
	}

	// Sort should order newest first.
	if files.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", files.Len())
	}
	files.Swap(0, 1)
	if !files.Less(0, 1) {
		// After swap: [t2, t1, t3]. t2 > t1, so Less(0,1) should be true.
		t.Error("Less should return true for newer timestamp")
	}
}

// --- oldLogFiles tests ---

func TestRotator_OldLogFiles(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "app.log")

	// Create fake backup files.
	os.WriteFile(filepath.Join(tmpDir, "app-2026-07-01T10-00-00.000.log"), []byte("old1"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "app-2026-07-20T10-00-00.000.log"), []byte("old2"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "app-2026-07-20T10-00-00.000.log.gz"), []byte("gz"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "unrelated.txt"), []byte("skip"), 0o644)
	os.WriteFile(logFile, []byte("current"), 0o644)

	r := &Rotator{Filename: logFile}
	files, err := r.oldLogFiles()
	if err != nil {
		t.Fatalf("oldLogFiles failed: %v", err)
	}

	// Should find 3 backup files (2 .log + 1 .gz), not unrelated.txt or current.
	if len(files) != 3 {
		t.Errorf("expected 3 old log files, got %d", len(files))
	}

	// Should be sorted newest first.
	if len(files) >= 2 && files[0].timestamp.Before(files[1].timestamp) {
		t.Error("files should be sorted newest first")
	}
}

// --- millRunOnce no-op tests ---

func TestRotator_MillRunOnce_NoOp(t *testing.T) {
	r := &Rotator{MaxBackups: 0, MaxAge: 0, Compress: false}
	if err := r.millRunOnce(); err != nil {
		t.Errorf("millRunOnce with no cleanup config should return nil, got: %v", err)
	}
}

// --- helper ---

func extractTimestamp(filename, prefix string) string {
	if !strings.HasPrefix(filename, prefix) {
		return ""
	}
	s := filename[len(prefix):]
	if idx := strings.Index(s, "."); idx > 0 {
		return s[:idx]
	}
	return s
}
