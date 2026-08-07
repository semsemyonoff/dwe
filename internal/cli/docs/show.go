package docs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	coredocs "github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/docs/render"
	pipeline "github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

type docsShowFlags struct {
	lang    string
	raw     bool
	source  string
	anchors bool
	toc     bool
}

func newDocsShowCmd(flags *cmdctx.RootFlags) *cobra.Command {
	df := &docsShowFlags{}

	cmd := &cobra.Command{
		Use:   "show <topic>",
		Short: "Show documentation for a topic",
		Long: `Display documentation for a specific topic in markdown format.

In a TTY, renders the markdown with syntax highlighting and colors.
In a pipe or with --raw, outputs plain markdown.

Topics are matched case-insensitively with fuzzy substring matching if exact match fails.
When more than one topic matches and there is no unique closest segment, the command
lists the candidates and exits with an error — pass a more specific path to
disambiguate. Common examples:
  - Multi-page topics like "config/services" are ambiguous on their own;
    pass the specific sub-page, e.g. "config/services/index",
    "config/services/fields", or "config/services/examples".

Append "#anchor" to scope output to a single section. The anchor is the GitHub-style heading
slug (lower-case, spaces become hyphens, punctuation dropped). Anchors are language-specific
(the slug is derived from the heading text); always pass --lang en together with an English
anchor or your locale will not have it. Unknown anchors list the available slugs in the
document.

Use --anchors to list the slug of every H2/H3 heading in the topic (one per line),
or --toc for a level/slug/text TSV. Both bypass the body and are useful for
scoping a follow-up "docs show topic#anchor" without reading the whole document.

This command always emits markdown — the global --output json flag is ignored
here, since the document is the payload.

Examples:
  dwe docs show config/services/index
  dwe docs show config/workspace#binary-overrides --lang en
  dwe docs show config/services/fields --lang en
  dwe docs show config/services/fields --anchors --lang en
  dwe docs show config/services/fields --toc --lang en`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsShow(cmd, flags, df, args[0])
		},
		SilenceUsage: true,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			// completionConfigPath must be called first: __complete bypasses PersistentPreRunE.
			_, projectRoot, err := cmdctx.CompletionConfigPath(flags, cmd)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			roots := coredocs.Sources(projectRoot)

			// Use a nil-safe translator for completion paths
			tr := i18n.TranslatorOrNop(flags.I18n)
			_ = tr // Unused here; available for future i18n of completion output

			topics := coredocs.AllTopics(roots, "en")
			var completions []string
			for _, topic := range topics {
				completions = append(completions, topic.Path)
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
	}

	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from --lang flag / userconfig / $LANG / en)")
	cmd.Flags().BoolVar(&df.raw, "raw", false, "Output raw markdown (no syntax highlighting, even in TTY)")
	cmd.Flags().StringVar(&df.source, "source", "all", "Doc source: dwe, project, or all (default: all)")
	cmd.Flags().BoolVar(&df.anchors, "anchors", false, "Print available anchor slugs (one per line) and exit")
	cmd.Flags().BoolVar(&df.toc, "toc", false, "Print table of contents (level\\tslug\\ttext, TSV) and exit")

	return cmd
}

