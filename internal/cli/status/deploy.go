package status

import (
	"path/filepath"
	"sort"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/stack"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"

	"github.com/spf13/cobra"
)

func newStatusDeployCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy [service]",
		Short: "Show deploy status (table) or per-service deploy detail",
		Long: `With no argument, shows the deploy status table for all tracked services.
With a service name, shows the per-phase/step deploy breakdown for that service.`,
		Example:           "  dwe status deploy\n  dwe status deploy main",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: trackedServiceCompletion(flags),
		SilenceUsage:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if flags.Output == "json" {
				if len(args) == 0 {
					return renderStatusSectionJSON(cmd, sc, sectionDeploy, flags)
				}
				return renderDeployDetailJSON(cmd, sc, args[0], flags)
			}
			if sc.State != nil {
				writeNonEmpty(cmd.OutOrStdout(), render.PendingBanner(sc.State.Pending))
			}
			if len(args) == 0 {
				writeNonEmpty(cmd.OutOrStdout(), stack.DeployStatus(sc.statusInput()))
				return nil
			}
			return stack.RenderServiceDeployDetail(cmd.OutOrStdout(), sc.State, sc.Tracked, args[0])
		},
	}
}

// trackedServiceCompletion returns shell completion names for the deploy
// subcommand's optional service argument. Follows the completion contract
// from CLAUDE.md (bypasses PersistentPreRunE).
func trackedServiceCompletion(flags *cmdctx.RootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := cmdctx.CompletionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, _ := usercommands.LoadRegistryFromConfigPath(configPath)
		tracked, _, err := deploy.LoadTrackedServices(cfg, reg, filepath.Dir(configPath))
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		sort.Strings(tracked)
		return tracked, cobra.ShellCompDirectiveNoFileComp
	}
}
