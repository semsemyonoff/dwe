package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"

	"github.com/spf13/cobra"
)

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

func TestDeployRun_NoService_InvalidatesCache(t *testing.T) {
	dir := t.TempDir()
	if err := promptcache.Write(dir, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	prev := runHelperFn
	t.Cleanup(func() { runHelperFn = prev })
	runHelperFn = func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, _ Opts) error {
		return nil
	}

	cmd := &cobra.Command{}
	flags := &cmdctx.RootFlags{Root: dir, ConfigPath: filepath.Join(dir, "workspace.yml")}
	if err := runDeployRun(context.Background(), cmd, flags, deployRunOpts{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readCacheState(t, dir); got != "" {
		t.Errorf("cache should be invalidated after deploy run; got %q", got)
	}
}

func TestDeployRun_WithService_InvalidatesCache(t *testing.T) {
	dir := t.TempDir()
	if err := promptcache.Write(dir, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	prev := runHelperFn
	t.Cleanup(func() { runHelperFn = prev })
	runHelperFn = func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, _ Opts) error {
		return nil
	}

	cmd := &cobra.Command{}
	flags := &cmdctx.RootFlags{Root: dir, ConfigPath: filepath.Join(dir, "workspace.yml")}
	if err := runDeployRun(context.Background(), cmd, flags, deployRunOpts{ServiceName: "main"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readCacheState(t, dir); got != "" {
		t.Errorf("cache should be invalidated after deploy run --service; got %q", got)
	}
}

func TestDeployRun_Failure_InvalidatesCache(t *testing.T) {
	dir := t.TempDir()
	if err := promptcache.Write(dir, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	prev := runHelperFn
	t.Cleanup(func() { runHelperFn = prev })
	runHelperFn = func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, _ Opts) error {
		return errors.New("deploy failed")
	}

	cmd := &cobra.Command{}
	flags := &cmdctx.RootFlags{Root: dir, ConfigPath: filepath.Join(dir, "workspace.yml")}
	if err := runDeployRun(context.Background(), cmd, flags, deployRunOpts{}); err == nil {
		t.Fatal("expected deploy failure")
	}
	// A failed deploy may have partially mutated container state; cache must be
	// invalidated so the next prompt refresh or `dwe status` reflects reality.
	if got := readCacheState(t, dir); got != "" {
		t.Errorf("cache should be invalidated on failure; got %q", got)
	}
}
