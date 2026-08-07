package scaffold

import (
	"strings"
	"testing"
	"testing/fstest"
)

func newTestOptions() Options {
	return Options{
		Name:    "myproj",
		Prefix:  "dwe",
		Service: "app",
		Branding: Branding{
			Title:   "My Project",
			Tagline: "ship it",
			Accent:  "#ff8800",
		},
	}
}

func TestRenderPlanFS_RendersTmpl(t *testing.T) {
	fsys := fstest.MapFS{
		"workspace.yml.tmpl": {Data: []byte("name: [[ .Name ]]\nprefix: [[ .Prefix ]]\n")},
	}

	plan, err := renderPlanFS(fsys, newTestOptions())
	if err != nil {
		t.Fatalf("renderPlanFS: %v", err)
	}

	got, ok := plan["workspace.yml"]
	if !ok {
		t.Fatalf("expected output path workspace.yml, got keys: %v", keys(plan))
	}
	want := "name: myproj\nprefix: dwe\n"
	if string(got) != want {
		t.Fatalf("rendered template mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderPlanFS_RendersNestedBranding(t *testing.T) {
	fsys := fstest.MapFS{
		"workspace/styles.yml.tmpl": {Data: []byte("accent: [[ .Branding.Accent ]]\n")},
	}

	plan, err := renderPlanFS(fsys, newTestOptions())
	if err != nil {
		t.Fatalf("renderPlanFS: %v", err)
	}
	got := string(plan["workspace/styles.yml"])
	if got != "accent: #ff8800\n" {
		t.Fatalf("nested branding mismatch: %q", got)
	}
}

func TestRenderPlanFS_VerbatimKeepsLiteralBraces(t *testing.T) {
	// A non-.tmpl file containing literal Go-template syntax must be copied
	// unchanged — it is NOT rendered.
	literal := "url: {{ .Project.Name }}.localhost\n"
	fsys := fstest.MapFS{
		"workspace/info.yml": {Data: []byte(literal)},
	}

	plan, err := renderPlanFS(fsys, newTestOptions())
	if err != nil {
		t.Fatalf("renderPlanFS: %v", err)
	}
	got := string(plan["workspace/info.yml"])
	if got != literal {
		t.Fatalf("verbatim file was modified:\n got: %q\nwant: %q", got, literal)
	}
}

func TestRenderPlanFS_ParseError(t *testing.T) {
	fsys := fstest.MapFS{
		"bad.yml.tmpl": {Data: []byte("name: [[ .Name ")}, // unterminated action
	}
	if _, err := renderPlanFS(fsys, newTestOptions()); err == nil {
		t.Fatal("expected parse error for malformed template, got nil")
	}
}

func TestRenderPlanFS_UnknownKey(t *testing.T) {
	fsys := fstest.MapFS{
		"bad.yml.tmpl": {Data: []byte("x: [[ .Nope ]]")},
	}
	_, err := renderPlanFS(fsys, newTestOptions())
	if err == nil {
		t.Fatal("expected error for unknown template key, got nil")
	}
	if !strings.Contains(err.Error(), "render template") {
		t.Fatalf("expected render error, got: %v", err)
	}
}

func TestMapEmbedPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"dot-gitignore.tmpl", ".gitignore"},
		{"dot-editorconfig", ".editorconfig"},
		{"dot-dwe/config", ".dwe/config"},
		{"workspace.yml.tmpl", "workspace.yml"},
		{"workspace/services/app/service.yml.tmpl", "workspace/services/app/service.yml"},
		{"compose.yaml", "compose.yaml"},
	}
	for _, c := range cases {
		if got := mapEmbedPath(c.in); got != c.want {
			t.Errorf("mapEmbedPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderPlan_EmbeddedFSLoads(t *testing.T) {
	// Smoke test: the real embedded FS walks and renders without error for
	// representative Options. Content assertions live in templates_content_test.go.
	plan, err := renderPlan(newTestOptions())
	if err != nil {
		t.Fatalf("renderPlan on embedded FS: %v", err)
	}
	if len(plan) == 0 {
		t.Fatal("expected embedded plan to contain files")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
