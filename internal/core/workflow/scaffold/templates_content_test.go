package scaffold

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// inertFiles are the shipped-commented override mirrors. Their built-in defaults
// stay active, so every line must be a comment or blank — an accidental active
// key would silently replace a whole built-in pipeline.
var inertFiles = []string{
	"workspace/deploy.yml",
	"workspace/lifecycle.yml",
	"workspace/info.yml",
	"workspace/docker.yml",
}

// yamlOutputs are the rendered output paths that must parse as YAML.
var yamlOutputs = []string{
	"workspace.yml",
	"compose.yaml",
	"workspace/defaults.yml",
	"workspace/styles.yml",
	"workspace/deploy.yml",
	"workspace/lifecycle.yml",
	"workspace/info.yml",
	"workspace/docker.yml",
	"workspace/services/app/service.yml",
}

func TestEmbeddedTemplates_RenderForRepresentativeOptions(t *testing.T) {
	// Both a fully-branded project and a minimal one (no branding, no service)
	// must render every embedded template without error.
	for _, opts := range []Options{newTestOptions(), {Name: "bare", Prefix: "dwe"}} {
		plan, err := renderPlan(opts)
		if err != nil {
			t.Fatalf("renderPlan(%+v): %v", opts, err)
		}
		for _, want := range yamlOutputs {
			if _, ok := plan[want]; !ok {
				t.Errorf("opts %+v: expected output path %q in plan; keys: %v", opts, want, keys(plan))
			}
		}
	}
}

func TestEmbeddedTemplates_YAMLParses(t *testing.T) {
	for _, opts := range []Options{newTestOptions(), {Name: "bare", Prefix: "dwe"}} {
		plan, err := renderPlan(opts)
		if err != nil {
			t.Fatalf("renderPlan: %v", err)
		}
		for _, path := range yamlOutputs {
			data, ok := plan[path]
			if !ok {
				t.Errorf("opts %+v: missing %q", opts, path)
				continue
			}
			var out any
			if err := yaml.Unmarshal(data, &out); err != nil {
				t.Errorf("opts %+v: %q is not valid YAML: %v\n---\n%s", opts, path, err, data)
			}
		}
	}
}

func TestEmbeddedTemplates_InertFilesAreAllComments(t *testing.T) {
	plan, err := renderPlan(newTestOptions())
	if err != nil {
		t.Fatalf("renderPlan: %v", err)
	}
	for _, path := range inertFiles {
		data, ok := plan[path]
		if !ok {
			t.Errorf("missing inert file %q", path)
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			t.Errorf("%s:%d is an active (non-comment) line: %q", path, i+1, line)
		}
		// An all-comment file unmarshals to nil — the strongest "inert" proof.
		var out any
		if err := yaml.Unmarshal(data, &out); err != nil {
			t.Errorf("inert %q failed to parse: %v", path, err)
		} else if out != nil {
			t.Errorf("inert %q parsed to a non-nil value %#v; expected no active keys", path, out)
		}
	}
}

// bodyKeyLine matches a de-commented top-level YAML key line: lowercase,
// snake_case, no leading whitespace, immediately followed by ':'. Real config
// keys in the inert mirrors are all lowercase (run, log, phases, sections,
// project_name, ...); the prose header above them is sentence-cased prose, so
// this reliably locates where the commented-out YAML body begins without
// hardcoding a per-file line offset.
var bodyKeyLine = regexp.MustCompile(`^[a-z][a-z0-9_-]*:(\s|$)`)

// uncommentInertBody finds the trailing commented-out YAML block in an inert
// scaffold mirror and strips its "# " comment prefix, leaving the prose
// header above it untouched. It simulates exactly what the file's own
// "uncomment to override" instruction tells the user to do.
func uncommentInertBody(t *testing.T, data []byte) []byte {
	t.Helper()
	lines := strings.Split(string(data), "\n")
	start := -1
	for i, line := range lines {
		rest, isComment := strings.CutPrefix(line, "#")
		if !isComment {
			continue
		}
		rest = strings.TrimPrefix(rest, " ")
		if bodyKeyLine.MatchString(rest) {
			start = i
			break
		}
	}
	if start == -1 {
		t.Fatalf("could not locate a top-level YAML key in inert file:\n%s", data)
	}
	for i := start; i < len(lines); i++ {
		rest, isComment := strings.CutPrefix(lines[i], "#")
		if !isComment {
			continue
		}
		lines[i] = strings.TrimPrefix(rest, " ")
	}
	return []byte(strings.Join(lines, "\n"))
}

