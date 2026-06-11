package vars

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// writeVarsFixture writes a minimal project with a vars: block in workspace.yml
// and a single local.yml override (vars.db.host) so layer attribution is
// exercised. It returns the workspace.yml path and the project root.
func writeVarsFixture(t *testing.T) (cfgPath, root string) {
	t.Helper()
	root = t.TempDir()
	cfgPath = filepath.Join(root, "workspace.yml")
	const workspace = `schema_version: "2"
project:
  name: varstest
  prefix: dwe
vars:
  app:
    name: myapp
  db:
    host: localhost
    port: 5432
`
	if err := os.WriteFile(cfgPath, []byte(workspace), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	wsDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	const local = `vars:
  db:
    host: override-host
`
	if err := os.WriteFile(filepath.Join(wsDir, "local.yml"), []byte(local), 0o644); err != nil {
		t.Fatalf("writing local.yml: %v", err)
	}
	return cfgPath, root
}

// runVarsCmd executes the vars command tree with the given args against the
// fixture, returning stdout, stderr, and the command error. The vars command is
// its own root here (the real cli.NewRootCmd wiring is tested via registration),
// which avoids the cli→vars import cycle while still exercising the full RunE.
func runVarsCmd(t *testing.T, flags *cmdctx.RootFlags, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewCmd("", flags)
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestVarsList_Text(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "list")
	if err != nil {
		t.Fatalf("vars list: %v", err)
	}
	for _, want := range []string{"vars.app.name", "vars.db.host", "vars.db.port", "override-host"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("list output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestVarsList_JSON(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, errOut, err := runVarsCmd(t, flags, "list")
	if err != nil {
		t.Fatalf("vars list --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}

	var data varsListJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal list json: %v\nraw: %s", e, out)
	}
	if len(data.Vars) != 3 {
		t.Fatalf("expected 3 leaves, got %d: %+v", len(data.Vars), data.Vars)
	}
	// Sorted by path: app.name, db.host, db.port.
	if data.Vars[0].Path != "vars.app.name" {
		t.Errorf("first leaf: want vars.app.name, got %q", data.Vars[0].Path)
	}
	// vars.db.host comes from local.yml → layer "local".
	var hostEntry *varListEntryJSON
	for i := range data.Vars {
		if data.Vars[i].Path == "vars.db.host" {
			hostEntry = &data.Vars[i]
		}
	}
	if hostEntry == nil {
		t.Fatal("vars.db.host missing from list")
	}
	if hostEntry.Value != "override-host" {
		t.Errorf("vars.db.host value: want override-host, got %v", hostEntry.Value)
	}
	if hostEntry.Layer != "local" {
		t.Errorf("vars.db.host layer: want local, got %q", hostEntry.Layer)
	}
}

func TestVarsList_NamespaceFilter(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "list", "vars.db")
	if err != nil {
		t.Fatalf("vars list vars.db: %v", err)
	}
	var data varsListJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal: %v", e)
	}
	if len(data.Vars) != 2 {
		t.Fatalf("namespace filter vars.db: want 2 leaves, got %d: %+v", len(data.Vars), data.Vars)
	}
	for _, e := range data.Vars {
		if e.Path != "vars.db.host" && e.Path != "vars.db.port" {
			t.Errorf("unexpected leaf under vars.db filter: %q", e.Path)
		}
	}
}

func TestVarsBare_DispatchesToList(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags)
	if err != nil {
		t.Fatalf("bare vars: %v", err)
	}
	var data varsListJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("bare vars should emit the list JSON shape: %v\nraw: %s", e, out)
	}
	if len(data.Vars) != 3 {
		t.Errorf("bare vars: want 3 leaves, got %d", len(data.Vars))
	}
}

func TestVarsGet_Scalar(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "get", "vars.db.host")
	if err != nil {
		t.Fatalf("vars get: %v", err)
	}
	if out != "override-host\n" {
		t.Errorf("get scalar: want %q, got %q", "override-host\n", out)
	}
}

