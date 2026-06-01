package completion

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// buildCompletionTestRoot builds a minimal cobra tree containing the
// install/uninstall subcommands under a synthetic "completion" parent. It
// stands in for the real dwe root in unit tests without dragging in the
// project-resolution PersistentPreRunE chain.
func buildCompletionTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "dwe"}
	completionCmd := &cobra.Command{Use: "completion"}
	root.AddCommand(completionCmd)
	AttachInstallUninstall(completionCmd, &cmdctx.RootFlags{})
	return root
}

// buildFreshInstallCmd builds a fresh root command and returns the install
// subcommand under `dwe completion install`. A fresh root is required per
// test because cobra accumulates flag state across Execute() calls.
func buildFreshInstallCmd(t *testing.T) func(args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return func(args ...string) (string, string, error) {
		root := buildCompletionTestRoot()
		var outBuf, errBuf bytes.Buffer
		root.SetOut(&outBuf)
		root.SetErr(&errBuf)
		root.SetArgs(append([]string{"completion", "install"}, args...))
		err := root.Execute()
		return outBuf.String(), errBuf.String(), err
	}
}

// --- shell auto-detection ---

func TestInstallCompletion_ShellAutoDetect_Zsh(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SHELL", "/bin/zsh")
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	origReadFile := completionReadFile
	t.Cleanup(func() { completionReadFile = origReadFile })
	completionReadFile = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	run := buildFreshInstallCmd(t)
	stdout, _, err := run("--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "zsh") {
		t.Errorf("expected stdout to mention zsh, got: %q", stdout)
	}
	expected := filepath.Join(tmpDir, ".zsh", "completions", "_dwe")
	if !strings.Contains(stdout, expected) {
		t.Errorf("expected stdout to contain %q, got: %q", expected, stdout)
	}
}

func TestInstallCompletion_ShellAutoDetect_Bash(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SHELL", "/usr/bin/bash")
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	run := buildFreshInstallCmd(t)
	stdout, _, err := run("--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "bash") {
		t.Errorf("expected stdout to mention bash, got: %q", stdout)
	}
}

func TestInstallCompletion_ExplicitArg_OverridesShell(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SHELL", "/bin/zsh")
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	run := buildFreshInstallCmd(t)
	stdout, _, err := run("bash", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should resolve to bash path, not zsh path.
	if !strings.Contains(stdout, "bash-completion") {
		t.Errorf("explicit arg should resolve bash path, got: %q", stdout)
	}
	if strings.Contains(stdout, ".zsh") {
		t.Errorf("explicit bash arg must not produce zsh path, got: %q", stdout)
	}
}

// --- unsupported shell ---

func TestInstallCompletion_UnsupportedShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")

	run := buildFreshInstallCmd(t)
	_, _, err := run()
	if err == nil {
		t.Fatal("expected error for unsupported shell sh")
	}
	if !errors.Is(err, ErrUnsupportedShell) {
		t.Errorf("expected ErrUnsupportedShell, got: %v", err)
	}
}

func TestInstallCompletion_UnsupportedShell_ExplicitArg(t *testing.T) {
	run := buildFreshInstallCmd(t)
	_, _, err := run("tcsh")
	if err == nil {
		t.Fatal("expected error for unsupported shell tcsh")
	}
	if !errors.Is(err, ErrUnsupportedShell) {
		t.Errorf("expected ErrUnsupportedShell, got: %v", err)
	}
}

// --- dry-run writes no file ---

func TestInstallCompletion_DryRun_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	run := buildFreshInstallCmd(t)
	stdout, _, err := run("bash", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	target := filepath.Join(tmpDir, ".local", "share", "bash-completion", "completions", "dwe")
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("dry-run must not create target file; stat returned: %v", statErr)
	}
	if !strings.Contains(stdout, "Would install") {
		t.Errorf("dry-run stdout should say 'Would install', got: %q", stdout)
	}
}

// --- --path flag override ---

func TestInstallCompletion_PathFlag_Zsh(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return "/should-not-be-used", nil }

	origReadFile := completionReadFile
	t.Cleanup(func() { completionReadFile = origReadFile })
	completionReadFile = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	run := buildFreshInstallCmd(t)
	_, _, err := run("zsh", "--path", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	target := filepath.Join(tmpDir, "_dwe")
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("expected %s to exist after install, got: %v", target, statErr)
	}
}

