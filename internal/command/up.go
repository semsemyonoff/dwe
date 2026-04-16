package command

import "github.com/spf13/cobra"

func newUpCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "up [services...]",
		Short: "Start compose services",
		Long: `Start all compose services defined in the project (detached).

When service names are provided, only those services are started.
Equivalent to running 'devbox docker up' with the resolved compose files and project name.`,
		Example: `  devbox up
  devbox up app-main
  devbox up app-main db`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "up")
			if err != nil {
				return err
			}
			return p.compose.Exec("up", args...)
		},
	}
}
