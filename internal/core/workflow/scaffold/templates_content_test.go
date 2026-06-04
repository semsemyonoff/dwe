package scaffold

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestEmbeddedTemplates_StarterServiceKeepsTypeAndContainerActive(t *testing.T) {
	svc := mustRender(t, newTestOptions())["workspace/services/app/service.yml"]
	var parsed map[string]any
	if err := yaml.Unmarshal(svc, &parsed); err != nil {
		t.Fatalf("parse service.yml: %v", err)
	}
	if parsed["type"] != "app" {
		t.Errorf("service.yml type = %v, want app", parsed["type"])
	}
	if parsed["container"] != "app" {
		t.Errorf("service.yml container = %v, want app", parsed["container"])
	}
	// Only the two active keys — everything else is commented.
	if len(parsed) != 2 {
		t.Errorf("service.yml has %d active keys %v, want exactly type+container", len(parsed), parsed)
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
