// Package docs hosts the `dwe docs` command tree.
package docs

import (
	"os"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	coredocs "github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/docs/mermaid"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/core/ui/docstui"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"golang.org/x/term"

	"github.com/spf13/cobra"
)

type docsFlags struct {
	output         string
	lang           string
	includePrivate bool
}

// NewCmd builds the `dwe docs` command tree.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Browse and manage documentation",
		Long: `Browse and manage dwe documentation.

View documentation interactively with a TUI browser or display specific topics.
Generate reference documentation for the declarative command registry.`,
		GroupID:      groupID,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// No TTY for the docs browser (pipe / CI) or forced
			// non-interactive (DWE_NONINTERACTIVE=1 — the bridge daemon sets
			// it for every container invocation): print the `docs list`
			// output instead of erroring, mirroring bare `dwe commands`.
			if !term.IsTerminal(int(os.Stdout.Fd())) || cmdctx.NonInteractiveEnv() {
				// source "all" mirrors the list subcommand's flag default.
				return runDocsList(cmd, flags, &docsListFlags{source: "all"})
			}

			return runDocsTUI(cmd, flags)
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

func runDocsTUI(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
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

	// Build mermaid renderer chain based on config.
	cacheDir, err := mermaid.CacheDir()
	if err != nil {
		cacheDir = ""
	}

	cacheCapBytes := int64(config.MermaidCacheSizeMB(cfg) * 1024 * 1024)
	mermaidMode := config.MermaidMode(cfg)
	mmdcOnPath := mmdcAvailable(config.MmdcBin(cfg))

	var renderer mermaid.Renderer
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
		// what users see today; the mmdcMissingNotice below feeds the
		// diagram error overlay (`E`) so users learn how to install mmdc
		// on demand.
		renderer = mermaid.Disabled{}
	case mermaidMode == "mmdc":
		// Strict mode: mmdc is required
		renderer = mermaid.New(config.MmdcBin(cfg), cacheDir, cacheCapBytes, true)
	default: // "auto"
		renderer = mermaid.New(config.MmdcBin(cfg), cacheDir, cacheCapBytes, false)
	}

	sources := coredocs.Sources(projectRoot)
	translator := i18n.TranslatorOrNop(flags.I18n)
	ctx := cmd.Context()

	title := render.BrandedTitleForConfig(cfg, "Documentation")

	// Diagram-error overlay notice: when mmdc is missing on $PATH (and the user
	// hasn't explicitly disabled mermaid), diagram placeholders render as
	// "rendering disabled" and advertise `E`. Pressing `E` surfaces this text in
	// the render-error overlay so users learn how to install mmdc on demand,
	// instead of a global banner nagging on every topic. Points at the canonical
	// install section in docs/reference/docs/commands.md.
	var mmdcMissingNotice string
	if mermaidMode != "off" && !mmdcOnPath {
		mmdcMissingNotice = "mmdc is not installed, so Mermaid diagrams cannot render.\n\n" +
			"Install it with:\n\n" +
			"    npm i -g @mermaid-js/mermaid-cli\n\n" +
			"See docs/reference/docs/commands.md § Installing mmdc for details."
	}

	return docstui.Run(ctx, docstui.Options{
		Roots:             sources,
		Renderer:          renderer,
		ProjectRoot:       projectRoot,
		MermaidTheme:      mermaidTheme,
		Title:             title,
		Locale:            locale,
		Translator:        translator,
		MmdcMissingNotice: mmdcMissingNotice,
	})
}
