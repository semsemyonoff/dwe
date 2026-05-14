//go:build windows

package lock

import (
	"errors"
)

// Lock represents an acquired file lock.
type Lock struct {
	path string
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
	return "lock is not supported on Windows"
}

func (e *HeldError) Unwrap() error {
	return e.err
}

// Acquire is not supported on Windows.
func Acquire(path string) (*Lock, error) {
	return nil, errors.New("file locking is not supported on Windows")
}

// Release is a no-op on Windows.
func (l *Lock) Release() error {
	return nil
}
