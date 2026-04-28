package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/git"
)

func TestRestartCmd_Use(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newRestartCmd(flags)
	if cmd.Use != "restart" {
		t.Errorf("Use = %q, want %q", cmd.Use, "restart")
	}
}

func TestRestartCmd_NoArgs(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newRestartCmd(flags)
	if cmd.Args == nil {
		t.Error("Args validator should be set (cobra.NoArgs)")
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("expected error when passing arguments to restart command")
	}
}

func TestRestartCmd_FlagsExist(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
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

// TestRunRestart_MissingLifecycleYML verifies that a missing lifecycle.yml surfaces
// the stop-side error (since stop runs first).
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

// TestRunRestart_MissingStopSection verifies that a lifecycle.yml with only a run:
// section (no stop:) surfaces the stop-side missing-section error, since stop runs first.
func TestRunRestart_MissingStopSection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	// lifecycle.yml with only run: section — stop: is missing.
	lifecycleYAML := "run:\n  final_message: ready\n  phases: []\n"
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
		t.Fatal("expected error for missing stop: section, got nil")
	}
	if !strings.Contains(err.Error(), "stop:") && !strings.Contains(err.Error(), "stop` section") {
		t.Errorf("error should mention missing stop section, got: %v", err)
	}
}

// TestRunRestart_MissingRunSection verifies that a lifecycle.yml with only a stop:
// section (no run:) surfaces the run-side missing-section error during the run leg.
func TestRunRestart_MissingRunSection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	// lifecycle.yml with only stop: section — run: is missing.
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

// TestRunRestart_NoUpdatePropagated verifies that restart always forces --no-update
// on the run leg, i.e., git fetch is never called during restart.
func TestRunRestart_NoUpdatePropagated(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	// Both stop and run sections with update enabled (auto mode).
	lifecycleYAML := `stop:
  final_message: "Stopped."
  phases:
    - name: s
      steps:
        - name: noop
          run: "true"
run:
  update:
    enabled: true
    mode: auto
  final_message: "Ready."
  phases:
    - name: s
      steps:
        - name: noop
          run: "true"
`
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	origProbe := gitProbeFunc
	t.Cleanup(func() { gitProbeFunc = origProbe })

	fetchCalled := false
	gitProbeFunc = func(workDir string, fetch bool) (git.Status, error) {
		if fetch {
			fetchCalled = true
		}
		return git.Status{IsRepo: false}, nil
	}

	flags := &rootFlags{configPath: cfgPath}
	cmd := newRestartCmd(flags)
	if err := runRestart(cmd, flags, false); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if fetchCalled {
		t.Error("git fetch should NOT be called during restart (run leg forces --no-update)")
	}
}
