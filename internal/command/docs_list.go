package command

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/core/docs"
	"devbox-cli/internal/shared/i18n"
	"devbox-cli/internal/userconfig"

	"github.com/spf13/cobra"
)

type docsListFlags struct {
	lang   string
	source string
	match  string
}

func newDocsListCmd(flags *cmdctx.RootFlags) *cobra.Command {
	df := &docsListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all available documentation topics",
		Long: `List all available documentation topics.

Output is tab-separated for easy parsing by scripts and agents:
  <source>\t<path>\t<lang>

The --match flag filters by topic path using shell-style globs. * matches
any characters within one path segment; ** spans separators (so reference/**
matches every nested topic under reference/).

Examples:
  devbox docs list
  devbox docs list --lang ru
  devbox docs list --source devbox
  devbox docs list --match 'reference/config/*'
  devbox docs list --match 'reference/commands/**'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsList(cmd, flags, df)
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from --lang flag / userconfig / $LANG / en)")
	cmd.Flags().StringVar(&df.source, "source", "all", "Doc source: devbox, project, or all (default: all)")
	cmd.Flags().StringVar(&df.match, "match", "", "Filter topics by shell-style glob on path (use ** to cross /)")

	return cmd
}

func runDocsList(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsListFlags) error {
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

	// Get all topics
	topics := docs.AllTopics(roots, locale)

	// Compile the glob once so an invalid pattern fails the command instead of
	// silently filtering nothing.
	matcher, err := compilePathGlob(df.match)
	if err != nil {
		return fmt.Errorf("--match: %w", err)
	}

	// Output tab-separated format: source\tpath\tlang
	for _, topic := range topics {
		if !matcher(topic.Path) {
			continue
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", topic.Source, topic.Path, topic.Lang)
	}

	return nil
}

// compilePathGlob builds a matcher for the --match flag. Semantics:
//
//   - empty pattern: match every path.
//   - `*` matches any run of non-`/` characters (one path segment).
//   - `**` matches across `/`, so `reference/**` covers every nested topic.
//   - `?` matches a single non-`/` character.
//
// Translated to an anchored regex (`^…$`); regex metacharacters in the
// pattern other than `*` and `?` are escaped so users can match topics with
// literal dots in their paths.
func compilePathGlob(pattern string) (func(string) bool, error) {
	if pattern == "" {
		return func(string) bool { return true }, nil
	}

	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			sb.WriteString(".*")
			i += 2
		case pattern[i] == '*':
			sb.WriteString("[^/]*")
			i++
		case pattern[i] == '?':
			sb.WriteString("[^/]")
			i++
		default:
			c := pattern[i]
			// Escape regex specials (everything that isn't a letter / digit /
			// underscore / hyphen / slash). path-friendly chars pass through.
			if !isGlobPlain(c) {
				sb.WriteByte('\\')
			}
			sb.WriteByte(c)
			i++
		}
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return nil, err
	}
	return re.MatchString, nil
}

func isGlobPlain(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '-' || c == '/'
}
