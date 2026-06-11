package vars

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
)

// localYAML reads workspace/local.yml from a fixture root.
func localYAML(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "workspace", "local.yml"))
	if err != nil {
		t.Fatalf("reading local.yml: %v", err)
	}
	return string(data)
}

// reloadVar resolves the effective value of path after a write.
func reloadVar(t *testing.T, cfgPath, path string) (any, bool) {
	t.Helper()
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	return config.ResolvePath(cfg.Raw, path)
}

func TestVarsSet_ScalarCoercion(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		arg      string
		wantYAML string // a substring the written local.yml must contain
		wantVal  any
	}{
		{"bool", "vars.feature.on", "true", "on: true", true},
		{"int", "vars.db.port", "6543", "port: 6543", 6543},
		{"float", "vars.scale.factor", "1.5", "factor: 1.5", 1.5},
		{"string", "vars.db.host", "db.internal", "host: db.internal", "db.internal"},
		{"quoted-int-stays-string", "vars.db.tag", `"42"`, `tag: "42"`, "42"},
		{"yes-is-string", "vars.flag.v", "yes", `v: "yes"`, "yes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, root := writeVarsFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

			_, _, err := runVarsCmd(t, flags, "set", tc.path, tc.arg)
			if err != nil {
				t.Fatalf("vars set %s %s: %v", tc.path, tc.arg, err)
			}
			if got := localYAML(t, root); !strings.Contains(got, tc.wantYAML) {
				t.Errorf("local.yml missing %q\ngot:\n%s", tc.wantYAML, got)
			}
			got, ok := reloadVar(t, cfgPath, tc.path)
			if !ok {
				t.Fatalf("%s not resolvable after set", tc.path)
			}
			if got != tc.wantVal {
				t.Errorf("effective %s: want %v (%T), got %v (%T)", tc.path, tc.wantVal, tc.wantVal, got, got)
			}
		})
	}
}

func TestVarsSet_PreservesComments(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "workspace.yml")
	const workspace = `schema_version: "2"
project:
  name: varstest
  prefix: dwe
vars:
  db:
    host: localhost
`
	if err := os.WriteFile(cfgPath, []byte(workspace), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	wsDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	const local = `# top-of-file note
vars:
  db:
    host: override-host # inline comment on host
`
	if err := os.WriteFile(filepath.Join(wsDir, "local.yml"), []byte(local), 0o644); err != nil {
		t.Fatalf("writing local.yml: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	if _, _, err := runVarsCmd(t, flags, "set", "vars.db.host", "newhost"); err != nil {
		t.Fatalf("vars set: %v", err)
	}
	got := localYAML(t, root)
	for _, want := range []string{"# top-of-file note", "# inline comment on host", "host: newhost"} {
		if !strings.Contains(got, want) {
			t.Errorf("local.yml missing %q after edit\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(got, "override-host") {
		t.Errorf("old value override-host should be gone\ngot:\n%s", got)
	}
}

func TestVarsSet_NewFileAndDeepNesting(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "workspace.yml")
	const workspace = `schema_version: "2"
project:
  name: varstest
  prefix: dwe
`
	if err := os.WriteFile(cfgPath, []byte(workspace), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	if _, _, err := runVarsCmd(t, flags, "set", "vars.a.b.c", "deep"); err != nil {
		t.Fatalf("vars set deep: %v", err)
	}
	got, ok := reloadVar(t, cfgPath, "vars.a.b.c")
	if !ok || got != "deep" {
		t.Errorf("deep nesting: want deep, got %v (ok=%v)", got, ok)
	}
}

func TestVarsSet_PathConfinement(t *testing.T) {
	tests := []string{"project.name", "vars", "vars..host", "runtime"}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			cfgPath, root := writeVarsFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			_, _, err := runVarsCmd(t, flags, "set", path, "x")
			if err == nil {
				t.Fatalf("expected path-confinement error for %q", path)
			}
			ce, ok := err.(*cmdctx.CodedError)
			if !ok || ce.Code != "vars_path_invalid" {
				t.Fatalf("want vars_path_invalid, got %v", err)
			}
		})
	}
}

func TestVarsSet_RejectsMapValue(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	_, _, err := runVarsCmd(t, flags, "set", "vars.db.host", "{a: b}")
	if err == nil {
		t.Fatal("expected coercion error for map value")
	}
	ce, ok := err.(*cmdctx.CodedError)
	if !ok || ce.Code != "vars_value_invalid" {
		t.Fatalf("want vars_value_invalid, got %v", err)
	}
}

