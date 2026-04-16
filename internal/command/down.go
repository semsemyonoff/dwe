package command

import "github.com/spf13/cobra"

func newDownCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop and remove compose services",
		Long: `Stop and remove all compose containers, networks, and volumes defined in the project.

Equivalent to running 'devbox docker down' with the resolved compose files and project name.`,
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
