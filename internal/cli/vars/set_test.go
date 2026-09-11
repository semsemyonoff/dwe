package vars

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/semsemyonoff/dwe/internal/shared/randval"
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

// TestVarsSet_PrefixOptional sets a var by its shorthand (no vars. prefix) and
// confirms it writes the canonical vars.db.host leaf.
func TestVarsSet_PrefixOptional(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	if _, _, err := runVarsCmd(t, flags, "set", "db.host", "shorthost"); err != nil {
		t.Fatalf("vars set db.host: %v", err)
	}
	got, ok := reloadVar(t, cfgPath, "vars.db.host")
	if !ok || got != "shorthost" {
		t.Errorf("set via shorthand: want vars.db.host=shorthost, got %v (ok=%v)", got, ok)
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
	// Structurally-invalid targets are still rejected: the bare vars root and any
	// path with an empty segment. (A non-vars first segment is no longer an error
	// — it is normalized under vars.*, see TestVarsSet_NonVarsHeadNormalizes.)
	tests := []string{"vars", "vars..host", ".host"}
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

// TestVarsSet_NonVarsHeadNormalizes pins the prefix-optional semantics: a target
// whose head segment is not "vars" (e.g. project.name) is NOT an escape — it is
// normalized under the sandbox and writes vars.project.name. The sandbox can
// never be left; container writes stay allowlist-gated on the normalized path.
func TestVarsSet_NonVarsHeadNormalizes(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	if _, _, err := runVarsCmd(t, flags, "set", "project.name", "x"); err != nil {
		t.Fatalf("set project.name should normalize, got error: %v", err)
	}
	if _, ok := reloadVar(t, cfgPath, "vars.project.name"); !ok {
		t.Error("set project.name should have written vars.project.name")
	}
	// It must NOT have written the real top-level project.name.
	if v, _ := reloadVar(t, cfgPath, "project.name"); v == "x" {
		t.Error("set must not write outside the vars sandbox")
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
	// The form title shows the display path (vars. prefix stripped).
	if !strings.Contains(gotTitle, "db.host") || strings.Contains(gotTitle, "vars.db.host") {
		t.Errorf("form title should show the stripped path: %q", gotTitle)
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

// generateSeed starts with 0x12 0x34 so hex:2 yields the all-digit "1234",
// the value CoerceScalar would have turned into an int. 64 bytes cover the
// default 32-byte spec.
var generateSeed = bytes.Repeat([]byte{
	0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0,
	0x0f, 0x1e, 0x2d, 0x3c, 0x4b, 0x5a, 0x69, 0x78,
}, 4)

// seedRandReader swaps randReader for a fixed reader over generateSeed.
func seedRandReader(t *testing.T) {
	t.Helper()
	orig := randReader
	randReader = bytes.NewReader(generateSeed)
	t.Cleanup(func() { randReader = orig })
}

// wantCode asserts err is a CodedError carrying code.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var ce *cmdctx.CodedError
	if !errors.As(err, &ce) || ce.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

// forbidForm fails the test if the interactive form opens, and makes the
// session look interactive so a missing --generate would reach the form.
func forbidForm(t *testing.T) {
	t.Helper()
	origAsk, origIsInteractive := runAsk, widgets.IsInteractiveFn
	runAsk = func(context.Context, string, []ask.Field, ask.RunOptions) (ask.Result, error) {
		t.Fatal("the interactive form must not open with --generate")
		return ask.Result{}, nil
	}
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	t.Cleanup(func() { runAsk, widgets.IsInteractiveFn = origAsk, origIsInteractive })
}

func TestVarsSet_GenerateKinds(t *testing.T) {
	tests := []struct {
		spec     string
		wantYAML string // a substring the written local.yml must contain; "" skips
	}{
		{"hex:2", `secret: "1234"`},
		{"hex", ""},
		{"base64url:5", ""},
		{"uuid", ""},
	}
	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			cfgPath, root := writeVarsFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			seedRandReader(t)
			forbidForm(t)

			spec, err := randval.Parse(tc.spec)
			if err != nil {
				t.Fatalf("parse %s: %v", tc.spec, err)
			}
			want, err := randval.Generate(spec, bytes.NewReader(generateSeed))
			if err != nil {
				t.Fatalf("generate %s: %v", tc.spec, err)
			}

			out, _, err := runVarsCmd(t, flags, "set", "app.secret", "--generate", tc.spec)
			if err != nil {
				t.Fatalf("vars set --generate %s: %v", tc.spec, err)
			}
			if !strings.Contains(out, want) {
				t.Errorf("confirmation should print the value %q\ngot:\n%s", want, out)
			}
			if tc.wantYAML != "" {
				if got := localYAML(t, root); !strings.Contains(got, tc.wantYAML) {
					t.Errorf("local.yml missing %q\ngot:\n%s", tc.wantYAML, got)
				}
			}
			got, ok := reloadVar(t, cfgPath, "vars.app.secret")
			if !ok {
				t.Fatal("vars.app.secret not resolvable after set")
			}
			if s, isStr := got.(string); !isStr || s != want {
				t.Errorf("effective value: want string %q, got %v (%T)", want, got, got)
			}
		})
	}
}

func TestVarsSet_GenerateUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code string
	}{
		{"value and generate", []string{"set", "app.secret", "x", "--generate", "hex"}, "vars_value_ambiguous"},
		{"unknown kind", []string{"set", "app.secret", "--generate", "sha"}, "vars_generate_invalid"},
		{"zero bytes", []string{"set", "app.secret", "--generate", "hex:0"}, "vars_generate_invalid"},
		{"uuid with n", []string{"set", "app.secret", "--generate", "uuid:16"}, "vars_generate_invalid"},
		{"explicit empty spec", []string{"set", "app.secret", "--generate="}, "vars_generate_invalid"},
		{"force alone", []string{"set", "app.secret", "x", "--force"}, "vars_force_requires_generate"},
		{"force alone no value", []string{"set", "app.secret", "--force"}, "vars_force_requires_generate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, root := writeVarsFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			seedRandReader(t)
			forbidForm(t)
			before := localYAML(t, root)

			_, _, err := runVarsCmd(t, flags, tc.args...)
			wantCode(t, err, tc.code)
			if after := localYAML(t, root); after != before {
				t.Errorf("local.yml changed on a usage error\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestVarsSet_GenerateRefusesExistingLocalValue(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	seedRandReader(t)
	before := localYAML(t, root)

	_, _, err := runVarsCmd(t, flags, "set", "db.host", "--generate", "hex")
	wantCode(t, err, "vars_value_exists")
	var ce *cmdctx.CodedError
	errors.As(err, &ce)
	if ce.Details["var"] != "vars.db.host" {
		t.Errorf("detail var: want vars.db.host, got %v", ce.Details["var"])
	}
	if !strings.Contains(ce.Hint, "--force") {
		t.Errorf("hint should name --force, got %q", ce.Hint)
	}
	if after := localYAML(t, root); after != before {
		t.Errorf("local.yml changed on refusal\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestVarsSet_GenerateForceOverwrites(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	seedRandReader(t)

	if _, _, err := runVarsCmd(t, flags, "set", "db.host", "--generate", "hex:2", "--force"); err != nil {
		t.Fatalf("vars set --generate --force: %v", err)
	}
	if got, ok := reloadVar(t, cfgPath, "vars.db.host"); !ok || got != "1234" {
		t.Errorf("forced overwrite: want \"1234\", got %v (%T, ok=%v)", got, got, ok)
	}
}

// TestVarsSet_GenerateLowerLayersDoNotBlock: an explicit null in local.yml
// and a workspace.yml default are not "a local value".
func TestVarsSet_GenerateLowerLayersDoNotBlock(t *testing.T) {
	tests := []struct {
		name  string
		local string
		path  string
	}{
		{"explicit null in local.yml", "vars:\n  app:\n    secret: null\n", "vars.app.secret"},
		{"workspace.yml default", "vars:\n  db:\n    host: override-host\n", "vars.app.name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, root := writeVarsFixture(t)
			if err := os.WriteFile(filepath.Join(root, "workspace", "local.yml"), []byte(tc.local), 0o644); err != nil {
				t.Fatalf("writing local.yml: %v", err)
			}
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			seedRandReader(t)

			if _, _, err := runVarsCmd(t, flags, "set", tc.path, "--generate", "hex:2"); err != nil {
				t.Fatalf("vars set --generate: %v", err)
			}
			if got, ok := reloadVar(t, cfgPath, tc.path); !ok || got != "1234" {
				t.Errorf("%s: want \"1234\", got %v (ok=%v)", tc.path, got, ok)
			}
		})
	}
}

func TestVarsSet_GenerateJSON(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	seedRandReader(t)

	out, errOut, err := runVarsCmd(t, flags, "set", "app.secret", "--generate", "hex:2")
	if err != nil {
		t.Fatalf("vars set --generate --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var data varSetJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal set json: %v\nraw: %s", e, out)
	}
	if data.Var != "vars.app.secret" || data.Value != "1234" {
		t.Errorf("json: want {vars.app.secret, \"1234\"}, got {%s, %v (%T)}", data.Var, data.Value, data.Value)
	}
}

func TestVarsSet_GenerateNonInteractive(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	seedRandReader(t)
	t.Setenv("DWE_NONINTERACTIVE", "1")
	origIsInteractive := widgets.IsInteractiveFn
	widgets.IsInteractiveFn = func(io.Reader) bool { return false }
	defer func() { widgets.IsInteractiveFn = origIsInteractive }()

	if _, _, err := runVarsCmd(t, flags, "set", "app.secret", "--generate", "hex:2"); err != nil {
		t.Fatalf("vars set --generate without a TTY: %v", err)
	}
	if got, ok := reloadVar(t, cfgPath, "vars.app.secret"); !ok || got != "1234" {
		t.Errorf("want \"1234\", got %v (ok=%v)", got, ok)
	}
}

// TestVarsSet_GenerateContainerGate: the container gate runs before the
// exists-check, so a denied var reports the denial even when it already has
// a local value.
func TestVarsSet_GenerateContainerGate(t *testing.T) {
	const workspace = `schema_version: "2"
project:
  name: varstest
  prefix: dwe
bridge:
  vars_writable:
    - vars.db.*
`
	const local = "vars:\n  app:\n    secret: already\n"
	t.Setenv(bridgeclient.EnvInvokedFrom, bridgeclient.InvokedFromContainer)

	tests := []struct {
		name string
		path string
		code string // "" = allowed
	}{
		{"writable", "vars.db.password", ""},
		{"not writable", "vars.app.token", "vars_not_container_writable"},
		{"not writable and already set", "vars.app.secret", "vars_not_container_writable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cfgPath := filepath.Join(root, "workspace.yml")
			if err := os.WriteFile(cfgPath, []byte(workspace), 0o644); err != nil {
				t.Fatalf("writing workspace.yml: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
				t.Fatalf("mkdir workspace: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "workspace", "local.yml"), []byte(local), 0o644); err != nil {
				t.Fatalf("writing local.yml: %v", err)
			}
			seedRandReader(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

			_, _, err := runVarsCmd(t, flags, "set", tc.path, "--generate", "hex")
			if tc.code == "" {
				if err != nil {
					t.Fatalf("expected allowed write, got %v", err)
				}
				got, ok := reloadVar(t, cfgPath, tc.path)
				if s, isStr := got.(string); !ok || !isStr || len(s) != 2*randval.DefaultBytes {
					t.Errorf("%s: want a %d-char hex string, got %v (%T, ok=%v)", tc.path, 2*randval.DefaultBytes, got, got, ok)
				}
				return
			}
			wantCode(t, err, tc.code)
			if after := localYAML(t, root); after != local {
				t.Errorf("local.yml changed on denial\nbefore:\n%s\nafter:\n%s", local, after)
			}
		})
	}
}

// TestVarsSet_GenerateInvalidLocalYAML: a local.yml the exists-check cannot
// parse is a coded config error, never "absent" — so --generate cannot
// overwrite a file it failed to read.
func TestVarsSet_GenerateInvalidLocalYAML(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	const broken = "vars: [\n"
	if err := os.WriteFile(filepath.Join(root, "workspace", "local.yml"), []byte(broken), 0o644); err != nil {
		t.Fatalf("writing local.yml: %v", err)
	}
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	seedRandReader(t)

	_, _, err := runVarsCmd(t, flags, "set", "app.secret", "--generate", "hex")
	wantCode(t, err, "project_invalid_config")
	if after := localYAML(t, root); after != broken {
		t.Errorf("local.yml changed on a config error\nbefore:\n%s\nafter:\n%s", broken, after)
	}
}

// TestCaptureRestoreLocalState exercises the rollback helpers directly: capture
// returns the current bytes (nil when absent), and restore reinstates them —
// crucially, a nil capture (file was absent pre-write) removes the file again so
// a failed write does not leave a partial local.yml behind.
func TestCaptureRestoreLocalState(t *testing.T) {
	t.Run("existing-file round-trips", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "local.yml")
		original := []byte("vars:\n  db:\n    host: orig # keep me\n")
		if err := os.WriteFile(p, original, 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}

		captured, err := captureLocalState(p)
		if err != nil {
			t.Fatalf("captureLocalState: %v", err)
		}
		if string(captured) != string(original) {
			t.Fatalf("captured %q, want %q", captured, original)
		}

		// Simulate a write that clobbered the file, then roll back.
		if err := os.WriteFile(p, []byte("garbage\n"), 0o600); err != nil {
			t.Fatalf("clobber: %v", err)
		}
		restoreLocalState(p, captured)

		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read after restore: %v", err)
		}
		if string(got) != string(original) {
			t.Errorf("restore: got %q, want %q", got, original)
		}
	})

	t.Run("absent-file capture removes on restore", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "local.yml")

		captured, err := captureLocalState(p)
		if err != nil {
			t.Fatalf("captureLocalState(absent): %v", err)
		}
		if captured != nil {
			t.Fatalf("captured = %q, want nil for absent file", captured)
		}

		// Simulate a write that created the file, then roll back to "absent".
		if err := os.WriteFile(p, []byte("created\n"), 0o600); err != nil {
			t.Fatalf("create: %v", err)
		}
		restoreLocalState(p, captured)

		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("restore of nil capture should remove the file, stat err = %v", err)
		}
	})
}
