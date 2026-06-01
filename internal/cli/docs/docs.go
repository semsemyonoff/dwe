// Package docs hosts the `devbox docs` command tree.
package docs

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	coredocs "github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/docs/mermaid"
	"github.com/semsemyonoff/dwe/internal/core/docs/tui"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/spf13/cobra"
)

type docsFlags struct {
	output         string
	format         string
	scope          string
	lang           string
	includeHidden  bool
	includePrivate bool
}

// docsSelectorTitle mirrors cli/command.SelectorTitle to avoid a cross-sibling
// cli import. The helper keeps the docs TUI title shape symmetrical with
// cmdbrowser without dragging cli/command into the docs package's import graph.
func docsSelectorTitle(projectName, base string) string {
	parts := []string{"Devbox"}
	if projectName != "" {
		parts = append(parts, projectName)
	}
	parts = append(parts, base)
	return strings.Join(parts, " · ")
}

// NewCmd builds the `devbox docs` command tree.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Browse and manage documentation",
		Long: `Browse and manage devbox documentation.

View documentation interactively with a TUI browser or display specific topics.
Generate reference documentation for the CLI and command registry.`,
		GroupID:      groupID,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if we're in a TTY
			if !term.IsTerminal(int(os.Stdout.Fd())) {
				return errors.New("devbox docs without arguments requires a TTY; use 'devbox docs show <topic>' or 'devbox docs list' for non-interactive use")
			}

			// Get terminal dimensions
			width, height, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				return fmt.Errorf("failed to get terminal size: %w", err)
			}

			return runDocsTUI(cmd, flags, width, height)
		},
	}
	cmd.AddCommand(newDocsShowCmd(flags))
	cmd.AddCommand(newDocsListCmd(flags))
	cmd.AddCommand(newDocsSearchCmd(flags))
	cmd.AddCommand(newDocsExportCmd(flags))
	cmd.AddCommand(newDocsCacheCmd(flags))
	cmd.AddCommand(newDocsGenerateCmd(flags))
	cmd.AddCommand(newDocsLlmsTxtCmd(flags))
	return cmd
}

func runDocsTUI(cmd *cobra.Command, flags *cmdctx.RootFlags, termWidth, termHeight int) error {
	// Load configuration for doc settings (mermaid config, etc.)
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		// If config fails to load, use defaults (docs still work without full config)
		cfg = &config.DweConfig{}
	}

	// Get project root, user config language, and mermaid theme override.
	projectRoot := flags.ProjectRoot()
	var cfgLang string
	var mermaidTheme string
	if ucfg, err := userpkg.Load(projectRoot); err == nil && ucfg != nil {
		cfgLang = ucfg.Language
		mermaidTheme = ucfg.MermaidTheme
	}

	// Resolve locale directly from config and environment — do NOT use flags.Locale
	// (it is clamped to the YAML i18n store, a different namespace from markdown translations).
	locale := i18n.ResolveLocale("", cfgLang, os.Getenv("LANG"))

	// Build mermaid renderer chain based on config
	var renderer mermaid.Renderer
	cacheDir, err := mermaid.CacheDir()
	if err != nil {
		cacheDir = ""
	}

	cacheCapBytes := int64(config.MermaidCacheSizeMB(cfg) * 1024 * 1024)
	mermaidMode := config.MermaidMode(cfg)
	mmdcOnPath := mmdcAvailable(config.MmdcBin(cfg))

	switch {
	case mermaidMode == "off":
		renderer = mermaid.Disabled{}
	case !mmdcOnPath:
		// mmdc is absent on $PATH. Wiring a FileCache → mmdc chain here
		// would have prefetch workers fail every queued diagram in a
		// rapid burst (each Render returns ErrMmdcNotAvailable after a
		// useless tempdir+write), and each fast tick re-runs
		// inlineDiagrams + viewport.SetContent on a doc with N
		// diagrams — compounding into a perceptible UI freeze on the
		// first heavy-diagram document. Substituting Disabled here
		// short-circuits the queue entirely (see applyTopicLoaded) and
		// keeps the "rendering disabled" placeholder consistent with
		// what users see today; the MmdcNotice banner below tells them
		// how to install mmdc.
		renderer = mermaid.Disabled{}
	case mermaidMode == "mmdc":
		// Strict mode: mmdc is required
		renderer = mermaid.New(config.MmdcBin(cfg), cacheDir, cacheCapBytes, true)
	default: // "auto"
		renderer = mermaid.New(config.MmdcBin(cfg), cacheDir, cacheCapBytes, false)
	}

	// Get sources (devbox + project docs)
	sources := coredocs.Sources(projectRoot)

	// Create translator for TUI strings
	translator := i18n.TranslatorOrNop(flags.I18n)

	// Create the model. Title shape matches cmdbrowser: "Devbox · <project> · Documentation".
	ctx := cmd.Context()
	projectName := ""
	if cfg != nil {
		projectName = cfg.Project.Name
	}
	title := docsSelectorTitle(projectName, "Documentation")
	model, err := tui.NewModel(ctx, sources, locale, translator, renderer, termWidth, termHeight, projectRoot, title, mermaidTheme)
	if err != nil {
		return fmt.Errorf("failed to create TUI model: %w", err)
	}

	// Banner: warn once at startup when mmdc is missing on $PATH (and the user
	// hasn't explicitly disabled mermaid). Skipping the install entirely would
	// leave users guessing why diagrams never render — the banner points them
	// at the canonical install section in docs/reference/docs/commands.md.
	if mermaidMode != "off" && !mmdcOnPath {
		model.MmdcNotice = "> **⚠ `mmdc` not installed.** Mermaid diagrams cannot render. " +
			"Install with `npm i -g @mermaid-js/mermaid-cli` — see " +
			"`docs/reference/docs/commands.md` § *Installing `mmdc`*.\n\n"
	}

	// Run via widgets.RunWithPromptHooks for proper signal handling
	runErr := widgets.RunWithPromptHooks(func() error {
		prog := tea.NewProgram(model, tea.WithContext(ctx))
		_, e := prog.Run()
		return e
	})

	if runErr != nil {
		if errors.Is(runErr, tea.ErrProgramPanic) {
			return runErr
		}
		if errors.Is(runErr, tea.ErrInterrupted) || errors.Is(runErr, tea.ErrProgramKilled) {
			return widgets.ErrCancelled
		}
		return runErr
	}

	return nil
}
