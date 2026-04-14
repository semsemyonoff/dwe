package tpl

import (
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
	got := CompileVarSyntax("${runtime.ports.app}")
	want := `{{ resolve .Raw "runtime.ports.app" }}`
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
