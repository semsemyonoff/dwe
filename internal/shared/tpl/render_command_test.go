package tpl

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// ---- CompileVarSyntax ----

func TestCompileVarSyntax_noOp(t *testing.T) {
	cases := []string{
		"plain text",
		"no vars here",
		"{{ .Name }}",
	}
	for _, in := range cases {
		got := CompileVarSyntax(in)
		if got != in {
			t.Errorf("CompileVarSyntax(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestCompileVarSyntax_simpleVar(t *testing.T) {
	got := CompileVarSyntax("${name}")
	want := `{{ resolve .Raw "name" }}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCompileVarSyntax_dotPath(t *testing.T) {
	got := CompileVarSyntax("${project.name}")
	want := `{{ resolve .Raw "project.name" }}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCompileVarSyntax_deepDotPath(t *testing.T) {
	got := CompileVarSyntax("${services.app.ports.http}")
	want := `{{ resolve .Raw "services.app.ports.http" }}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCompileVarSyntax_hostUID(t *testing.T) {
	got := CompileVarSyntax("${host.uid}")
	want := "{{ .Host.UID }}"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCompileVarSyntax_hostGID(t *testing.T) {
	got := CompileVarSyntax("${host.gid}")
	want := "{{ .Host.GID }}"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCompileVarSyntax_param(t *testing.T) {
	got := CompileVarSyntax("${param.branch}")
	want := `{{ resolveMap .Params "branch" }}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCompileVarSyntax_context(t *testing.T) {
	got := CompileVarSyntax("${context.container}")
	want := `{{ resolveMap .Context "container" }}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCompileVarSyntax_mixed(t *testing.T) {
	in := "docker exec ${context.container} ${param.cmd}"
	got := CompileVarSyntax(in)
	if !strings.Contains(got, `{{ resolveMap .Context "container" }}`) {
		t.Errorf("missing context replacement in %q", got)
	}
	if !strings.Contains(got, `{{ resolveMap .Params "cmd" }}`) {
		t.Errorf("missing param replacement in %q", got)
	}
}

func TestCompileVarSyntax_goTemplatePreserved(t *testing.T) {
	in := "{{ .Name }} and ${project.name}"
	got := CompileVarSyntax(in)
	if !strings.Contains(got, "{{ .Name }}") {
		t.Errorf("go template expression removed from %q", got)
	}
	if !strings.Contains(got, `{{ resolve .Raw "project.name" }}`) {
		t.Errorf("dollar var not compiled in %q", got)
	}
}

// ---- RenderCommand ----

func TestRenderCommand_plainString(t *testing.T) {
	ctx := &RenderContext{}
	got, err := RenderCommand("hello world", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCommand_rawConfigPath(t *testing.T) {
	ctx := &RenderContext{
		Raw: map[string]any{
			"project": map[string]any{
				"name": "laravel",
			},
		},
	}
	got, err := RenderCommand("${project.name}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "laravel" {
		t.Errorf("got %q, want %q", got, "laravel")
	}
}

func TestRenderCommand_paramResolution(t *testing.T) {
	ctx := &RenderContext{
		Params: map[string]any{
			"branch": "main",
		},
	}
	got, err := RenderCommand("git checkout ${param.branch}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "git checkout main" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCommand_contextResolution(t *testing.T) {
	ctx := &RenderContext{
		Context: map[string]any{
			"container": "app-main",
		},
	}
	got, err := RenderCommand("docker exec ${context.container}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "docker exec app-main" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCommand_hostUID(t *testing.T) {
	ctx := &RenderContext{
		Host: HostInfo{UID: "1001", GID: "1001"},
	}
	got, err := RenderCommand("uid=${host.uid}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "uid=1001" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCommand_hostGID(t *testing.T) {
	ctx := &RenderContext{
		Host: HostInfo{UID: "1000", GID: "1002"},
	}
	got, err := RenderCommand("gid=${host.gid}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "gid=1002" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCommand_missingRawPath(t *testing.T) {
	ctx := &RenderContext{
		Raw: map[string]any{},
	}
	got, err := RenderCommand("${missing.key}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	// missing path resolves to empty string
	if got != "" {
		t.Errorf("got %q, want empty string for missing path", got)
	}
}

func TestRenderCommand_nilParams(t *testing.T) {
	ctx := &RenderContext{}
	got, err := RenderCommand("${param.x}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string for nil params", got)
	}
}

func TestRenderCommand_nilContext(t *testing.T) {
	ctx := &RenderContext{}
	got, err := RenderCommand("${context.x}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string for nil context", got)
	}
}

func TestRenderCommand_invalidGoTemplate(t *testing.T) {
	ctx := &RenderContext{}
	_, err := RenderCommand("{{ .Unclosed", ctx)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestRenderCommand_mixedVarsAndGoTemplate(t *testing.T) {
	ctx := &RenderContext{
		Raw: map[string]any{
			"project": map[string]any{"name": "myapp"},
		},
		Host: HostInfo{UID: "500", GID: "500"},
	}
	expr := "${project.name}-uid-${host.uid}"
	got, err := RenderCommand(expr, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "myapp-uid-500" {
		t.Errorf("got %q", got)
	}
}

// ---- resolveMapPath (internal) ----

func TestResolveMapPath_shallow(t *testing.T) {
	m := map[string]any{"key": "val"}
	got := resolveMapPath(m, "key")
	if got != "val" {
		t.Errorf("got %v", got)
	}
}

func TestResolveMapPath_deep(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": 42,
			},
		},
	}
	got := resolveMapPath(m, "a.b.c")
	if got != 42 {
		t.Errorf("got %v", got)
	}
}

func TestResolveMapPath_missing(t *testing.T) {
	m := map[string]any{"x": "y"}
	got := resolveMapPath(m, "missing")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestResolveMapPath_nilMap(t *testing.T) {
	got := resolveMapPath(nil, "a.b")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

// ---- Sprout template functions (tested in funcs_test.go) ----
// Legacy date, datetime, base, dir tests removed — see Task 2 of plan.
// Sprout functions are tested in funcs_test.go with table-driven approach.

func TestRenderCommand_nowDateExpression(t *testing.T) {
	// Verify that the sprout 'now | date' pipeline works through RenderCommand,
	// covering the command-template path (commandFuncMap inherits time functions).
	got, err := RenderCommand(`backup_{{ now | date "2006-01-02" }}.sql`, nil)
	if err != nil {
		t.Fatal(err)
	}
	pattern := `^backup_\d{4}-\d{2}-\d{2}\.sql$`
	matched, err := regexp.MatchString(pattern, got)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Errorf("RenderCommand date output %q does not match expected pattern %s", got, pattern)
	}
}

func TestRenderCommand_sproutFunctionInheritance(t *testing.T) {
	// Prove that commandFuncMap inherits sprout functions from the base FuncMap.
	// Uses the 'default' function from sprout's std registry.
	ctx := &RenderContext{
		Raw: map[string]any{
			"optional_key": "",
		},
	}
	got, err := RenderCommand(`{{ default "fallback" (resolve .Raw "optional_key") }}`, ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "fallback"
	if got != want {
		t.Errorf("sprout default func = %q, want %q", got, want)
	}
}

// ---- Files namespace ----

func TestCompileVarSyntax_filesPath(t *testing.T) {
	got := CompileVarSyntax("${files.dump.path}")
	want := `{{ resolveFile .Files "dump" "path" }}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCompileVarSyntax_filesPathInContext(t *testing.T) {
	in := "backup at ${files.dump.path}"
	got := CompileVarSyntax(in)
	if !strings.Contains(got, `{{ resolveFile .Files "dump" "path" }}`) {
		t.Errorf("files path not compiled in %q", got)
	}
}

func TestCompileVarSyntax_filesHyphenNotMatched(t *testing.T) {
	// File IDs with hyphens should NOT match (they're not in the grammar)
	// so ${files.foo-bar.path} should be left as a literal string.
	in := "${files.foo-bar.path}"
	got := CompileVarSyntax(in)
	// The varPattern doesn't match identifiers with hyphens, so it returns unchanged
	if got != in {
		t.Errorf("hyphenated file id was matched; got %q, want %q", got, in)
	}
}

func TestRenderCommand_filesResolution(t *testing.T) {
	ctx := &RenderContext{
		Files: map[string]ResolvedFile{
			"dump": {Path: "/tmp/db_2026-04-29.sql.gz"},
		},
	}
	got, err := RenderCommand("${files.dump.path}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/db_2026-04-29.sql.gz" {
		t.Errorf("got %q, want /tmp/db_2026-04-29.sql.gz", got)
	}
}

func TestRenderCommand_filesMissingID(t *testing.T) {
	ctx := &RenderContext{
		Files: map[string]ResolvedFile{},
	}
	got, err := RenderCommand("${files.missing.path}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	// missing file id → empty string
	if got != "" {
		t.Errorf("got %q, want empty string for missing file id", got)
	}
}

func TestRenderCommand_filesUnknownSubkey(t *testing.T) {
	ctx := &RenderContext{
		Files: map[string]ResolvedFile{
			"dump": {Path: "/tmp/db.sql.gz"},
		},
	}
	got, err := RenderCommand("${files.dump.unknown}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	// unknown subkey → empty string
	if got != "" {
		t.Errorf("got %q, want empty string for unknown subkey", got)
	}
}

func TestRenderCommand_filesNilMap(t *testing.T) {
	ctx := &RenderContext{
		Files: nil,
	}
	got, err := RenderCommand("${files.dump.path}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string for nil files", got)
	}
}

func TestRenderCommand_filesMixedVars(t *testing.T) {
	ctx := &RenderContext{
		Files: map[string]ResolvedFile{
			"dump": {Path: "/backup/db.sql.gz"},
		},
		Params: map[string]any{
			"database": "production",
		},
	}
	expr := "Restoring ${param.database} from ${files.dump.path}"
	got, err := RenderCommand(expr, ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "Restoring production from /backup/db.sql.gz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---- EvalCommandCondition ----

func TestEvalCommandCondition_emptyExpr(t *testing.T) {
	ctx := &RenderContext{}
	ok, err := EvalCommandCondition("", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("empty expr should return true")
	}
}

func TestEvalCommandCondition_paramTruthy(t *testing.T) {
	ctx := &RenderContext{
		Params: map[string]any{
			"enabled": "true",
		},
	}
	ok, err := EvalCommandCondition("${param.enabled}", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("${param.enabled}=true should return true")
	}
}

func TestEvalCommandCondition_paramFalsy(t *testing.T) {
	ctx := &RenderContext{
		Params: map[string]any{
			"enabled": "false",
		},
	}
	ok, err := EvalCommandCondition("${param.enabled}", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("${param.enabled}=false should return false")
	}
}

func TestEvalCommandCondition_paramOne(t *testing.T) {
	ctx := &RenderContext{
		Params: map[string]any{
			"count": "1",
		},
	}
	ok, err := EvalCommandCondition("${param.count}", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("${param.count}=1 should return true")
	}
}

func TestEvalCommandCondition_paramZero(t *testing.T) {
	ctx := &RenderContext{
		Params: map[string]any{
			"count": "0",
		},
	}
	ok, err := EvalCommandCondition("${param.count}", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("${param.count}=0 should return false")
	}
}

func TestEvalCommandCondition_paramEmpty(t *testing.T) {
	ctx := &RenderContext{
		Params: map[string]any{
			"optional": "",
		},
	}
	ok, err := EvalCommandCondition("${param.optional}", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("${param.optional}=empty should return false")
	}
}

func TestEvalCommandCondition_builtinDirExists(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := &RenderContext{}
	ok, err := EvalCommandCondition("dir-exists "+tmpDir, ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("dir-exists for existing dir should return true")
	}
}

func TestEvalCommandCondition_builtinDirMissing(t *testing.T) {
	ctx := &RenderContext{}
	ok, err := EvalCommandCondition("dir-missing /nonexistent/path", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("dir-missing for nonexistent path should return true")
	}
}

func TestEvalCommandCondition_builtinFileExistsInTemplate(t *testing.T) {
	tmpFile := t.TempDir() + "/test.txt"
	// Create the temp file
	if err := os.WriteFile(tmpFile, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := &RenderContext{
		Params: map[string]any{
			"path": tmpFile,
		},
	}
	ok, err := EvalCommandCondition("file-exists ${param.path}", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("file-exists for existing file should return true")
	}
}

func TestEvalCommandCondition_cmdTrue(t *testing.T) {
	ctx := &RenderContext{}
	ok, err := EvalCommandCondition("cmd: true", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("cmd: true should return true")
	}
}

func TestEvalCommandCondition_cmdFalse(t *testing.T) {
	ctx := &RenderContext{}
	ok, err := EvalCommandCondition("cmd: false", ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("cmd: false should return false")
	}
}

func TestEvalCommandCondition_renderError(t *testing.T) {
	ctx := &RenderContext{
		Params: nil,
	}
	// Invalid go template
	_, err := EvalCommandCondition("{{ .Unclosed", ctx, "/tmp")
	if err == nil {
		t.Error("expected error for invalid template")
	}
	if !strings.Contains(err.Error(), "eval when") {
		t.Errorf("error should start with 'eval when', got %q", err.Error())
	}
}

func TestEvalCommandCondition_malformedBuiltin(t *testing.T) {
	ctx := &RenderContext{}
	// dir-empty with no path argument
	_, err := EvalCommandCondition("dir-empty", ctx, "/tmp")
	if err == nil {
		t.Error("expected error for malformed builtin")
	}
	if !strings.Contains(err.Error(), "eval when") {
		t.Errorf("error should be wrapped with 'eval when', got %q", err.Error())
	}
}

// TestRenderCommand_ServicesNestedPortsHosts verifies that the generic dot-path
// resolver walks two-level nested maps for the new services.<name>.ports.<port-name>
// and services.<name>.hosts.<host-name> shapes without any tpl-side changes.
//
// This is a plumbing-verification test for the unified-services-schema refactor:
// the producer (injectServicesIntoRaw) hasn't been rewritten yet, but the consumer
// (CompileVarSyntax → resolve → resolveMapPath) already walks nested maps via .Raw
// generically. No tpl changes are required.
func TestRenderCommand_ServicesNestedPortsHosts(t *testing.T) {
	raw := map[string]any{
		"services": map[string]any{
			"foo": map[string]any{
				"ports": map[string]any{
					"http": 8080,
					"grpc": 9090,
				},
				"hosts": map[string]any{
					"main": "foo.local",
				},
			},
		},
	}
	ctx := &RenderContext{Raw: raw}
	cases := []struct {
		expr string
		want string
	}{
		{"${services.foo.ports.http}", "8080"},
		{"${services.foo.ports.grpc}", "9090"},
		{"${services.foo.hosts.main}", "foo.local"},
		{"${services.foo.ports.missing}", ""},
		{"${services.missing.ports.http}", ""},
	}
	for _, tc := range cases {
		got, err := RenderCommand(tc.expr, ctx)
		if err != nil {
			t.Fatalf("RenderCommand(%q): %v", tc.expr, err)
		}
		if got != tc.want {
			t.Errorf("RenderCommand(%q) = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestEvalCommandCondition_typoOnBuiltinVerb(t *testing.T) {
	ctx := &RenderContext{}
	// dir-emty (typo) instead of dir-empty
	_, err := EvalCommandCondition("dir-emty /tmp", ctx, "/tmp")
	if err == nil {
		t.Error("expected error for typo in builtin verb")
	}
	if !strings.Contains(err.Error(), "unknown builtin predicate") {
		t.Errorf("error should mention unknown predicate, got %q", err.Error())
	}
}
