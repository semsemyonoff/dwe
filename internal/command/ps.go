package command

import "github.com/spf13/cobra"

func newPsCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "ps",
		Short:        "List compose containers",
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
