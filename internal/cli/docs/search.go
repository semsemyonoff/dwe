package docs

import (
	"fmt"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	coredocs "github.com/semsemyonoff/dwe/internal/core/docs"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"github.com/spf13/cobra"
)

type docsSearchFlags struct {
	lang   string
	source string
	limit  int
}

// docsSearchResult is one JSON record in `dwe docs search --output json`.
// Field names form an agent-facing contract; keep stable.
type docsSearchResult struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Anchor string `json:"anchor"`
	Count  int    `json:"count"`
}

func newDocsSearchCmd(flags *cmdctx.RootFlags) *cobra.Command {
	df := &docsSearchFlags{}

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search documentation for a literal substring",
		Long: `Search every documentation topic for a case-insensitive literal substring
and emit the sections that contain it.

Default output is tab-separated (one row per matching section):
  <source>\t<path>#<anchor>\t<count>

With --output json, emits a JSON array of {source, path, anchor, count}
records (path and anchor are split; anchor is empty for lead text under
the H1 before the first H2/H3).

Sections are sorted by match count (desc), then by path. Lead text under the
H1 (before the first H2) is reported with an empty anchor. Matches inside
fenced code blocks are counted — that's where schema names usually appear.

Examples:
  dwe docs search depends_on
  dwe docs search 'RunContext.Render' --source dwe
  dwe docs search topo-sort --lang en --limit 5`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsSearch(cmd, flags, df, args[0])
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from --lang flag / userconfig / $LANG / en)")
	cmd.Flags().StringVar(&df.source, "source", "all", "Doc source: dwe, project, or all (default: all)")
	cmd.Flags().IntVar(&df.limit, "limit", 50, "Maximum result rows (0 = unlimited)")

	return cmd
}

func runDocsSearch(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsSearchFlags, query string) error {
	projectRoot := rflags.ProjectRoot()
	allRoots := coredocs.Sources(projectRoot)

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

	hits := coredocs.Search(roots, query, locale)
	if df.limit > 0 && len(hits) > df.limit {
		hits = hits[:df.limit]
	}

	results := make([]docsSearchResult, 0, len(hits))
	for _, h := range hits {
		results = append(results, docsSearchResult{
			Source: h.Source,
			Path:   h.Path,
			Anchor: h.Section,
			Count:  h.Count,
		})
	}

	return cmdctx.WriteData(rflags, cmd, results, renderDocsSearchText)
}

// renderDocsSearchText renders the default TSV (one row per result):
//
//	<source>\t<path>#<anchor>\t<count>
//
// When anchor is empty, only `<path>` is printed (no trailing '#').
// WriteData appends a single trailing newline; rows here are joined with '\n'
// so the on-the-wire format matches the prior per-row Fprintf("…\n").
func renderDocsSearchText(results []docsSearchResult) string {
	var sb strings.Builder
	for i, r := range results {
		topicRef := r.Path
		if r.Anchor != "" {
			topicRef = r.Path + "#" + r.Anchor
		}
		if i > 0 {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(&sb, "%s\t%s\t%d", r.Source, topicRef, r.Count)
	}
	return sb.String()
}
