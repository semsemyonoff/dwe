//go:build !windows

package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Lock represents an acquired file lock.
type Lock struct {
	path     string
	file     *os.File
	released bool
}

var (
	// ErrLockHeld is returned when the lock file is already held by another process.
	ErrLockHeld = errors.New("lock is held by another process")
)

// HeldError wraps ErrLockHeld with the PID of the holding process.
type HeldError struct {
	PID int
	err error
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("lock held by process %d", e.PID)
}

func (e *HeldError) Unwrap() error {
	return e.err
}

// Acquire acquires an exclusive lock on the file at the given path.
// If the lock is already held by another live process, returns ErrLockHeld.
// If the lock is held by a stale process (dead PID), cleans it up and retries once.
func Acquire(path string) (*Lock, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}

	// Attempt to open/create the lock file
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	// Attempt non-blocking exclusive lock
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		// Lock acquired successfully
		return finalizeAcquire(file, path)
	}

	// Lock is held; check if the holding process is alive
	if err := checkAndCleanStaleLock(file, path); err == nil {
		// Stale lock was cleaned; retry once
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			// Retry succeeded
			return finalizeAcquire(file, path)
		}
	}

	_ = file.Close()

	// Lock is held by a live process
	heldPID, _ := readPIDFromLockFile(path)
	return nil, &HeldError{PID: heldPID, err: ErrLockHeld}
}

// finalizeAcquire writes the current PID into a freshly-locked file and returns
// the Lock. It runs after a successful Flock (initial attempt or post-stale
// retry): the file offset is rewound, the previous holder's PID truncated, and
// the current PID written and synced. On any I/O failure the file is closed and
// the error wrapped.
func finalizeAcquire(file *os.File, path string) (*Lock, error) {
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("seek lock file: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("truncate lock file: %w", err)
	}

	// Write current PID to the lock file
	pid := os.Getpid()
	pidStr := strconv.Itoa(pid)
	if _, err := file.WriteString(pidStr); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write pid to lock file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sync lock file: %w", err)
	}

	return &Lock{path: path, file: file}, nil
}

// Release releases the lock. The lock file is intentionally left on disk so
// that the next Acquire can reuse the same inode; removing the file after
// LOCK_UN creates a race where another process can acquire the old inode and
// a third process can simultaneously create and lock a new file at the path.
// Acquire always truncates and overwrites the PID on success, so the file's
// contents are always fresh after the next acquire.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}

	// If already released, this is a no-op
	if l.released {
		return nil
	}

	l.released = true

	// Unlock the file
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.file.Close()
		return fmt.Errorf("unlock file: %w", err)
	}

	return l.file.Close()
}

// checkAndCleanStaleLock checks if the lock is held by a stale process.
// If the PID in the lock file is not alive, it truncates the file and returns nil.
// Otherwise, it returns an error.
func checkAndCleanStaleLock(file *os.File, path string) error {
	pid, err := readPIDFromLockFile(path)
	if err != nil {
		// Could not read PID; assume the lock is stale
		if err := file.Truncate(0); err != nil {
			return fmt.Errorf("truncate stale lock: %w", err)
		}
		return nil
	}

	// Check if the process is alive
	if err := syscall.Kill(pid, 0); err != nil {
		// Process is not alive (ESRCH); clean up the stale lock
		if errors.Is(err, syscall.ESRCH) {
			if err := file.Truncate(0); err != nil {
				return fmt.Errorf("truncate stale lock: %w", err)
			}
			return nil
		}
		// Some other error occurred; assume the lock is live
		return fmt.Errorf("kill check failed: %w", err)
	}

	// Process is alive
	return errors.New("lock is held by a live process")
}

// readPIDFromLockFile reads the PID from the lock file.
func readPIDFromLockFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read lock file: %w", err)
	}

	if len(data) == 0 {
		return 0, errors.New("lock file is empty")
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse pid: %w", err)
	}

	return pid, nil
}
