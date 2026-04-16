package command

import "github.com/spf13/cobra"

func newLogsCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "logs [services...]",
		Short:        "View compose service logs",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "logs")
			if err != nil {
				return err
			}
			return p.compose.Exec("logs", args...)
		},
	}
}
