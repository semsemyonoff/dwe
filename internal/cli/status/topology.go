package status

import (
	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

func newStatusTopologyCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "topology",
		Short:        "Show only the topology section",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if flags.Output == "json" {
				return renderStatusSectionJSON(cmd, sc, sectionTopology, flags)
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sectionTopology)
		},
	}
}
