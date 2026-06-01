package env

import (
	"context"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
)

func TestExecutableInPathValidate(t *testing.T) {
	t.Parallel()
	b := ExecutableInPath{}
	if err := b.Validate(map[string]any{"name": "ls"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := b.Validate(map[string]any{}); err == nil {
		t.Fatal("want error on missing name")
	}
}

func TestExecutableInPathDescribe(t *testing.T) {
	t.Parallel()
	got := ExecutableInPath{}.Describe(map[string]any{"name": "git"})
	if !strings.Contains(got, "git") {
		t.Fatalf("describe: %q", got)
	}
}

func TestExecutableInPathRun(t *testing.T) {
	t.Parallel()
	b := ExecutableInPath{}
	if err := b.Run(context.Background(), map[string]any{"name": "sh"}, spec.ExecContext{}); err != nil {
		t.Fatalf("sh should be in PATH: %v", err)
	}
	err := b.Run(context.Background(), map[string]any{"name": "definitely-not-a-binary-xyzzy"}, spec.ExecContext{})
	if err == nil || !strings.Contains(err.Error(), "not found in PATH") {
		t.Fatalf("want not found, got %v", err)
	}
}
