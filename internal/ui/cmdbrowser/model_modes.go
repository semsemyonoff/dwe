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
	// If a matched item is currently highlighted, try to keep tree focus on
	// the nearest ancestor of that item in the restored tree.
	if it, ok := m.list.SelectedItem().(listItem); ok {
		g := groupOf(m.items[it.origIdx].ID)
		// Walk upward until we find a node that exists in the restored set.
		for g != "" {
			if _, exists := m.tree.nodesByID[g]; exists {
				m.tree.focusedID = g
				break
			}
			g = groupOf(g)
		}
	}
	m.filter.restoreExpansion(m.tree)
	m.filter = nil
	m.focus = focusRight
	m.refreshList()
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
		out = append(out, listItem{origIdx: idx, id: it.ID, desc: it.Description, typ: it.Type})
	}
	m.list.SetItems(out)
	if m.opts.AutoCollapseEmpty {
		m.filter.applyAutoCollapse(m.tree)
	}
}

// updateFilter handles keypresses while filter mode is active. Printable
// characters extend the query; Backspace trims it; Esc exits; Enter selects;
// arrow keys move the list cursor; everything else is forwarded to the list.
func (m *Model) updateFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Cancel) {
		m.exitFilter()
		return m, nil
	}
	if key.Matches(msg, m.keys.Enter) {
		if it, ok := m.list.SelectedItem().(listItem); ok {
			m.result = Result{Idx: it.origIdx, Action: actionForMode(m.opts.Mode), SkipConfirm: m.skipConfirm}
			return m, tea.Quit
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Inspect) && m.opts.Mode == ModeInspect {
		// Inspect inside filter: leave filter session intact so Esc still
		// restores expansion when the overlay closes.
		m.openInspect()
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
	// Forward navigation keys to the list (j/k/arrows/PgUp/PgDn/Home/End).
	if key.Matches(msg, m.keys.Up) || key.Matches(msg, m.keys.Down) ||
		key.Matches(msg, m.keys.PgUp) || key.Matches(msg, m.keys.PgDn) ||
		key.Matches(msg, m.keys.Home) || key.Matches(msg, m.keys.End) {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	// Treat printable text as filter input. Use msg.Text (set for keys that
	// produce a printable rune); ignore control keys and unset Text.
	if t := msg.Text; t != "" && isPrintable(t) {
		if m.filter != nil {
			m.filter.query += t
			m.refreshFilterMatches()
		}
		return m, nil
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
	rw := rightWidth(m.width)
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
// highlighted command, or -1 when nothing is selected. Used to seed the
// inspect viewport regardless of focus / filter mode.
func (m *Model) selectedItemIdx() int {
	if it, ok := m.list.SelectedItem().(listItem); ok {
		return it.origIdx
	}
	return -1
}
