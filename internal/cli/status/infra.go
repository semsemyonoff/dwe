package status

import (
	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"
	"github.com/semsemyonoff/devbox/internal/core/ui/render"

	"github.com/spf13/cobra"
)

func newStatusInfraCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "infra",
		Short:        "Show only the infra section",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if flags.Output == "json" {
				return renderStatusSectionJSON(cmd, sc, sectionInfra, flags)
			}
			if sc.State != nil {
				writeNonEmpty(cmd.OutOrStdout(), render.PendingBanner(sc.State.Pending))
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sectionInfra)
		},
	}
}
