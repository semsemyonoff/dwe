package docstui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/colorprofile"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	// Pin the lipgloss/v2 colour profile to NoTTY so View() output is
	// byte-identical across local + CI regardless of TERM / COLORTERM env.
	// Without this, snapshot assertions and ANSI-escape checks would be
	// non-deterministic.
	compat.Profile = colorprofile.NoTTY
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

// waitForEventMatching drains the watcher event channel until it sees an event
// whose path matches want, or the timeout elapses. Returns true on a match.
func waitForEventMatching(t *testing.T, w *Watcher, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	want = filepath.Clean(want)
	for {
		select {
		case msg, ok := <-w.Events():
			if !ok {
				t.Fatal("events channel closed before the expected event arrived")
			}
			if filepath.Clean(msg.Path) == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// TestWatcherSurvivesContextCancellation pins the documented contract that the
// event pump does NOT exit on ctx cancellation (only Close() stops it). After
// cancelling we assert the events channel is still open (a closed channel would
// signal the pump exited), then Close() tears it down cleanly.
func TestWatcherSurvivesContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}

	cancel()
	time.Sleep(100 * time.Millisecond)

	select {
	case _, ok := <-watcher.Events():
		if !ok {
			t.Fatal("events channel closed after ctx cancellation; pump must stay alive until Close()")
		}
		// An incidental event is fine — the channel being open is what matters.
	default:
		// No event, channel open — the expected steady state.
	}

	if err := watcher.Close(); err != nil {
		t.Errorf("Close() after ctx cancellation failed: %v", err)
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
	defer func() { _ = watcher.Close() }()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := os.WriteFile(testFile, []byte("modified"), 0o644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	if !waitForEventMatching(t, watcher, testFile, 3*time.Second) {
		t.Fatalf("did not receive a FileChangedMsg for %s within timeout", testFile)
	}
}

func TestWatcherRecursive(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the nested tree BEFORE constructing the watcher: walkAndWatch
	// registers existing subdirectories recursively at NewWatcher time. This is
	// what "recursive" means here — the pump does not re-walk dirs created later.
	nestedDir := filepath.Join(tmpDir, "sub", "deep")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("Failed to create nested directories: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	testFile := filepath.Join(nestedDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("Failed to create test file in nested dir: %v", err)
	}
	if err := os.WriteFile(testFile, []byte("modified"), 0o644); err != nil {
		t.Fatalf("Failed to modify nested file: %v", err)
	}

	// A delivered event for the deeply-nested file proves walkAndWatch
	// registered the subdirectories recursively.
	if !waitForEventMatching(t, watcher, testFile, 3*time.Second) {
		t.Fatalf("did not receive a FileChangedMsg for nested file %s; recursive watch not registered", testFile)
	}
}
