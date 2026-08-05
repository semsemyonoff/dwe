package docs

import (
	"fmt"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	coredocs "github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

type docsSearchFlags struct {
	lang    string
	source  string
	limit   int
	literal bool
}

// docsSearchResult is one JSON record in `dwe docs search --output json`.
// Field names form an agent-facing contract; keep stable.
type docsSearchResult struct {
	Source  string `json:"source"`
	Path    string `json:"path"`
	Anchor  string `json:"anchor"`
	Count   int    `json:"count"`
	Snippet string `json:"snippet"`
}

func newDocsSearchCmd(flags *cmdctx.RootFlags) *cobra.Command {
	df := &docsSearchFlags{}

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search documentation for all words of a query",
		Long: `Search every documentation topic and emit the sections that contain the query.

The query is split on whitespace and every word must be present in a section
(AND). Each word matches as a case-insensitive SUBSTRING, so identifiers work
(depends_on:, RunContext.Render) — at the cost of short words matching inside
longer ones ("uid" also matches "guide"). Use --literal to match the whole
query as one substring instead.

When no single section of a document holds every word, but the document as a
whole does, one row is emitted for that document, anchored at its densest
section. Those rows sort after the exact section matches.

Default output is tab-separated (one row per matching section):
  <source>\t<path>#<anchor>\t<count>\t<snippet>

With --output json, emits a JSON array of {source, path, anchor, count,
snippet} records (path and anchor are split; anchor is empty for lead text
under the H1 before the first H2/H3).

Sections are sorted by match count (desc), then by path; the count is the
rarest word's occurrences, not the total, so a section about the whole query
outranks one that merely repeats its commonest word. The snippet is the source
line carrying the most distinct words of the query, whitespace-collapsed and
truncated. Matches inside fenced code blocks are counted — that's where schema
names usually appear.

Examples:
  dwe docs search depends_on
  dwe docs search 'RunContext.Render' --source dwe --literal
  dwe docs search 'UID GID env' --lang en --limit 5`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsSearch(cmd, flags, df, args[0])
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from --lang flag / userconfig / $LANG / en)")
	cmd.Flags().StringVar(&df.source, "source", "all", "Doc source: dwe, project, or all (default: all)")
	cmd.Flags().IntVar(&df.limit, "limit", 50, "Maximum result rows (0 = unlimited)")
	cmd.Flags().BoolVar(&df.literal, "literal", false, "Match the whole query as one substring instead of splitting it into words")

	return cmd
}

func runDocsSearch(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsSearchFlags, query string) error {
	projectRoot := rflags.ProjectRoot()
	allRoots := coredocs.Sources(projectRoot)

	roots := filterDocRoots(allRoots, df.source)
	if len(roots) == 0 {
		return fmt.Errorf("no documentation sources available with --source=%s", df.source)
	}

	cfgLang := docsCfgLang(projectRoot)
	locale := i18n.ResolveLocale(df.lang, cfgLang, os.Getenv("LANG"))

	hits := coredocs.Search(roots, query, locale, coredocs.SearchOptions{Literal: df.literal})
	if df.limit > 0 && len(hits) > df.limit {
		hits = hits[:df.limit]
	}

	results := make([]docsSearchResult, 0, len(hits))
	for _, h := range hits {
		results = append(results, docsSearchResult{
			Source:  h.Source,
			Path:    h.Path,
			Anchor:  h.Section,
			Count:   h.Count,
			Snippet: h.Snippet,
		})
	}

	emitNoSearchMatches(cmd, rflags, df, query, locale, len(results))

	return cmdctx.WriteData(rflags, cmd, results, renderDocsSearchText)
}

// emitNoSearchMatches writes a single info line to stderr when a search found
// nothing. Without it a zero-result search is indistinguishable from a command
// that silently did nothing: text mode deliberately writes no stdout for an
// empty result set (see cmdctx.WriteData), and the exit code stays 0 because
// "no matches" is an outcome, not an error.
//
// The line names the two filters that most often cause a false empty result —
// --source (a non-"all" value hides whole doc trees) and the resolved locale
// (translations lag the English source, so a term may exist only in `--lang en`).
//
// No-op in JSON mode: there the empty array on stdout is already an unambiguous
// answer, and the notice would be noise for a parsing consumer.
func emitNoSearchMatches(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsSearchFlags, query, locale string, found int) {
	if found > 0 || rflags.Output == "json" {
		return
	}
	hint := "Every word of the query must appear in the same document — try dropping a word, " +
		"another --source, or --lang en."
	if df.literal {
		hint = "--literal matches the whole query as one substring — drop --literal to match " +
			"word by word, or try another --source or --lang en."
	}
	render.NewWriter(cmd.ErrOrStderr()).Info(fmt.Sprintf(
		"No documentation matches %q (searched --source=%s in locale %s). %s",
		query, df.source, locale, hint,
	))
}

// renderDocsSearchText renders the default TSV (one row per result):
//
//	<source>\t<path>#<anchor>\t<count>\t<snippet>
//
// When anchor is empty, only `<path>` is printed (no trailing '#').
// WriteData appends a single trailing newline; rows here are joined with '\n'
// so the on-the-wire format matches the prior per-row Fprintf("…\n").
//
// The snippet is a FOURTH column rather than JSON-only on purpose: without it
// every hit costs a second `docs show` call to find out whether it was worth
// following. The column is append-only — a consumer reading fields [0..2] is
// unaffected — and coredocs.Search has already stripped tabs and newlines from
// the snippet, so the row can never gain a fifth field.
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
		fmt.Fprintf(&sb, "%s\t%s\t%d\t%s", r.Source, topicRef, r.Count, r.Snippet)
	}
	return sb.String()
}
