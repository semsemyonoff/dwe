package command

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildFreshUninstallCmd builds a fresh root command and returns a runner for
// `devbox completion uninstall`. A fresh root is required per test because
// cobra accumulates flag state across Execute() calls.
func buildFreshUninstallCmd(t *testing.T) func(args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return func(args ...string) (string, string, error) {
		root := NewRootCmd()
		var outBuf, errBuf bytes.Buffer
		root.SetOut(&outBuf)
		root.SetErr(&errBuf)
		root.SetArgs(append([]string{"completion", "uninstall"}, args...))
		err := root.Execute()
		return outBuf.String(), errBuf.String(), err
	}
}

func TestUninstallCompletion_DeletesInstalledFile(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	target := filepath.Join(tmpDir, ".local", "share", "bash-completion", "completions", "devbox")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("creating dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("# completion\n"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	run := buildFreshUninstallCmd(t)
	stdout, _, err := run("bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("expected file to be deleted; stat returned: %v", statErr)
	}
	if !strings.Contains(stdout, "Uninstalled") {
		t.Errorf("expected 'Uninstalled' in stdout, got: %q", stdout)
	}
}

func TestUninstallCompletion_MissingFile_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	run := buildFreshUninstallCmd(t)
	stdout, _, err := run("bash")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if !strings.Contains(stdout, "not found") {
		t.Errorf("expected 'not found' in stdout for missing file, got: %q", stdout)
	}
}

func TestUninstallCompletion_PathFlag(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return "/should-not-be-used", nil }

	target := filepath.Join(tmpDir, "_devbox")
	if err := os.WriteFile(target, []byte("# zsh completion\n"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	run := buildFreshUninstallCmd(t)
	_, _, err := run("zsh", "--path", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("expected file to be deleted; stat returned: %v", statErr)
	}
}

func TestUninstallCompletion_UnsupportedShell(t *testing.T) {
	run := buildFreshUninstallCmd(t)
	_, _, err := run("tcsh")
	if err == nil {
		t.Fatal("expected error for unsupported shell tcsh")
	}
	if !errors.Is(err, ErrUnsupportedShell) {
		t.Errorf("expected ErrUnsupportedShell, got: %v", err)
	}
}

func TestUninstallCompletion_UnsupportedShell_FromEnv(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	run := buildFreshUninstallCmd(t)
	_, _, err := run()
	if err == nil {
		t.Fatal("expected error for unsupported shell sh from $SHELL")
	}
	if !errors.Is(err, ErrUnsupportedShell) {
		t.Errorf("expected ErrUnsupportedShell, got: %v", err)
	}
}

func TestCompletionCmdHasUninstallSubcommand(t *testing.T) {
	root := NewRootCmd()
	completionCmd, _, err := root.Find([]string{"completion"})
	if err != nil || completionCmd == nil {
		t.Fatal("completion command not found")
	}
	var found bool
	for _, sub := range completionCmd.Commands() {
		if sub.Name() == "uninstall" {
			found = true
			break
		}
	}
	if !found {
		t.Error("completion command should have 'uninstall' subcommand")
	}
}

func TestUninstallCompletionCmd_ValidArgsFunction(t *testing.T) {
	root := NewRootCmd()
	uninstallCmd, _, err := root.Find([]string{"completion", "uninstall"})
	if err != nil || uninstallCmd == nil {
		t.Fatal("completion uninstall command not found")
	}
	if uninstallCmd.ValidArgsFunction == nil {
		t.Fatal("completion uninstall should have a ValidArgsFunction")
	}
	completions, directive := uninstallCmd.ValidArgsFunction(uninstallCmd, []string{}, "")
	if len(completions) != 4 {
		t.Errorf("expected 4 shell completions, got %d: %v", len(completions), completions)
	}
	if directive != 4 { // cobra.ShellCompDirectiveNoFileComp == 4
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
	completions2, _ := uninstallCmd.ValidArgsFunction(uninstallCmd, []string{"bash"}, "")
	if len(completions2) != 0 {
		t.Errorf("expected 0 completions when arg already set, got %d", len(completions2))
	}
}

func TestUninstallCompletion_AllShells_PathFlag(t *testing.T) {
	cases := []struct {
		shell    string
		filename string
	}{
		{"bash", "devbox"},
		{"zsh", "_devbox"},
		{"fish", "devbox.fish"},
		{"powershell", "devbox-completion.ps1"},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			tmpDir := t.TempDir()
			target := filepath.Join(tmpDir, tc.filename)
			if err := os.WriteFile(target, []byte("# content\n"), 0o644); err != nil {
				t.Fatalf("writing file: %v", err)
			}
			run := buildFreshUninstallCmd(t)
			_, _, err := run(tc.shell, "--path", tmpDir)
			if err != nil {
				t.Fatalf("unexpected error for shell %s: %v", tc.shell, err)
			}
			if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("shell %s: expected file deleted, stat: %v", tc.shell, statErr)
			}
		})
	}
}
