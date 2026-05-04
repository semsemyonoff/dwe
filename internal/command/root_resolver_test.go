package command

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/project"
)

// makeV2Project creates a minimal v2 devbox.yml in dir and returns the config path.
func makeV2Project(t *testing.T, dir string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "devbox.yml")
	yml := "schema_version: \"2\"\nproject:\n  name: testproject\n  prefix: devbox\n"
	if err := os.WriteFile(cfgPath, []byte(yml), 0644); err != nil {
		t.Fatalf("writing devbox.yml: %v", err)
	}
	return cfgPath
}

// makeV1Project creates a legacy v1 devbox.yml in dir and returns the config path.
func makeV1Project(t *testing.T, dir string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "devbox.yml")
	yml := "schema_version: \"1\"\nproject:\n  name: legacy\n  prefix: devbox\n"
	if err := os.WriteFile(cfgPath, []byte(yml), 0644); err != nil {
		t.Fatalf("writing v1 devbox.yml: %v", err)
	}
	return cfgPath
}

// runRootWithConfig builds and executes a root command with an explicit --config flag.
// Returns the cobra error (if any) and the combined stdout+stderr output.
func runRootWithConfig(args []string, configPath string) (string, error) {
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	if configPath != "" {
		if e := root.PersistentFlags().Set("config", configPath); e != nil {
			return "", e
		}
	}
	err := root.Execute()
	return buf.String(), err
}

// TestRootResolver_ExplicitGoodPathV2 verifies that an explicit --config pointing
// to a v2 devbox.yml resolves successfully and the command runs.
func TestRootResolver_ExplicitGoodPathV2(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeV2Project(t, dir)

	_, err := runRootWithConfig([]string{"info"}, cfgPath)
	// info will fail because info.yml is missing, but the error should NOT be schema-related.
	if err != nil && strings.Contains(err.Error(), "schema_version") {
		t.Errorf("expected schema validation to pass for v2 project, got: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "config file") && strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected config-not-found error for explicit good path: %v", err)
	}
}

// TestRootResolver_ExplicitBadPath verifies that an explicit --config pointing to a
// non-existent file is always fatal, even for allowlisted commands like version.
func TestRootResolver_ExplicitBadPath_AlwaysFatal(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "nonexistent.yml")

	_, err := runRootWithConfig([]string{"version"}, badPath)
	if err == nil {
		t.Fatal("expected fatal error for explicit bad config path on allowlisted command, got nil")
	}
	// Must be a file-not-found error, NOT project.ErrNotFound.
	if errors.Is(err, project.ErrNotFound) {
		t.Errorf("explicit bad path should NOT produce ErrNotFound sentinel, got: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist in error chain for explicit bad path, got: %v", err)
	}
}

// TestRootResolver_ExplicitV1Path verifies that an explicit --config pointing to a
// v1 project always produces a schema error.
func TestRootResolver_ExplicitV1Path_SchemaError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeV1Project(t, dir)

	_, err := runRootWithConfig([]string{"version"}, cfgPath)
	if err == nil {
		t.Fatal("expected schema error for v1 project on allowlisted command, got nil")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("expected schema_version error, got: %v", err)
	}
	if errors.Is(err, project.ErrNotFound) {
		t.Errorf("v1 schema error should NOT be ErrNotFound, got: %v", err)
	}
}

// TestRootResolver_DiscoveryFromSubdir verifies that running from a subdirectory
// of a v2 project finds devbox.yml via upward walk.
func TestRootResolver_DiscoveryFromSubdir(t *testing.T) {
	dir := t.TempDir()
	makeV2Project(t, dir)

	// Create and chdir into a subdir.
	subdir := filepath.Join(dir, "services", "main")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("creating subdir: %v", err)
	}
	t.Chdir(subdir)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{}) // root with no args — shows help if resolved

	// Execute without setting --config flag.
	if err := root.Execute(); err != nil {
		// Root with no project shows help, which is nil. With a project it also shows help.
		t.Errorf("root from subdir should not error: %v", err)
	}
	// Output should contain help text.
	if !strings.Contains(buf.String(), "devbox") {
		t.Errorf("expected help output, got: %s", buf.String())
	}
}

// TestRootResolver_NoProject_AllowlistedCommands verifies that allowlisted commands
// (version, completion, print) succeed when run from a directory with no devbox.yml.
func TestRootResolver_NoProject_AllowlistedVersion(t *testing.T) {
	t.Chdir(t.TempDir()) // no devbox.yml anywhere

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Errorf("version command should succeed without a project, got: %v", err)
	}
}

