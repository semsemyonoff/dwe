package services

import (
	"maps"
	"slices"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/core/project/config"

	"github.com/spf13/cobra"
)

// NameCompletion returns a cobra ValidArgsFunction that completes service
// names from the resolved devbox config. Errors yield empty completions
// silently (completion never surfaces errors to the terminal).
func NameCompletion(flags *cmdctx.RootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
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
		return slices.Sorted(maps.Keys(cfg.Services)), cobra.ShellCompDirectiveNoFileComp
	}
}
