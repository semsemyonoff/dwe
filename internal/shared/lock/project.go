package lock

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ProjectLockHeldError signals that one of the project's mutating locks
// (deploy.lock or snapshot.lock) is held by another live process. The
// Operation field identifies which lock blocked: "deploy" or "snapshot".
type ProjectLockHeldError struct {
	Operation string
	PID       int
}

func (e *ProjectLockHeldError) Error() string {
	return fmt.Sprintf("%s operation in progress: pid %d (wait for it to finish or kill it and retry)", e.Operation, e.PID)
}

// ExitCode returns 2 so the CLI surfaces lock-held as a distinct,
// machine-readable failure mode.
func (e *ProjectLockHeldError) ExitCode() int { return 2 }

// DeployLockPath returns the canonical path to deploy.lock for the given
// project root.
func DeployLockPath(baseDir string) string {
	return filepath.Join(baseDir, ".devbox", "deploy", "deploy.lock")
}

// SnapshotLockPath returns the canonical path to snapshot.lock for the given
// project root.
func SnapshotLockPath(baseDir string) string {
	return filepath.Join(baseDir, ".devbox", "snapshots", "snapshot.lock")
}

// AcquireProjectLocks acquires the deploy and snapshot locks for the project,
// in deterministic order (deploy first, then snapshot — alphabetical). On
// success, returns a release function that unlocks in reverse order.
//
// If either lock is held by another live process, returns a
// *ProjectLockHeldError naming the blocking operation and PID. When the
// snapshot lock cannot be acquired, the already-held deploy lock is released
// before returning, so callers do not leak locks on the partial-acquire path.
//
// This is the single helper used by deploy lifecycle commands (deploy, run,
// stop, restart, reset) and snapshot mutating commands (create, restore,
// rollback, remove, pack, unpack) — never call Acquire on these two paths
// directly from command code.
func AcquireProjectLocks(baseDir string) (release func(), err error) {
	deployLock, err := Acquire(DeployLockPath(baseDir))
	if err != nil {
		if held, ok := errors.AsType[*HeldError](err); ok {
			return nil, &ProjectLockHeldError{Operation: "deploy", PID: held.PID}
		}
		return nil, fmt.Errorf("acquiring deploy lock: %w", err)
	}

	snapshotLock, err := Acquire(SnapshotLockPath(baseDir))
	if err != nil {
		_ = deployLock.Release()
		if held, ok := errors.AsType[*HeldError](err); ok {
			return nil, &ProjectLockHeldError{Operation: "snapshot", PID: held.PID}
		}
		return nil, fmt.Errorf("acquiring snapshot lock: %w", err)
	}

	return func() {
		_ = snapshotLock.Release()
		_ = deployLock.Release()
	}, nil
}
