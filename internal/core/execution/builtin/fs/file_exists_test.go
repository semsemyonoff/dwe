package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/core/execution/builtin/spec"
)

func TestFileExistsValidate(t *testing.T) {
	t.Parallel()
	b := FileExists{}
	if err := b.Validate(map[string]any{"path": "x"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := b.Validate(map[string]any{}); err == nil {
		t.Fatal("want error on missing path")
	}
}

func TestFileExistsDescribe(t *testing.T) {
	t.Parallel()
	got := FileExists{}.Describe(map[string]any{"path": "x/y"})
	if !strings.Contains(got, "x/y") {
		t.Fatalf("describe: %q", got)
	}
}

func TestFileExistsRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := FileExists{}
	if err := b.Run(context.Background(), map[string]any{"path": "exists.txt"}, spec.ExecContext{ProjectRoot: dir}); err != nil {
		t.Fatalf("present: %v", err)
	}
	err := b.Run(context.Background(), map[string]any{"path": "missing.txt"}, spec.ExecContext{ProjectRoot: dir})
	if err == nil || !strings.Contains(err.Error(), "file not found: missing.txt") {
		t.Fatalf("want not found, got %v", err)
	}
}
