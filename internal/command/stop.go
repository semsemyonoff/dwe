package command

import "github.com/spf13/cobra"

func newStopCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "stop [services...]",
		Short:        "Stop compose services",
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
