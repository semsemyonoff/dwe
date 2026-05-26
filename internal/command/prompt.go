package command

import (
	"devbox-cli/internal/prompt"

	"github.com/spf13/cobra"
)

// promptExitError carries a fixed exit code so main.go's ExitCode() dispatch
// propagates the prompt's silent-failure semantics through cobra.
type promptExitError struct {
	code int
}

func (e *promptExitError) Error() string { return "" }
func (e *promptExitError) ExitCode() int { return e.code }

func newPromptCmd(_ *rootFlags) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "prompt",
		Short: "Print a compact shell-prompt segment for the current project",
		Long: `Print a compact, prompt-ready segment describing the current devbox project.

Designed for shell-prompt integration (e.g. starship). The output is a single
line of the form '{▪} <project-name> <status-icon>', where the logomark uses
the project's accent color and the status icon reflects deploy state.

The hot path bypasses cobra and dispatches directly from main; this command
exists primarily for --help discoverability and shell completion. Exits 0
inside a devbox project and 1 outside (or on any silent failure).`,
		Example: "  devbox prompt\n  devbox prompt --check",
		Args:    cobra.NoArgs,
		// Prompt output is consumed by shells — never let cobra print usage or
		// error banners that would corrupt the rendered prompt line.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var passthrough []string
			if check {
				passthrough = []string{"--check"}
			}
			code := prompt.Run(cmd.OutOrStdout(), passthrough)
			if code != 0 {
				return &promptExitError{code: code}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "exit-only mode for shell when-predicates; no output")
	return cmd
}
