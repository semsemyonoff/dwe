package command

import (
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"github.com/spf13/cobra"
)

// registryIDCompletion returns a ValidArgsFunction that completes command IDs
// from the registry. When includePrivate is true, private AND hidden command
// IDs are returned — this path is used for the `inspect` flag, which is the
// documented escape hatch for users debugging why a command disappeared from
// listings. When includePrivate is false (public listing/run), both Private
// and Hidden command IDs are filtered out.
func registryIDCompletion(flags *cmdctx.RootFlags, includePrivate bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Only complete the first positional argument.
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, projectRoot, err := cmdctx.CompletionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, err := usercommands.LoadRegistryFromConfigPath(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		// Best-effort visibility application: ApplyVisibility is fail-open
		// (per-expression failures log via slog and treat the entry as
		// visible) so this never short-circuits completions on a bad hide
		// expression. A cfg load error still applies the pass with nil cfg
		// (tolerated, same as `commands list`): hide expressions fail open
		// to visible, while the env-only bridge gate keeps filtering the
		// container surface even on a broken project.
		cfg, _ := config.LoadConfig(configPath)
		_ = reg.ApplyVisibility(cfg, projectRoot)
		var defs []*usercommands.CommandDef
		if includePrivate {
			// Inspect path: surface Hidden commands so users can tab-discover
			// the ID they need to debug. runbyid.go explicitly allows inspect
			// on hidden commands; this completion variant matches that contract.
			defs = reg.ListAllIncludingHidden("")
		} else {
			defs = reg.List("")
		}
		completions := make([]string, 0, len(defs)+1)
		if !includePrivate && len(defs) > 0 {
			// Active Help: hint for run subcommand.
			completions = cobra.AppendActiveHelp(completions, "Use 'dwe commands --inspect <id>' to see command details")
		}
		translator := i18n.TranslatorOrNop(flags.I18n)
		for _, d := range defs {
			completions = append(completions, commandCompletionEntry(d, translator, flags.Locale))
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

// commandCompletionEntry is one completion candidate: the ID, plus the
// translated summary when there is one. The summary is single-line and
// tab-free — the shell reads one candidate per line and a tab separates the
// value from its description.
func commandCompletionEntry(d *usercommands.CommandDef, translator i18n.Translator, locale string) string {
	desc := usercommands.SummaryLine(translator.CommandDescription(locale, d.ID, d.Description))
	if desc == "" {
		return d.ID
	}
	return cobra.CompletionWithDesc(d.ID, desc)
}
