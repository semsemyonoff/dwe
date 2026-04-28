package command

import "github.com/spf13/cobra"

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
			return runRestart(cmd, flags, yes)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	return cmd
}

func runRestart(cmd *cobra.Command, flags *rootFlags, yes bool) error {
	if err := runStop(flags, yes); err != nil {
		return err
	}
	return runRun(cmd, flags, true /* noUpdate */, "", yes)
}
