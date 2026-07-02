package statustui

import (
	"context"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// brand is the fixed left-zone status-line brand string; the Frame joins it
// with the project name (via RunOptions.Project) as "brand · project".
const brand = "dwe"

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
	m := newModel(d, runCtx, 0, 0) // Frame owns geometry; sized via Plugin.ViewPanel
	p := newPlugin(m, cancel)      // Close() calls cancel

	tr := d.Translator
	if tr == nil {
		tr = i18n.NopTranslator{}
	}

	_, err := runStatusTUI(p, tui.RunOptions{
		Brand:      brand,
		Project:    d.ProjectName,
		Mouse:      true,
		Translator: tr,
		Locale:     d.Locale,
	})
	return err
}
