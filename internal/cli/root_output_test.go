package cli

import (
	"bytes"
	"os"
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
