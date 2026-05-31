package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"
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

func TestRunRun_MissingLifecycleYML_UsesDefault(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	var errBuf strings.Builder
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"run"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("expected success when lifecycle.yml absent (built-in default), got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Using built-in default run pipeline") {
		t.Errorf("expected info line in stderr, got: %q", errBuf.String())
	}
}

func TestRunRun_MissingRunSection_UsesDefault(t *testing.T) {
	stubRunPhases(t)
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

	var errBuf strings.Builder
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"run"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("expected success when run: section absent (built-in default), got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Using built-in default run pipeline") {
		t.Errorf("expected info line in stderr, got: %q", errBuf.String())
	}
}

func TestRunRun_JSONMode_NoInfoLine(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	var errBuf strings.Builder
	flags := &cmdctx.RootFlags{Output: "json"}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"run"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	// JSON mode: expect success and no info line in stderr.
	_ = root.Execute() // may succeed or fail; what matters is no info line
	if strings.Contains(errBuf.String(), "Using built-in default") {
		t.Errorf("JSON mode must not emit info line; got stderr: %q", errBuf.String())
	}
}

func TestRunRun_WithLifecycleYML_NoInfoLine(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "run:\n  final_message: ready\n  phases:\n    - name: start\n      steps:\n        - name: noop\n          type: shell\n          cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	var errBuf strings.Builder
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"run"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(errBuf.String(), "Using built-in default") {
		t.Errorf("no info line expected when lifecycle.yml present; got stderr: %q", errBuf.String())
	}
}
