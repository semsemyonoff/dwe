package command

import "github.com/spf13/cobra"

func newDownCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop and remove compose services",
		Long: `Low-level Docker Compose operation: stop and remove compose containers, networks, and volumes.

Equivalent to running 'devbox docker down' with the resolved compose files and project name.

For the full project lifecycle (hook phases, final goodbye message), use 'devbox stop' instead.
'devbox down' is the raw compose passthrough. For a raw compose stop (without removal), use
'devbox docker stop'.`,
		Example:      "  devbox down",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "down")
			if err != nil {
				return err
			}
			return p.compose.Exec("down", args...)
		},
	}
}
