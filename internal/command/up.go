package command

import "github.com/spf13/cobra"

func newUpCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "up [services...]",
		Short:        "Start compose services",
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
