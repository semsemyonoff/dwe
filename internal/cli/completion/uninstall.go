package completion

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newUninstallCompletionCmd() *cobra.Command {
	var customDir string

	cmd := &cobra.Command{
		Use:   "uninstall [shell]",
		Short: "Uninstall shell completion for dwe",
		Long: `Remove the dwe shell completion script from the standard location for your
shell. The target file is determined automatically from the shell name (or
detected from $SHELL).

Supported shells: bash, zsh, fish, powershell

To uninstall for a specific shell:

    dwe completion uninstall zsh
    dwe completion uninstall bash

Override the target directory:

    dwe completion uninstall zsh --path ~/.config/completions

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
