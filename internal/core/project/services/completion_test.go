package services_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/services"

	"github.com/spf13/cobra"
)

func rootCmdForCompletion(flags *cmdctx.RootFlags, configPath string) *cobra.Command {
	root := &cobra.Command{Use: "devbox"}
	root.PersistentFlags().StringVarP(&flags.ConfigPath, "config", "c", "", "")
	if configPath != "" {
		_ = root.PersistentFlags().Set("config", configPath)
	}
	return root
}

// TestNameCompletion_brokenSchema documents the __complete contract: a
// project with an invalid schema_version must yield zero completions and
// ShellCompDirectiveNoFileComp — never surface the error to the terminal.
func TestNameCompletion_brokenSchema(t *testing.T) {
	projectDir := t.TempDir()
	yml := "schema_version: \"1\"\nproject:\n  name: legacy\n  prefix: devbox\n"
	if err := os.WriteFile(filepath.Join(projectDir, "devbox.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)

	flags := &cmdctx.RootFlags{}
	root := rootCmdForCompletion(flags, "")
	completions, directive := services.NameCompletion(flags)(root, nil, "")

	if len(completions) != 0 {
		t.Errorf("got %d completions, want 0", len(completions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}

// TestNameCompletion_malformedManifestDoesNotBlock documents the boundary
// between completion (config-scoped) and rendering (manifest-scoped): a
// malformed template manifest must not affect service-name completion.
func TestNameCompletion_malformedManifestDoesNotBlock(t *testing.T) {
	projectDir := t.TempDir()
	devboxDir := filepath.Join(projectDir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `schema_version: "2"
project:
  name: testproject
  prefix: devbox
`
	if err := os.WriteFile(filepath.Join(projectDir, "devbox.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main", "worker"} {
		dir := filepath.Join(devboxDir, "services", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "service.yml"), []byte("type: app\ndir: services/"+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Broken manifest under a template pack — must NOT affect completion.
	packDir := filepath.Join(devboxDir, "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "manifest.yml"), []byte("render: [bad: yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)

	flags := &cmdctx.RootFlags{}
	root := rootCmdForCompletion(flags, "")
	completions, _ := services.NameCompletion(flags)(root, nil, "")
	slices.Sort(completions)
	if !slices.Equal(completions, []string{"main", "worker"}) {
		t.Errorf("got %v, want [main worker]", completions)
	}
}

// TestNameCompletion_noSecondArg: when a positional arg is already given,
// no completions are produced — guards against over-eager suggestions.
func TestNameCompletion_noSecondArg(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	completions, directive := services.NameCompletion(flags)(nil, []string{"already"}, "")
	if len(completions) != 0 {
		t.Errorf("got %d completions, want 0", len(completions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}
