package slogs

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestRotatorRejectsUseAfterClose(t *testing.T) {
	rotator := &Rotator{Filename: filepath.Join(t.TempDir(), "closed.log"), MaxSize: 1}
	if _, err := rotator.Write([]byte("before close")); err != nil {
		t.Fatal(err)
	}
	if err := rotator.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := rotator.Write([]byte("after close")); !errors.Is(err, errRotatorClosed) {
		t.Fatalf("Write() error = %v", err)
	}
	if err := rotator.Rotate(); !errors.Is(err, errRotatorClosed) {
		t.Fatalf("Rotate() error = %v", err)
	}
	if err := rotator.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}
