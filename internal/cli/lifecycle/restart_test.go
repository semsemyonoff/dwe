package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
)

func TestRestartCmd_Use(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewRestartCmd(groupEnvironment, flags)
	if cmd.Use != "restart" {
		t.Errorf("Use = %q, want %q", cmd.Use, "restart")
	}
}

func TestRestartCmd_NoArgs(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewRestartCmd(groupEnvironment, flags)
	if cmd.Args == nil {
		t.Error("Args validator should be set (cobra.NoArgs)")
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("expected error when passing arguments to restart command")
	}
}

func TestRestartCmd_FlagsExist(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewRestartCmd(groupEnvironment, flags)
	if cmd.Flags().Lookup("yes") == nil {
		t.Error("missing --yes flag")
	}
}

func TestRestartCmd_RegisteredAtRoot(t *testing.T) {
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	for _, c := range root.Commands() {
		if c.Name() == "restart" {
			return
		}
	}
	t.Error("restart command not registered at root level")
}

func TestRunRestart_MissingLifecycleYML_UsesDefault(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	var errBuf strings.Builder
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"restart"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("restart with missing lifecycle.yml should succeed (built-in default), got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Using built-in default run pipeline") {
		t.Errorf("expected info line in stderr, got: %q", errBuf.String())
	}
}

func TestRunRestart_MissingStopSection(t *testing.T) {
	// Missing stop: uses the default stop config (type:devbox step); stub to prevent recursion.
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "run:\n  final_message: ready\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"restart"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("restart with missing stop: should succeed, got: %v", err)
	}
}

func TestRunRestart_MissingRunSection_UsesDefault(t *testing.T) {
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
	root.SetArgs([]string{"restart"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("restart with missing run: section should succeed (built-in default), got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Using built-in default run pipeline") {
		t.Errorf("expected info line in stderr, got: %q", errBuf.String())
	}
}
