package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
)

func TestShellValidate(t *testing.T) {
	t.Parallel()
	b := Shell{}
	tests := []struct {
		name    string
		with    map[string]any
		wantErr string
	}{
		{"ok", map[string]any{"cmd": "true"}, ""},
		{"missing cmd", map[string]any{}, "missing required param 'cmd'"},
		{"empty cmd", map[string]any{"cmd": ""}, "missing required param 'cmd'"},
		{"bad timeout", map[string]any{"cmd": "true", "timeout": "nope"}, "invalid duration"},
		{"negative timeout", map[string]any{"cmd": "true", "timeout": "-5s"}, "must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := b.Validate(tt.with)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("want nil, got %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("want %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestShellDescribe(t *testing.T) {
	t.Parallel()
	got := Shell{}.Describe(map[string]any{"cmd": "echo hi"})
	if !strings.Contains(got, "echo hi") {
		t.Fatalf("describe missing cmd: %q", got)
	}
}

func TestShellRun(t *testing.T) {
	t.Parallel()
	b := Shell{}
	ctx := context.Background()

	if err := b.Run(ctx, map[string]any{"cmd": "exit 0"}, spec.ExecContext{}); err != nil {
		t.Fatalf("exit 0: unexpected error: %v", err)
	}

	err := b.Run(ctx, map[string]any{"cmd": "echo boom >&2; exit 3"}, spec.ExecContext{})
	if err == nil || !strings.Contains(err.Error(), "exit status 3") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected exit status 3 with stderr tail, got %v", err)
	}
}

func TestShellRunUsesProjectRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	b := Shell{}
	ectx := spec.ExecContext{ProjectRoot: dir}
	// Relative path must resolve against ProjectRoot, exactly as
	// condition.EvalCmd resolves a `when:`.
	if err := b.Run(context.Background(), map[string]any{"cmd": "test -e marker"}, ectx); err != nil {
		t.Fatalf("test -e marker in project root: %v", err)
	}
	if err := b.Run(context.Background(), map[string]any{"cmd": "test -e nope"}, ectx); err == nil {
		t.Fatal("expected failure for a missing file")
	}
}

func TestShellRunZeroTimeoutIsUnbounded(t *testing.T) {
	t.Parallel()
	// context.WithTimeout(ctx, 0) yields an already-expired context, so before
	// the fix an explicit `timeout: "0"` failed instantly. 0 now means
	// unbounded, matching parseStepTimeout and `when:` (which has no timeout).
	err := Shell{}.Run(context.Background(), map[string]any{"cmd": "sleep 0.2; exit 0", "timeout": "0"}, spec.ExecContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShellRunNegativeTimeoutRejected(t *testing.T) {
	t.Parallel()
	// 0 is the unbounded sentinel, so a negative value must be an error rather
	// than falling through the `timeout > 0` guard and running unbounded too.
	err := Shell{}.Run(context.Background(), map[string]any{"cmd": "exit 0", "timeout": "-5s"}, spec.ExecContext{})
	if err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("want negative-timeout error, got %v", err)
	}
}

func TestShellRunTimeout(t *testing.T) {
	t.Parallel()
	b := Shell{}
	start := time.Now()
	err := b.Run(context.Background(), map[string]any{"cmd": "sleep 5", "timeout": "50ms"}, spec.ExecContext{})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("want timeout error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}
