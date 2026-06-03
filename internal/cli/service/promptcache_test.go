package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/cli/deploy"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/core/workflow/lifecycle"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"

	"github.com/spf13/cobra"
)

// readCacheState parses .dwe/prompt-cache.yml under root and returns the state
// value, or "" if absent / unparseable.
func readCacheState(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".dwe", "prompt-cache.yml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		t.Fatalf("read prompt-cache.yml: %v", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "state:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// TestExecuteTogglePlan_FullSuccess_WritesRunning verifies the cache write
// happens after pending-clear AND any after-hooks.
func TestExecuteTogglePlan_FullSuccess_WritesRunning(t *testing.T) {
	var order []string
	var dir string
	deps, baseDir := makeExecuteDeps(t,
		func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, _ deploy.Opts) error {
			order = append(order, "deploy")
			return nil
		},
		func(_ lifecycle.RunContext) error {
			order = append(order, "restart")
			return nil
		},
		func(_ context.Context, rc runtime.RunContext) error {
			order = append(order, "hook:"+rc.Cmd.ID)
			// Hook fires BEFORE cache write — assert cache absent at this point.
			if got := readCacheState(t, dir); got != "" {
				t.Errorf("cache should not be written yet during after-hook; got %q", got)
			}
			return nil
		},
	)
	dir = baseDir
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(makeShellCmd("foo:post"))
	deps.CmdReg = reg
	deps.Cfg = &config.DweConfig{}

	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingRestart}},
		AfterSteps: []PlanStep{{CommandID: "foo:post"}},
	}
	contributors := []Contributor{{Service: "foo", Requires: config.RequiresRestart}}
	if err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := readCacheState(t, dir); got != promptcache.StateRunning {
		t.Errorf("cache state = %q, want %q", got, promptcache.StateRunning)
	}
	wantOrder := []string{"restart", "hook:foo:post"}
	if len(order) != len(wantOrder) {
		t.Fatalf("order = %v, want %v", order, wantOrder)
	}
}

// TestExecuteTogglePlan_FailureAfterDeploy_DoesNotWrite verifies that when an
// apply step succeeds but the next phase (after-hook) fails, the cache is NOT
// written.
func TestExecuteTogglePlan_FailureAfterDeploy_DoesNotWrite(t *testing.T) {
	deps, dir := makeExecuteDeps(t,
		nil,
		func(_ lifecycle.RunContext) error { return nil },
		func(_ context.Context, _ runtime.RunContext) error {
			return fmt.Errorf("after-hook boom")
		},
	)
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(makeShellCmd("foo:post"))
	deps.CmdReg = reg
	deps.Cfg = &config.DweConfig{}

	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingRestart}},
		AfterSteps: []PlanStep{{CommandID: "foo:post"}},
	}
	contributors := []Contributor{{Service: "foo", Requires: config.RequiresRestart}}
	err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors})
	if err == nil {
		t.Fatal("expected after-hook failure")
	}
	if got := readCacheState(t, dir); got != "" {
		t.Errorf("cache should NOT be written when after-hook fails; got %q", got)
	}
}

// TestExecuteTogglePlan_EmptyPlan_DoesNotWriteCache verifies an empty plan (no
// apply steps) leaves the cache untouched.
func TestExecuteTogglePlan_EmptyPlan_DoesNotWriteCache(t *testing.T) {
	deps, dir := makeExecuteDeps(t, nil, nil, nil)
	if err := executeTogglePlan(context.Background(), deps, TogglePlan{}, ExecuteOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readCacheState(t, dir); got != "" {
		t.Errorf("cache should be untouched on empty plan; got %q", got)
	}
}
