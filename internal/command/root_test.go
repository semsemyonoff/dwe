package command

import (
	"testing"
)

// TestCommandGroups verifies that all expected command groups are defined on
// the root command and that each command is assigned to the correct group.
func TestCommandGroups(t *testing.T) {
	root := NewRootCmd()

	// Verify the five groups exist.
	wantGroups := []string{groupCore, groupEnvironment, groupConfiguration, groupPipelines, groupAdvanced}
	groupSet := make(map[string]bool)
	for _, g := range root.Groups() {
		groupSet[g.ID] = true
	}
	for _, gid := range wantGroups {
		if !groupSet[gid] {
			t.Errorf("root command missing group %q", gid)
		}
	}

	// Build a name→groupID map from registered subcommands.
	cmdGroupID := make(map[string]string)
	for _, c := range root.Commands() {
		cmdGroupID[c.Name()] = c.GroupID
	}

	// Core group: info, version
	for _, name := range []string{"info", "version"} {
		if cmdGroupID[name] != groupCore {
			t.Errorf("command %q groupID = %q, want %q", name, cmdGroupID[name], groupCore)
		}
	}

	// Environment group: lifecycle + shell + status
	for _, name := range []string{"up", "down", "stop", "restart", "logs", "ps", "wait", "shell", "status"} {
		if cmdGroupID[name] != groupEnvironment {
			t.Errorf("command %q groupID = %q, want %q", name, cmdGroupID[name], groupEnvironment)
		}
	}

	// Configuration group: services, tools (render registered as "render")
	for _, name := range []string{"services", "tools", "render"} {
		if cmdGroupID[name] != groupConfiguration {
			t.Errorf("command %q groupID = %q, want %q", name, cmdGroupID[name], groupConfiguration)
		}
	}

	// Pipelines group: deploy, reset
	for _, name := range []string{"deploy", "reset"} {
		if cmdGroupID[name] != groupPipelines {
			t.Errorf("command %q groupID = %q, want %q", name, cmdGroupID[name], groupPipelines)
		}
	}

	// Advanced group: commands, docker, compose
	for _, name := range []string{"commands", "docker", "compose"} {
		if cmdGroupID[name] != groupAdvanced {
			t.Errorf("command %q groupID = %q, want %q", name, cmdGroupID[name], groupAdvanced)
		}
	}
}

// TestPrintCmdIsHidden verifies that the print command is hidden (internal Make compatibility).
func TestPrintCmdIsHidden(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "print" {
			if !c.Hidden {
				t.Error("print command should be hidden")
			}
			return
		}
	}
	t.Error("print command not found in root commands")
}

// TestPrintCmdHasNoGroupID verifies that the hidden print command is not assigned to any group.
func TestPrintCmdHasNoGroupID(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "print" {
			if c.GroupID != "" {
				t.Errorf("print command should have no groupID, got %q", c.GroupID)
			}
			return
		}
	}
}

// TestRenderCmdIsInConfigurationGroup verifies "render" specifically (it has a subcommand use field).
func TestRenderCmdRegisteredWithGroup(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "render" {
			if c.GroupID != groupConfiguration {
				t.Errorf("render command groupID = %q, want %q", c.GroupID, groupConfiguration)
			}
			return
		}
	}
	t.Error("render command not found in root commands")
}
