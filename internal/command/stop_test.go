package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStopCmd_Use(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newStopCmd(flags)
	if cmd.Use != "stop" {
		t.Errorf("Use = %q, want %q", cmd.Use, "stop")
	}
}

func TestStopCmd_NoArgs(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newStopCmd(flags)
	if cmd.Args == nil {
		t.Error("Args validator should be set (cobra.NoArgs)")
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("expected error when passing arguments to stop command")
	}
}

func TestStopCmd_FlagsExist(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newStopCmd(flags)
	if cmd.Flags().Lookup("yes") == nil {
		t.Error("missing --yes flag")
	}
}

func TestStopCmd_RegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "stop" {
			return
		}
	}
	t.Error("stop command not registered at root level")
}

func TestRunStop_MissingLifecycleYML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	root := NewRootCmd()
	root.SetArgs([]string{"stop"})
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

func TestRunStop_MissingStopSection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	// lifecycle.yml with only run: section, no stop:
	lifecycleYAML := "run:\n  final_message: ready\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	root := NewRootCmd()
	root.SetArgs([]string{"stop"})
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

func TestRunStop_HappyPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "stop:\n  final_message: \"Goodbye!\"\n  phases:\n    - name: down\n      steps:\n        - name: noop\n          run: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	flags := &rootFlags{configPath: cfgPath}
	if err := runStop(flags, false); err != nil {
		t.Errorf("unexpected error on happy path: %v", err)
	}
}
