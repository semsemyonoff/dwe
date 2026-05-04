package command

import (
	"devbox-cli/internal/lifecycle"

	"github.com/spf13/cobra"
)

func newRestartCmd(flags *rootFlags) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the project (stop, then run --no-update)",
		Long: `Restart the project by running the full stop lifecycle then the full run lifecycle.

The run leg always skips the git update probe (equivalent to 'devbox run --no-update').

Use 'devbox docker restart' for the low-level compose restart passthrough.`,
		Example:      `  devbox restart`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return lifecycle.RunRestart(lifecycle.RunContext{
				ConfigPath: flags.configPath,
				Yes:        yes,
				ShowInfo:   func() error { return runInfo(cmd, flags) },
			})
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	return cmd
}
