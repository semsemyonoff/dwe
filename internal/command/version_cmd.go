package command

import (
	"fmt"

	"devbox-cli/internal/version"

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
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), version.Info())
		},
	}
}