func TestInstallCompletion_PathFlag_Fish(t *testing.T) {
	tmpDir := t.TempDir()
	run := buildFreshInstallCmd(t)
	_, _, err := run("fish", "--path", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	target := filepath.Join(tmpDir, "dwe.fish")
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("expected %s to exist after install, got: %v", target, statErr)
	}
}

func TestInstallCompletion_PathFlag_Bash(t *testing.T) {
	tmpDir := t.TempDir()
	run := buildFreshInstallCmd(t)
	_, _, err := run("bash", "--path", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	target := filepath.Join(tmpDir, "dwe")
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("expected %s to exist after install, got: %v", target, statErr)
	}
}

// --- idempotency ---

func TestInstallCompletion_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	origReadFile := completionReadFile
	t.Cleanup(func() { completionReadFile = origReadFile })
	completionReadFile = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	target := filepath.Join(tmpDir, ".local", "share", "bash-completion", "completions", "dwe")

	// First install.
	run := buildFreshInstallCmd(t)
	if _, _, err := run("bash"); err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	// Pre-populate with stale content to ensure second install overwrites.
	if err := os.WriteFile(target, []byte("stale content\n"), 0o644); err != nil {
		t.Fatalf("writing stale content: %v", err)
	}

	// Second install should succeed and replace content.
	run2 := buildFreshInstallCmd(t)
	if _, _, err := run2("bash"); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading target after second install: %v", err)
	}
	if string(content) == "stale content\n" {
		t.Error("second install must overwrite stale content")
	}
	if !strings.Contains(string(content), "bash") {
		t.Errorf("installed file should contain bash completion content, got: %q", string(content)[:min(100, len(content))])
	}

	// Ensure no temp file left over.
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".dwe-completion-") {
			t.Errorf("temp file left over: %s", e.Name())
		}
	}
}

// --- zsh fpath hint ---

func TestInstallCompletion_ZshFpathHint_Emitted(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	origReadFile := completionReadFile
	t.Cleanup(func() { completionReadFile = origReadFile })
	// Simulate ~/.zshrc not referencing the completion dir.
	completionReadFile = func(name string) ([]byte, error) {
		return []byte("# empty zshrc\n"), nil
	}

	run := buildFreshInstallCmd(t)
	_, stderr, err := run("zsh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	installDir := filepath.Join(tmpDir, ".zsh", "completions")
	if !strings.Contains(stderr, installDir) {
		t.Errorf("expected fpath hint with %q in stderr, got: %q", installDir, stderr)
	}
	if !strings.Contains(stderr, "fpath") {
		t.Errorf("expected 'fpath' in stderr hint, got: %q", stderr)
	}
}

func TestInstallCompletion_ZshFpathHint_Suppressed(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	origReadFile := completionReadFile
	t.Cleanup(func() { completionReadFile = origReadFile })

	// Simulate ~/.zshrc already referencing the completion dir.
	installDir := filepath.Join(tmpDir, ".zsh", "completions")
	completionReadFile = func(name string) ([]byte, error) {
		return fmt.Appendf(nil, "fpath=(%s $fpath)\nautoload -Uz compinit && compinit\n", installDir), nil
	}

	run := buildFreshInstallCmd(t)
	_, stderr, err := run("zsh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stderr, "fpath") {
		t.Errorf("fpath hint must be suppressed when zshrc already contains dir, got stderr: %q", stderr)
	}
}

// --- PowerShell hint ---

func TestInstallCompletion_PowerShell_HintPrinted(t *testing.T) {
	tmpDir := t.TempDir()
	origProf := resolvePowerShellProfile
	t.Cleanup(func() { resolvePowerShellProfile = origProf })
	resolvePowerShellProfile = func() (string, error) {
		return filepath.Join(tmpDir, "Documents", "PowerShell", "profile.ps1"), nil
	}

	run := buildFreshInstallCmd(t)
	_, stderr, err := run("powershell")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "dwe-completion.ps1") {
		t.Errorf("expected PowerShell hint to mention dwe-completion.ps1, got: %q", stderr)
	}
	if !strings.Contains(stderr, "$PROFILE") || !strings.Contains(stderr, "source") && !strings.Contains(stderr, ".") {
		t.Errorf("expected PowerShell sourcing hint in stderr, got: %q", stderr)
	}
}

