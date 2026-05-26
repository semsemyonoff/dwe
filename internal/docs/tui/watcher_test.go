package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestWatcherCreation(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	if watcher == nil {
		t.Error("Watcher should not be nil")
	}
	if err := watcher.Close(); err != nil {
		t.Errorf("Close() failed: %v", err)
	}
}

func TestWatcherClose(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}

	if err := watcher.Close(); err != nil {
		t.Fatalf("watcher.Close() failed: %v", err)
	}

	// Closing again should not panic; error is acceptable (already closed).
	_ = watcher.Close()
}

func TestWatcherContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}

	cancel()
	time.Sleep(100 * time.Millisecond)

	_ = watcher.Close()
}

func TestWatcherFileChange(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(testFile, []byte("modified"), 0o644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
}

func TestWatcherRecursive(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	nestedDir := filepath.Join(tmpDir, "sub", "deep")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("Failed to create nested directories: %v", err)
	}

	testFile := filepath.Join(nestedDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("Failed to create test file in nested dir: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(testFile, []byte("modified"), 0o644); err != nil {
		t.Fatalf("Failed to modify nested file: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
}
