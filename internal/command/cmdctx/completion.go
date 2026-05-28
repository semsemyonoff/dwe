package cmdctx

import (
	"devbox-cli/internal/project"

	"github.com/spf13/cobra"
)

// CompletionConfigPath resolves the project config path for ValidArgsFunction
// callbacks. Cobra's __complete path bypasses PersistentPreRunE, so completion
// handlers must re-resolve the project themselves. Any error means the caller
// should return empty completions silently (completion never surfaces errors).
func CompletionConfigPath(flags *RootFlags, cmd *cobra.Command) (configPath, projectRoot string, err error) {
	if flags.Root != "" {
		return flags.ConfigPath, flags.Root, nil
	}

	explicit := false
	if cmd != nil {
		if rootCmd := cmd.Root(); rootCmd != nil {
			if f := rootCmd.PersistentFlags().Lookup("config"); f != nil {
				explicit = f.Changed
			}
		}
	}

	var configArg string
	if explicit {
		configArg = flags.ConfigPath
	}

	resolved, err := project.Resolve(configArg)
	if err != nil {
		return "", "", err
	}
	return resolved.ConfigPath, resolved.Root, nil
}
