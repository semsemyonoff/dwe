package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ErrUnsupportedShell is returned when a shell name is not one of the four
// supported values: bash, zsh, fish, powershell.
var ErrUnsupportedShell = errors.New("unsupported shell")

// supportedShells is the canonical list accepted by install/uninstall.
var supportedShells = []string{"bash", "zsh", "fish", "powershell"}

// Seams for unit testing — overridden in tests.
var (
	// resolvePowerShellProfile returns the path to the PowerShell
	// CurrentUserAllHosts profile file by invoking pwsh. Tests replace this
	// with a stub.
	resolvePowerShellProfile = defaultResolvePowerShellProfile

	// completionHomeDir resolves the current user's home directory.
	completionHomeDir = os.UserHomeDir

	// completionReadFile reads the named file (used for zsh fpath check).
	completionReadFile = os.ReadFile
)

func newInstallCompletionCmd() *cobra.Command {
	var dryRun bool
	var customDir string

	cmd := &cobra.Command{
		Use:   "install [shell]",
		Short: "Install shell completion for devbox",
		Long: `Install the devbox shell completion script to the standard location for your
shell. The target file is determined automatically from the shell name (or
detected from $SHELL), and the parent directory is created if missing.

Supported shells: bash, zsh, fish, powershell

To install for a specific shell:

    devbox completion install zsh
    devbox completion install bash

Override the target directory:

    devbox completion install zsh --path ~/.config/completions

Preview without writing:

    devbox completion install --dry-run

NOTES
  - Dotfiles (e.g. ~/.zshrc, PowerShell $PROFILE) are never modified.
  - For zsh, add the completion directory to fpath and call compinit.
  - For PowerShell, source the installed .ps1 from your $PROFILE.`,
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return supportedShells, cobra.ShellCompDirectiveNoFileComp
		},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			shell, err := resolveShellName(args)
			if err != nil {
				return err
			}
			targetPath, err := resolveInstallPath(shell, customDir)
			if err != nil {
				return err
			}
			if dryRun {
				return runInstallDryRun(cmd, shell, targetPath)
			}
			return runInstall(cmd, shell, targetPath)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the target path and generated content preview; do not write")
	cmd.Flags().StringVar(&customDir, "path", "", "install to this directory instead of the default location")

	return cmd
}

// resolveShellName returns the shell name from the positional arg or $SHELL.
func resolveShellName(args []string) (string, error) {
	if len(args) == 1 {
		s := strings.ToLower(args[0])
		if !isSupportedShell(s) {
			return "", fmt.Errorf("%w: %q; supported shells: %s", ErrUnsupportedShell, args[0], strings.Join(supportedShells, ", "))
		}
		return s, nil
	}
	// Detect from $SHELL.
	shellEnv := os.Getenv("SHELL")
	if shellEnv == "" {
		return "", fmt.Errorf("%w: $SHELL is not set; pass the shell name explicitly", ErrUnsupportedShell)
	}
	base := strings.ToLower(filepath.Base(shellEnv))
	if !isSupportedShell(base) {
		return "", fmt.Errorf("%w: %q (from $SHELL); pass the shell name explicitly; supported shells: %s", ErrUnsupportedShell, base, strings.Join(supportedShells, ", "))
	}
	return base, nil
}

func isSupportedShell(s string) bool {
	return slices.Contains(supportedShells, s)
}

// resolveInstallPath returns the absolute target file path for the completion script.
func resolveInstallPath(shell, customDir string) (string, error) {
	if customDir != "" {
		absDir, err := filepath.Abs(customDir)
		if err != nil {
			return "", fmt.Errorf("resolving --path: %w", err)
		}
		return completionFileInDir(shell, absDir), nil
	}

	home, err := completionHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory; pass --path <dir> explicitly")
	}

	switch shell {
	case "bash":
		return filepath.Join(home, ".local", "share", "bash-completion", "completions", "devbox"), nil
	case "zsh":
		return filepath.Join(home, ".zsh", "completions", "_devbox"), nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "completions", "devbox.fish"), nil
	case "powershell":
		return resolvePowerShellInstallPath(home)
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedShell, shell)
	}
}

// completionFileInDir returns the target file name within dir for the given shell.
func completionFileInDir(shell, dir string) string {
	switch shell {
	case "zsh":
		return filepath.Join(dir, "_devbox")
	case "fish":
		return filepath.Join(dir, "devbox.fish")
	case "powershell":
		return filepath.Join(dir, "devbox-completion.ps1")
	default: // bash and anything else
		return filepath.Join(dir, "devbox")
	}
}

// resolvePowerShellInstallPath resolves the directory to write devbox-completion.ps1
// into, using resolvePowerShellProfile (seam) with fallback to ~/.config/powershell/.
func resolvePowerShellInstallPath(home string) (string, error) {
	profilePath, err := resolvePowerShellProfile()
	if err == nil && profilePath != "" {
		dir := filepath.Dir(profilePath)
		return filepath.Join(dir, "devbox-completion.ps1"), nil
	}
	// Fallback: pwsh not installed or failed — use the documented default.
	dir := filepath.Join(home, ".config", "powershell")
	return filepath.Join(dir, "devbox-completion.ps1"), nil
}

