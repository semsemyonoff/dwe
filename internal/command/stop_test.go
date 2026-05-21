package command

import (
	"os"
	"path/filepath"
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
	// lifecycle.yml is optional for stop — the auto-injected reap phase
	// runs alone.
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	root := NewRootCmd()
	root.SetArgs([]string{"stop"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("stop with missing lifecycle.yml should succeed, got: %v", err)
	}
}

func TestRunStop_MissingStopSection(t *testing.T) {
	// lifecycle.yml without a stop: section is fine — auto-reap runs alone.
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
	root.SetArgs([]string{"stop"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("stop with no stop: section should succeed, got: %v", err)
	}
}
