// Package version provides the `devbox version` command.
package version

import (
	"fmt"

	"devbox-cli/internal/core/ui"
	versioninfo "devbox-cli/internal/shared/version"

	"github.com/spf13/cobra"
)

// NewCmd builds the `devbox version` command.
func NewCmd(groupID string) *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "Print version information",
		Long:         `Print the devbox-cli version, git commit, and build date.`,
		Example:      "  devbox version",
		SilenceUsage: true,
		GroupID:      groupID,
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s Devbox v%s (commit %s, built %s)\n",
				ui.LogoMark(), versioninfo.Version, versioninfo.Commit, versioninfo.Date)
		},
	}
}
