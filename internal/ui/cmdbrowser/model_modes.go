package cmdbrowser

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// enterFilter snapshots the current expanded set and focused tree id, then
// switches focus to focusFilter. The right panel begins displaying ranked
// matches across the entire item set.
func (m *Model) enterFilter() {
	m.filter = newFilterState(m.tree.expanded, m.tree.focusedID)
	m.priorFocus = m.focus
	m.focus = focusFilter
	m.refreshFilterMatches()
}

// exitFilter discards filter state, restores the prior expanded set + focused
// tree id (per §8 cursor-restoration: keep cursor on the highlighted command's
// nearest ancestor when present in the original tree), and returns focus to
// the right panel so the user can keep navigating.
func (m *Model) exitFilter() {
	if m.filter == nil {
		return
	}
	// Track the target origIdx so we can re-position the list cursor after
	// refreshList rebuilds the items (SetItems preserves index, not identity).
	targetOrigIdx := -1
	if m.filter.query == "" {
		// Empty query: user entered and immediately Esc'd without typing.
		// The first item in the list is highlighted but that doesn't indicate
		// intent — restore the exact cursor position from before filter entry.
		m.tree.focusedID = m.filter.savedFocusID
	} else if it, ok := m.list.SelectedItem().(listItem); ok && !it.header {
		targetOrigIdx = it.origIdx
		// If a matched item is currently highlighted, try to keep tree focus on
		// the nearest ancestor of that item in the restored tree.
		g := groupOf(m.items[it.origIdx].ID)
		if g == "" {
			// Root-level command: focus the root node.
			m.tree.focusedID = ""
		} else {
			// Walk upward until we find a node that exists in the restored set.
			for g != "" {
				if _, exists := m.tree.nodesByID[g]; exists {
					m.tree.focusedID = g
					break
				}
				g = groupOf(g)
			}
		}
	}
	m.filter.restoreExpansion(m.tree)
	// After expansion is restored the saved state may have kept the focused
	// node's parent collapsed, leaving focusedID pointing to a hidden node.
	// Walk up to the nearest visible ancestor so the tree and right panel
	// stay consistent.
	if m.tree.focusedID != "" && m.tree.indexOfFocused() < 0 {
		m.tree.focusedID = m.tree.nearestVisibleAncestor(m.tree.focusedID)
	}
	m.filter = nil
	m.focus = focusRight
	// populateList routes to refreshSingleList in single-panel mode (flat list
	// with pseudo-headers) and to refreshList in two-panel mode. Calling
	// refreshList directly would emit only the focused-group items in
	// single-panel mode, producing a broken view after filter exit.
	m.populateList()
	// Re-position the list cursor on the item the user had highlighted in
	// filter mode. SetItems preserves the previous cursor index, not the item
	// identity, so without this the cursor lands on the wrong entry.
	// The !it.header guard prevents matching a pseudo-header whose origIdx
	// zero-value would collide with the real items[0] when targetOrigIdx == 0.
	if targetOrigIdx >= 0 {
		for i, li := range m.list.Items() {
			if it, ok := li.(listItem); ok && !it.header && it.origIdx == targetOrigIdx {
				m.list.Select(i)
				break
			}
		}
	}
}

// refreshFilterMatches re-ranks against the current query and rebuilds the
// right-panel list with the flat result. When AutoCollapseEmpty is true the
// tree is also re-expanded to show only subtrees containing matches.
func (m *Model) refreshFilterMatches() {
	if m.filter == nil {
		return
	}
	m.filter.recompute(m.items, m.opts.IncludePrivate)
	out := make([]list.Item, 0, len(m.filter.matched))
	for _, idx := range m.filter.matched {
		it := m.items[idx]
		out = append(out, listItem{origIdx: idx, id: it.ID, desc: it.Description, typ: it.Type, paramCount: it.ParamCount})
	}
	m.list.SetItems(out)
	if m.opts.AutoCollapseEmpty {
		m.filter.applyAutoCollapse(m.tree)
	}
}

