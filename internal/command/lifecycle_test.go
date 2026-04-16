package command

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestRootLevelLifecycleCommandsExist verifies that all lifecycle commands are
// registered at the root level (not just under the docker subcommand group).
func TestRootLevelLifecycleCommandsExist(t *testing.T) {
	root := NewRootCmd()

	want := []string{"up", "down", "stop", "restart", "logs", "ps", "wait"}

	nameSet := make(map[string]bool)
	for _, c := range root.Commands() {
		nameSet[c.Name()] = true
	}

	for _, name := range want {
		if !nameSet[name] {
			t.Errorf("root command missing lifecycle subcommand %q", name)
		}
	}
}

// TestRootLevelLifecycleCommandUse verifies the Use field of each lifecycle command.
func TestRootLevelLifecycleCommandUse(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}

	tests := []struct {
		name    string
		cmd     *cobra.Command
		wantUse string
	}{
		{"up", newUpCmd(flags), "up [services...]"},
		{"down", newDownCmd(flags), "down"},
		{"stop", newStopCmd(flags), "stop [services...]"},
		{"restart", newRestartCmd(flags), "restart [services...]"},
		{"logs", newLogsCmd(flags), "logs [services...]"},
		{"ps", newPsCmd(flags), "ps"},
		{"wait", newWaitCmd(flags), "wait"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cmd.Use != tt.wantUse {
				t.Errorf("Use = %q, want %q", tt.cmd.Use, tt.wantUse)
			}
		})
	}
}

// TestDockerGroupStillIntact verifies the docker subcommand group still has all its subcommands.
func TestDockerGroupStillIntact(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	dockerCmd := newDockerCmd(flags)

	want := []string{"up", "down", "stop", "restart", "logs", "ps", "exec", "run", "wait", "project-name"}

	nameSet := make(map[string]bool)
	for _, c := range dockerCmd.Commands() {
		nameSet[c.Name()] = true
	}

	for _, name := range want {
		if !nameSet[name] {
			t.Errorf("docker subcommand missing %q", name)
		}
	}
}

// TestWaitCmdHasFlags verifies the wait command exposes timeout and interval flags.
func TestWaitCmdHasFlags(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newWaitCmd(flags)

	if cmd.Flags().Lookup("timeout") == nil {
		t.Error("wait command missing --timeout flag")
	}
	if cmd.Flags().Lookup("interval") == nil {
		t.Error("wait command missing --interval flag")
	}
}
