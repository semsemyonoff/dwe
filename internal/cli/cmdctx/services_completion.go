package cmdctx

import (
	"maps"
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"

	"github.com/spf13/cobra"
)

// ServiceNameCompletion returns a cobra ValidArgsFunction that completes
// service names from the resolved devbox config. Errors yield empty
// completions silently (completion never surfaces errors to the terminal).
func ServiceNameCompletion(flags *RootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := CompletionConfigPath(flags, cmd)
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