// updateFilter handles keypresses while filter mode is active. Printable
// characters extend the query; Backspace trims it; Esc exits; Enter selects;
// arrow keys move the list cursor (vi-keys j/k type into the query instead).
func (m *Model) updateFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Enter) {
		if it, ok := m.list.SelectedItem().(listItem); ok && !it.header {
			m.result = Result{Idx: it.origIdx, Action: actionForMode(m.opts.Mode), SkipConfirm: m.skipConfirm}
			return m, tea.Quit
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Inspect) {
		// Inspect inside filter: leave filter session intact so Esc still
		// restores expansion when the overlay closes.
		m.openInspect()
		return m, nil
	}
	if key.Matches(msg, m.keys.SkipConfirm) && m.opts.Mode == ModeRun {
		m.skipConfirm = !m.skipConfirm
		return m, nil
	}
	if key.Matches(msg, m.keys.Backspace) {
		if m.filter != nil && len(m.filter.query) > 0 {
			runes := []rune(m.filter.query)
			m.filter.query = string(runes[:len(runes)-1])
			m.refreshFilterMatches()
		}
		return m, nil
	}
	// Treat printable text as filter input before checking Cancel or navigation
	// so that "q", "j", "k" and similar keys type into the query rather than
	// acting as quit/vi-navigation shortcuts. Non-printable keys (esc, arrows,
	// ctrl sequences) have empty or non-printable msg.Text and fall through.
	if t := msg.Text; t != "" && isPrintable(t) {
		if m.filter != nil {
			m.filter.query += t
			m.refreshFilterMatches()
		}
		return m, nil
	}
	// Esc exits filter. "q" is consumed by the printable branch above.
	if key.Matches(msg, m.keys.Cancel) {
		m.exitFilter()
		return m, nil
	}
	// Forward arrow navigation keys only (j/k are printable and handled above).
	switch msg.Code {
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

// isPrintable reports whether s is a single visible character that should
// extend the filter query. Multi-byte runes (UTF-8) are allowed; control
// characters (tab, esc, etc.) are not.
func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return s != ""
}

// openInspect swaps the focus to the inspect viewport. Behaviour differs
// slightly between modes: in ModeRun the right panel must hold a selected
// item; in filter mode we use the currently-highlighted match.
func (m *Model) openInspect() {
	idx := m.selectedItemIdx()
	if idx < 0 {
		return
	}
	content := m.items[idx].Inspect
	if strings.TrimSpace(content) == "" {
		content = "(no inspect content available)"
	}
	// Pass the inspect viewport the **inner content width** (frame - 2 borders),
	// matching the right panel's content area — newInspectState shrinks it
	// further to fit the inspect header / padding.
	rw := rightWidth(m.width) - 2
	if singlePanel(m.width) {
		rw = singlePanelWidth(m.width) - 2
	}
	bh := max(m.height-3, 5)
	m.inspect = newInspectState(rw, bh-2, content, idx)
	m.priorFocus = m.focus
	m.focus = focusInspect
}

// closeInspect tears down the overlay and returns focus to the prior panel.
func (m *Model) closeInspect() {
	m.inspect = nil
	if m.priorFocus == focusFilter {
		m.focus = focusFilter
	} else {
		m.focus = focusRight
	}
}

// updateInspect routes keys while the viewport overlay is open. Enter closes
// the program with an ActionInspect / ActionRun result; Esc closes the
// overlay; everything else is delegated to the viewport for scrolling.
func (m *Model) updateInspect(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.inspect == nil {
		return m, nil
	}
	if key.Matches(msg, m.keys.Cancel) {
		m.closeInspect()
		return m, nil
	}
	if key.Matches(msg, m.keys.Enter) {
		idx := m.inspect.inspectIdx
		m.result = Result{Idx: idx, Action: actionForMode(m.opts.Mode), SkipConfirm: m.skipConfirm}
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.inspect.vp, cmd = m.inspect.vp.Update(msg)
	return m, cmd
}

// selectedItemIdx returns the original-items index of the currently-
// highlighted command, or -1 when nothing is selected or a header row is
// focused. Used to seed the inspect viewport regardless of focus / filter mode.
func (m *Model) selectedItemIdx() int {
	if it, ok := m.list.SelectedItem().(listItem); ok {
		if it.header {
			return -1
		}
		return it.origIdx
	}
	return -1
}
