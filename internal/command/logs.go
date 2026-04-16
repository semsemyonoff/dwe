package command

import "github.com/spf13/cobra"

func newLogsCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "logs [services...]",
		Short: "View compose service logs",
		Long: `Stream or display logs from compose services.

When service names are provided, only those services' logs are shown.
Pass docker compose log flags after '--' for advanced options (e.g. --follow, --tail).`,
		Example: `  devbox logs
  devbox logs app-main
  devbox logs app-main db`,
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
