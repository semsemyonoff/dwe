package command

import (
	pipeline "devbox-cli/internal/pipeline"
	"devbox-cli/internal/shared/prompt"

	"devbox-cli/internal/command/cmdctx"

	"github.com/spf13/cobra"
)

func newPromptCmd(_ *cmdctx.RootFlags) *cobra.Command {
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
			if prompt.Run(cmd.OutOrStdout(), passthrough) != 0 {
				return pipeline.ErrSilent
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "exit-only mode for shell when-predicates; no output")
	return cmd
}
