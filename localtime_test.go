package slogs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalTimeDefaultsPreserveUTC(t *testing.T) {
	for _, cfg := range []LogConfig{{}, NewConfig("", "", ""), DefaultConfig()} {
		if cfg.LocalTime {
			t.Fatal("existing constructors must preserve UTC rotation names")
		}
	}
}

func TestLoggerLocalTimeRotation(t *testing.T) {
	// Fixed offset makes local-vs-UTC observable on machines configured for UTC.
	previous := time.Local
	time.Local = time.FixedZone("test-offset", 8*60*60)
	t.Cleanup(func() { time.Local = previous })
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "utc", true: "local"}[local], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.log")
			logger, err := NewLogger(LogConfig{ConsoleFormat: "off", LogFilePath: path, LocalTime: local})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = logger.Close() })
			rotator, ok := logger.lifecycle.closers[0].(*Rotator)
			if !ok || rotator.LocalTime != local {
				t.Fatal("LocalTime was not passed to Rotator")
			}
			logger.Info("before rotation")
			before := time.Now()
			if err := rotator.Rotate(); err != nil {
				t.Fatal(err)
			}
			after := time.Now()
			logger.Info("after rotation")
			if err := logger.Close(); err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "app-*.log"))
			if err != nil || len(files) != 1 {
				t.Fatalf("rotation files %v: %v", files, err)
			}
			zone := time.UTC
			if local {
				zone = time.Local
			}
			stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(files[0]), "app-"), ".log")
			when, err := time.ParseInLocation("2006-01-02T15-04-05.000", stamp, zone)
			if err != nil || when.Before(before.Truncate(time.Millisecond)) || when.After(after) {
				t.Fatalf("unexpected rotation timestamp %q (%v)", stamp, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil || !strings.Contains(string(data), "before rotation") {
				t.Fatalf("backup: %s, %v", data, err)
			}
			data, err = os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), "after rotation") {
				t.Fatalf("active log: %s, %v", data, err)
			}
		})
	}
}
