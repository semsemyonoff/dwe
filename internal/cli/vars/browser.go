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
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

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
			Translator:           i18n.TranslatorOrNop(flags.I18n),
			Locale:               flags.Locale,
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
		committed, err := runVarsSet(cmd, flags, leaves[res.Idx], "", false)
		if err != nil {
			return err
		}
		if committed {
			// Close the browser after a successful edit so the `✓ set …`
			// confirmation (printed by the write path) is the final thing on
			// screen. An aborted form leaves committed=false and falls through
			// to the loop, reopening the browser to pick another var or quit.
			return nil
		}
		// Form aborted — loop: reload + rebuild and reopen the browser.
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

	// inspectCache memoizes the width-independent resolve + usage scan per path
	// for the lifetime of this browser session. The Inspect closure is invoked
	// on every overlay open AND every resize (cmdbrowser applyLayout); without
	// this each call would re-read the three config layers and re-walk the
	// workspace usage scan — visible lag on larger workspaces. Only the final
	// width-dependent VarInspectView wrap re-runs per width. A reload after an
	// edit rebuilds items (and this cache), so stale values are not served.
	inspectCache := make(map[string]*uirender.VarInspect)

	items := make([]cmdbrowser.Item, 0, len(leaves))
	for _, leaf := range leaves {
		path := leaf // capture for the closures below
		value, _ := varsusage.ResolveVar(cfg, path)
		items = append(items, cmdbrowser.Item{
			// ID drives the namespace tree for DISPLAY; the `vars.` prefix is
			// stripped here (redundant under `dwe vars`). Resolution/editing uses
			// the parallel `leaves` slice via Result.Idx, NOT this ID, so the
			// canonical path is preserved where it matters.
			ID:          uirender.DisplayVarPath(path),
			Description: inlineBrowserValue(value),
			Type:        layerBadge(layers, localPath, path),
			Inspect: func(width int) string {
				return renderVarInspectCached(flags, inspectCache, path, width)
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

// renderVarInspectCached returns the inspect-view string for a single var at
// the given width, resolving + scanning it at most once per session (memoized
// in cache, keyed by path) and re-wrapping only the width-dependent
// VarInspectView per call. A resolution failure caches a nil entry so a broken
// layer set is not re-read on every resize; the overlay shows a placeholder.
func renderVarInspectCached(flags *cmdctx.RootFlags, cache map[string]*uirender.VarInspect, path string, width int) string {
	inspect, ok := cache[path]
	if !ok {
		inspect = resolveVarInspect(flags, path)
		cache[path] = inspect // may be nil (resolution failed) — cache the miss too
	}
	if inspect == nil {
		return ""
	}
	return uirender.VarInspectView(*inspect, width)
}

// resolveVarInspect performs the width-independent resolution + usage scan for
// a single var, reusing the same resolution + scan as `vars inspect`. Returns
// nil on a layer-resolution failure.
func resolveVarInspect(flags *cmdctx.RootFlags, path string) *uirender.VarInspect {
	layered, err := config.ResolveLayeredPath(flags.ConfigPath, path)
	if err != nil {
		return nil
	}
	scan, _ := varsusage.ScanUsages(flags.ProjectRoot(), path)
	return &uirender.VarInspect{
		Path:      path,
		Default:   layered.Default,
		DefaultOK: layered.DefaultOK,
		Local:     layered.Local,
		LocalOK:   layered.LocalOK,
		Current:   layered.Current,
		CurrentOK: layered.CurrentOK,
		Origin:    originDisplay(flags, layered.Origin),
		Usages:    scan.Usages,
	}
}
