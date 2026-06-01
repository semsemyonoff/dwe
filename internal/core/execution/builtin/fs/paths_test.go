package fs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

func newTestExecCtx(root string) spec.ExecContext {
	return spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: root,
		Output:      render.NewWriter(&bytes.Buffer{}),
	}
}

// --- RemovePaths.Validate ---

func TestRemovePaths_Validate_MissingPaths(t *testing.T) {
	b := RemovePaths{}
	if err := b.Validate(nil); err == nil {
		t.Fatal("expected error for missing paths param")
	}
	if err := b.Validate(map[string]any{}); err == nil {
		t.Fatal("expected error for empty map")
	}
}

func TestRemovePaths_Validate_EmptyPath(t *testing.T) {
	b := RemovePaths{}
	err := b.Validate(map[string]any{"paths": []any{""}})
	if err == nil {
		t.Fatal("expected error for empty path string")
	}
}

func TestRemovePaths_Validate_AbsolutePath(t *testing.T) {
	b := RemovePaths{}
	err := b.Validate(map[string]any{"paths": []any{"/etc/passwd"}})
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestRemovePaths_Validate_EscapingPath(t *testing.T) {
	b := RemovePaths{}
	err := b.Validate(map[string]any{"paths": []any{"../../etc/passwd"}})
	if err == nil {
		t.Fatal("expected error for path escaping project root")
	}
}

func TestRemovePaths_Validate_RootEquivalent(t *testing.T) {
	b := RemovePaths{}
	err := b.Validate(map[string]any{"paths": []any{"."}})
	if err == nil {
		t.Fatal("expected error for root-equivalent path '.'")
	}
}

func TestRemovePaths_Validate_Valid(t *testing.T) {
	b := RemovePaths{}
	err := b.Validate(map[string]any{"paths": []any{"services/main", "logs"}})
	if err != nil {
		t.Fatalf("unexpected error for valid paths: %v", err)
	}
}

// --- RemovePaths.Describe ---

func TestRemovePaths_Describe(t *testing.T) {
	b := RemovePaths{}
	desc := b.Describe(map[string]any{"paths": []any{"a", "b"}})
	if !strings.Contains(desc, "remove_paths") {
		t.Errorf("expected builtin name in describe, got %q", desc)
	}
}

// --- RemovePaths.Run ---

func TestRemovePaths_Run_RemovesFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "toremove.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := RemovePaths{}
	ctx := newTestExecCtx(root)
	err := b.Run(context.Background(), map[string]any{"paths": []any{"toremove.txt"}}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("expected file to be removed")
	}
}

func TestRemovePaths_Run_NonexistentPathIsOK(t *testing.T) {
	root := t.TempDir()
	b := RemovePaths{}
	ctx := newTestExecCtx(root)
	err := b.Run(context.Background(), map[string]any{"paths": []any{"does_not_exist"}}, ctx)
	if err != nil {
		t.Fatalf("os.RemoveAll on non-existent path should not error: %v", err)
	}
}

func TestRemovePaths_Run_AbsolutePathRejected(t *testing.T) {
	root := t.TempDir()
	b := RemovePaths{}
	ctx := newTestExecCtx(root)
	err := b.Run(context.Background(), map[string]any{"paths": []any{"/etc/hosts"}}, ctx)
	if err == nil {
		t.Fatal("expected error for absolute path in Run")
	}
}

func TestRemovePaths_Run_InvalidPathsType(t *testing.T) {
	root := t.TempDir()
	b := RemovePaths{}
	ctx := newTestExecCtx(root)
	err := b.Run(context.Background(), map[string]any{"paths": 99}, ctx)
	if err == nil {
		t.Fatal("expected error for invalid paths type in Run")
	}
}

// --- RemovePaths.Validate edge case ---

func TestRemovePaths_Validate_InvalidPathsType(t *testing.T) {
	b := RemovePaths{}
	err := b.Validate(map[string]any{"paths": 123})
	if err == nil {
		t.Fatal("expected error for invalid paths type")
	}
}
