package vars

import (
	"errors"
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
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
// cmdbrowser.Item per vars leaf (the dot-path drives the namespace tree) and
// runs the browser in ModeEdit with an EditSpec: on the ≥80-col frame path,
// Enter opens the `set` form as an in-TUI overlay (edit-and-stay) — the edit is
// written, the row refreshes in place with a status flash, and the browser
// stays up; the frame path only ever exits via widgets.ErrCancelled (→ nil).
//
// The narrow (<80-col) fallback has no overlay: it returns a Result with a valid
// Idx and ActionRun, so the loop still drives the old exit→form→reopen path
// through runVarsSet (reload + rebuild + reopen after a committed edit).
//
// Non-interactive and container callers never reach this — the bare-command
// dispatch routes them to `vars list` first.
func runVarsBrowser(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	for {
		cfg, err := loadConfigForVars(flags)
		if err != nil {
			return err
		}
		items, leaves, inspectCache := buildVarsBrowserItems(cfg, flags)
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
			Edit:                 newVarsEditSpec(flags, leaves, inspectCache),
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

		// Only the narrow fallback reaches here (the frame path handles edits
		// in-TUI and exits via ErrCancelled). Edit the selected leaf via the
		// shared set write path (no value → the huh form opens with inspect-style
		// per-layer info); the fallback returns ActionRun rather than ActionEdit,
		// so the intent is taken from a valid Idx, not the Action.
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

// newVarsEditSpec builds the cmdbrowser.EditSpec that drives in-TUI vars edits.
// BuildForm opens the shared `set` form (huh's own help line suppressed —
// ShowHelp:false — so the FormOverlay hint row is authoritative) for the
// selected leaf; Commit coerces the submitted value, writes it via the SILENT
// lock wrapper (a held-lock error surfaces as an in-TUI flash, never printed —
// the alt-screen is live), reloads config, invalidates the leaf's memoized
// inspect, and rebuilds the one row with a fresh value + badge + inspect closure.
// The closures capture leaves + inspectCache; idx indexes into leaves.
func newVarsEditSpec(flags *cmdctx.RootFlags, leaves []string, inspectCache map[string]*uirender.VarInspect) *cmdbrowser.EditSpec {
	showHelp := false
	return &cmdbrowser.EditSpec{
		BuildForm: func(idx int) (*ask.Form, error) {
			if idx < 0 || idx >= len(leaves) {
				return nil, fmt.Errorf("vars edit: index %d out of range [0, %d)", idx, len(leaves))
			}
			path := leaves[idx]
			disp := uirender.DisplayVarPath(path)
			return ask.Build("dwe vars › set "+disp, buildVarSetFields(flags, path),
				ask.RunOptions{ShowHelp: &showHelp})
		},
		Commit: func(idx int, res ask.Result) (cmdbrowser.CommitOutcome, error) {
			if idx < 0 || idx >= len(leaves) {
				return cmdbrowser.CommitOutcome{}, fmt.Errorf("vars edit: index %d out of range [0, %d)", idx, len(leaves))
			}
			path := leaves[idx]
			coerced, err := varsusage.CoerceScalar(res.String("value"))
			if err != nil {
				return cmdbrowser.CommitOutcome{}, err
			}
			newCfg, err := writeVarOverrideSilent(flags, path, coerced)
			if err != nil {
				return cmdbrowser.CommitOutcome{}, err
			}
			// Invalidate the memoized inspect so the row's overlay re-reads the
			// just-written value; the layers are re-read fresh so the badge
			// reflects local.yml now supplying the leaf.
			delete(inspectCache, path)
			layers, _ := config.LoadLayers(flags.ConfigPath)
			localPath := config.LocalLayerPath(flags.ConfigPath)
			item := buildVarBrowserItem(newCfg, flags, layers, localPath, inspectCache, path)
			return cmdbrowser.CommitOutcome{Item: item, Flash: varEditFlash(path, coerced)}, nil
		},
	}
}

// varEditFlash formats the success status flash for a committed edit, e.g.
// `✓ db.host = db.internal`. Value rendering mirrors the row description
// (uirender.VarValue), so the flash matches what the refreshed row shows.
func varEditFlash(path string, value any) string {
	disp := uirender.DisplayVarPath(path)
	rendered, err := uirender.VarValue(value)
	if err != nil {
		return "✓ " + disp
	}
	return fmt.Sprintf("✓ %s = %s", disp, strings.Join(strings.Fields(rendered), " "))
}

// buildVarsBrowserItems projects the merged vars leaves onto cmdbrowser.Items
// and returns the parallel leaf-path slice (Result.Idx indexes into it) plus the
// shared inspect cache. Each Item carries the effective value as its
// description, the originating layer as the type badge, and an Inspect closure
// rendering the full inspect view. The returned cache is threaded into the
// EditSpec so an in-TUI edit can invalidate the edited leaf's entry.
func buildVarsBrowserItems(cfg *config.DweConfig, flags *cmdctx.RootFlags) ([]cmdbrowser.Item, []string, map[string]*uirender.VarInspect) {
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
	// width-dependent VarInspectView wrap re-runs per width. An in-TUI edit
	// deletes the edited leaf's entry (see newVarsEditSpec) so a stale value is
	// never served after a write.
	inspectCache := make(map[string]*uirender.VarInspect)

	items := make([]cmdbrowser.Item, 0, len(leaves))
	for _, leaf := range leaves {
		items = append(items, buildVarBrowserItem(cfg, flags, layers, localPath, inspectCache, leaf))
	}
	return items, leaves, inspectCache
}

// buildVarBrowserItem builds one cmdbrowser.Item for a single vars leaf: the
// effective value as the row description, the originating layer as the type
// badge, and an Inspect closure (memoized via inspectCache). Shared by the
// initial projection and the post-edit in-place row refresh (newVarsEditSpec).
func buildVarBrowserItem(cfg *config.DweConfig, flags *cmdctx.RootFlags, layers []config.Layer, localPath string, inspectCache map[string]*uirender.VarInspect, path string) cmdbrowser.Item {
	value, _ := varsusage.ResolveVar(cfg, path)
	badge, encrypted := leafBadge(layers, cfg.SecretsState, localPath, path)
	state := cfg.SecretsState
	return cmdbrowser.Item{
		// ID drives the namespace tree for DISPLAY; the `vars.` prefix is
		// stripped here (redundant under `dwe vars`). Resolution/editing uses the
		// parallel `leaves` slice via Result.Idx, NOT this ID, so the canonical
		// path is preserved where it matters.
		ID:          uirender.DisplayVarPath(path),
		Description: inlineBrowserValue(value),
		Type:        uirender.VarLayerBadge(badge, encrypted),
		Inspect: func(width int) string {
			return renderVarInspectCached(flags, state, inspectCache, path, width)
		},
	}
}

// inlineBrowserValue renders a leaf value on a single line for the browser row
// description: scalars verbatim, composites flattened to space-separated YAML.
func inlineBrowserValue(value any) string {
	masked, _ := uirender.MaskSecretValue(value)
	rendered, err := uirender.VarValue(masked)
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
func renderVarInspectCached(flags *cmdctx.RootFlags, state config.SecretsState, cache map[string]*uirender.VarInspect, path string, width int) string {
	inspect, ok := cache[path]
	if !ok {
		inspect = resolveVarInspect(flags, state, path)
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
func resolveVarInspect(flags *cmdctx.RootFlags, state config.SecretsState, path string) *uirender.VarInspect {
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
		Secret:    secretNote(state, layered.Origin, path),
		Usages:    scan.Usages,
	}
}
