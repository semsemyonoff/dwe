package llmstxt_test

import (
	"flag"
	"os"
	"strings"
	"testing"

	coredocs "github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/docs/llmstxt"
)

var update = flag.Bool("update", false, "update golden files")

func TestGenerate_NoProject(t *testing.T) {
	opts := llmstxt.Opts{}
	got, err := llmstxt.Generate(opts)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	golden := "testdata/llms_txt_no_project.golden"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0644); err != nil {
			t.Fatalf("failed to update golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read golden file %s: %v", golden, err)
	}
	if got != string(want) {
		t.Errorf("Generate (no-project) output mismatch\ngot:\n%s\nwant:\n%s", got, string(want))
	}
}

func TestGenerate_NoProject_Structure(t *testing.T) {
	opts := llmstxt.Opts{}
	got, err := llmstxt.Generate(opts)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if !strings.HasPrefix(got, "# DWE\n") {
		t.Errorf("expected output to start with '# DWE\\n', got: %q", got[:min(len(got), 30)])
	}
	if !strings.Contains(got, "> DWE (Dev Workspace Engine) is") {
		t.Errorf("expected blockquote summary in output")
	}
	if !strings.Contains(got, "## Quick start") {
		t.Errorf("expected '## Quick start' section")
	}
	// No Commands or Documentation sections when both are empty.
	if strings.Contains(got, "## Commands") {
		t.Errorf("unexpected '## Commands' section when no commands provided")
	}
	if strings.Contains(got, "## Documentation") {
		t.Errorf("unexpected '## Documentation' section when no topics provided")
	}
}

func TestGenerate_EmptyOpts(t *testing.T) {
	got, err := llmstxt.Generate(llmstxt.Opts{})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if got == "" {
		t.Errorf("Generate returned empty output for empty opts")
	}
}

func TestGenerate_WithCommands(t *testing.T) {
	opts := llmstxt.Opts{
		Commands: []llmstxt.CommandSummary{
			{ID: "build", Description: "build the project"},
			{ID: "test", Description: "run tests"},
		},
	}
	got, err := llmstxt.Generate(opts)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !strings.Contains(got, "## Commands") {
		t.Errorf("expected '## Commands' section")
	}
	if !strings.Contains(got, "build") {
		t.Errorf("expected 'build' command in output")
	}
	if !strings.Contains(got, "build the project") {
		t.Errorf("expected command description in output")
	}
}

func TestGenerate_WithDocTopics(t *testing.T) {
	topics := []coredocs.TopicEntry{
		{Path: "reference/config/services", DisplayName: "Services", Source: "dwe"},
		{Path: "reference/config/dwe", DisplayName: "DWE Config", Source: "dwe"},
		{Path: "internals/packages", DisplayName: "Package Layout", Source: "dwe"},
	}

	t.Run("without internals", func(t *testing.T) {
		opts := llmstxt.Opts{DocTopics: topics}
		got, err := llmstxt.Generate(opts)
		if err != nil {
			t.Fatalf("Generate error: %v", err)
		}
		if !strings.Contains(got, "## Documentation") {
			t.Errorf("expected Documentation section")
		}
		if strings.Contains(got, "internals/packages") {
			t.Errorf("internals topic should be excluded when IncludeIntern=false")
		}
		if !strings.Contains(got, "dwe-docs://reference/config/services") {
			t.Errorf("expected reference topic link in output")
		}
	})

	t.Run("with internals", func(t *testing.T) {
		opts := llmstxt.Opts{DocTopics: topics, IncludeIntern: true}
		got, err := llmstxt.Generate(opts)
		if err != nil {
			t.Fatalf("Generate error: %v", err)
		}
		if !strings.Contains(got, "internals/packages") {
			t.Errorf("expected internals topic when IncludeIntern=true")
		}
	})
}

func TestGenerate_EmptyDocTopics(t *testing.T) {
	opts := llmstxt.Opts{DocTopics: []coredocs.TopicEntry{}}
	got, err := llmstxt.Generate(opts)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if strings.Contains(got, "## Documentation") {
		t.Errorf("unexpected Documentation section with empty topics")
	}
}

// stubBriefingOpts fills the sections the CLI layer collects, so the docs layer
// is tested against injected data (the registry itself is pinned in builtin/).
func stubBriefingOpts() llmstxt.Opts {
	return llmstxt.Opts{
		Builtins: []llmstxt.BuiltinSummary{
			{Name: "confirm", Kind: "action", Summary: "interactive confirmation prompt"},
			{Name: "shell", Kind: "predicate", Summary: "run an arbitrary sh -c command"},
			{Name: "daemons_reap", Kind: "internal", Summary: "stop every project daemon container"},
		},
		Conditions: []llmstxt.ConditionSummary{
			{Name: "dir-empty", Args: "<path>", Summary: "path is missing or is an empty directory"},
		},
		ReservedEnvNames: []string{"PROJECT", "UID", "GID"},
	}
}

