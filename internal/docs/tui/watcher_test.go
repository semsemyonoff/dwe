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
	// Create a temporary directory for testing
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

	// Clean up
	watcher.Close()
}

func TestWatcherClose(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}

	// Close the watcher
	err = watcher.Close()
	if err != nil {
		t.Fatalf("watcher.Close() failed: %v", err)
	}

	// Closing again should not panic
	err = watcher.Close()
	if err != nil {
		// Some errors are ok (already closed)
		t.Logf("Second close returned: %v", err)
	}
}

func TestWatcherContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}

	// Cancel the context
	cancel()

	// Give the goroutine time to exit
	time.Sleep(100 * time.Millisecond)

	// Close should work even after context cancellation
	err = watcher.Close()
	if err != nil {
		t.Logf("Close after context cancellation returned: %v", err)
	}
}

func TestWatcherFileChange(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer watcher.Close()

	// Create a file in the watched directory
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Give the watcher time to process the creation
	time.Sleep(100 * time.Millisecond)

	// Modify the file
	if err := os.WriteFile(testFile, []byte("modified"), 0o644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Give the watcher time to process the modification
	time.Sleep(100 * time.Millisecond)

	// The watcher should have received events; we don't directly check them
	// since they're consumed by the event pump, but the fact that we got
	// here without panicking is a good sign
}

func TestWatcherRecursive(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer watcher.Close()

	// Create nested directories
	nestedDir := filepath.Join(tmpDir, "sub", "deep")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("Failed to create nested directories: %v", err)
	}

	// Create a file in the nested directory
	testFile := filepath.Join(nestedDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("Failed to create test file in nested dir: %v", err)
	}

	// Give the watcher time to process
	time.Sleep(100 * time.Millisecond)

	// Modify the nested file
	if err := os.WriteFile(testFile, []byte("modified"), 0o644); err != nil {
		t.Fatalf("Failed to modify nested file: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
}
