package deploy

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/execution/preflight"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/lock"

	"github.com/spf13/cobra"
)

// recordBridgePrepare swaps the bridge prepare seam for a recorder that
// returns err (a sentinel error doubles as a pipeline-stopper: the hook sits
// before the deploy pipeline, so failing here proves the pipeline never ran).
func recordBridgePrepare(t *testing.T, err error) *[]bridge.PrepareOptions {
	t.Helper()
	var calls []bridge.PrepareOptions
	prev := bridgePrepareFn
	t.Cleanup(func() { bridgePrepareFn = prev })
	bridgePrepareFn = func(opts bridge.PrepareOptions) error {
		calls = append(calls, opts)
		return err
	}
	return &calls
}

func writeDeployTestWorkspace(t *testing.T) (dir, cfgPath string) {
	t.Helper()
	dir = t.TempDir()
	cfgPath = filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, cfgPath
}

func noopPreflight(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
	return nil
}

// TestRunHelper_BridgePrepareCyclesAfterLocks pins the deploy hook contract
// (design D6): the prepare hook runs after preflight + project locks, with
// the CYCLE daemon step, rooted at the project dir.
func TestRunHelper_BridgePrepareCyclesAfterLocks(t *testing.T) {
	dir, cfgPath := writeDeployTestWorkspace(t)
	sentinel := errors.New("stop before pipeline")
	calls := recordBridgePrepare(t, sentinel)

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	err := RunHelper(context.Background(), &cobra.Command{}, flags, Opts{
		NonInteractive: true,
		Silent:         true,
		PreflightFn:    noopPreflight,
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
	if !strings.Contains(err.Error(), "preparing host bridge") {
		t.Errorf("err = %q, want 'preparing host bridge' context", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("prepare called %d times, want 1", len(*calls))
	}
	opts := (*calls)[0]
	if opts.BaseDir != dir {
		t.Errorf("prepare BaseDir = %q, want %q", opts.BaseDir, dir)
	}
	if !opts.CycleDaemon {
		t.Error("deploy must CYCLE the daemon (CycleDaemon = false, want true — design D6)")
	}
	if opts.Cfg == nil {
		t.Error("prepare Cfg = nil, want loaded config")
	}
	// The hook failure must release the project locks on the way out.
	release, lockErr := lock.AcquireProjectLocks(dir)
	if lockErr != nil {
		t.Fatalf("project locks not released after hook failure: %v", lockErr)
	}
	release()
}

func TestRunHelper_BridgePrepareNotCalledWhenLockHeld(t *testing.T) {
	dir, cfgPath := writeDeployTestWorkspace(t)
	calls := recordBridgePrepare(t, nil)

	held, err := lock.Acquire(lock.DeployLockPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	err = RunHelper(context.Background(), &cobra.Command{}, flags, Opts{
		NonInteractive: true,
		Silent:         true,
		PreflightFn:    noopPreflight,
	})
	if !errors.As(err, new(*lock.ProjectLockHeldError)) {
		t.Fatalf("err = %v, want *lock.ProjectLockHeldError", err)
	}
	if len(*calls) != 0 {
		t.Errorf("prepare called %d times with lock held, want 0 (hook is AFTER locks)", len(*calls))
	}
}

func TestRunHelper_BridgePrepareNotCalledWhenPreflightFails(t *testing.T) {
	_, cfgPath := writeDeployTestWorkspace(t)
	calls := recordBridgePrepare(t, nil)

	pfErr := &preflight.Error{}
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	err := RunHelper(context.Background(), &cobra.Command{}, flags, Opts{
		NonInteractive: true,
		Silent:         true,
		PreflightFn: func(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
			return pfErr
		},
	})
	if !errors.Is(err, pfErr) {
		t.Fatalf("err = %v, want preflight error", err)
	}
	if len(*calls) != 0 {
		t.Errorf("prepare called %d times after preflight failure, want 0", len(*calls))
	}
}
