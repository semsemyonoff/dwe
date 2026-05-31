package render

import (
	"github.com/spf13/cobra"

	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"
)

// NewCmd builds the `devbox render` command tree: env / ide / ai / git
// subcommands that generate artifacts derived from the merged devbox config.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		GroupID: groupID,
		Use:     "render",
		Short:   "Render derived artifacts from the merged devbox config",
		Long: `Generate files derived from the merged devbox config (devbox.yml + defaults.yml + local.yml).

Subcommands:
  env  — generate .env from the exports.env spec
  ide  — generate IDE config files from template packs
  ai   — generate hub-level agents documentation from template packs
  git  — generate shell git hooks from template packs`,
		Example: `  devbox render env --out .env
  devbox render ide
  devbox render ai
  devbox render git`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newEnvCmd(flags))
	cmd.AddCommand(newIDECmd(flags))
	cmd.AddCommand(newAICmd(flags))
	cmd.AddCommand(newGitCmd(flags))
	return cmd
}
