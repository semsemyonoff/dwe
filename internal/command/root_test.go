package command

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

	// Advanced group: commands, docker, compose, docs
	for _, name := range []string{"commands", "docker", "compose", "docs"} {
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

// TestRootCmdRunEIsSet verifies root RunE is configured.
func TestRootCmdRunEIsSet(t *testing.T) {
	root := NewRootCmd()
	if root.RunE == nil {
		t.Error("root command should have a RunE handler")
	}
}

// TestRootCmdNoConfigShowsHelp verifies that running root without a config file
// still produces help output (no error, no crash).
func TestRootCmdNoConfigShowsHelp(t *testing.T) {
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{})
	if err := root.PersistentFlags().Set("config", "/tmp/nonexistent-devbox-xyz-123.yml"); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	// Execute must not return an error when config is missing.
	if err := root.Execute(); err != nil {
		t.Errorf("root command returned unexpected error when config missing: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "devbox") {
		t.Errorf("root output should contain 'devbox', got:\n%s", out)
	}
}

// TestRootCmdWithConfigShowsSummaryAndHelp verifies that root command shows the
// project summary (from config) followed by Cobra help output.
func TestRootCmdWithConfigShowsSummaryAndHelp(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	cfgYAML := `project:
  name: testproject
  prefix: devbox
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Errorf("root command returned error: %v", err)
	}
	out := buf.String()

	// Project name must appear in the summary.
	if !strings.Contains(out, "testproject") {
		t.Errorf("root output should contain project name 'testproject', got:\n%s", out)
	}
	// Help must be present (root command name).
	if !strings.Contains(out, "devbox") {
		t.Errorf("root output should contain help text with 'devbox', got:\n%s", out)
	}
}

// TestRootCmdInfoIsNotDuplicated verifies that `devbox info` is a separate code
// path from root: the info command still exists as a subcommand.
func TestRootCmdInfoIsNotDuplicated(t *testing.T) {
	root := NewRootCmd()
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "info" {
			found = true
			break
		}
	}
	if !found {
		t.Error("info command should still be a distinct subcommand")
	}
}

// TestRootCmd_StylesMissingIsGraceful verifies that root command does not error
// when devbox/styles.yml is absent (defaults apply).
func TestRootCmd_StylesMissingIsGraceful(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: styletest\n  prefix: devbox\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	// No devbox/styles.yml — must not cause an error.

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Errorf("root command returned unexpected error without styles.yml: %v", err)
	}
	if !strings.Contains(buf.String(), "styletest") {
		t.Errorf("expected project name in output, got:\n%s", buf.String())
	}
}

// TestRootCmd_StylesWithHeaderRendered verifies that when devbox/styles.yml
// contains a header block, root command renders ASCII art without error.
func TestRootCmd_StylesWithHeaderRendered(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: headertest\n  prefix: devbox\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	stylesYAML := "header:\n  lines:\n    - \"Devbox\"\n  font: standard\n  color: none\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "styles.yml"), []byte(stylesYAML), 0644); err != nil {
		t.Fatalf("writing styles.yml: %v", err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Errorf("root command returned error with styles.yml header: %v", err)
	}
	// ASCII art and project summary both appear.
	out := buf.String()
	if !strings.Contains(out, "headertest") {
		t.Errorf("expected project name in output, got:\n%s", out)
	}
}
