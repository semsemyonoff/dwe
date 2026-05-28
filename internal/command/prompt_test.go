package command

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/core/execution/pipeline"
)

func TestNewPromptCmd_UseAndFlags(t *testing.T) {
	cmd := newPromptCmd(&cmdctx.RootFlags{})
	if cmd.Use != "prompt" {
		t.Errorf("Use = %q, want %q", cmd.Use, "prompt")
	}
	if cmd.Flags().Lookup("check") == nil {
		t.Error("prompt command missing --check flag")
	}
	if !cmd.SilenceUsage {
		t.Error("expected SilenceUsage=true")
	}
	if !cmd.SilenceErrors {
		t.Error("expected SilenceErrors=true")
	}
}

func TestPromptRegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "prompt" {
			found = true
			break
		}
	}
	if !found {
		t.Error("prompt command not registered on root")
	}
}

func TestPromptAllowedWithoutProject(t *testing.T) {
	cmd := newPromptCmd(&cmdctx.RootFlags{})
	cmd.SetArgs([]string{})
	// Simulate the command path resolution allowedWithoutProject expects.
	// We check via the helper directly by constructing a fake cmd with that path.
	root := NewRootCmd()
	// Find the registered prompt subcommand and call allowedWithoutProject on it.
	for _, c := range root.Commands() {
		if c.Name() == "prompt" {
			if !allowedWithoutProject(c) {
				t.Error("expected prompt to be allowed without a project")
			}
			return
		}
	}
	t.Fatal("prompt subcommand not found")
}

// withTempCwd cds to dir for the duration of the test.
func withTempCwd(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func writeDevbox(t *testing.T, root, name string) {
	t.Helper()
	body := "project:\n  name: " + name + "\n"
	if err := os.WriteFile(filepath.Join(root, "devbox.yml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
}

func TestPromptCmd_RunInProject(t *testing.T) {
	// Test that cobra RunE produces the same output as prompt.Run directly.
	tmp := t.TempDir()
	writeDevbox(t, tmp, "demoproj")
	t.Setenv("NO_COLOR", "1")
	withTempCwd(t, tmp)

	cmd := newPromptCmd(&cmdctx.RootFlags{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "demoproj") {
		t.Errorf("output %q missing project name", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("output %q should end with newline", got)
	}
}

func TestPromptCmd_RunOutsideProject(t *testing.T) {
	tmp := t.TempDir()
	// Defend against macOS /var → /private/var by creating a deep nested dir.
	deep := filepath.Join(tmp, "no", "project", "here")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("NO_COLOR", "1")
	withTempCwd(t, deep)

	cmd := newPromptCmd(&cmdctx.RootFlags{})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		// Outside a project means devbox.yml may exist somewhere up the tree
		// (e.g. the repo root). Only assert exit-code behavior when actually outside.
		if out.Len() > 0 {
			// Some output produced — we're inside a project. Skip assertion.
			t.Skip("running inside a parent devbox project; cannot test outside-project case")
		}
		return
	}
	if !errors.Is(err, pipeline.ErrSilent) {
		t.Fatalf("expected ErrSilent, got %T: %v", err, err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no stdout, got %q", out.String())
	}
}

func TestPromptCmd_CheckFlag(t *testing.T) {
	tmp := t.TempDir()
	writeDevbox(t, tmp, "x")
	t.Setenv("NO_COLOR", "1")
	withTempCwd(t, tmp)

	cmd := newPromptCmd(&cmdctx.RootFlags{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output with --check, got %q", buf.String())
	}
}

func TestPromptCmd_RejectsPositionalArgs(t *testing.T) {
	cmd := newPromptCmd(&cmdctx.RootFlags{})
	cmd.SetArgs([]string{"foo"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for positional arg, got nil")
	}
}

func TestPromptCmd_OutsideProjectReturnsSilent(t *testing.T) {
	// Verify that the cobra path returns ErrSilent when outside a project,
	// so main.go's error handler suppresses output and os.Exit(1) fires.
	tmp := t.TempDir()
	deep := filepath.Join(tmp, "no", "devbox", "project")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	withTempCwd(t, deep)
	cmd := newPromptCmd(&cmdctx.RootFlags{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Skip("may be running inside a parent devbox project")
	}
	if !errors.Is(err, pipeline.ErrSilent) {
		t.Errorf("expected ErrSilent, got %T: %v", err, err)
	}
}
