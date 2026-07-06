package docstui

import (
	"context"
	"errors"
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/docs/mermaid"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// Options carries all configuration for the docs browser. It collects the
// inputs that the old NewModel constructor took (excluding termWidth/termHeight
// — the Frame owns sizing now) plus the MmdcMissingNotice side-channel that the
// old caller set directly on the Model.
type Options struct {
	Roots        []docs.DocRoot
	Renderer     mermaid.Renderer
	ProjectRoot  string
	MermaidTheme string
	Title        string
	Locale       string
	Translator   i18n.Translator
	// MmdcMissingNotice is the install guidance shown in the diagram render-error
	// overlay (opened with `E`) when mmdc is missing on $PATH. Empty means the
	// renderer is either working or explicitly disabled, so `E` shows nothing.
	MmdcMissingNotice string
}

// mermaidThemeResolverFn is the package-level seam for the mermaid-theme
// probe (auto → lipgloss.HasDarkBackground). Production uses resolveMermaidTheme;
// tests swap it to return a deterministic value and avoid a terminal-background
// probe (Decision #11).
var mermaidThemeResolverFn = resolveMermaidTheme

// runDocsTUI is the package-level seam through which Run drives the tui
// framework. Tests swap it to exercise error-mapping paths without a real
// terminal; production uses tui.Run.
var runDocsTUI = tui.Run

// Run launches the docs browser as a tui.Plugin on the shared framework Frame.
// It returns:
//   - nil on a normal clean exit (user quit the browser).
//   - [widgets.ErrCancelled] on a user-initiated quit via q / Esc / Ctrl-C.
//   - [tui.ErrNotTTY] when stdout is not a terminal (caller may fall back to
//     list output).
//   - a clean "terminal too small" error when the terminal is below the
//     framework minimum width.
//   - a wrapped panic error on a recovered tea panic.
//
// Note: the old caller used tea.WithContext(ctx) to make parent-context
// cancellation force-kill the program. tui.Run does NOT thread the context into
// the tea program. External parent-context cancellation no longer force-kills
// the browser; ctrl+c / q still terminate it cleanly via bubbletea's own signal
// handling, and Close() tears down the watcher + prefetch on every exit path.
func Run(ctx context.Context, opts Options) error {
	model, err := newModelFromOpts(ctx, opts)
	if err != nil {
		return fmt.Errorf("docstui: creating model: %w", err)
	}

	tr := opts.Translator
	if tr == nil {
		tr = i18n.NopTranslator{}
	}

	_, runErr := runDocsTUI(newBrowser(ctx, model), tui.RunOptions{
		Brand:      opts.Title,
		Mouse:      true,
		Translator: tr,
		Locale:     opts.Locale,
	})
	if runErr != nil {
		if errors.Is(runErr, tui.ErrTooNarrow) {
			return fmt.Errorf("terminal too small for the docs browser")
		}
		// ErrNotTTY, ErrCancelled, and wrapped panics are passed through unchanged.
		return runErr
	}
	return nil
}

// newModelFromOpts builds a *Model from the given Options. The mermaid theme
// is resolved through mermaidThemeResolverFn (swappable in tests per Decision
// #11) so the auto probe runs exactly once and the result is stored in
// model.Theme. Sizes are initialised to zero because the Frame supplies
// geometry later via Plugin.Resize and Update(WindowSizeMsg).
func newModelFromOpts(ctx context.Context, opts Options) (*Model, error) {
	// Resolve the theme before passing to NewModel so the seam overrides the
	// auto→HasDarkBackground probe for golden/unit tests. NewModel's own
	// resolveMermaidTheme is idempotent for the concrete values ("dark"/"light"),
	// so passing the already-resolved string is safe.
	resolvedTheme := mermaidThemeResolverFn(opts.MermaidTheme)

	model, err := NewModel(
		ctx,
		opts.Roots,
		opts.Locale,
		opts.Translator,
		opts.Renderer,
		0, 0, // Frame owns geometry; initial size is zero (set on first WindowSizeMsg)
		opts.ProjectRoot,
		opts.Title,
		resolvedTheme,
	)
	if err != nil {
		return nil, err
	}
	model.MmdcMissingNotice = opts.MmdcMissingNotice
	return model, nil
}