func runDocsShow(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsShowFlags, topic string) error {
	// Load project config for mermaid settings; tolerate missing config.
	cfg, err := config.LoadConfig(rflags.ConfigPath)
	if err != nil {
		cfg = &config.DweConfig{}
	}

	projectRoot := rflags.ProjectRoot()
	cfgLang := docsCfgLang(projectRoot)

	// Parse topic to extract anchor
	topicPath, anchor, err := coredocs.ParseTopic(topic)
	if err != nil {
		return fmt.Errorf("invalid topic: %w", err)
	}

	allRoots := coredocs.Sources(projectRoot)

	roots := filterDocRoots(allRoots, df.source)
	if len(roots) == 0 {
		return fmt.Errorf("no documentation sources available with --source=%s", df.source)
	}

	// Resolve the locale: use --lang flag, fallback to userconfig, then env
	locale := i18n.ResolveLocale(df.lang, cfgLang, os.Getenv("LANG"))

	// Resolve topic across roots
	resolved, err := coredocs.Resolve(roots, topicPath, locale)
	if err != nil {
		// Extract suggestions for user-friendly error messages
		switch e := err.(type) {
		case *coredocs.NotFoundError:
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: topic %q not found\n", e.Topic)
			if len(e.Suggestions) > 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Did you mean: %s\n", strings.Join(e.Suggestions, ", "))
			}
		case *coredocs.MultipleMatchesError:
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: ambiguous topic %q\n", e.Topic)
			if e.Total > len(e.Candidates) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Candidates (showing %d of %d):\n", len(e.Candidates), e.Total)
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Candidates:\n")
			}
			for _, c := range e.Candidates {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", c)
			}
			if e.Total > len(e.Candidates) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  ... and %d more\n", e.Total-len(e.Candidates))
			}
		default:
			return err
		}
		return pipeline.ErrSilent
	}

	// Find the root that contains the resolved topic
	sourceRoot := coredocs.RootByName(roots, resolved.Source)

	// Resolve content with language fallback and staleness check
	content, sourceLang, isStale, err := coredocs.ResolveContent(sourceRoot, resolved.Path+".md", locale)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", resolved.Path, err)
	}

	// --anchors / --toc short-circuit content rendering: they return structural
	// metadata so an agent can decide which section to pull next, without
	// reading the (potentially long) document body. Mutually exclusive — flag
	// validation below picks --toc when both are set.
	if df.anchors || df.toc {
		if df.toc {
			for _, h := range coredocs.ParseHeadingSlugs(content) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\t%s\n", h.Level, h.Slug, h.Text)
			}
		} else {
			seen := make(map[string]struct{})
			for _, h := range coredocs.ParseHeadingSlugs(content) {
				if _, ok := seen[h.Slug]; ok {
					continue
				}
				seen[h.Slug] = struct{}{}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), h.Slug)
			}
		}
		return nil
	}

	// If an anchor was supplied, scope the content to that section so the
	// output is the requested sub-tree, not the whole file. Banners (below)
	// still apply to the sliced view.
	if anchor != "" {
		sliced, matched, anchors, ok := coredocs.SliceByAnchor(content, anchor)
		if !ok {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: anchor %q not found in %s\n", anchor, resolved.Path)
			if len(anchors) > 0 {
				shown := anchors
				more := 0
				if len(shown) > coredocs.MaxAmbiguousCandidates {
					shown = shown[:coredocs.MaxAmbiguousCandidates]
					more = len(anchors) - coredocs.MaxAmbiguousCandidates
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Available anchors:\n")
				for _, a := range shown {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", a)
				}
				if more > 0 {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  ... and %d more\n", more)
				}
			}
			return pipeline.ErrSilent
		}
		_ = matched
		content = sliced
	}

	// Add translation status banners if applicable
	var contentWithBanners []byte
	switch {
	case sourceLang != locale:
		// Missing translation banner
		banner := "> **ℹ Note:** This file is not available in `" + locale + "`. Showing English version.\n\n"
		contentWithBanners = append(append(contentWithBanners, []byte(banner)...), content...)
	case isStale:
		// Stale translation banner
		banner := "> **⚠ Warning:** This translation is outdated. Use `--lang en` to view the English version.\n\n"
		contentWithBanners = append(append(contentWithBanners, []byte(banner)...), content...)
	default:
		contentWithBanners = content
	}

	// Determine if we're in a TTY and should render
	isInteractive := term.IsTerminal(os.Stdout.Fd())
	shouldRender := isInteractive && !df.raw

	emitLongDocHint(cmd, resolved.Path, content, anchor, isInteractive)

	if !shouldRender {
		_, _ = cmd.OutOrStdout().Write(contentWithBanners)
		return nil
	}

	// Render with glamour
	theme := render.ThemeFromBackground()
	termWidth := getTermWidth()

	placeholderFunc := buildShowMermaidPlaceholder(cmd.Context(), cfg, contentWithBanners, theme, termWidth)
	result, err := render.Render(contentWithBanners, render.Opts{
		Theme: theme,
		Width: termWidth,
	}, placeholderFunc)
	if err != nil {
		return fmt.Errorf("failed to render: %w", err)
	}

	_, _ = cmd.OutOrStdout().Write(result.Output)

	return nil
}

// Thresholds for emitLongDocHint. Deliberately high: the hint must read as a
// property of *this* document being large, not as a banner on every read.
const (
	longDocMinLines    = 120
	longDocMinSections = 4
)

// emitLongDocHint writes a one-line stderr note when a whole long document is
// piped somewhere, naming the flags that would have fetched just the wanted
// part.
//
// This exists because documenting the flags was measurably not enough. In an
// observed agent session the mandatory `dwe docs llms-txt` briefing — read in
// full as the very first command — carries the line "`--toc` / `--anchors` …
// use these instead of piping through `head`/`sed`", and the same session then
// piped 17 of 17 `docs show` calls through `head`/`sed`. A briefing read once
// at the start does not govern behaviour fifteen minutes later; a note at the
// point of use does.
//
// Three gates keep it from becoming noise:
//   - only when no anchor was requested — asking for a section is already the
//     behaviour the hint teaches;
//   - only when stdout is NOT a terminal, i.e. the output is being piped or
//     captured. A human reading in a terminal is not nagged;
//   - only for documents long and structured enough to be worth slicing.
//
// It goes to stderr specifically so that `docs show x | head` — the exact shape
// this addresses — still delivers it, since the pipe truncates stdout only.
func emitLongDocHint(cmd *cobra.Command, path string, content []byte, anchor string, isInteractive bool) {
	if anchor != "" || isInteractive {
		return
	}
	lines := bytes.Count(content, []byte("\n")) + 1
	if lines < longDocMinLines {
		return
	}
	sections := len(coredocs.ParseHeadingSlugs(content))
	if sections < longDocMinSections {
		return
	}
	sharedrender.NewWriter(cmd.ErrOrStderr()).Info(fmt.Sprintf(
		"%s is %d lines in %d sections. `dwe docs show %s --toc` lists them; "+
			"`dwe docs show '%s#<anchor>'` prints one.",
		path, lines, sections, path, path,
	))
}

// filterDocRoots filters documentation sources by the --source flag.
func filterDocRoots(roots []coredocs.DocRoot, sourceFlag string) []coredocs.DocRoot {
	switch sourceFlag {
	case "dwe":
		for i, r := range roots {
			if r.Name == "dwe" {
				return roots[i : i+1]
			}
		}
		return nil
	case "project":
		for i, r := range roots {
			if r.Name == "project" {
				return roots[i : i+1]
			}
		}
		return nil
	case "all":
		return roots
	default:
		return nil
	}
}

// getTermWidth attempts to determine the terminal width; defaults to 100.
func getTermWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return 100
	}
	return w
}

// buildShowMermaidPlaceholder always returns nil so PreprocessMermaid leaves
// mermaid fenced blocks verbatim. `dwe docs show` is meant for piping /
// scripting; rendering diagrams to PNG and showing a "cached" placeholder
// added noise without ever displaying the diagram. The interactive
// `dwe docs` TUI is the place to view rendered diagrams.
func buildShowMermaidPlaceholder(_ context.Context, _ *config.DweConfig, _ []byte, _ string, _ int) render.PlaceholderFunc {
	return nil
}
