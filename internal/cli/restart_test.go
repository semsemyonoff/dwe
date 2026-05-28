package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
)

func TestRestartCmd_Use(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := newRestartCmd(flags)
	if cmd.Use != "restart" {
		t.Errorf("Use = %q, want %q", cmd.Use, "restart")
	}
}

func TestRestartCmd_NoArgs(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := newRestartCmd(flags)
	if cmd.Args == nil {
		t.Error("Args validator should be set (cobra.NoArgs)")
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("expected error when passing arguments to restart command")
	}
}

func TestRestartCmd_FlagsExist(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := newRestartCmd(flags)
	if cmd.Flags().Lookup("yes") == nil {
		t.Error("missing --yes flag")
	}
}

func TestRestartCmd_RegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "restart" {
			return
		}
	}
	t.Error("restart command not registered at root level")
}

func TestRunRestart_MissingLifecycleYML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	root := NewRootCmd()
	root.SetArgs([]string{"restart"})
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

func TestRunRestart_MissingStopSection(t *testing.T) {
	// Missing stop: section no longer aborts restart — the auto-reap phase
	// runs and the run leg proceeds.
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

	root := NewRootCmd()
	root.SetArgs([]string{"restart"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("restart with missing stop: should succeed, got: %v", err)
	}
}

func TestRunRestart_MissingRunSection(t *testing.T) {
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

	root := NewRootCmd()
	root.SetArgs([]string{"restart"})
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
