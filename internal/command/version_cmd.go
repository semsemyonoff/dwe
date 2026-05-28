package command

import (
	"fmt"

	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/shared/version"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "Print version information",
		Long:         `Print the devbox-cli version, git commit, and build date.`,
		Example:      "  devbox version",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s Devbox v%s (commit %s, built %s)\n",
				ui.LogoMark(), version.Version, version.Commit, version.Date)
		},
	}
}
