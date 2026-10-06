package render

import (
	"github.com/spf13/cobra"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
)

// NewCmd builds the `dwe render` command tree: env / config / ide / ai / git / workspace
// subcommands that generate artifacts derived from the merged workspace config.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		GroupID: groupID,
		Use:     "render",
		Short:   "Render derived artifacts from the merged workspace config",
		Long: `Generate files derived from the merged workspace config (workspace.yml + defaults.yml + local.yml).

Subcommands:
  env       — generate .env from the exports.env spec
  config    — render service config files from template packs
  ide       — generate IDE config files from template packs
  ai        — generate hub-level agents documentation from template packs
  git       — generate shell git hooks from template packs
  workspace — render template packs into the project root`,
		Example: `  dwe render env --out .env
  dwe render config
  dwe render ide
  dwe render ai
  dwe render git
  dwe render workspace
  dwe render workspace ralphex`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newEnvCmd(flags))
	cmd.AddCommand(newConfigCmd(flags))
	cmd.AddCommand(newIDECmd(flags))
	cmd.AddCommand(newAICmd(flags))
	cmd.AddCommand(newGitCmd(flags))
	cmd.AddCommand(newWorkspaceCmd(flags))
	return cmd
}