func TestVarsGet_JSON(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, errOut, err := runVarsCmd(t, flags, "get", "vars.db.port")
	if err != nil {
		t.Fatalf("vars get --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var data varGetJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal get json: %v\nraw: %s", e, out)
	}
	if data.Var != "vars.db.port" {
		t.Errorf("var: want vars.db.port, got %q", data.Var)
	}
	// JSON numbers decode to float64.
	if data.Value != float64(5432) {
		t.Errorf("value: want 5432, got %v (%T)", data.Value, data.Value)
	}
}

func TestVarsGet_Subtree(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "get", "vars.db")
	if err != nil {
		t.Fatalf("vars get subtree: %v", err)
	}
	// A namespace path prints the subtree as YAML.
	for _, want := range []string{"host: override-host", "port: 5432"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("subtree output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestVarsGet_NotFound(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	_, _, err := runVarsCmd(t, flags, "get", "vars.does.not.exist")
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	ce, ok := err.(*cmdctx.CodedError)
	if !ok {
		t.Fatalf("error is not *CodedError: %T (%v)", err, err)
	}
	if ce.Code != "vars_not_found" {
		t.Errorf("error code: want vars_not_found, got %q", ce.Code)
	}
	if got := cmdctx.ExitCodeFor(err); got != 1 {
		t.Errorf("exit code: want 1, got %d", got)
	}
}

func TestVarsGet_JSONStdoutClean(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "get", "vars.app.name")
	if err != nil {
		t.Fatalf("vars get: %v", err)
	}
	// stdout must be exactly one JSON object and nothing else.
	if !json.Valid([]byte(out)) {
		t.Errorf("stdout is not valid JSON: %q", out)
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(out)))
	var first varGetJSON
	if e := dec.Decode(&first); e != nil {
		t.Fatalf("decode: %v", e)
	}
	if dec.More() {
		t.Error("stdout contains trailing content after the JSON object")
	}
}

func TestLeafCompletion_ReturnsLeaves(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	fn := leafCompletion(flags)
	got, directive := fn(&cobra.Command{}, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive: want NoFileComp, got %v", directive)
	}
	want := map[string]bool{"vars.app.name": false, "vars.db.host": false, "vars.db.port": false}
	for _, c := range got {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for leaf, seen := range want {
		if !seen {
			t.Errorf("completion missing leaf %q (got %v)", leaf, got)
		}
	}
}

func TestLeafCompletion_NoSecondArg(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
	fn := leafCompletion(flags)
	got, directive := fn(&cobra.Command{}, []string{"vars.x"}, "")
	if len(got) != 0 {
		t.Errorf("expected no completions when an arg is present, got %v", got)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive: want NoFileComp, got %v", directive)
	}
}

func TestNamespaceCandidates(t *testing.T) {
	leaves := []string{"vars.app.name", "vars.db.host", "vars.db.port"}
	got := namespaceCandidates(leaves)
	want := map[string]bool{
		"vars.app":      false,
		"vars.app.name": false,
		"vars.db":       false,
		"vars.db.host":  false,
		"vars.db.port":  false,
	}
	for _, c := range got {
		if _, ok := want[c]; !ok {
			t.Errorf("unexpected candidate %q", c)
			continue
		}
		want[c] = true
	}
	for cand, seen := range want {
		if !seen {
			t.Errorf("missing candidate %q (got %v)", cand, got)
		}
	}
}

func TestNamespaceContains(t *testing.T) {
	tests := []struct {
		path, ns string
		want     bool
	}{
		{"vars.db.host", "", true},
		{"vars.db.host", "vars.db", true},
		{"vars.db", "vars.db", true},
		{"vars.dbx.host", "vars.db", false},
		{"vars.app.name", "vars.db", false},
	}
	for _, tc := range tests {
		if got := namespaceContains(tc.path, tc.ns); got != tc.want {
			t.Errorf("namespaceContains(%q, %q) = %v, want %v", tc.path, tc.ns, got, tc.want)
		}
	}
}
