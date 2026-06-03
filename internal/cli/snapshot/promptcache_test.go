package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"
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

// snapshotProjectWithConfig sets up a project root with workspace.yml AND
// workspace/snapshot.yml so runSnapshotRestore can proceed past the
// snapshot-config check.
func snapshotProjectWithConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(
		"schema_version: 1\nproject:\n  name: testproj\n"), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	wsDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	// Minimal snapshot.yml with a rollback_target so rollback can run.
	if err := os.WriteFile(filepath.Join(wsDir, "snapshot.yml"), []byte(
		"rollback_target: backup\n"), 0o644); err != nil {
		t.Fatalf("write snapshot.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "snapshots"), 0o755); err != nil {
		t.Fatalf("mkdir snapshots: %v", err)
	}
	return dir
}

func TestSnapshotRestore_InvalidatesCache(t *testing.T) {
	base := snapshotProjectWithConfig(t)
	// Pre-seed cache so we can verify the Remove call.
	if err := promptcache.Write(base, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// Stub the real restore call.
	prev := snapshotRestoreFn
	t.Cleanup(func() { snapshotRestoreFn = prev })
	snapshotRestoreFn = func(_ context.Context, _ snapshot.RestoreParams) (*snapshot.RestoreResult, error) {
		return &snapshot.RestoreResult{Status: meta.StatusOk, Manifest: &meta.Manifest{Name: "snap"}}, nil
	}

	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}
	cmd, _, _ := makeTestCmd(t)
	if err := runSnapshotRestore(cmd, flags, "snap", true, true, true, "restore", "snapshot:restore"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readCacheState(t, base); got != "" {
		t.Errorf("cache should be invalidated after successful restore; got %q", got)
	}
}

func TestSnapshotRollback_InvalidatesCache(t *testing.T) {
	base := snapshotProjectWithConfig(t)
	if err := promptcache.Write(base, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	prev := snapshotRollbackFn
	t.Cleanup(func() { snapshotRollbackFn = prev })
	snapshotRollbackFn = func(_ context.Context, _ snapshot.RestoreParams) (*snapshot.RestoreResult, error) {
		return &snapshot.RestoreResult{Status: meta.StatusOk, Manifest: &meta.Manifest{Name: "backup"}}, nil
	}

	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}
	cmd, _, _ := makeTestCmd(t)
	if err := runSnapshotRollback(cmd, flags, true, true, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readCacheState(t, base); got != "" {
		t.Errorf("cache should be invalidated after rollback; got %q", got)
	}
}

func TestSnapshotRollback_Failure_InvalidatesCache(t *testing.T) {
	base := snapshotProjectWithConfig(t)
	if err := promptcache.Write(base, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	prev := snapshotRollbackFn
	t.Cleanup(func() { snapshotRollbackFn = prev })
	snapshotRollbackFn = func(_ context.Context, _ snapshot.RestoreParams) (*snapshot.RestoreResult, error) {
		return nil, errors.New("rollback failed")
	}

	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}
	cmd, _, _ := makeTestCmd(t)
	if err := runSnapshotRollback(cmd, flags, true, true, true); err == nil {
		t.Fatal("expected error from stubbed rollback failure")
	}
	// A failed rollback may have partially mutated workspace/container state;
	// cache must be invalidated so the next prompt reflects reality.
	if got := readCacheState(t, base); got != "" {
		t.Errorf("cache should be invalidated on rollback failure; got %q", got)
	}
}

func TestSnapshotRestore_Failure_InvalidatesCache(t *testing.T) {
	base := snapshotProjectWithConfig(t)
	if err := promptcache.Write(base, promptcache.StateRunning); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	prev := snapshotRestoreFn
	t.Cleanup(func() { snapshotRestoreFn = prev })
	snapshotRestoreFn = func(_ context.Context, _ snapshot.RestoreParams) (*snapshot.RestoreResult, error) {
		return nil, errors.New("restore failed")
	}

	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}
	cmd, _, _ := makeTestCmd(t)
	if err := runSnapshotRestore(cmd, flags, "snap", true, true, true, "restore", "snapshot:restore"); err == nil {
		t.Fatal("expected error from stubbed restore failure")
	}
	// A failed restore may have partially mutated workspace/container state;
	// cache must be invalidated so the next prompt reflects reality.
	if got := readCacheState(t, base); got != "" {
		t.Errorf("cache should be invalidated on restore failure; got %q", got)
	}
}
