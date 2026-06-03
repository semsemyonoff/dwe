package cmdctx_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

func writeV2Project(t *testing.T, dir string) {
	t.Helper()
	yml := "schema_version: \"2\"\nproject:\n  name: testproject\n  prefix: dwe\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(yml), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
}

// rootCmdForCompletion returns a minimal cobra root carrying the --config
// persistent flag, optionally pre-set as if --config were passed on the CLI.
func rootCmdForCompletion(flags *cmdctx.RootFlags, configPath string) *cobra.Command {
	root := &cobra.Command{Use: "dwe"}
	root.PersistentFlags().StringVar(&flags.ConfigPath, "config", "", "")
	if configPath != "" {
		_ = root.PersistentFlags().Set("config", configPath)
	}
	return root
}

// TestCompletionConfigPath_subdirDiscovery: __complete from a subdirectory
// must walk upward and find the v2 workspace.yml.
func TestCompletionConfigPath_subdirDiscovery(t *testing.T) {
	projectDir := t.TempDir()
	writeV2Project(t, projectDir)
	subdir := filepath.Join(projectDir, "services", "main")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)

	flags := &cmdctx.RootFlags{}
	configPath, projectRoot, err := cmdctx.CompletionConfigPath(flags, rootCmdForCompletion(flags, ""))
	if err != nil {
		t.Fatalf("expected discovery to succeed, got: %v", err)
	}
	if configPath == "" || projectRoot == "" {
		t.Errorf("empty result: configPath=%q projectRoot=%q", configPath, projectRoot)
	}
}

// TestCompletionConfigPath_noProject: no workspace.yml anywhere → error
// (caller returns empty completions silently).
func TestCompletionConfigPath_noProject(t *testing.T) {
	t.Chdir(t.TempDir())

	flags := &cmdctx.RootFlags{}
	if _, _, err := cmdctx.CompletionConfigPath(flags, rootCmdForCompletion(flags, "")); err == nil {
		t.Fatal("expected error when no project exists, got nil")
	}
}

// TestCompletionConfigPath_explicitBadPath: explicit --config /missing.yml
// must return an error so __complete returns empty.
func TestCompletionConfigPath_explicitBadPath(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "nonexistent.yml")
	flags := &cmdctx.RootFlags{}
	if _, _, err := cmdctx.CompletionConfigPath(flags, rootCmdForCompletion(flags, badPath)); err == nil {
		t.Fatal("expected error for explicit bad path, got nil")
	}
}
