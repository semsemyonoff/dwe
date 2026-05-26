package command

import (
	"fmt"
	"os"
	"strings"

	"devbox-cli/internal/docs"
	"devbox-cli/internal/docs/render"
	"devbox-cli/internal/i18n"
	"devbox-cli/internal/userconfig"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

type docsShowFlags struct {
	lang   string
	raw    bool
	source string
}

func newDocsShowCmd(flags *rootFlags) *cobra.Command {
	df := &docsShowFlags{}

	cmd := &cobra.Command{
		Use:   "show <topic>",
		Short: "Show documentation for a topic",
		Long: `Display documentation for a specific topic in markdown format.

In a TTY, renders the markdown with syntax highlighting and colors.
In a pipe or with --raw, outputs plain markdown.

Topics are matched case-insensitively with fuzzy substring matching if exact match fails.
Examples:
  devbox docs show config/services
  devbox docs show config/services#section
  devbox docs show config/services --lang ru`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsShow(cmd, flags, df, args[0])
		},
		SilenceUsage: true,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			// Load sources for completion (tolerant of errors)
			projectRoot := flags.ProjectRoot()
			roots := docs.Sources(projectRoot)

			// Use a nil-safe translator for completion paths
			tr := i18n.TranslatorOrNop(flags.I18n)
			_ = tr // Unused here; available for future i18n of completion output

			topics := docs.AllTopics(roots, "en")
			var completions []string
			for _, topic := range topics {
				completions = append(completions, topic.Path)
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
	}

	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from --lang flag / userconfig / $LANG / en)")
	cmd.Flags().BoolVar(&df.raw, "raw", false, "Output raw markdown (no syntax highlighting, even in TTY)")
	cmd.Flags().StringVar(&df.source, "source", "all", "Doc source: devbox, project, or all (default: all)")

	return cmd
}

func runDocsShow(cmd *cobra.Command, rflags *rootFlags, df *docsShowFlags, topic string) error {
	// Load user config to get the configured language
	var cfgLang string
	projectRoot := rflags.ProjectRoot()
	if projectRoot != "" {
		ucfg, err := userconfig.Load(projectRoot)
		if err == nil && ucfg != nil {
			cfgLang = ucfg.Language
		}
	}

	// Parse topic to extract anchor
	topicPath, _, err := docs.ParseTopic(topic)
	if err != nil {
		return fmt.Errorf("invalid topic: %w", err)
	}

	// Load sources for documentation
	allRoots := docs.Sources(projectRoot)

	// Filter by --source flag
	roots := filterDocRoots(allRoots, df.source)
	if len(roots) == 0 {
		return fmt.Errorf("no documentation sources available with --source=%s", df.source)
	}

	// Resolve the locale: use --lang flag, fallback to userconfig, then env
	locale := i18n.ResolveLocale(df.lang, cfgLang, os.Getenv("LANG"))

	// Resolve topic across roots
	resolved, err := docs.Resolve(roots, topicPath, locale)
	if err != nil {
		// Extract suggestions for user-friendly error messages
		switch e := err.(type) {
		case *docs.NotFoundError:
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: topic %q not found\n", e.Topic)
			if len(e.Suggestions) > 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Did you mean: %s\n", strings.Join(e.Suggestions, ", "))
			}
		case *docs.MultipleMatchesError:
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: ambiguous topic %q\n", e.Topic)
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Candidates: %s\n", strings.Join(e.Candidates, ", "))
		default:
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
		}
		return err
	}

	// Find the root that contains the resolved topic
	var sourceRoot docs.DocRoot
	for _, r := range roots {
		if r.Name == resolved.Source {
			sourceRoot = r
			break
		}
	}

	// Resolve content with language fallback and staleness check
	content, sourceLang, isStale, err := docs.ResolveContent(sourceRoot, resolved.Path+".md", locale)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", resolved.Path, err)
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

	if !shouldRender {
		// Output raw markdown (no rendering)
		_, _ = cmd.OutOrStdout().Write(contentWithBanners)
		return nil
	}

	// Render with glamour
	theme := render.ThemeFromBackground()
	termWidth := getTermWidth()

	result, err := render.Render(contentWithBanners, render.Opts{
		Theme: theme,
		Width: termWidth,
	}, render.PlaceholderForDisabled)
	if err != nil {
		return fmt.Errorf("failed to render: %w", err)
	}

	// Write the rendered output
	_, _ = cmd.OutOrStdout().Write(result.Output)

	return nil
}

// filterDocRoots filters documentation sources by the --source flag.
func filterDocRoots(roots []docs.DocRoot, sourceFlag string) []docs.DocRoot {
	switch sourceFlag {
	case "devbox":
		for i, r := range roots {
			if r.Name == "devbox" {
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
		return roots // Default to all
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
