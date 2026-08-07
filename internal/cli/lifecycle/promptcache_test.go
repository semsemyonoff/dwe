package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"
)

// readCacheState parses .dwe/prompt-cache.yml and returns the state field, or
// "" if the file is absent or unparseable. Test-only helper.
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

func TestRun_WritesRunning_OnSuccess(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"run"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config: %v", err)
	}
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := readCacheState(t, dir); got != promptcache.StateRunning {
		t.Errorf("cache state = %q, want %q", got, promptcache.StateRunning)
	}
}

func TestStop_NoFlag_WritesStopped(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"stop"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config: %v", err)
	}
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := readCacheState(t, dir); got != promptcache.StateStopped {
		t.Errorf("cache state = %q, want %q", got, promptcache.StateStopped)
	}
}

func TestStop_WithService_InvalidatesCache(t *testing.T) {
	// This test asserts what happens AFTER preflight, so it must not depend on
	// a real docker probe: `StopService` runs preflight before the locks, and a
	// probe killed on its 5s deadline would fail the command for an unrelated
	// reason. The direct-`StopService` tests get this via `SkipPreflight: true`;
	// the cobra path builds its own deps, so it stubs the seam instead.
	stubPreflightRun(t)

	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"postgres": {enabled: true, container: "pg"},
	})
	baseDir := filepath.Dir(cfgPath)
	// Pre-seed cache.
	if err := promptcache.Write(baseDir, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	prev := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prev })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: baseDir}
	cmd := NewStopCmd(groupEnvironment, flags)
	cmd.SilenceErrors = true
	if err := cmd.RunE(cmd, []string{"postgres"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := readCacheState(t, baseDir); got != "" {
		t.Errorf("cache should be invalidated; state = %q", got)
	}
}

func TestRestart_NoArg_WritesRunning_OnSuccess(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"restart"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config: %v", err)
	}
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := readCacheState(t, dir); got != promptcache.StateRunning {
		t.Errorf("cache state = %q, want %q", got, promptcache.StateRunning)
	}
}

func TestRestart_WithService_InvalidatesCache(t *testing.T) {
	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"postgres": {enabled: true, container: "pg"},
	})
	baseDir := filepath.Dir(cfgPath)
	if err := promptcache.Write(baseDir, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: baseDir}
	cmd := NewRestartCmd(groupEnvironment, flags)
	cmd.SilenceErrors = true
	if err := cmd.RunE(cmd, []string{"postgres"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := readCacheState(t, baseDir); got != "" {
		t.Errorf("cache should be invalidated; state = %q", got)
	}
}

func TestReset_ProjectWide_WritesStopped(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "workspace.yml")
	if err := os.WriteFile(configPath, []byte(
		"schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resetDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(resetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Bare reset.yml with a no-op shell step (avoids docker dependency).
	if err := os.WriteFile(filepath.Join(resetDir, "reset.yml"), []byte(
		"phases:\n  - name: cleanup\n    steps:\n      - name: noop\n        type: shell\n        cmd: \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", configPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readCacheState(t, tmpDir); got != promptcache.StateStopped {
		t.Errorf("cache state = %q, want %q", got, promptcache.StateStopped)
	}
}

func TestReset_PerService_InvalidatesCache(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	statePath := filepath.Join(dir, journal.DefaultRelPath)

	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      map[string]*journal.ServiceState{"postgres": {Status: journal.StatusDeployed}},
	}
	if err := journal.Save(statePath, initial); err != nil {
		t.Fatal(err)
	}
	// Pre-seed cache (this simulates a prior running state).
	if err := promptcache.Write(dir, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readCacheState(t, dir); got != "" {
		t.Errorf("cache should be invalidated after per-service reset; state = %q", got)
	}
}

func TestRun_CacheWriteFailure_DoesNotFailCommand(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	// Pre-create .dwe writable (lifecycle needs to write deploy lock there)
	// but plant a directory at .dwe/prompt-cache.yml so the cache rename fails.
	dweDir := filepath.Join(dir, ".dwe")
	if err := os.MkdirAll(filepath.Join(dweDir, "prompt-cache.yml"), 0o755); err != nil {
		t.Fatalf("mkdir prompt-cache.yml as directory: %v", err)
	}

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"run"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config: %v", err)
	}
	// Command must still exit 0 even when cache write fails (the rename onto
	// a directory will error inside promptcache.Write but is best-effort).
	if err := root.Execute(); err != nil {
		t.Fatalf("cache write failure should not surface as command error, got: %v", err)
	}
}
