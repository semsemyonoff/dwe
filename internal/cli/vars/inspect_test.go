package vars

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
)

// writeUsageFixture extends writeVarsFixture with a service whose deploy.yml
// references vars.db.host via ${...}, so inspect's usage scan has a hit.
func writeUsageFixture(t *testing.T) (cfgPath, root string) {
	t.Helper()
	cfgPath, root = writeVarsFixture(t)
	svcDir := filepath.Join(root, "workspace", "services", "api")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("creating service dir: %v", err)
	}
	const deploy = `phases:
  - name: connect
    cmd: "psql -h ${vars.db.host} -p ${vars.db.port}"
`
	if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte(deploy), 0o644); err != nil {
		t.Fatalf("writing deploy.yml: %v", err)
	}
	return cfgPath, root
}

func TestVarsInspect_Text_LocalOverride(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "inspect", "vars.db.host")
	if err != nil {
		t.Fatalf("vars inspect: %v", err)
	}
	// Author layer = localhost, local override = override-host, effective = override-host.
	for _, want := range []string{"vars.db.host", "localhost", "override-host", "local.yml"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("inspect output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestVarsInspect_JSON_LocalOverride(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, errOut, err := runVarsCmd(t, flags, "inspect", "vars.db.host")
	if err != nil {
		t.Fatalf("vars inspect --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var data varInspectJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal inspect json: %v\nraw: %s", e, out)
	}
	if data.Var != "vars.db.host" {
		t.Errorf("var: want vars.db.host, got %q", data.Var)
	}
	if data.Layers.Author != "localhost" || !data.Layers.AuthorSet {
		t.Errorf("author layer: want localhost set, got %v (set=%v)", data.Layers.Author, data.Layers.AuthorSet)
	}
	if data.Layers.Local != "override-host" || !data.Layers.LocalSet {
		t.Errorf("local layer: want override-host set, got %v (set=%v)", data.Layers.Local, data.Layers.LocalSet)
	}
	if data.Layers.Effective != "override-host" || !data.Layers.EffectiveSet {
		t.Errorf("effective layer: want override-host set, got %v (set=%v)", data.Layers.Effective, data.Layers.EffectiveSet)
	}
	if filepath.ToSlash(data.Origin) != "workspace/local.yml" {
		t.Errorf("origin: want workspace/local.yml, got %q", data.Origin)
	}
}

func TestVarsInspect_AuthorOnly_NoLocal(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	// vars.app.name has no local override.
	out, _, err := runVarsCmd(t, flags, "inspect", "vars.app.name")
	if err != nil {
		t.Fatalf("vars inspect vars.app.name: %v", err)
	}
	var data varInspectJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", e, out)
	}
	if !data.Layers.AuthorSet || data.Layers.Author != "myapp" {
		t.Errorf("author: want myapp set, got %v (set=%v)", data.Layers.Author, data.Layers.AuthorSet)
	}
	if data.Layers.LocalSet {
		t.Errorf("local layer should be unset for vars.app.name, got %v", data.Layers.Local)
	}
	if filepath.ToSlash(data.Origin) != "workspace.yml" {
		t.Errorf("origin: want workspace.yml, got %q", data.Origin)
	}
	if len(data.Usages) != 0 {
		t.Errorf("expected no usages for vars.app.name, got %+v", data.Usages)
	}
}

func TestVarsInspect_WithUsages(t *testing.T) {
	cfgPath, root := writeUsageFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "inspect", "vars.db.host")
	if err != nil {
		t.Fatalf("vars inspect with usages: %v", err)
	}
	var data varInspectJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", e, out)
	}
	if len(data.Usages) != 1 {
		t.Fatalf("want 1 usage, got %d: %+v", len(data.Usages), data.Usages)
	}
	u := data.Usages[0]
	if filepath.ToSlash(u.File) != "workspace/services/api/deploy.yml" {
		t.Errorf("usage file: want workspace/services/api/deploy.yml, got %q", u.File)
	}
	if u.Kind != "template" {
		t.Errorf("usage kind: want template, got %q", u.Kind)
	}
	if !bytes.Contains([]byte(u.Text), []byte("${vars.db.host}")) {
		t.Errorf("usage text should contain the reference, got %q", u.Text)
	}
}

func TestVarsInspect_WithUsages_Text(t *testing.T) {
	cfgPath, root := writeUsageFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "inspect", "vars.db.host")
	if err != nil {
		t.Fatalf("vars inspect text with usages: %v", err)
	}
	for _, want := range []string{"workspace/services/api/deploy.yml", "Usages"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("inspect text missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestVarsInspect_NamespacePrefixMatch(t *testing.T) {
	// A query of vars.db should match the usage of vars.db.host (namespace
	// prefix), exercising refMatches via the inspect path.
	cfgPath, root := writeUsageFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "inspect", "vars.db")
	if err != nil {
		t.Fatalf("vars inspect vars.db: %v", err)
	}
	var data varInspectJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal: %v", e)
	}
	// vars.db.host and vars.db.port are both referenced in deploy.yml on the
	// same line → one usage row after dedupe (same file/line/kind/text).
	if len(data.Usages) == 0 {
		t.Errorf("namespace query should match nested usages, got none")
	}
}

func TestVarsInspect_NotFound(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	_, _, err := runVarsCmd(t, flags, "inspect", "vars.nope.missing")
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

func TestVarsInspect_JSONStdoutClean(t *testing.T) {
	cfgPath, root := writeUsageFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "inspect", "vars.db.host")
	if err != nil {
		t.Fatalf("vars inspect: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(out)))
	var first varInspectJSON
	if e := dec.Decode(&first); e != nil {
		t.Fatalf("decode: %v", e)
	}
	if dec.More() {
		t.Error("stdout contains trailing content after the JSON object")
	}
}
