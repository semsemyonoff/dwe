package status

import (
	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/ui"

	"github.com/spf13/cobra"
)

func newStatusAppsCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "apps",
		Short:        "Show only the apps section",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if flags.Output == "json" {
				return renderStatusSectionJSON(cmd, sc, sectionApps, flags)
			}
			if sc.State != nil {
				writeNonEmpty(cmd.OutOrStdout(), ui.RenderPendingBanner(sc.State.Pending))
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sectionApps)
		},
	}
}
