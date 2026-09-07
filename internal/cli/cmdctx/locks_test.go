package cmdctx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// acquireRawLock opens the file at path, takes an exclusive flock, and returns
// a cleanup function. Used to hold a project lock file so AcquireProjectLocks
// sees it as held by the current process (non-zero PID).
func acquireRawLock(t *testing.T, path string) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("flock: %v", err)
	}
	// Write a fake PID so the stale-PID check does not release the lock.
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}

func TestAcquireProjectLocksOrReport_Success(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	release, err := AcquireProjectLocksOrReport(dir, w)
	if err != nil {
		t.Fatalf("expected success, got err: %v", err)
	}
	if release == nil {
		t.Fatal("expected non-nil release func")
	}
	release()
	if buf.Len() != 0 {
		t.Errorf("expected no output on success, got %q", buf.String())
	}
}

func TestAcquireProjectLocksOrReport_HeldError(t *testing.T) {
	dir := t.TempDir()

	// Hold the deploy lock before calling AcquireProjectLocksOrReport.
	deployLockPath := lock.DeployLockPath(dir)
	cleanup := acquireRawLock(t, deployLockPath)
	defer cleanup()

	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	release, err := AcquireProjectLocksOrReport(dir, w)
	if err == nil {
		release()
		t.Fatal("expected error, got nil")
	}

	// Error must be *lock.ProjectLockHeldError (exit code 2 preserved).
	var phe *lock.ProjectLockHeldError
	if !errors.As(err, &phe) {
		t.Fatalf("err = %T(%v), want *lock.ProjectLockHeldError", err, err)
	}
	if phe.ExitCode() != 2 {
		t.Errorf("ExitCode = %d, want 2", phe.ExitCode())
	}

	// The error message must be printed to the writer.
	got := buf.String()
	if !strings.Contains(got, phe.Error()) {
		t.Errorf("writer output %q does not contain error message %q", got, phe.Error())
	}
}

func TestAcquireProjectLocksSilent_Success(t *testing.T) {
	dir := t.TempDir()

	release, err := AcquireProjectLocksSilent(dir)
	if err != nil {
		t.Fatalf("expected success, got err: %v", err)
	}
	if release == nil {
		t.Fatal("expected non-nil release func")
	}
	release()
}

func TestAcquireProjectLocksSilent_HeldError(t *testing.T) {
	dir := t.TempDir()

	// Hold the deploy lock before calling AcquireProjectLocksSilent.
	deployLockPath := lock.DeployLockPath(dir)
	cleanup := acquireRawLock(t, deployLockPath)
	defer cleanup()

	release, err := AcquireProjectLocksSilent(dir)
	if err == nil {
		release()
		t.Fatal("expected error, got nil")
	}

	// Error must be *lock.ProjectLockHeldError (exit code 2 preserved), returned
	// unchanged — the silent variant prints nothing but keeps the typed error.
	var phe *lock.ProjectLockHeldError
	if !errors.As(err, &phe) {
		t.Fatalf("err = %T(%v), want *lock.ProjectLockHeldError", err, err)
	}
	if phe.ExitCode() != 2 {
		t.Errorf("ExitCode = %d, want 2", phe.ExitCode())
	}
}

func TestAcquireProjectLocksSilent_GenericErrorWrapped(t *testing.T) {
	// Null byte in the path forces a non-ProjectLockHeldError OS error.
	dir := "/\x00invalid"

	_, err := AcquireProjectLocksSilent(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := errors.AsType[*lock.ProjectLockHeldError](err); ok {
		t.Fatalf("expected generic-wrapped error, got *lock.ProjectLockHeldError: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "acquiring project locks: ") {
		t.Errorf("err = %q, want prefix %q", err.Error(), "acquiring project locks: ")
	}
}

func TestAcquireProjectLocksOrReport_GenericErrorWrapped(t *testing.T) {
	// Pass a path that cannot exist on any OS (null byte in name) to force an
	// OS-level error that is NOT a *lock.ProjectLockHeldError.
	dir := "/\x00invalid"

	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	_, err := AcquireProjectLocksOrReport(dir, w)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Must NOT be a ProjectLockHeldError.
	if _, ok := errors.AsType[*lock.ProjectLockHeldError](err); ok {
		t.Fatalf("expected generic-wrapped error, got *lock.ProjectLockHeldError: %v", err)
	}

	// Must be wrapped with the standard prefix.
	if !strings.HasPrefix(err.Error(), "acquiring project locks: ") {
		t.Errorf("err = %q, want prefix %q", err.Error(), "acquiring project locks: ")
	}

	// Nothing must be printed to the writer (only ProjectLockHeldError prints).
	if buf.Len() != 0 {
		t.Errorf("expected no output for generic error, got %q", buf.String())
	}
}
