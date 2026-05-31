package command

import (
	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"
	"github.com/semsemyonoff/devbox/internal/core/usercommands"
	"github.com/semsemyonoff/devbox/internal/shared/i18n"

	"github.com/spf13/cobra"
)

// registryIDCompletion returns a ValidArgsFunction that completes command IDs
// from the registry. When includePrivate is true, private command IDs are also
// returned (useful for `inspect`). When false, only public IDs are returned
// (useful for `run`).
func registryIDCompletion(flags *cmdctx.RootFlags, includePrivate bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Only complete the first positional argument.
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := cmdctx.CompletionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, err := usercommands.LoadRegistryFromConfigPath(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var defs []*usercommands.CommandDef
		if includePrivate {
			defs = reg.ListAll("")
		} else {
			defs = reg.List("")
		}
		completions := make([]string, 0, len(defs)+1)
		if !includePrivate && len(defs) > 0 {
			// Active Help: hint for run subcommand.
			completions = cobra.AppendActiveHelp(completions, "Use 'devbox commands --inspect <id>' to see command details")
		}
		translator := i18n.TranslatorOrNop(flags.I18n)
		for _, d := range defs {
			entry := d.ID
			desc := translator.CommandDescription(flags.Locale, d.ID, d.Description)
			if desc != "" {
				entry = cobra.CompletionWithDesc(d.ID, desc)
			}
			completions = append(completions, entry)
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}
