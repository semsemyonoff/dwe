package render

import (
	"github.com/spf13/cobra"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
)

// NewCmd builds the `dwe render` command tree: env / ide / ai / git
// subcommands that generate artifacts derived from the merged workspace config.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		GroupID: groupID,
		Use:     "render",
		Short:   "Render derived artifacts from the merged workspace config",
		Long: `Generate files derived from the merged workspace config (workspace.yml + defaults.yml + local.yml).

Subcommands:
  env  — generate .env from the exports.env spec
  ide  — generate IDE config files from template packs
  ai   — generate hub-level agents documentation from template packs
  git  — generate shell git hooks from template packs`,
		Example: `  dwe render env --out .env
  dwe render ide
  dwe render ai
  dwe render git`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newEnvCmd(flags))
	cmd.AddCommand(newIDECmd(flags))
	cmd.AddCommand(newAICmd(flags))
	cmd.AddCommand(newGitCmd(flags))
	return cmd
}
