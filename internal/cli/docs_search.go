package cli

import (
	"fmt"
	"os"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/docs"
	userpkg "devbox-cli/internal/core/project/user"
	"devbox-cli/internal/shared/i18n"

	"github.com/spf13/cobra"
)

type docsSearchFlags struct {
	lang   string
	source string
	limit  int
}

func newDocsSearchCmd(flags *cmdctx.RootFlags) *cobra.Command {
	df := &docsSearchFlags{}

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search documentation for a literal substring",
		Long: `Search every documentation topic for a case-insensitive literal substring
and emit the sections that contain it.

Output is tab-separated (one row per matching section):
  <source>\t<path>#<anchor>\t<count>

Sections are sorted by match count (desc), then by path. Lead text under the
H1 (before the first H2) is reported with an empty anchor. Matches inside
fenced code blocks are counted — that's where schema names usually appear.

Examples:
  devbox docs search depends_on
  devbox docs search 'RunContext.Render' --source devbox
  devbox docs search topo-sort --lang en --limit 5`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsSearch(cmd, flags, df, args[0])
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from --lang flag / userconfig / $LANG / en)")
	cmd.Flags().StringVar(&df.source, "source", "all", "Doc source: devbox, project, or all (default: all)")
	cmd.Flags().IntVar(&df.limit, "limit", 50, "Maximum result rows (0 = unlimited)")

	return cmd
}

func runDocsSearch(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsSearchFlags, query string) error {
	projectRoot := rflags.ProjectRoot()
	allRoots := docs.Sources(projectRoot)

	roots := filterDocRoots(allRoots, df.source)
	if len(roots) == 0 {
		return fmt.Errorf("no documentation sources available with --source=%s", df.source)
	}

	var cfgLang string
	if projectRoot != "" {
		ucfg, err := userpkg.Load(projectRoot)
		if err == nil && ucfg != nil {
			cfgLang = ucfg.Language
		}
	}
	locale := i18n.ResolveLocale(df.lang, cfgLang, os.Getenv("LANG"))

	hits := docs.Search(roots, query, locale)
	if df.limit > 0 && len(hits) > df.limit {
		hits = hits[:df.limit]
	}
	for _, h := range hits {
		topicRef := h.Path
		if h.Section != "" {
			topicRef = h.Path + "#" + h.Section
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%d\n", h.Source, topicRef, h.Count)
	}
	return nil
}
