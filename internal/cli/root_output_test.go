package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOutputFlag_InvalidValue verifies that --output with an unrecognized value
// returns a coded error before any subcommand logic runs.
func TestOutputFlag_InvalidValue(t *testing.T) {
	t.Chdir(t.TempDir())

	root := NewRootCmd()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--output", "bogus", "version"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for --output bogus, got nil")
	}
	if !strings.Contains(err.Error(), "unknown output format") {
		t.Errorf("error message should contain 'unknown output format', got: %q", err.Error())
	}
}

// TestOutputFlag_JSON_SetsNoColor verifies that --output json sets NO_COLOR=1
// in the environment (so lipgloss doesn't emit ANSI sequences).
func TestOutputFlag_JSON_SetsNoColor(t *testing.T) {
	t.Chdir(t.TempDir())

	// Ensure NO_COLOR is unset at the start so the test is authoritative.
	prev := os.Getenv("NO_COLOR")
	_ = os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if prev != "" {
			_ = os.Setenv("NO_COLOR", prev)
		} else {
			_ = os.Unsetenv("NO_COLOR")
		}
	})

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--output", "json", "version"})

	// Execute returns no error for version (allowed without project).
	_ = root.Execute()

	if os.Getenv("NO_COLOR") != "1" {
		t.Errorf("expected NO_COLOR=1 after --output json, got %q", os.Getenv("NO_COLOR"))
	}
}

// TestOutputFlag_JSON_SilencesErrors verifies that --output json sets both
// SilenceErrors and SilenceUsage on the root command so cobra does not print
// its own "Error: ..." or usage block when a subcommand fails.
func TestOutputFlag_JSON_SilencesErrors(t *testing.T) {
	t.Chdir(t.TempDir())

	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--output", "json", "version"})

	_ = root.Execute()

	if !root.SilenceErrors {
		t.Error("expected SilenceErrors = true after --output json")
	}
	if !root.SilenceUsage {
		t.Error("expected SilenceUsage = true after --output json")
	}
}

// TestOutputFlag_Text_DoesNotSilenceErrors verifies that the default text mode
// does not force-set SilenceErrors/SilenceUsage (fang may still set them, but
// PersistentPreRunE must not do it for text mode).
func TestOutputFlag_Text_DoesNotSetNoColor(t *testing.T) {
	t.Chdir(t.TempDir())

	prev := os.Getenv("NO_COLOR")
	_ = os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if prev != "" {
			_ = os.Setenv("NO_COLOR", prev)
		} else {
			_ = os.Unsetenv("NO_COLOR")
		}
	})

	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"version"}) // default text mode

	_ = root.Execute()

	if os.Getenv("NO_COLOR") == "1" {
		t.Error("NO_COLOR should not be set in default text mode")
	}
}

// TestRootJSON_NoProject verifies that `devbox --output json` with no project
// emits `{"project":null,"deploy_summary":null,"pending":null}`.
func TestRootJSON_NoProject(t *testing.T) {
	t.Chdir(t.TempDir())

	root := NewRootCmd()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--output", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if _, ok := got["project"]; !ok {
		t.Error("expected 'project' key")
	}
	if got["project"] != nil {
		t.Errorf("expected project=null when no project, got %v", got["project"])
	}
}

// TestRootJSON_WithProject verifies that `devbox --output json` with a v2 project
// emits a JSON object with project name, version, and root set.
func TestRootJSON_WithProject(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeV2Project(t, dir)

	root := NewRootCmd()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--output", "json"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}

	proj, ok := got["project"].(map[string]any)
	if !ok || proj == nil {
		t.Fatalf("expected project object, got %v", got["project"])
	}
	if proj["name"] != "devbox-testproject" {
		t.Errorf("project.name: got %v, want devbox-testproject", proj["name"])
	}
	wantRoot, _ := filepath.EvalSymlinks(filepath.Dir(cfgPath))
	if proj["root"] != wantRoot {
		t.Errorf("project.root: got %v, want %v", proj["root"], wantRoot)
	}
	if proj["version"] == "" {
		t.Error("project.version should not be empty")
	}
	// No state file → deploy_summary and pending should be null.
	if got["deploy_summary"] != nil {
		t.Errorf("deploy_summary: expected null when no state file, got %v", got["deploy_summary"])
	}
	if got["pending"] != nil {
		t.Errorf("pending: expected null when no state file, got %v", got["pending"])
	}
}

// TestRootJSON_NoHelp verifies that JSON mode never emits cobra help text.
func TestRootJSON_NoHelp(t *testing.T) {
	t.Chdir(t.TempDir())

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--output", "json"})

	_ = root.Execute()

	if strings.Contains(out.String(), "Usage:") {
		t.Errorf("JSON mode should not emit cobra help text, got: %s", out.String())
	}
}

// TestNewRootCmdWithFlags verifies that NewRootCmdWithFlags returns a non-nil
// flags pointer and that the root command is the same one referenced by flags
// after Execute.
func TestNewRootCmdWithFlags(t *testing.T) {
	t.Chdir(t.TempDir())

	root, flags := NewRootCmdWithFlags()
	if root == nil {
		t.Fatal("NewRootCmdWithFlags returned nil root")
	}
	if flags == nil {
		t.Fatal("NewRootCmdWithFlags returned nil flags")
	}

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--output", "json", "version"})
	_ = root.Execute()

	if flags.Output != "json" {
		t.Errorf("flags.Output should be 'json' after Execute, got %q", flags.Output)
	}
}
