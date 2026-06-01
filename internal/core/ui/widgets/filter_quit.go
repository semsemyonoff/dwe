package widgets

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// newFilterAwareQuit configures the form Quit binding for filter-aware
// behavior and returns the filter function to install via tea.WithFilter.
//
// huh matches the form-level keymap.Quit BEFORE the active field sees the
// keypress, so a static binding that includes q or esc would intercept the
// keys the filter input needs (q as a typed character, esc as ClearFilter).
// setQuit is invoked on three transitions:
//
//   - filter activation (`/`): swap Quit to quitNarrow (ctrl+c only) so q
//     types into the filter and esc clears/exits the filter via the field's
//     ClearFilter handler.
//   - filter exit (`enter` / `esc` while filtering): mark a pending restore.
//     We can NOT restore Quit on the same tick, because the exit key (esc)
//     would then match the just-restored Quit and abort the form instead of
//     letting the field handle ClearFilter.
//   - any subsequent key: apply the pending restore so q and esc go back to
//     quitting the form outside the filter.
//
// quitFull is the binding used outside the filter (ctrl+c, esc, q);
// quitNarrow is used while the filter is active (ctrl+c only).
func newFilterAwareQuit(setQuit func(key.Binding), quitFull, quitNarrow key.Binding) func(tea.Model, tea.Msg) tea.Msg {
	setQuit(quitFull)
	filterActive := false
	pendingRestore := false
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		keyMsg, ok := msg.(tea.KeyPressMsg)
		if !ok {
			return msg
		}
		if pendingRestore {
			setQuit(quitFull)
			pendingRestore = false
		}
		switch s := keyMsg.String(); {
		case !filterActive && s == "/":
			filterActive = true
			setQuit(quitNarrow)
		case filterActive && (s == "enter" || s == "esc"):
			filterActive = false
			pendingRestore = true
		}
		return msg
	}
}
