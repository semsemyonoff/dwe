package command

import "github.com/spf13/cobra"

func newRestartCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "restart [services...]",
		Short:        "Restart compose services",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "restart")
			if err != nil {
				return err
			}
			return p.compose.Exec("restart", args...)
		},
	}
}
