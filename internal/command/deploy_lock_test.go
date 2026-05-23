package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/lock"

	"github.com/spf13/cobra"
)

// TestDeployRunCmd_SnapshotLockHeldBlocksDeploy proves that when another
// process holds the snapshot lock, `devbox deploy` fails with a
// lockHeldError naming the snapshot operation. The mutual exclusion is
// symmetric: snapshot mutating operations also hold deploy.lock, and deploy
// commands hold snapshot.lock.
func TestDeployRunCmd_SnapshotLockHeldBlocksDeploy(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: test\n  prefix: devbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-acquire snapshot.lock to simulate a parallel snapshot operation.
	snapLk, err := lock.Acquire(lock.SnapshotLockPath(dir))
	if err != nil {
		t.Fatalf("seed snapshot lock: %v", err)
	}
	defer func() { _ = snapLk.Release() }()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	flags := &rootFlags{configPath: cfgPath}
	err = deployRunCmd(cmd, flags, "", false, false, true, false)
	if err == nil {
		t.Fatal("expected deploy to fail when snapshot lock is held")
	}

	var lhe *lockHeldError
	if !errors.As(err, &lhe) {
		t.Fatalf("err = %T %v, want *lockHeldError", err, err)
	}
	if lhe.operation != "snapshot" {
		t.Errorf("lockHeldError.operation = %q, want %q", lhe.operation, "snapshot")
	}
	if lhe.pid != os.Getpid() {
		t.Errorf("lockHeldError.pid = %d, want %d", lhe.pid, os.Getpid())
	}
}

// TestDeployRunCmd_DeployLockHeldBlocksDeploy keeps the pre-existing
// deploy-self-conflict behavior: a held deploy.lock still blocks a new
// deploy with the deploy operation name.
func TestDeployRunCmd_DeployLockHeldBlocksDeploy(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: test\n  prefix: devbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deployLk, err := lock.Acquire(lock.DeployLockPath(dir))
	if err != nil {
		t.Fatalf("seed deploy lock: %v", err)
	}
	defer func() { _ = deployLk.Release() }()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	flags := &rootFlags{configPath: cfgPath}
	err = deployRunCmd(cmd, flags, "", false, false, true, false)
	if err == nil {
		t.Fatal("expected deploy to fail when deploy lock is held")
	}

	var lhe *lockHeldError
	if !errors.As(err, &lhe) {
		t.Fatalf("err = %T %v, want *lockHeldError", err, err)
	}
	if lhe.operation != "deploy" {
		t.Errorf("lockHeldError.operation = %q, want %q", lhe.operation, "deploy")
	}
}
