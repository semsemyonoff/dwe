package status

import (
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// newStatusDaemonsCmd creates the `dwe status daemons` subcommand: a
// stand-alone view of running daemon containers for the current project.
// Routes through renderSection so the section formatting matches the default
// orchestrator exactly.
func newStatusDaemonsCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "daemons",
		Short:        "Show only the daemons section",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if flags.Output == "json" {
				return renderStatusSectionJSON(cmd, sc, sectionDaemons, flags)
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sectionDaemons)
		},
	}
}
