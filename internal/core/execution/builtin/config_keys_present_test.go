package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestConfigKeysPresentValidate(t *testing.T) {
	t.Parallel()
	b := ConfigKeysPresent{}
	tests := []struct {
		name    string
		with    map[string]any
		wantErr string
	}{
		{"ok single", map[string]any{"keys": "workspace.domain"}, ""},
		{"ok list", map[string]any{"keys": []any{"a.b", "c"}}, ""},
		{"missing keys", map[string]any{}, "missing required param 'keys'"},
		{"empty keys", map[string]any{"keys": []any{}}, "missing required param 'keys'"},
		{"bad type", map[string]any{"keys": 42}, "expected string or list"},
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

func TestConfigKeysPresentDescribe(t *testing.T) {
	t.Parallel()
	got := ConfigKeysPresent{}.Describe(map[string]any{"keys": []any{"workspace.domain", "services.db.env.DB_PASSWORD"}})
	if !strings.Contains(got, "workspace.domain") || !strings.Contains(got, "services.db.env.DB_PASSWORD") {
		t.Fatalf("describe missing keys: %q", got)
	}
}

func TestConfigKeysPresentRun(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"workspace": map[string]any{"domain": "example.test"},
		"services": map[string]any{
			"db": map[string]any{
				"env": map[string]any{
					"DB_PASSWORD": "secret",
					"EMPTY":       "",
				},
			},
		},
		"port":   5432,
		"flag":   false,
		"nilval": nil,
	}
	ectx := spec.ExecContext{Config: &config.DweConfig{Raw: raw}}
	b := ConfigKeysPresent{}
	ctx := context.Background()

	tests := []struct {
		name    string
		keys    []any
		wantErr string // substring; "" means success
	}{
		{"present string", []any{"workspace.domain"}, ""},
		{"present nested", []any{"services.db.env.DB_PASSWORD"}, ""},
		{"present number scalar", []any{"port"}, ""},
		{"present bool scalar", []any{"flag"}, ""},
		{"multiple present", []any{"workspace.domain", "services.db.env.DB_PASSWORD"}, ""},
		{"absent path", []any{"services.db.env.MISSING"}, "services.db.env.MISSING"},
		{"empty value", []any{"services.db.env.EMPTY"}, "services.db.env.EMPTY"},
		{"nil value", []any{"nilval"}, "nilval"},
		{"absent intermediate", []any{"services.cache.env.X"}, "services.cache.env.X"},
		{"one missing among present", []any{"workspace.domain", "services.db.env.MISSING"}, "services.db.env.MISSING"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := b.Run(ctx, map[string]any{"keys": tt.keys}, ectx)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestConfigKeysPresentRunNilConfig(t *testing.T) {
	t.Parallel()
	err := ConfigKeysPresent{}.Run(context.Background(), map[string]any{"keys": "a"}, spec.ExecContext{})
	if err == nil || !strings.Contains(err.Error(), "merged config not available") {
		t.Fatalf("want nil-config error, got %v", err)
	}
}
