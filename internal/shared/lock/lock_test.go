package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestAcquireAndRelease(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")

	lock1, err := Acquire(lockPath)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer func() { _ = lock1.Release() }()

	// Verify the lock file exists and contains our PID
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}

	pidStr := fmt.Sprintf("%d", os.Getpid())
	if string(data) != pidStr {
		t.Errorf("lock file contains %q, expected %q", string(data), pidStr)
	}

	if err := lock1.Release(); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// The lock file is intentionally left on disk after release so the next
	// Acquire can reuse the same inode, avoiding an unlock-then-remove race.
	// Verify a new acquire can obtain the lock.
	lock2, err := Acquire(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	defer func() { _ = lock2.Release() }()
}

func TestParallelAcquireReturnsError(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")

	// Acquire the first lock
	lock1, err := Acquire(lockPath)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer func() { _ = lock1.Release() }()

	// Try to acquire the same lock from another context (simulate by using the same path)
	// Since we can't actually fork in Go tests easily, we'll create a goroutine that tries to acquire
	done := make(chan error, 1)
	go func() {
		_, err := Acquire(lockPath)
		done <- err
	}()

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("acquire blocked indefinitely (expected immediate error)")
	case err := <-done:
		var heldErr *HeldError
		if !errors.As(err, &heldErr) {
			t.Fatalf("expected HeldError, got %T: %v", err, err)
		}
		if heldErr.PID != os.Getpid() {
			t.Errorf("lock held by PID %d, expected %d", heldErr.PID, os.Getpid())
		}
	}
}

func TestStaleLockCleanup(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")

	// Create a lock file with a stale (non-existent) PID
	stalePID := 999999 // Unlikely to exist
	pidStr := strconv.Itoa(stalePID)
	if err := os.WriteFile(lockPath, []byte(pidStr), 0o644); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	// Acquire should succeed by cleaning up the stale lock
	lock, err := Acquire(lockPath)
	if err != nil {
		t.Fatalf("acquire after stale lock failed: %v", err)
	}
	defer func() { _ = lock.Release() }()

	// Verify the lock file now contains our PID
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}

	currentPID := fmt.Sprintf("%d", os.Getpid())
	if string(data) != currentPID {
		t.Errorf("lock file contains %q, expected %q", string(data), currentPID)
	}
}

func TestReleaseAllowsNextAcquire(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")

	// First acquire and release
	lock1, err := Acquire(lockPath)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	if err := lock1.Release(); err != nil {
		t.Fatalf("first release failed: %v", err)
	}

	// Second acquire should succeed immediately
	lock2, err := Acquire(lockPath)
	if err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}
	defer func() { _ = lock2.Release() }()

	_, err = os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
}

func TestCreateParentDir(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "subdir", "deep", "test.lock")

	// Acquire should create parent directories
	lock, err := Acquire(lockPath)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer func() { _ = lock.Release() }()

	// Verify the lock file exists
	_, err = os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
}

func TestReleaseTwiceIsOK(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")

	lock, err := Acquire(lockPath)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("first release failed: %v", err)
	}

	// Second release should be a no-op
	if err := lock.Release(); err != nil {
		t.Fatalf("second release failed: %v", err)
	}
}

func TestReleaseNilLock(t *testing.T) {
	var lock *Lock
	if err := lock.Release(); err != nil {
		t.Fatalf("release nil lock failed: %v", err)
	}
}

func TestLockFileHasRestrictivePermissions(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")

	lock, err := Acquire(lockPath)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer func() { _ = lock.Release() }()

	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}

	// Check that the file was created with 0o644 (user read/write, other read)
	perms := info.Mode().Perm()
	if perms != 0o644 {
		t.Errorf("lock file perms are %o, expected %o", perms, 0o644)
	}
}
