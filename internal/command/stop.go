package command

import "github.com/spf13/cobra"

func newStopCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "stop [services...]",
		Short: "Stop compose services without removing them",
		Long: `Stop running compose services without removing their containers.

When service names are provided, only those services are stopped.
Use 'devbox down' to stop and remove containers.`,
		Example: `  devbox stop
  devbox stop app-main`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "stop")
			if err != nil {
				return err
			}
			return p.compose.Exec("stop", args...)
		},
	}
}
