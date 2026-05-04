package command

import (
	"devbox-cli/internal/lifecycle"

	"github.com/spf13/cobra"
)

func newStopCmd(flags *rootFlags) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the project (full lifecycle: before-stop hooks → docker down → after-stop hooks)",
		Long: `Stop the project driven by devbox/lifecycle.yml.

Execution order: before-stop hooks → docker down → after-stop hooks → final message.

Use 'devbox down' for a bare Docker Compose stop-and-remove without hooks.
Use 'devbox docker stop' for the low-level compose stop (no container removal).`,
		Example:      `  devbox stop`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return lifecycle.RunStop(lifecycle.StopContext{
				ConfigPath: flags.configPath,
				Yes:        yes,
			})
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	return cmd
}