// --- PowerShell path resolution cases ---

func TestResolvePowerShellInstallPath_PathFlagOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	origProf := resolvePowerShellProfile
	t.Cleanup(func() { resolvePowerShellProfile = origProf })
	// Even if pwsh would return something, --path overrides everything.
	resolvePowerShellProfile = func() (string, error) {
		return "/should/not/be/used/profile.ps1", nil
	}

	path, err := resolveInstallPath("powershell", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(tmpDir, "dwe-completion.ps1")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestResolvePowerShellInstallPath_PwshReturnsPath(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	origProf := resolvePowerShellProfile
	t.Cleanup(func() { resolvePowerShellProfile = origProf })
	psDir := filepath.Join(tmpDir, "Documents", "PowerShell")
	resolvePowerShellProfile = func() (string, error) {
		return filepath.Join(psDir, "profile.ps1"), nil
	}

	path, err := resolveInstallPath("powershell", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(psDir, "dwe-completion.ps1")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestResolvePowerShellInstallPath_PwshMissing_Fallback(t *testing.T) {
	tmpDir := t.TempDir()
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return tmpDir, nil }

	origProf := resolvePowerShellProfile
	t.Cleanup(func() { resolvePowerShellProfile = origProf })
	resolvePowerShellProfile = func() (string, error) {
		return "", errors.New("pwsh not found")
	}

	path, err := resolveInstallPath("powershell", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(tmpDir, ".config", "powershell", "dwe-completion.ps1")
	if path != expected {
		t.Errorf("expected fallback path %q, got %q", expected, path)
	}

	// Verify MkdirAll creates the parent so install actually succeeds.
	run := buildFreshInstallCmd(t)
	_, _, err = run("powershell")
	if err != nil {
		t.Fatalf("install with pwsh-missing fallback should succeed: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("expected completion file to exist at %s: %v", path, statErr)
	}
}

func TestResolvePowerShellInstallPath_HomeUnresolvable(t *testing.T) {
	origHomeDir := completionHomeDir
	t.Cleanup(func() { completionHomeDir = origHomeDir })
	completionHomeDir = func() (string, error) { return "", errors.New("HOME not set") }

	origProf := resolvePowerShellProfile
	t.Cleanup(func() { resolvePowerShellProfile = origProf })
	resolvePowerShellProfile = func() (string, error) { return "", errors.New("pwsh not found") }

	_, err := resolveInstallPath("powershell", "")
	if err == nil {
		t.Fatal("expected error when HOME is unresolvable")
	}
	if !strings.Contains(err.Error(), "home directory") {
		t.Errorf("expected 'home directory' in error, got: %v", err)
	}
}

// --- completion command has install subcommand ---

func TestCompletionCmdHasInstallSubcommand(t *testing.T) {
	root := buildCompletionTestRoot()
	completionCmd, _, err := root.Find([]string{"completion"})
	if err != nil || completionCmd == nil {
		t.Fatal("completion command not found")
	}
	var found bool
	for _, sub := range completionCmd.Commands() {
		if sub.Name() == "install" {
			found = true
			break
		}
	}
	if !found {
		t.Error("completion command should have 'install' subcommand")
	}
}

func TestInstallCompletionCmd_ValidArgsFunction(t *testing.T) {
	root := buildCompletionTestRoot()
	installCmd, _, err := root.Find([]string{"completion", "install"})
	if err != nil || installCmd == nil {
		t.Fatal("completion install command not found")
	}
	if installCmd.ValidArgsFunction == nil {
		t.Fatal("completion install should have a ValidArgsFunction")
	}
	// No args yet — should return 4 shells.
	completions, directive := installCmd.ValidArgsFunction(installCmd, []string{}, "")
	if len(completions) != 4 {
		t.Errorf("expected 4 shell completions, got %d: %v", len(completions), completions)
	}
	if directive != 4 { // cobra.ShellCompDirectiveNoFileComp == 4
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
	// With one arg already provided — should return empty.
	completions2, _ := installCmd.ValidArgsFunction(installCmd, []string{"bash"}, "")
	if len(completions2) != 0 {
		t.Errorf("expected 0 completions when arg already set, got %d", len(completions2))
	}
}
