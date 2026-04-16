package command

import "github.com/spf13/cobra"

func newPsCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "ps",
		Short:        "List compose containers",
		Long:         `Show the status of containers for the current project.`,
		Example:      "  devbox ps",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "ps")
			if err != nil {
				return err
			}
			return p.compose.Exec("ps", args...)
		},
	}
}
