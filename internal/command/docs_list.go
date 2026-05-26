package command

import (
	"fmt"
	"os"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docs"
	"devbox-cli/internal/i18n"
	"devbox-cli/internal/userconfig"

	"github.com/spf13/cobra"
)

type docsListFlags struct {
	lang   string
	source string
}

func newDocsListCmd(flags *rootFlags) *cobra.Command {
	df := &docsListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all available documentation topics",
		Long: `List all available documentation topics.

Output is tab-separated for easy parsing by scripts and agents:
  <source>\t<path>\t<lang>

Examples:
  devbox docs list
  devbox docs list --lang ru
  devbox docs list --source devbox`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsList(cmd, flags, df)
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from --lang flag / userconfig / $LANG / en)")
	cmd.Flags().StringVar(&df.source, "source", "all", "Doc source: devbox, project, or all (default: all)")

	return cmd
}

func runDocsList(cmd *cobra.Command, rflags *rootFlags, df *docsListFlags) error {
	// Determine project root and load sources
	projectRoot := rflags.ProjectRoot()
	allRoots := docs.Sources(projectRoot)

	// Filter by --source flag
	roots := filterDocRoots(allRoots, df.source)
	if len(roots) == 0 {
		// No docs available for this source; output nothing (not an error)
		return nil
	}

	// Load user config to get the configured language
	var cfgLang string
	if projectRoot != "" {
		ucfg, err := userconfig.Load(projectRoot)
		if err == nil && ucfg != nil {
			cfgLang = ucfg.Language
		}
	}

	// Resolve the locale
	locale := i18n.ResolveLocale(df.lang, cfgLang, os.Getenv("LANG"))
	_ = config.DevboxConfig{} // Use config import

	// Get all topics
	topics := docs.AllTopics(roots, locale)

	// Output tab-separated format: source\tpath\tlang
	for _, topic := range topics {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", topic.Source, topic.Path, topic.Lang)
	}

	return nil
}
