package cmdbrowser

import (
	"os"

	"github.com/charmbracelet/x/term"

	"devbox-cli/internal/ui"
)

// Test seams. The real term.IsTerminal / term.GetSize and the package-private
// ui.runSelectFormFn cannot be stubbed from this package, so production code
// indirects through these variables. Tests reassign and restore via
// t.Cleanup.
var (
	isTerminalFn = func() bool { return term.IsTerminal(os.Stdout.Fd()) }

	terminalSizeFn = func() (w, h int, err error) {
		w, h, err = term.GetSize(os.Stdout.Fd())
		return w, h, err
	}

	runSelectorFn = ui.RunSelector
)

// runFallback delegates to the flat huh-backed selector. The Items are
// projected onto ui.SelectorItem (label + description). The returned index
// maps 1:1 into the original items slice, and the action is always ActionRun
// — the flat fallback cannot express inspect intent (the call site at
// command_cmd.go for ModeInspect still proceeds to inspect because it builds
// its own follow-up from the returned ID).
// includePrivate mirrors Options.IncludePrivate so private commands are
// excluded from the selector when the caller has not opted in.
func runFallback(title string, items []Item, includePrivate bool) (Result, error) {
	si := make([]ui.SelectorItem, 0, len(items))
	// selectorIdx maps selector-list position → original items index so that
	// Result.Idx always refers into the unfiltered slice, matching the contract
	// that two-panel mode provides.
	selectorIdx := make([]int, 0, len(items))
	for i, it := range items {
		if !includePrivate && it.Private {
			continue
		}
		si = append(si, ui.SelectorItem{Label: it.ID, Description: it.Description})
		selectorIdx = append(selectorIdx, i)
	}
	idx, err := runSelectorFn(title, si)
	if err != nil {
		return Result{}, err
	}
	return Result{Idx: selectorIdx[idx], Action: ActionRun}, nil
}