// TestEmbeddedTemplates_InertBodyUncommentsCleanly proves the "uncomment to
// override" instruction each inert mirror carries actually works: taking the
// commented-out YAML body literally and stripping only the comment markers
// must load through the same strict decoder the real file goes through. This
// is the regression guard for the class of defect fixed in task 11 (a
// commented example referencing a field the schema no longer has).
func TestEmbeddedTemplates_InertBodyUncommentsCleanly(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		load func(t *testing.T, dir string)
	}{
		{
			name: "lifecycle.yml",
			rel:  filepath.Join("workspace", "lifecycle.yml"),
			load: func(t *testing.T, dir string) {
				if _, err := config.LoadLifecycleConfig(filepath.Join(dir, "workspace", "lifecycle.yml")); err != nil {
					t.Errorf("LoadLifecycleConfig: %v", err)
				}
			},
		},
		{
			name: "deploy.yml",
			rel:  filepath.Join("workspace", "deploy.yml"),
			load: func(t *testing.T, dir string) {
				if _, err := config.LoadProjectDeployConfig(filepath.Join(dir, "workspace", "deploy.yml")); err != nil {
					t.Errorf("LoadProjectDeployConfig: %v", err)
				}
			},
		},
		{
			name: "info.yml",
			rel:  filepath.Join("workspace", "info.yml"),
			load: func(t *testing.T, dir string) {
				if _, err := config.LoadInfoConfig(filepath.Join(dir, "workspace", "info.yml")); err != nil {
					t.Errorf("LoadInfoConfig: %v", err)
				}
			},
		},
		{
			name: "docker.yml",
			rel:  filepath.Join("workspace", "docker.yml"),
			load: func(t *testing.T, dir string) {
				cfg, err := config.LoadConfig(filepath.Join(dir, "workspace.yml"))
				if err != nil {
					t.Fatalf("LoadConfig: %v", err)
				}
				if _, err := config.LoadDockerConfig(dir, cfg); err != nil {
					t.Errorf("LoadDockerConfig: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Scaffold(defaultValidityOptions(dir)); err != nil {
				t.Fatalf("Scaffold: %v", err)
			}
			path := filepath.Join(dir, tc.rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if err := os.WriteFile(path, uncommentInertBody(t, data), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			tc.load(t, dir)
		})
	}
}

func TestEmbeddedTemplates_StylesRendersNestedBranding(t *testing.T) {
	opts := newTestOptions()
	plan, err := renderPlan(opts)
	if err != nil {
		t.Fatalf("renderPlan: %v", err)
	}
	var styles struct {
		Header struct {
			Lines   []string `yaml:"lines"`
			Tagline string   `yaml:"tagline"`
		} `yaml:"header"`
		Colors struct {
			Accent string `yaml:"accent"`
		} `yaml:"colors"`
	}
	if err := yaml.Unmarshal(plan["workspace/styles.yml"], &styles); err != nil {
		t.Fatalf("parse styles.yml: %v", err)
	}
	// Branding must render the NESTED schema (no flat title:), or LoadStylesConfig
	// would silently drop it.
	if len(styles.Header.Lines) != 1 || styles.Header.Lines[0] != opts.Branding.Title {
		t.Errorf("header.lines = %v, want [%q]", styles.Header.Lines, opts.Branding.Title)
	}
	if styles.Header.Tagline != opts.Branding.Tagline {
		t.Errorf("header.tagline = %q, want %q", styles.Header.Tagline, opts.Branding.Tagline)
	}
	if styles.Colors.Accent != opts.Branding.Accent {
		t.Errorf("colors.accent = %q, want %q", styles.Colors.Accent, opts.Branding.Accent)
	}
	if strings.Contains(string(plan["workspace/styles.yml"]), "\ntitle:") {
		t.Error("styles.yml contains a flat top-level title: key, which does not exist in the schema")
	}
}

func TestEmbeddedTemplates_ServiceTogglePresentOnlyWhenNamed(t *testing.T) {
	with := string(mustRender(t, newTestOptions())["workspace/defaults.yml"])
	if !strings.Contains(with, "services:\n  \"app\":\n    enabled: true") {
		t.Errorf("defaults.yml with Service=app missing active toggle:\n%s", with)
	}

	without := string(mustRender(t, Options{Name: "bare", Prefix: "dwe"})["workspace/defaults.yml"])
	var parsed struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(without), &parsed); err != nil {
		t.Fatalf("parse defaults.yml: %v", err)
	}
	if parsed.Services != nil {
		t.Errorf("defaults.yml with no service should have no active services overlay, got %v", parsed.Services)
	}
}

// TestEmbeddedTemplates_StarterServiceActiveKeys pins the class-1 set the
// starter service ships active: the identity pair, the hub triplet (identical
// across every surveyed app service), and the two display fields every service
// fills anyway. Everything else stays a commented example.
//
// `ports` in particular MUST stay commented: the scaffolded compose.yaml has no
// services block, so an active port binds nothing, and it would make
// `dwe validate` depend on whether that host port happens to be busy
// (portsFreeValidator emits SeverityError), turning the deterministic
// scaffold-validates-clean guard into a host-dependent one.
func TestEmbeddedTemplates_StarterServiceActiveKeys(t *testing.T) {
	svc := mustRender(t, newTestOptions())["workspace/services/app/service.yml"]
	var parsed map[string]any
	if err := yaml.Unmarshal(svc, &parsed); err != nil {
		t.Fatalf("parse service.yml: %v", err)
	}
	want := map[string]any{
		"type":              "app",
		"container":         "app",
		"dir":               "./services/app",
		"dir_internal":      "/workspace",
		"work_dir_internal": "/workspace/src",
		"icon":              "📦",
		"info":              map[string]any{"title": "app"},
	}
	for key, wantVal := range want {
		got, ok := parsed[key]
		if !ok {
			t.Errorf("service.yml missing active key %q", key)
			continue
		}
		if gotMap, isMap := got.(map[string]any); isMap {
			wantMap, _ := wantVal.(map[string]any)
			if len(gotMap) != len(wantMap) {
				t.Errorf("service.yml %q = %v, want %v", key, got, wantVal)
				continue
			}
			for k, v := range wantMap {
				if gotMap[k] != v {
					t.Errorf("service.yml %s.%s = %v, want %v", key, k, gotMap[k], v)
				}
			}
			continue
		}
		if got != wantVal {
			t.Errorf("service.yml %q = %v, want %v", key, got, wantVal)
		}
	}
	for key := range parsed {
		if _, ok := want[key]; !ok {
			t.Errorf("service.yml has unexpected active key %q = %v", key, parsed[key])
		}
	}
	for _, commented := range []string{"ports", "hosts", "required", "depends_on"} {
		if _, ok := parsed[commented]; ok {
			t.Errorf("service.yml key %q is active; it must stay a commented example", commented)
		}
	}
}

// TestEmbeddedTemplates_PortPairIsDocumentedOnBothSides guards the one class-1
// rule the scaffold can only teach in prose: a port is display-only until a
// matching exports.env rule exports it. Both halves ship commented, so the
// pairing has to be stated in each file or the reader sees only one side.
func TestEmbeddedTemplates_PortPairIsDocumentedOnBothSides(t *testing.T) {
	plan := mustRender(t, newTestOptions())
	svc := string(plan["workspace/services/app/service.yml"])
	if !strings.Contains(svc, "exports.env") {
		t.Errorf("service.yml does not mention the paired exports.env rule:\n%s", svc)
	}
	defaults := string(plan["workspace/defaults.yml"])
	if !strings.Contains(defaults, "display-only") {
		t.Errorf("defaults.yml does not state the display-only rule:\n%s", defaults)
	}
	// The reserved auto-injected names are a documented trap (they are rejected
	// as user rule names), and exports.env is the only place they surface.
	for _, name := range config.ReservedExportNames {
		if !strings.Contains(defaults, name) {
			t.Errorf("defaults.yml does not name the reserved export %q", name)
		}
	}
}

func mustRender(t *testing.T, opts Options) map[string][]byte {
	t.Helper()
	plan, err := renderPlan(opts)
	if err != nil {
		t.Fatalf("renderPlan: %v", err)
	}
	return plan
}

// TestYamlEsc_ControlCharsProduceValidYAML verifies that control characters in
// Name, Prefix, and each Branding field produce YAML that parses cleanly, and
// that the round-tripped value matches the original input.
func TestYamlEsc_ControlCharsProduceValidYAML(t *testing.T) {
	controls := []struct {
		label string
		value string
	}{
		{"newline", "foo\nbar"},
		{"tab", "foo\tbar"},
		{"carriage-return", "foo\rbar"},
		{"nul", "foo\x00bar"},
		{"bell", "foo\x07bar"},
		{"backspace", "foo\x08bar"},
		{"escape", "foo\x1bbar"},
		{"del", "foo\x7fbar"},
		{"c1-pad", "foo\u0080bar"},
		{"c1-nel", "foo\u0085bar"},
		{"c1-ocs", "foo\u009fbar"},
	}

	for _, ctrl := range controls {
		t.Run(ctrl.label+"/name", func(t *testing.T) {
			opts := Options{Name: ctrl.value, Prefix: "dwe"}
			assertYAMLRoundTrip(t, opts, "workspace.yml", "project.name", ctrl.value)
		})
		t.Run(ctrl.label+"/prefix", func(t *testing.T) {
			opts := Options{Name: "myproj", Prefix: ctrl.value}
			assertYAMLRoundTrip(t, opts, "workspace.yml", "project.prefix", ctrl.value)
		})
		t.Run(ctrl.label+"/title", func(t *testing.T) {
			opts := Options{Name: "myproj", Prefix: "dwe", Branding: Branding{Title: ctrl.value, Tagline: "t", Accent: "#ff0000"}}
			assertYAMLRoundTrip(t, opts, "workspace/styles.yml", "header.lines[0]", ctrl.value)
		})
		t.Run(ctrl.label+"/tagline", func(t *testing.T) {
			opts := Options{Name: "myproj", Prefix: "dwe", Branding: Branding{Title: "T", Tagline: ctrl.value, Accent: "#ff0000"}}
			assertYAMLRoundTrip(t, opts, "workspace/styles.yml", "header.tagline", ctrl.value)
		})
		t.Run(ctrl.label+"/accent", func(t *testing.T) {
			opts := Options{Name: "myproj", Prefix: "dwe", Branding: Branding{Title: "T", Tagline: "t", Accent: ctrl.value}}
			assertYAMLRoundTrip(t, opts, "workspace/styles.yml", "colors.accent", ctrl.value)
		})
	}
}

// assertYAMLRoundTrip renders the plan, confirms YAML parses, then checks that
// the value at fieldDesc matches want. fieldDesc is a human-readable label only
// (used in error messages); the caller decides which struct field to inspect.
func assertYAMLRoundTrip(t *testing.T, opts Options, path, fieldDesc, want string) {
	t.Helper()
	plan, err := renderPlan(opts)
	if err != nil {
		t.Fatalf("renderPlan: %v", err)
	}
	data, ok := plan[path]
	if !ok {
		t.Fatalf("missing %q in plan", path)
	}
	var out any
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s with %q: YAML parse failed: %v\n---\n%s", fieldDesc, want, err, data)
	}
	// Spot-check workspace.yml round-trip values via typed struct.
	if path == "workspace.yml" {
		var ws struct {
			Project struct {
				Name   string `yaml:"name"`
				Prefix string `yaml:"prefix"`
			} `yaml:"project"`
		}
		if err := yaml.Unmarshal(data, &ws); err != nil {
			t.Fatalf("%s round-trip parse: %v", fieldDesc, err)
		}
		var got string
		if fieldDesc == "project.name" {
			got = ws.Project.Name
		} else {
			got = ws.Project.Prefix
		}
		if got != want {
			t.Errorf("%s round-trip: got %q, want %q\n---\n%s", fieldDesc, got, want, data)
		}
	}
}
