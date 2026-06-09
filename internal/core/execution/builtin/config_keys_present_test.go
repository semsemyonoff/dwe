package builtin

import (
	"context"
	"os"
	"path/filepath"
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

// TestConfigKeysPresentRun_endToEndFromLoadedConfig proves the documented
// post-setup recipe actually works: a top-level value written to local.yml
// (the wizard's legal write target) survives LoadConfig into cfg.Raw and
// resolves through the builtin. Guards against the docs advertising a path the
// config loader rejects — e.g. services.<name>.env.* is NOT a legal local.yml
// overlay key, so it must NOT be the documented example.
func TestConfigKeysPresentRun_endToEndFromLoadedConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"),
		[]byte("schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "workspace", "services", "app")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"),
		[]byte("type: app\ncontainer: app\nrequired: true\ndir: ./services/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// db.api_key is a legal top-level wizard/local.yml write target (see
	// docs/reference/config/setup.md write-scope); app.log_level is present-but-empty.
	localYML := "db:\n  api_key: secret123\napp:\n  log_level: \"\"\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace", "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	ectx := spec.ExecContext{Config: cfg}
	b := ConfigKeysPresent{}
	ctx := context.Background()

	if err := b.Run(ctx, map[string]any{"keys": []any{"db.api_key"}}, ectx); err != nil {
		t.Fatalf("db.api_key should resolve from loaded local.yml, got %v", err)
	}
	if err := b.Run(ctx, map[string]any{"keys": []any{"app.log_level"}}, ectx); err == nil || !strings.Contains(err.Error(), "app.log_level") {
		t.Fatalf("empty app.log_level should be reported missing, got %v", err)
	}
	if err := b.Run(ctx, map[string]any{"keys": []any{"db.missing"}}, ectx); err == nil || !strings.Contains(err.Error(), "db.missing") {
		t.Fatalf("absent db.missing should be reported missing, got %v", err)
	}
}

func TestConfigKeysPresentRunNilConfig(t *testing.T) {
	t.Parallel()
	err := ConfigKeysPresent{}.Run(context.Background(), map[string]any{"keys": "a"}, spec.ExecContext{})
	if err == nil || !strings.Contains(err.Error(), "merged config not available") {
		t.Fatalf("want nil-config error, got %v", err)
	}
}
