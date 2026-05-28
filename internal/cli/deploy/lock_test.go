package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/shared/lock"

	"github.com/spf13/cobra"
)

// TestDeployRunCmd_LockHeldBlocksDeploy covers both halves of the deploy /
// snapshot mutual exclusion: a held snapshot.lock blocks deploy with
// operation="snapshot", and a held deploy.lock blocks deploy with
// operation="deploy".
func TestDeployRunCmd_LockHeldBlocksDeploy(t *testing.T) {
	cases := []struct {
		name    string
		lockFn  func(string) string
		wantOp  string
		wantPid bool
	}{
		{"snapshot lock held", lock.SnapshotLockPath, "snapshot", true},
		{"deploy lock held", lock.DeployLockPath, "deploy", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "devbox.yml")
			if err := os.WriteFile(cfgPath, []byte("project:\n  name: test\n  prefix: devbox\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			lk, err := lock.Acquire(tc.lockFn(dir))
			if err != nil {
				t.Fatalf("seed lock: %v", err)
			}
			defer func() { _ = lk.Release() }()

			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
			err = deployRunCmd(cmd, flags, "", false, false, true, false, false)
			if err == nil {
				t.Fatal("expected deploy to fail when lock is held")
			}

			var lhe *lock.ProjectLockHeldError
			if !errors.As(err, &lhe) {
				t.Fatalf("err = %T %v, want *lock.ProjectLockHeldError", err, err)
			}
			if lhe.Operation != tc.wantOp {
				t.Errorf("Operation = %q, want %q", lhe.Operation, tc.wantOp)
			}
			if tc.wantPid && lhe.PID != os.Getpid() {
				t.Errorf("PID = %d, want %d", lhe.PID, os.Getpid())
			}
		})
	}
}
