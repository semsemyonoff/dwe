// Package version provides the `devbox version` command.
package version

import (
	"fmt"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/ui"
	versioninfo "devbox-cli/internal/shared/version"

	"github.com/spf13/cobra"
)

// versionJSON is the DTO emitted when --output json is set.
type versionJSON struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
	BuiltBy string `json:"built_by,omitempty"`
}

// NewCmd builds the `devbox version` command.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "Print version information",
		Long:         `Print the devbox-cli version, git commit, and build date.`,
		Example:      "  devbox version",
		SilenceUsage: true,
		GroupID:      groupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			dto := versionJSON{
				Version: versioninfo.Version,
				Commit:  versioninfo.Commit,
				BuiltAt: versioninfo.Date,
				BuiltBy: versioninfo.BuiltBy,
			}
			return cmdctx.WriteData(flags, cmd, dto, func(d versionJSON) string {
				return fmt.Sprintf("%s Devbox v%s (commit %s, built %s)",
					ui.LogoMark(), d.Version, d.Commit, d.BuiltAt)
			})
		},
	}
}
