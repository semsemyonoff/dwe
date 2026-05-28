package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
)

// --- cobra wiring tests ---

func TestRunCmd_Use(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewRunCmd(groupEnvironment, flags)
	if cmd.Use != "run" {
		t.Errorf("Use = %q, want %q", cmd.Use, "run")
	}
}

func TestRunCmd_NoArgs(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewRunCmd(groupEnvironment, flags)
	if cmd.Args == nil {
		t.Error("Args validator should be set (cobra.NoArgs)")
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("expected error when passing arguments to run command")
	}
}

func TestRunCmd_FlagsExist(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewRunCmd(groupEnvironment, flags)

	if cmd.Flags().Lookup("no-update") == nil {
		t.Error("missing --no-update flag")
	}
	if cmd.Flags().Lookup("update") == nil {
		t.Error("missing --update flag")
	}
	if cmd.Flags().Lookup("yes") == nil {
		t.Error("missing --yes flag")
	}
}

func TestRunCmd_RegisteredAtRoot(t *testing.T) {
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "run" {
			found = true
			break
		}
	}
	if !found {
		t.Error("run command not registered at root level")
	}
}

func TestRunCmd_InEnvironmentGroup(t *testing.T) {
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	for _, c := range root.Commands() {
		if c.Name() == "run" {
			if c.GroupID != groupEnvironment {
				t.Errorf("run command groupID = %q, want %q", c.GroupID, groupEnvironment)
			}
			return
		}
	}
	t.Error("run command not found in root commands")
}

// --- config loading error tests (cobra integration) ---

// makeMinimalDevboxYML writes the minimum devbox.yml needed for config.LoadConfig to succeed.
func makeMinimalDevboxYML(t *testing.T, dir string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "devbox.yml")
	content := "schema_version: \"2\"\nproject:\n  name: test\n  prefix: devbox\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing devbox.yml: %v", err)
	}
	return cfgPath
}

func TestRunRun_MissingLifecycleYML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"run"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing lifecycle.yml, got nil")
	}
	if !strings.Contains(err.Error(), "no lifecycle.yml") {
		t.Errorf("error should mention 'no lifecycle.yml', got: %v", err)
	}
}

func TestRunRun_MissingRunSection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "stop:\n  final_message: bye\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"run"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing run: section, got nil")
	}
	if !strings.Contains(err.Error(), "run:") && !strings.Contains(err.Error(), "run` section") {
		t.Errorf("error should mention missing run section, got: %v", err)
	}
}
