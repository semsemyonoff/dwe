package command

import (
	"devbox-cli/internal/project"

	"github.com/spf13/cobra"
)

// completionConfigPath resolves the project config path and root for use inside
// ValidArgsFunction callbacks. Cobra's hidden __complete command bypasses
// PersistentPreRunE, so completion callbacks cannot rely on flags.projectRoot
// being pre-populated — they must re-resolve the project themselves.
//
// Fast path: if PersistentPreRunE already ran (flags.projectRoot is set), return
// the already-resolved values immediately.
//
// Slow path: detect whether --config/-c was explicitly supplied (via the root
// persistent flag's Changed bit), then call project.Resolve. The same
// explicit-vs-discovery logic used in PersistentPreRunE is applied here so that
// tab-complete behaves identically to normal command execution.
//
// On any error — ErrNotFound, schema error, or explicit-bad-path — the caller
// should return empty completions and cobra.ShellCompDirectiveNoFileComp. The
// error is never surfaced to the terminal during tab-complete.
func completionConfigPath(flags *rootFlags, cmd *cobra.Command) (configPath, projectRoot string, err error) {
	// Fast path: PersistentPreRunE already resolved the project.
	if flags.projectRoot != "" {
		return flags.configPath, flags.projectRoot, nil
	}

	// Detect whether --config/-c was explicitly set by the user.
	explicit := false
	if cmd != nil {
		if root := cmd.Root(); root != nil {
			if f := root.PersistentFlags().Lookup("config"); f != nil {
				explicit = f.Changed
			}
		}
	}

	var configArg string
	if explicit {
		configArg = flags.configPath
	}

	resolved, err := project.Resolve(configArg)
	if err != nil {
		return "", "", err
	}
	return resolved.ConfigPath, resolved.Root, nil
}
