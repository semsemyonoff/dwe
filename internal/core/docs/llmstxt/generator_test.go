package llmstxt_test

import (
	"flag"
	"os"
	"strings"
	"testing"

	coredocs "devbox-cli/internal/core/docs"
	"devbox-cli/internal/core/docs/llmstxt"
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

	if !strings.HasPrefix(got, "# devbox\n") {
		t.Errorf("expected output to start with '# devbox\\n', got: %q", got[:min(len(got), 30)])
	}
	if !strings.Contains(got, "> devbox is") {
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
		{Path: "reference/config/services", DisplayName: "Services", Source: "devbox"},
		{Path: "reference/config/devbox", DisplayName: "Devbox Config", Source: "devbox"},
		{Path: "internals/packages", DisplayName: "Package Layout", Source: "devbox"},
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
		if !strings.Contains(got, "devbox-docs://reference/config/services") {
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

func TestGenerate_ProjectAware_DefaultName(t *testing.T) {
	opts := llmstxt.Opts{
		ProjectRoot: "/some/project",
		ProjectName: "",
	}
	got, err := llmstxt.Generate(opts)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if !strings.HasPrefix(got, "# devbox project\n") {
		t.Errorf("expected default project name in H1, got: %q", got[:min(len(got), 40)])
	}
}

