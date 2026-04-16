package command

import "github.com/spf13/cobra"

func newDownCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "down",
		Short:        "Stop and remove compose services",
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
