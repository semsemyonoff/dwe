package tpl

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

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

// A head-only ${...} is a shell variable that happens to be spelled like a
// namespace, not a reference: every namespace form in the authoring contract is
// dotted. Rewriting it resolved a whole config sub-map into the command text
// (or erased it to "" when no root key matched) — silently, and now that
// pipeline cmd: strings render, in commands that never opted into templating.
// ${args} is the one bare form the contract defines and keeps its rewrite.
func TestCompileVarSyntax_headOnlyIsLiteral(t *testing.T) {
	for _, in := range []string{
		"${project}",
		"${services}",
		"${vars}",
		"${host}",
		"${files}",
		"${param}",
		"${update}",
		"for f in ${files}; do echo $f; done",
	} {
		if got := CompileVarSyntax(in); got != in {
			t.Errorf("CompileVarSyntax(%q) = %q, want unchanged", in, got)
		}
	}
}

// The head-only rule must not leak into a dotted reference sharing the string.
func TestCompileVarSyntax_headOnlyBesideDotPath(t *testing.T) {
	got := CompileVarSyntax("curl http://${host}:${services.app.ports.http}/")
	want := `curl http://${host}:{{ resolve .Raw "services.app.ports.http" }}/`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIsVarNamespaceRef(t *testing.T) {
	cases := map[string]bool{
		"vars.db.host": true,
		"project.name": true,
		"host.uid":     true,
		"args":         true, // the one bare form the contract defines
		"args.0":       true,
		"project":      false,
		"files":        false,
		"host":         false,
		"HOME":         false,
		"CONTAINER":    false,
		"__configPath": false,
	}
	for inner, want := range cases {
		if got := IsVarNamespaceRef(inner); got != want {
			t.Errorf("IsVarNamespaceRef(%q) = %v, want %v", inner, got, want)
		}
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

// ---- Unknown head whitelist ----

func TestCompileVarSyntax_knownHeadsCompile(t *testing.T) {
	cases := map[string]string{
		"${vars.x}":                  `{{ resolve .Raw "vars.x" }}`,
		"${services.app.ports.http}": `{{ resolve .Raw "services.app.ports.http" }}`,
		"${project.name}":            `{{ resolve .Raw "project.name" }}`,
		// secrets: is a formalized root key, so it resolves from Raw like any
		// other — harmless (the recipient is public) but it must not survive
		// as a literal, which would silently print "${secrets.recipient}".
		"${secrets.recipient}": `{{ resolve .Raw "secrets.recipient" }}`,
	}
	for in, want := range cases {
		if got := CompileVarSyntax(in); got != want {
			t.Errorf("CompileVarSyntax(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompileVarSyntax_unknownHeadsSurviveLiteral(t *testing.T) {
	cases := []string{"${HOME}", "${PATH}", "${UNKNOWN_THING}"}
	for _, in := range cases {
		if got := CompileVarSyntax(in); got != in {
			t.Errorf("CompileVarSyntax(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestCompileVarSyntax_unknownDottedHeadSurvivesLiteral(t *testing.T) {
	in := "${FOO.bar}"
	if got := CompileVarSyntax(in); got != in {
		t.Errorf("CompileVarSyntax(%q) = %q, want unchanged", in, got)
	}
}

// TestCompileVarSyntax_configPathExcluded pins the decision that
// __configPath (an internal key the config loader injects, not part of the
// authoring contract) is deliberately NOT in KnownVarHeads — a reference to
// it renders as a literal rather than leaking loader internals.
func TestCompileVarSyntax_configPathExcluded(t *testing.T) {
	in := "${__configPath}"
	if got := CompileVarSyntax(in); got != in {
		t.Errorf("CompileVarSyntax(%q) = %q, want unchanged (excluded from KnownVarHeads)", in, got)
	}
}

// TestRenderCommand_knownHeadUnknownSubkey pins that a KNOWN head with an
// unresolvable sub-key still resolves to "" (lenient, like every other
// ${...} resolver) rather than surviving literal — only the HEAD gates
// whitelisting, not the full path.
func TestRenderCommand_knownHeadUnknownSubkey(t *testing.T) {
	cases := []string{"${host.bogus}", "${args.0}"}
	for _, expr := range cases {
		got, err := RenderCommand(expr, &RenderContext{})
		if err != nil {
			t.Fatalf("RenderCommand(%q): %v", expr, err)
		}
		if got != "" {
			t.Errorf("RenderCommand(%q) = %q, want empty string", expr, got)
		}
	}
}

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
	got, err := RenderCommand("${vars.missing.key}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	// known head, missing path resolves to empty string
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

// A key that is PRESENT but holds nil is the shape resolve.Context produces for
// a declared, non-required context whose from: does not resolve. Without the
// guard text/template prints a nil interface as the literal "<no value>", so
// `docker exec ${context.container}` ran against a container named <no value>.
func TestRenderCommand_presentNilValuesRenderEmpty(t *testing.T) {
	cases := []struct {
		name string
		ctx  *RenderContext
		expr string
		want string
	}{
		{
			name: "context",
			ctx:  &RenderContext{Context: map[string]any{"container": nil}},
			expr: "docker exec ${context.container}",
			want: "docker exec ",
		},
		{
			name: "param",
			ctx:  &RenderContext{Params: map[string]any{"branch": nil}},
			expr: "git checkout ${param.branch}",
			want: "git checkout ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderCommand(tc.expr, tc.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
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

// ---- Sprout template functions (table-driven coverage in funcs_test.go) ----

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
// resolver walks two-level nested maps for the services.<name>.ports.<port-name>
// and services.<name>.hosts.<host-name> shapes — the resolver is shape-agnostic,
// so the nesting needs no tpl-side special case.
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

func TestCompileVarSyntax_generated(t *testing.T) {
	got := CompileVarSyntax("${generated.app_key}")
	want := `{{ resolveGenerated .Generated "app_key" }}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCompileVarSyntax_generatedInContext(t *testing.T) {
	in := "APP_KEY=${generated.app_key}"
	got := CompileVarSyntax(in)
	if !strings.Contains(got, `{{ resolveGenerated .Generated "app_key" }}`) {
		t.Errorf("generated namespace not compiled in %q", got)
	}
}

func TestRenderCommand_generatedResolution(t *testing.T) {
	ctx := &RenderContext{
		Generated: map[string]string{
			"app_key": "base64:Xa3==",
		},
	}
	got, err := RenderCommand("APP_KEY=${generated.app_key}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "APP_KEY=base64:Xa3==" {
		t.Errorf("got %q, want APP_KEY=base64:Xa3==", got)
	}
}

func TestRenderCommand_generatedMissingKey(t *testing.T) {
	ctx := &RenderContext{
		Generated: map[string]string{},
	}
	got, err := RenderCommand("${generated.app_key}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	// absent key → empty string (lenient, consistent with .Raw resolvers)
	if got != "" {
		t.Errorf("got %q, want empty string for absent generated key", got)
	}
}

func TestRenderCommand_generatedNilMap(t *testing.T) {
	ctx := &RenderContext{}
	got, err := RenderCommand("${generated.app_key}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string for nil generated map", got)
	}
}

// TestRenderCommand_servicesInjectedSubset documents that only the curated
// subset injected by injectServicesIntoRaw resolves via ${services.<name>...};
// an uninjected/omitted field renders "" (lenient).
func TestRenderCommand_servicesInjectedSubset(t *testing.T) {
	ctx := &RenderContext{
		Raw: map[string]any{
			"services": map[string]any{
				"main": map[string]any{
					"container": "app-main",
					"ports": map[string]any{
						"http": 8080,
					},
				},
			},
		},
	}
	cases := []struct {
		expr string
		want string
	}{
		{"${services.main.container}", "app-main"},
		{"${services.main.ports.http}", "8080"},
		// "render"/"generated"/arbitrary fields are NOT injected → ""
		{"${services.main.render.config.template}", ""},
		{"${services.main.generated.app_key}", ""},
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

// TestRenderCommand_generatedCoexistsWithNamespaces verifies the generated
// namespace works alongside the other ${...} namespaces in one template.
func TestRenderCommand_generatedCoexistsWithNamespaces(t *testing.T) {
	ctx := &RenderContext{
		Raw: map[string]any{
			"vars": map[string]any{"databases": map[string]any{"magento": "magentodb"}},
		},
		Generated: map[string]string{"crypt_key": "241f4fa6"},
		Host:      HostInfo{UID: "1000", GID: "1000"},
	}
	expr := "db=${vars.databases.magento} key=${generated.crypt_key} uid=${host.uid}"
	got, err := RenderCommand(expr, ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "db=magentodb key=241f4fa6 uid=1000"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
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

func TestValidateRawScope(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{"param", "git checkout ${param.branch}", true},
		{"context", "echo ${context.env}", true},
		{"files", "cat ${files.dump.path}", true},
		{"generated", "echo ${generated.app_key}", true},
		{"args", "go test ${args}", true},
		{"raw head", "clone ${vars.source.repo} ${project.name}", false},
		{"host", "chown ${host.uid}:${host.gid} .", false},
		{"unknown head", "echo ${HOME}", false},
		// Head-only tokens are shell variables, not references — a deploy step
		// iterating over a `files` shell var must not be rejected as a use of
		// the files namespace. ${args} is the contract's one bare form and stays
		// rejected: it genuinely has no source on this path.
		{"head-only files", "for f in ${files}; do echo $f; done", false},
		{"head-only param", "echo ${param}", false},
		{"head-only generated", "echo ${generated}", false},
		{"snapshot stays validateSnapshotScope's", "tar ${snapshot.path}", false},
		{"no reference", "docker inspect -f '{{.State.Status}}' app", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRawScope(tc.expr)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateRawScope(%q) = %v, wantErr %v", tc.expr, err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "has no source here") {
				t.Errorf("error should explain the namespace is unavailable, got %q", err.Error())
			}
		})
	}
}