// defaultResolvePowerShellProfile invokes `pwsh -NoProfile -Command "$PROFILE.CurrentUserAllHosts"`
// and returns the trimmed output as the profile path.
func defaultResolvePowerShellProfile() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pwsh", "-NoProfile", "-Command", "$PROFILE.CurrentUserAllHosts").Output()
	if err != nil {
		return "", fmt.Errorf("invoking pwsh: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// generateCompletionContent generates the shell completion script and returns
// it as a []byte. It calls the cobra Gen* methods on the root command.
func generateCompletionContent(cmd *cobra.Command, shell string) ([]byte, error) {
	root := cmd.Root()
	var buf bytes.Buffer
	var err error
	switch shell {
	case "bash":
		err = root.GenBashCompletion(&buf)
	case "zsh":
		err = root.GenZshCompletion(&buf)
	case "fish":
		err = root.GenFishCompletion(&buf, true)
	case "powershell":
		err = root.GenPowerShellCompletionWithDesc(&buf)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedShell, shell)
	}
	if err != nil {
		return nil, fmt.Errorf("generating %s completion: %w", shell, err)
	}
	return buf.Bytes(), nil
}

// atomicWriteCompletion writes content to targetPath atomically using a temp
// file in the same directory followed by os.Rename. Creates parent dirs at
// 0o755.
func atomicWriteCompletion(targetPath string, content []byte) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".devbox-completion-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Clean up on any failure path.
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(content); err != nil {
		return fmt.Errorf("writing completion content: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("setting file permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("installing completion file: %w", err)
	}
	return nil
}

// runInstall performs the full install: generate content, write atomically,
// print success, and emit shell-specific hints.
func runInstall(cmd *cobra.Command, shell, targetPath string) error {
	content, err := generateCompletionContent(cmd, shell)
	if err != nil {
		return err
	}
	if err := atomicWriteCompletion(targetPath, content); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Installed %s completion to %s\n", shell, targetPath)
	emitShellHints(cmd, shell, targetPath)
	return nil
}

// runInstallDryRun prints the target path and a preview of the generated
// content without writing anything.
func runInstallDryRun(cmd *cobra.Command, shell, targetPath string) error {
	content, err := generateCompletionContent(cmd, shell)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Would install %s completion to: %s\n\n", shell, targetPath)
	lines := strings.Split(string(content), "\n")
	limit := min(10, len(lines))
	for _, l := range lines[:limit] {
		_, _ = fmt.Fprintln(out, l)
	}
	if len(lines) > 10 {
		_, _ = fmt.Fprintf(out, "... (%d more lines)\n", len(lines)-10)
	}
	return nil
}

// emitShellHints prints shell-specific setup instructions to stderr.
func emitShellHints(cmd *cobra.Command, shell, targetPath string) {
	errW := cmd.ErrOrStderr()
	switch shell {
	case "zsh":
		emitZshFpathHint(cmd, targetPath)
	case "powershell":
		abs, _ := filepath.Abs(targetPath)
		_, _ = fmt.Fprintf(errW, "\nTo enable completions, add the following line to your PowerShell profile ($PROFILE):\n")
		_, _ = fmt.Fprintf(errW, ". %q\n", abs)
	}
}

func newUninstallCompletionCmd() *cobra.Command {
	var customDir string

	cmd := &cobra.Command{
		Use:   "uninstall [shell]",
		Short: "Uninstall shell completion for devbox",
		Long: `Remove the devbox shell completion script from the standard location for your
shell. The target file is determined automatically from the shell name (or
detected from $SHELL).

Supported shells: bash, zsh, fish, powershell

To uninstall for a specific shell:

    devbox completion uninstall zsh
    devbox completion uninstall bash

Override the target directory:

    devbox completion uninstall zsh --path ~/.config/completions

NOTES
  - Dotfiles (e.g. ~/.zshrc, PowerShell $PROFILE) are never modified.
  - If the completion file does not exist, the command exits successfully.
  - Homebrew tap users should use generate_completions_from_executable instead.`,
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return supportedShells, cobra.ShellCompDirectiveNoFileComp
		},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			shell, err := resolveShellName(args)
			if err != nil {
				return err
			}
			targetPath, err := resolveInstallPath(shell, customDir)
			if err != nil {
				return err
			}
			return runUninstall(cmd, targetPath)
		},
	}

	cmd.Flags().StringVar(&customDir, "path", "", "uninstall from this directory instead of the default location")

	return cmd
}

// runUninstall removes the completion file at targetPath. A missing file is
// treated as success (idempotent).
func runUninstall(cmd *cobra.Command, targetPath string) error {
	err := os.Remove(targetPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Completion file not found (already uninstalled): %s\n", targetPath)
			return nil
		}
		return fmt.Errorf("removing completion file: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Uninstalled completion from %s\n", targetPath)
	return nil
}

// emitZshFpathHint checks ~/.zshrc for a reference to the completion dir; if
// missing it prints the fpath snippet to stderr.
func emitZshFpathHint(cmd *cobra.Command, targetPath string) {
	installDir := filepath.Dir(targetPath)
	home, err := completionHomeDir()
	if err != nil {
		return
	}
	zshrc := filepath.Join(home, ".zshrc")
	data, err := completionReadFile(zshrc)
	if err == nil {
		if strings.Contains(string(data), installDir) {
			return
		}
	}
	// Print the fpath hint.
	errW := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(errW, "\nTo enable completions, add the following to %s:\n\n", zshrc)
	_, _ = fmt.Fprintf(errW, "    fpath=(%s $fpath)\n", installDir)
	_, _ = fmt.Fprintf(errW, "    autoload -Uz compinit && compinit\n")
}