func TestGenerate_BuiltinsSection(t *testing.T) {
	got, err := llmstxt.Generate(stubBriefingOpts())
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}

	if !strings.Contains(got, "## Builtins") {
		t.Fatalf("expected '## Builtins' section")
	}
	for _, want := range []string{
		"`confirm` — action — interactive confirmation prompt",
		"`shell` — predicate — run an arbitrary sh -c command",
		"`daemons_reap` — internal — stop every project daemon container",
		"`dir-empty <path>` — path is missing or is an empty directory",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected inventory line %q in output", want)
		}
	}
	// The two-registries distinction is the point of the section.
	if !strings.Contains(got, "Two disjoint registries") {
		t.Errorf("expected the disjoint-registries note")
	}
	if !strings.Contains(got, "`when:` registry only") {
		t.Errorf("expected the when: registry to be named separately")
	}
}

func TestGenerate_BuiltinsSection_OmittedWhenEmpty(t *testing.T) {
	got, err := llmstxt.Generate(llmstxt.Opts{})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if strings.Contains(got, "## Builtins") {
		t.Errorf("unexpected Builtins section without an inventory")
	}
	if strings.Contains(got, "## Reserved env names") {
		t.Errorf("unexpected Reserved env names section without names")
	}
}

func TestGenerate_StaticBriefingSections(t *testing.T) {
	// Template syntax and diagnostics are static text: present with empty Opts.
	got, err := llmstxt.Generate(llmstxt.Opts{})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}

	if !strings.Contains(got, "## Template syntax by site") {
		t.Errorf("expected the template-syntax section")
	}
	for _, want := range []string{
		"| Site | Syntax | Notes |",
		"plan-resolution time",
		"workspace/templates/{ide,ai,git}/**",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected template-syntax content %q", want)
		}
	}

	if !strings.Contains(got, "## Diagnostics and machine-readable output") {
		t.Errorf("expected the diagnostics section")
	}
	for _, want := range []string{"--quiet", "--level error,warning", "--debug", "--toc", "--anchors"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected diagnostic flag %q in output", want)
		}
	}
}

func TestGenerate_ReservedEnvSection(t *testing.T) {
	got, err := llmstxt.Generate(stubBriefingOpts())
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if !strings.Contains(got, "## Reserved env names") {
		t.Fatalf("expected the reserved-env section")
	}
	if !strings.Contains(got, "`PROJECT`, `UID`, `GID`") {
		t.Errorf("expected the reserved names joined in the section body")
	}
}

func TestGenerate_BriefingSectionsInProjectOutput(t *testing.T) {
	opts := stubBriefingOpts()
	opts.ProjectRoot = "/fake/project"
	opts.ProjectName = "my-project"

	got, err := llmstxt.Generate(opts)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	// The briefing describes dwe, not the project — it must survive both shapes.
	for _, want := range []string{
		"## Builtins",
		"## Template syntax by site",
		"## Diagnostics and machine-readable output",
		"## Reserved env names",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in project-aware output", want)
		}
	}
}

func TestGenerate_ProjectAware(t *testing.T) {
	opts := llmstxt.Opts{
		ProjectRoot: "/some/project",
		ProjectName: "my-app",
		Services: []llmstxt.ServiceSummary{
			{Name: "api", Type: "container", Title: "API server"},
		},
		InfoSnapshot: &llmstxt.InfoSummary{
			URLs:  []string{"http://localhost:8080"},
			Hosts: []string{"api.local"},
		},
	}
	got, err := llmstxt.Generate(opts)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}

	if !strings.HasPrefix(got, "# my-app\n") {
		t.Errorf("expected H1 with project name, got: %q", got[:min(len(got), 40)])
	}
	if !strings.Contains(got, "## Project") {
		t.Errorf("expected Project section for project-aware output")
	}
	if !strings.Contains(got, "Service: api") {
		t.Errorf("expected service entry")
	}
	if !strings.Contains(got, "API server") {
		t.Errorf("expected service title")
	}
	if !strings.Contains(got, "http://localhost:8080") {
		t.Errorf("expected URL from info snapshot")
	}
}

func TestGenerate_ProjectAware_Golden(t *testing.T) {
	opts := llmstxt.Opts{
		ProjectRoot: "/fake/project",
		ProjectName: "my-project",
		Services: []llmstxt.ServiceSummary{
			{Name: "api", Type: "app", Title: "API Server"},
			{Name: "db", Type: "infra", Title: ""},
		},
		Commands: []llmstxt.CommandSummary{
			{ID: "build", Description: "build the project"},
			{ID: "test", Description: "run tests"},
		},
		InfoSnapshot: &llmstxt.InfoSummary{
			URLs:  []string{"http://api.local"},
			Hosts: []string{"api.local"},
		},
	}
	got, err := llmstxt.Generate(opts)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	golden := "testdata/llms_txt_project.golden"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0644); err != nil {
			t.Fatalf("failed to update golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read golden file %s: %v", golden, err)
	}
	if got != string(want) {
		t.Errorf("Generate (project-aware) output mismatch\ngot:\n%s\nwant:\n%s", got, string(want))
	}
}

func TestGenerate_ProjectAware_DefaultName(t *testing.T) {
	opts := llmstxt.Opts{
		ProjectRoot: "/some/project",
		ProjectName: "",
	}
	got, err := llmstxt.Generate(opts)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if !strings.HasPrefix(got, "# DWE project\n") {
		t.Errorf("expected default project name in H1, got: %q", got[:min(len(got), 40)])
	}
}
