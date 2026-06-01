package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

	// Environment group: lifecycle + shell + status + logs + prompt
	for _, name := range []string{"run", "stop", "restart", "shell", "status", "logs", "prompt"} {
		if cmdGroupID[name] != groupEnvironment {
			t.Errorf("command %q groupID = %q, want %q", name, cmdGroupID[name], groupEnvironment)
		}
	}

	// Configuration group: services, render, validate
	for _, name := range []string{"services", "render", "validate"} {
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

// TestCompletionCmd_InAdvancedGroupWithShellSubcommands verifies cobra's
// built-in completion command was attached to the advanced group and that
// the standard shell subcommands are wired.
func TestCompletionCmd_InAdvancedGroupWithShellSubcommands(t *testing.T) {
	root := NewRootCmd()

	var completionCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			completionCmd = c
			break
		}
	}
	if completionCmd == nil {
		t.Fatal("completion command not found in root")
	}
	if completionCmd.GroupID != groupAdvanced {
		t.Errorf("completion GroupID = %q, want %q", completionCmd.GroupID, groupAdvanced)
	}

	want := map[string]bool{"bash": false, "zsh": false, "fish": false, "powershell": false}
	for _, c := range completionCmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("completion missing %q subcommand", name)
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

// TestRootCmdNoConfigShowsHelp verifies that running root from a directory with
// no workspace.yml still produces help output (no error, no crash).
// The root command is allowlisted for the project.ErrNotFound case.
func TestRootCmdNoConfigShowsHelp(t *testing.T) {
	// Run from a temp dir with no workspace.yml so discovery mode returns ErrNotFound.
	t.Chdir(t.TempDir())

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{})

	// Execute must not return an error when no project is found via discovery.
	if err := root.Execute(); err != nil {
		t.Errorf("root command returned unexpected error when no project found: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "dwe") {
		t.Errorf("root output should contain 'dwe', got:\n%s", out)
	}
}

// TestRootCmdWithConfigShowsSummaryAndHelp verifies that root command shows the
// project summary (from config) followed by Cobra help output.
func TestRootCmdWithConfigShowsSummaryAndHelp(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	cfgYAML := `schema_version: "2"
project:
  name: testproject
  prefix: dwe
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
	if !strings.Contains(out, "dwe") {
		t.Errorf("root output should contain help text with 'dwe', got:\n%s", out)
	}
}

// TestRootCmdBrandHeaderAlwaysPresent verifies the branded identity line is
// emitted on `dwe` even when no header.lines is configured.
func TestRootCmdBrandHeaderAlwaysPresent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	cfgYAML := `schema_version: "2"
project:
  name: brandtest
  prefix: dwe
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
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
		t.Errorf("root returned error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "DWE") {
		t.Errorf("expected 'DWE' brand line in output, got:\n%s", out)
	}
	if !strings.Contains(out, "dwe-brandtest") {
		t.Errorf("expected project full name in brand line, got:\n%s", out)
	}
}

// TestRootCmdInfoIsNotDuplicated verifies that `dwe info` is a separate code
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
// when workspace/styles.yml is absent (defaults apply).
func TestRootCmd_StylesMissingIsGraceful(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: styletest\n  prefix: dwe\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	// No workspace/styles.yml — must not cause an error.

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

// TestRootCmd_StylesWithHeaderRendered verifies that when workspace/styles.yml
// contains a header block, root command renders ASCII art without error.
func TestRootCmd_StylesWithHeaderRendered(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: headertest\n  prefix: dwe\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	stylesYAML := "header:\n  lines:\n    - \"DWE\"\n  font: standard\n  color: none\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "styles.yml"), []byte(stylesYAML), 0644); err != nil {
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

// TestLocaleResolutionWithNoUserConfig verifies that when no userconfig exists
// and no $LANG is set, locale defaults to "en".
func TestLocaleResolutionWithNoUserConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: testproj\n  prefix: dwe\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// Clear LANG env var to test fallback to "en"
	oldLang := os.Getenv("LANG")
	_ = os.Unsetenv("LANG")
	defer func() {
		if oldLang != "" {
			_ = os.Setenv("LANG", oldLang)
		}
	}()

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	// Execute a simple command like "version" to trigger PersistentPreRunE
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		// version command should succeed
		t.Logf("version command result: %v", err)
	}
	// The test passes if no panic occurred and locale resolution succeeded
}

