package builtin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/devbox/internal/core/execution/builtin/spec"
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
