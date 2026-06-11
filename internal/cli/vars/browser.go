package vars

import (
	"errors"
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	"github.com/semsemyonoff/dwe/internal/core/ui/cmdbrowser"
	uirender "github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"

	"github.com/spf13/cobra"
)

// Test seams — overridden in browser_test.go to drive the TUI and the
// interactivity probe deterministically. Subtests that override these MUST NOT
// call t.Parallel() (global state across goroutines).
var (
	runBrowser    = cmdbrowser.Run
	isInteractive = widgets.IsInteractiveFn
)

// runVarsBrowser is the no-arg interactive entry point. It builds one
// cmdbrowser.Item per vars leaf (the dot-path drives the namespace tree),
// runs the browser in ModeEdit, and on a selection opens the `set` form for
// the chosen var via the shared write path. After a successful edit it reloads
// the config and re-runs the browser so the next edit sees the just-written
// value. Quitting the browser (Esc/q/Ctrl-C) exits cleanly.
//
// Non-interactive and container callers never reach this — the bare-command
// dispatch routes them to `vars list` first.
func runVarsBrowser(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	for {
		cfg, err := loadConfigForVars(flags)
		if err != nil {
			return err
		}
		items, leaves := buildVarsBrowserItems(cfg, flags)
		if len(items) == 0 {
			// Nothing to browse — fall back to the (empty) list so the user gets
			// the same "no vars" surface as `vars list` rather than an empty TUI.
			return runVarsList(cmd, flags, "")
		}

		title := uirender.BrandedSelectorTitle(cfg.Project.Name, "Vars")
		opts := cmdbrowser.Options{
			DefaultExpandedDepth: config.UICommandsDefaultDepth(cfg),
			AutoCollapseEmpty:    config.UICommandsAutoCollapseEmpty(cfg),
			ShowTypeBadges:       config.UICommandsShowTypeBadges(cfg),
			Mode:                 cmdbrowser.ModeEdit,
		}
		res, err := runBrowser(title, items, opts)
		if err != nil {
			if errors.Is(err, widgets.ErrCancelled) {
				return nil
			}
			return err
		}
		if res.Idx < 0 || res.Idx >= len(leaves) {
			return fmt.Errorf("vars browser: result index %d out of range [0, %d)", res.Idx, len(leaves))
		}

		// Edit the selected leaf via the shared set write path (no value → the
		// huh form opens with inspect-style per-layer info). The narrow/short
		// fallback returns ActionRun rather than ActionEdit, so the intent is
		// taken from a valid Idx, not the Action.
		if err := runVarsSet(cmd, flags, leaves[res.Idx], "", false); err != nil {
			return err
		}
		// Loop: reload + rebuild so further edits build on the previous write.
	}
}

// buildVarsBrowserItems projects the merged vars leaves onto cmdbrowser.Items
// and returns the parallel leaf-path slice (Result.Idx indexes into it). Each
// Item carries the effective value as its description, the originating layer as
// the type badge, and an Inspect closure rendering the full inspect view.
func buildVarsBrowserItems(cfg *config.DweConfig, flags *cmdctx.RootFlags) ([]cmdbrowser.Item, []string) {
	leaves := varsusage.EnumerateVars(cfg)
	// Layers are read once and shared across every leaf's origin lookup, rather
	// than re-reading the three files per path. A load failure degrades the
	// badge to "" — the browser stays usable.
	layers, _ := config.LoadLayers(flags.ConfigPath)
	localPath := config.LocalLayerPath(flags.ConfigPath)

	items := make([]cmdbrowser.Item, 0, len(leaves))
	for _, leaf := range leaves {
		path := leaf // capture for the closures below
		value, _ := varsusage.ResolveVar(cfg, path)
		items = append(items, cmdbrowser.Item{
			ID:          path,
			Description: inlineBrowserValue(value),
			Type:        layerBadge(layers, localPath, path),
			Inspect: func(width int) string {
				return renderVarInspectFor(flags, path, width)
			},
		})
	}
	return items, leaves
}

// inlineBrowserValue renders a leaf value on a single line for the browser row
// description: scalars verbatim, composites flattened to space-separated YAML.
func inlineBrowserValue(value any) string {
	rendered, err := uirender.VarValue(value)
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(rendered), " ")
}

// renderVarInspectFor builds the inspect-view string for a single var at the
// given width, reusing the same resolution + scan + renderer as `vars inspect`.
// Resolution failures degrade to an empty string (the overlay shows a
// placeholder).
func renderVarInspectFor(flags *cmdctx.RootFlags, path string, width int) string {
	layered, err := config.ResolveLayeredPath(flags.ConfigPath, path)
	if err != nil {
		return ""
	}
	scan, _ := varsusage.ScanUsages(flags.ProjectRoot(), path)
	inspect := uirender.VarInspect{
		Path:        path,
		Author:      layered.Author,
		AuthorOK:    layered.AuthorOK,
		Local:       layered.Local,
		LocalOK:     layered.LocalOK,
		Effective:   layered.Effective,
		EffectiveOK: layered.EffectiveOK,
		Origin:      originDisplay(flags, layered.Origin),
		Usages:      scan.Usages,
	}
	return uirender.VarInspectView(inspect, width)
}
