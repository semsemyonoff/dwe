package statustui

import (
	"context"

	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// statusTitleBase is the base label of the status-line brand string. The full
// left-zone brand is composed via render.BrandedTitleForConfig (the shared TUI
// title helper) so the dashboard advertises itself identically to the docs
// browser and command browser ("{▪} DWE · <project> · Status"), passed whole as
// RunOptions.Brand.
const statusTitleBase = "Status"

// runStatusTUI is the package-level seam through which Run drives the tui
// framework. Tests swap it to exercise error-mapping paths without a real
// terminal; production uses tui.Run.
var runStatusTUI = tui.Run

// Run launches the status dashboard as a tui.Plugin on the shared framework
// Frame. A context is owned by Run and canceled by the plugin's Close()
// (invoked by tui.Run on every exit path) so in-flight buildTabs goroutines
// stop cleanly when the user quits.
//
// It returns:
//   - nil on a normal clean exit (user quit the dashboard).
//   - [tui.ErrNotTTY] when stdout is not a terminal — unreachable in practice
//     since the caller's shouldUseTUI already gates on TTY before calling Run.
//   - [tui.ErrTooNarrow] when the terminal is below the framework minimum
//     width; the caller falls back to a plain-text render.
//   - a wrapped panic error on a recovered tea panic.
func Run(ctx context.Context, d Deps) error {
	runCtx, cancel := context.WithCancel(ctx)
	m := newModel(d, runCtx)  // Frame owns geometry; sized via Plugin.ViewPanel
	p := newPlugin(m, cancel) // Close() calls cancel

	tr := d.Translator
	if tr == nil {
		tr = i18n.NopTranslator{}
	}

	_, err := runStatusTUI(p, tui.RunOptions{
		Brand:      render.BrandedTitleForConfig(d.Cfg, statusTitleBase),
		Mouse:      true,
		Translator: tr,
		Locale:     d.Locale,
	})
	return err
}
