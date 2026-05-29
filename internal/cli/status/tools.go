package status

import (
	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/ui"

	"github.com/spf13/cobra"
)

func newStatusToolsCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "tools",
		Short:        "Show only the tools section",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if flags.Output == "json" {
				return renderStatusSectionJSON(cmd, sc, sectionTools, flags)
			}
			if sc.State != nil {
				writeNonEmpty(cmd.OutOrStdout(), ui.RenderPendingBanner(sc.State.Pending))
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sectionTools)
		},
	}
}