func TestVarsSet_JSONValueRequired(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	_, _, err := runVarsCmd(t, flags, "set", "vars.db.host")
	if err == nil {
		t.Fatal("expected value-required error in JSON mode")
	}
	ce, ok := err.(*cmdctx.CodedError)
	if !ok || ce.Code != "vars_value_required" {
		t.Fatalf("want vars_value_required, got %v", err)
	}
}

func TestVarsSet_JSONConfirmationPayload(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	out, errOut, err := runVarsCmd(t, flags, "set", "vars.db.port", "9999")
	if err != nil {
		t.Fatalf("vars set --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var data varSetJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal set json: %v\nraw: %s", e, out)
	}
	if data.Var != "vars.db.port" {
		t.Errorf("var: want vars.db.port, got %q", data.Var)
	}
	if data.Value != float64(9999) {
		t.Errorf("value: want 9999, got %v (%T)", data.Value, data.Value)
	}
}

// TestVarsSet_InteractiveForm drives the no-value path through the runAsk seam.
func TestVarsSet_InteractiveForm(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	orig := runAsk
	defer func() { runAsk = orig }()
	var gotTitle string
	runAsk = func(_ context.Context, _ string, fields []ask.Field, _ ask.RunOptions) (ask.Result, error) {
		if len(fields) == 1 {
			gotTitle = fields[0].Title
		}
		return ask.NewResultForTest(map[string]any{"value": "formhost"}), nil
	}

	// Force the interactive branch: env not non-interactive, text output. The
	// IsInteractiveFn TTY probe would normally reject a buffer, so override it.
	origIsInteractive := widgets.IsInteractiveFn
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	defer func() { widgets.IsInteractiveFn = origIsInteractive }()

	if _, _, err := runVarsCmd(t, flags, "set", "vars.db.host"); err != nil {
		t.Fatalf("vars set (form): %v", err)
	}
	if !strings.Contains(gotTitle, "vars.db.host") {
		t.Errorf("form title missing var path: %q", gotTitle)
	}
	got, ok := reloadVar(t, cfgPath, "vars.db.host")
	if !ok || got != "formhost" {
		t.Errorf("form write: want formhost, got %v (ok=%v)", got, ok)
	}
}

func TestVarsSet_ContainerWriteGate(t *testing.T) {
	// Project with an allowlist that permits vars.db.* but not vars.app.*.
	root := t.TempDir()
	cfgPath := filepath.Join(root, "workspace.yml")
	const workspace = `schema_version: "2"
project:
  name: varstest
  prefix: dwe
bridge:
  vars_writable:
    - vars.db.*
    - vars.app.name
vars:
  app:
    name: myapp
    title: original
  db:
    host: localhost
`
	if err := os.WriteFile(cfgPath, []byte(workspace), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	// Simulate a container invocation.
	t.Setenv(bridgeclient.EnvInvokedFrom, bridgeclient.InvokedFromContainer)

	tests := []struct {
		name    string
		path    string
		allowed bool
	}{
		{"wildcard match", "vars.db.host", true},
		{"exact match", "vars.app.name", true},
		{"wildcard denies base", "vars.db", false}, // base itself denied (also path-invalid? no, vars.db is leaf-shaped)
		{"not in allowlist", "vars.app.title", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			_, _, err := runVarsCmd(t, flags, "set", tc.path, "x")
			if tc.allowed {
				if err != nil {
					t.Fatalf("expected allowed write, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected container-write denial for %q", tc.path)
			}
			ce, ok := err.(*cmdctx.CodedError)
			if !ok || ce.Code != "vars_not_container_writable" {
				t.Fatalf("want vars_not_container_writable, got %v", err)
			}
		})
	}
}

func TestVarsSet_HostUnrestricted(t *testing.T) {
	// No bridge env set → host context → no allowlist enforcement even when one
	// is configured.
	root := t.TempDir()
	cfgPath := filepath.Join(root, "workspace.yml")
	const workspace = `schema_version: "2"
project:
  name: varstest
  prefix: dwe
bridge:
  vars_writable:
    - vars.db.host
vars:
  app:
    name: myapp
`
	if err := os.WriteFile(cfgPath, []byte(workspace), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	// vars.app.name is NOT in the allowlist but host writes are unrestricted.
	if _, _, err := runVarsCmd(t, flags, "set", "vars.app.name", "renamed"); err != nil {
		t.Fatalf("host write should be unrestricted, got %v", err)
	}
	if got, ok := reloadVar(t, cfgPath, "vars.app.name"); !ok || got != "renamed" {
		t.Errorf("host write: want renamed, got %v (ok=%v)", got, ok)
	}
}