// TestRootResolver_NoProject_NonAllowlistedFails verifies that non-allowlisted
// commands fail with ErrNotFound when no project is found via discovery.
func TestRootResolver_NoProject_NonAllowlistedFails(t *testing.T) {
	t.Chdir(t.TempDir()) // no devbox.yml anywhere

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"info"})

	err := root.Execute()
	if err == nil {
		t.Fatal("info command should fail when no project found via discovery")
	}
	if !errors.Is(err, project.ErrNotFound) {
		t.Errorf("expected project.ErrNotFound, got: %v", err)
	}
}

// TestRootResolver_ExplicitDefaultMatchingValue verifies that passing --config devbox.yml
// (matching the old default value) is treated as an explicit path, not as discovery mode.
// Pin this via the Changed flag: the user typed the flag, so Changed must be true.
func TestRootResolver_ExplicitDefaultMatchingValue(t *testing.T) {
	// Run from a temp dir that has no devbox.yml.
	// If the resolver treated "--config devbox.yml" as discovery mode, it would return
	// ErrNotFound (allowlisted root shows help). But since it's explicit, it should fail
	// because "devbox.yml" doesn't exist in the temp dir.
	t.Chdir(t.TempDir())

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{})
	// Explicitly set --config to "devbox.yml" (the old default value).
	if err := root.PersistentFlags().Set("config", "devbox.yml"); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	err := root.Execute()
	if err == nil {
		t.Fatal("explicit --config devbox.yml (nonexistent) should be fatal, got nil")
	}
	// Should be an os.ErrNotExist, not ErrNotFound.
	if errors.Is(err, project.ErrNotFound) {
		t.Errorf("explicit bad path should not produce ErrNotFound, got: %v", err)
	}
}

// TestRootResolver_DocsScope_NoProject_CLIWorks verifies that docs generate --scope cli
// works without a project (uses cwd as output root).
func TestRootResolver_DocsScope_NoProject_CLIWorks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	// Use a temp output dir so we don't pollute cwd.
	outDir := filepath.Join(tmpDir, "docs")
	root.SetArgs([]string{"docs", "generate", "--scope", "cli", "--output", outDir})

	if err := root.Execute(); err != nil {
		t.Errorf("docs generate --scope cli without project should succeed, got: %v", err)
	}
}

// TestRootResolver_DocsScope_NoProject_CommandsFails verifies that docs generate
// --scope commands fails with a clear error when no project is found.
func TestRootResolver_DocsScope_NoProject_CommandsFails(t *testing.T) {
	t.Chdir(t.TempDir())

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"docs", "generate", "--scope", "commands"})

	err := root.Execute()
	if err == nil {
		t.Fatal("docs generate --scope commands without project should fail, got nil")
	}
	if !strings.Contains(err.Error(), "devbox project") {
		t.Errorf("error should mention 'devbox project', got: %v", err)
	}
}

// TestRootResolver_DocsScope_NoProject_AllFails verifies that docs generate
// --scope all fails with a clear error when no project is found, and that the
// error message names "all scope" rather than just "commands scope".
func TestRootResolver_DocsScope_NoProject_AllFails(t *testing.T) {
	t.Chdir(t.TempDir())

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"docs", "generate", "--scope", "all"})

	err := root.Execute()
	if err == nil {
		t.Fatal("docs generate --scope all without project should fail, got nil")
	}
	if !strings.Contains(err.Error(), "devbox project") {
		t.Errorf("error should mention 'devbox project', got: %v", err)
	}
	if !strings.Contains(err.Error(), "all scope") {
		t.Errorf("error should mention 'all scope', got: %v", err)
	}
}

// TestRootResolver_FlagsPopulated verifies that after PersistentPreRunE runs,
// flags.configPath is the absolute path and flags.projectRoot is the directory.
// We test this indirectly through a command that would fail if the path were wrong.
func TestRootResolver_FlagsPopulated_RelativePath(t *testing.T) {
	dir := t.TempDir()
	makeV2Project(t, dir)

	// Chdir to dir so relative "devbox.yml" resolves correctly.
	t.Chdir(dir)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{})
	// Use a relative path — should be resolved to absolute.
	if err := root.PersistentFlags().Set("config", "devbox.yml"); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	// Root with a valid project should show the summary (no error).
	if err := root.Execute(); err != nil {
		t.Errorf("root with relative path resolved from cwd should succeed, got: %v", err)
	}
	if !strings.Contains(buf.String(), "testproject") {
		t.Errorf("expected project name in output, got: %s", buf.String())
	}
}
