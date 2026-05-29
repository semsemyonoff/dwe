package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/core/execution/builtin/spec"
)

func TestEnvKeysPresentValidate(t *testing.T) {
	t.Parallel()
	b := envKeysPresentBuiltin{}
	cases := []struct {
		name    string
		with    map[string]any
		wantErr string
	}{
		{"ok", map[string]any{"file": ".env", "keys": []any{"A"}}, ""},
		{"missing file", map[string]any{"keys": []any{"A"}}, "missing required param 'file'"},
		{"missing keys", map[string]any{"file": ".env"}, "missing required param 'keys'"},
		{"empty keys", map[string]any{"file": ".env", "keys": []any{}}, "missing required param 'keys'"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := b.Validate(tt.with)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("want %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestEnvKeysPresentRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("FOO=bar\nBAZ=\nQUUX=\"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := envKeysPresentBuiltin{}

	if err := b.Run(context.Background(), map[string]any{"file": ".env", "keys": []any{"FOO"}}, spec.ExecContext{ProjectRoot: dir}); err != nil {
		t.Fatalf("FOO present: %v", err)
	}

	err := b.Run(context.Background(), map[string]any{"file": ".env", "keys": []any{"FOO", "BAZ", "QUUX", "MISSING"}}, spec.ExecContext{ProjectRoot: dir})
	if err == nil {
		t.Fatal("want missing-or-empty error")
	}
	if !strings.Contains(err.Error(), "missing or empty keys") {
		t.Fatalf("got %v", err)
	}
	for _, k := range []string{"BAZ", "QUUX", "MISSING"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("want %q in error, got %v", k, err)
		}
	}
	if strings.Contains(err.Error(), "FOO") {
		t.Errorf("FOO should not appear, got %v", err)
	}

	err = b.Run(context.Background(), map[string]any{"file": "nope.env", "keys": []any{"X"}}, spec.ExecContext{ProjectRoot: dir})
	if err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("want file not found, got %v", err)
	}
}
