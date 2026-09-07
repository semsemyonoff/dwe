package lock

import (
	"errors"
	"os"
	"testing"
)

func TestAcquireProjectLocks_Clean(t *testing.T) {
	dir := t.TempDir()

	release, err := AcquireProjectLocks(dir)
	if err != nil {
		t.Fatalf("AcquireProjectLocks: %v", err)
	}
	if release == nil {
		t.Fatal("release func is nil")
	}

	// Both lock files were created with our PID.
	for _, p := range []string{DeployLockPath(dir), SnapshotLockPath(dir)} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if len(data) == 0 {
			t.Errorf("lock file %s is empty after acquire", p)
		}
	}

	release()

	// After release, can re-acquire.
	release2, err := AcquireProjectLocks(dir)
	if err != nil {
		t.Fatalf("re-acquire failed: %v", err)
	}
	release2()
}

func TestAcquireProjectLocks_DeployHeld(t *testing.T) {
	dir := t.TempDir()

	// Pre-acquire deploy lock from this process so the helper sees it held.
	deployLk, err := Acquire(DeployLockPath(dir))
	if err != nil {
		t.Fatalf("seed deploy lock: %v", err)
	}
	defer func() { _ = deployLk.Release() }()

	_, err = AcquireProjectLocks(dir)
	var phe *ProjectLockHeldError
	if !errors.As(err, &phe) {
		t.Fatalf("err = %T %v, want *ProjectLockHeldError", err, err)
	}
	if phe.Operation != "deploy" {
		t.Errorf("Operation = %q, want %q", phe.Operation, "deploy")
	}
	if phe.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", phe.PID, os.Getpid())
	}

	// Snapshot lock must NOT have been created (deploy failed first).
	if _, statErr := os.Stat(SnapshotLockPath(dir)); !os.IsNotExist(statErr) {
		t.Errorf("snapshot lock file should not exist after deploy-held abort; stat err = %v", statErr)
	}
}

func TestAcquireProjectLocks_SnapshotHeld(t *testing.T) {
	dir := t.TempDir()

	// Pre-acquire the snapshot lock; deploy lock should be free.
	snapLk, err := Acquire(SnapshotLockPath(dir))
	if err != nil {
		t.Fatalf("seed snapshot lock: %v", err)
	}
	defer func() { _ = snapLk.Release() }()

	_, err = AcquireProjectLocks(dir)
	var phe *ProjectLockHeldError
	if !errors.As(err, &phe) {
		t.Fatalf("err = %T %v, want *ProjectLockHeldError", err, err)
	}
	if phe.Operation != "snapshot" {
		t.Errorf("Operation = %q, want %q", phe.Operation, "snapshot")
	}
	if phe.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", phe.PID, os.Getpid())
	}

	// The deploy lock that the helper acquired transiently must have been
	// released so a follow-on caller can grab it. Acquire it directly to
	// prove the file is unlocked.
	dlk, err := Acquire(DeployLockPath(dir))
	if err != nil {
		t.Fatalf("deploy lock should be released after partial-acquire rollback: %v", err)
	}
	_ = dlk.Release()
}

func TestAcquireProjectLocks_ExitCode(t *testing.T) {
	e := &ProjectLockHeldError{Operation: "deploy", PID: 1234}
	if got := e.ExitCode(); got != 2 {
		t.Errorf("ExitCode = %d, want 2", got)
	}
	if !errors.As(error(e), new(*ProjectLockHeldError)) {
		t.Error("errors.As should match *ProjectLockHeldError")
	}
}

func TestAcquireProjectLocks_SequentialQueue(t *testing.T) {
	dir := t.TempDir()

	rel1, err := AcquireProjectLocks(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	rel1()

	rel2, err := AcquireProjectLocks(dir)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	rel2()

	rel3, err := AcquireProjectLocks(dir)
	if err != nil {
		t.Fatalf("third acquire: %v", err)
	}
	rel3()
}
