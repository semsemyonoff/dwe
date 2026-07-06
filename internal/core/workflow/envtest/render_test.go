package envtest

import (
	"reflect"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// testConfig returns a DweConfig whose Raw map exposes a handful of dot-paths
// for the ${...} substrate to resolve against.
func testConfig() *config.DweConfig {
	return &config.DweConfig{
		Raw: map[string]any{
			"vars": map[string]any{
				"host":     "db.internal",
				"http_url": "http://localhost:8080/health",
			},
			"project": map[string]any{
				"name": "acme",
			},
		},
	}
}

func TestRenderSteps_CmdAndStringLeaves(t *testing.T) {
	steps := []config.DeployStep{
		{
			Type: "shell",
			Cmd:  "echo ${vars.host} for ${project.name}",
			With: map[string]any{
				"url":     "${vars.http_url}",
				"status":  200,
				"verify":  true,
				"headers": map[string]any{"Host": "${vars.host}"},
				"tags":    []any{"a", "${project.name}", 3},
			},
		},
	}
	if err := RenderSteps(steps, testConfig()); err != nil {
		t.Fatalf("RenderSteps: %v", err)
	}
	s := steps[0]
	if s.Cmd != "echo db.internal for acme" {
		t.Errorf("Cmd = %q", s.Cmd)
	}
	if got := s.With["url"]; got != "http://localhost:8080/health" {
		t.Errorf("with.url = %v", got)
	}
	// Non-string scalars keep their YAML type untouched.
	if got := s.With["status"]; got != 200 {
		t.Errorf("with.status = %v (%T), want int 200", got, got)
	}
	if got := s.With["verify"]; got != true {
		t.Errorf("with.verify = %v (%T), want bool true", got, got)
	}
	// String leaves nested in a map are rendered; container type preserved.
	headers, ok := s.With["headers"].(map[string]any)
	if !ok {
		t.Fatalf("with.headers is %T, want map", s.With["headers"])
	}
	if headers["Host"] != "db.internal" {
		t.Errorf("with.headers.Host = %v", headers["Host"])
	}
	// String leaves in a list are rendered; non-strings preserved in place.
	tags, ok := s.With["tags"].([]any)
	if !ok {
		t.Fatalf("with.tags is %T, want []any", s.With["tags"])
	}
	if !reflect.DeepEqual(tags, []any{"a", "acme", 3}) {
		t.Errorf("with.tags = %v", tags)
	}
}

func TestRenderSteps_AbsentVarBecomesEmpty(t *testing.T) {
	steps := []config.DeployStep{
		{Type: "shell", Cmd: "x=${vars.missing}"},
	}
	if err := RenderSteps(steps, testConfig()); err != nil {
		t.Fatalf("RenderSteps: %v", err)
	}
	if steps[0].Cmd != "x=" {
		t.Errorf("Cmd = %q, want %q", steps[0].Cmd, "x=")
	}
}

func TestRenderSteps_StepWithoutWith(t *testing.T) {
	steps := []config.DeployStep{
		{Type: "shell", Cmd: "echo ${project.name}"},
	}
	if err := RenderSteps(steps, testConfig()); err != nil {
		t.Fatalf("RenderSteps: %v", err)
	}
	if steps[0].Cmd != "echo acme" {
		t.Errorf("Cmd = %q", steps[0].Cmd)
	}
	if steps[0].With != nil {
		t.Errorf("With = %v, want nil", steps[0].With)
	}
}

func TestRenderSteps_ParallelSubsteps(t *testing.T) {
	steps := []config.DeployStep{
		{
			Parallel: &config.ParallelGroup{
				Steps: []config.DeployStep{
					{Type: "shell", Cmd: "echo ${vars.host}"},
					{Type: "builtin", Cmd: "http_check", With: map[string]any{"url": "${vars.http_url}"}},
				},
			},
		},
	}
	if err := RenderSteps(steps, testConfig()); err != nil {
		t.Fatalf("RenderSteps: %v", err)
	}
	sub := steps[0].Parallel.Steps
	if sub[0].Cmd != "echo db.internal" {
		t.Errorf("sub[0].Cmd = %q", sub[0].Cmd)
	}
	if got := sub[1].With["url"]; got != "http://localhost:8080/health" {
		t.Errorf("sub[1].with.url = %v", got)
	}
}

func TestRenderSteps_NilConfig(t *testing.T) {
	steps := []config.DeployStep{
		{Type: "shell", Cmd: "x=${vars.missing}"},
	}
	// A nil config must not panic; absent Raw resolves everything to empty.
	if err := RenderSteps(steps, nil); err != nil {
		t.Fatalf("RenderSteps: %v", err)
	}
	if steps[0].Cmd != "x=" {
		t.Errorf("Cmd = %q", steps[0].Cmd)
	}
}