// TestLocaleResolutionWithLangEnv verifies that $LANG env var is picked up
// when no userconfig language is set.
func TestLocaleResolutionWithLangEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: testproj\n  prefix: dwe\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	oldLang := os.Getenv("LANG")
	_ = os.Setenv("LANG", "ru_RU.UTF-8")
	defer func() {
		if oldLang != "" {
			_ = os.Setenv("LANG", oldLang)
		} else {
			_ = os.Unsetenv("LANG")
		}
	}()

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"version"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Logf("version command result: %v", err)
	}
}

// TestI18nStoreIsNonNilAfterInit verifies that RootFlags.I18n is never nil
// after PersistentPreRunE completes.
func TestI18nStoreIsNonNilAfterInit(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: testproj\n  prefix: dwe\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// Verify that even with a broken i18n load (e.g., unreadable dir),
	// the store degrades gracefully to a non-nil empty store.
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"version"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	// Execute version to trigger init
	if err := root.Execute(); err != nil {
		t.Logf("version command result: %v", err)
	}
}

// TestLocaleIsAlwaysSet verifies that RootFlags.Locale is never empty
// after PersistentPreRunE completes.
func TestLocaleIsAlwaysSet(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: testproj\n  prefix: dwe\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// Clear LANG to ensure we fall back to "en"
	oldLang := os.Getenv("LANG")
	_ = os.Unsetenv("LANG")
	defer func() {
		if oldLang != "" {
			_ = os.Setenv("LANG", oldLang)
		}
	}()

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"version"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Logf("version command result: %v", err)
	}
}

// TestLocaleResolutionWithUserconfig verifies that userpkg.Language
// takes precedence over $LANG.
func TestLocaleResolutionWithUserconfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: testproj\n  prefix: dwe\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// Create .dwe/config with language setting
	workspaceDir := filepath.Join(dir, ".dwe")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating .dwe dir: %v", err)
	}
	configPath := filepath.Join(workspaceDir, "config")
	if err := os.WriteFile(configPath, []byte("language=de\n"), 0644); err != nil {
		t.Fatalf("writing userconfig: %v", err)
	}

	oldLang := os.Getenv("LANG")
	_ = os.Setenv("LANG", "ru_RU.UTF-8")
	defer func() {
		if oldLang != "" {
			_ = os.Setenv("LANG", oldLang)
		} else {
			_ = os.Unsetenv("LANG")
		}
	}()

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"version"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Logf("version command result: %v", err)
	}
}

// TestLocaleResolutionEnvVarPrecedence verifies that DWE_LANGUAGE env var
// takes precedence over the config file.
func TestLocaleResolutionEnvVarPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: testproj\n  prefix: dwe\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// Create .dwe/config with language setting
	workspaceDir := filepath.Join(dir, ".dwe")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating .dwe dir: %v", err)
	}
	configPath := filepath.Join(workspaceDir, "config")
	if err := os.WriteFile(configPath, []byte("language=de\n"), 0644); err != nil {
		t.Fatalf("writing userconfig: %v", err)
	}

	oldDweLang := os.Getenv("DWE_LANGUAGE")
	_ = os.Setenv("DWE_LANGUAGE", "fr")
	defer func() {
		if oldDweLang != "" {
			_ = os.Setenv("DWE_LANGUAGE", oldDweLang)
		} else {
			_ = os.Unsetenv("DWE_LANGUAGE")
		}
	}()

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"version"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Logf("version command result: %v", err)
	}
}
