package statustui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// tabHitZone is the panel-local column range [start, end) a rendered tab
// label occupies in the tab strip.
type tabHitZone struct{ start, end int }

// tabHitZones mirrors renderTabStrip's layout exactly (leading 1-col pad, the
// ▌ ▐ decorations on the active tab, 3-space gaps between tabs) so click
// hit-zones match what is drawn.
func (m *model) tabHitZones() []tabHitZone {
	if len(m.tabs) == 0 {
		return nil
	}
	zones := make([]tabHitZone, len(m.tabs))
	col := 1 // leading " " pad
	for i, t := range m.tabs {
		w := lipgloss.Width(t.title)
		if i == m.active {
			w += 2 // ▌ ▐
		}
		zones[i] = tabHitZone{start: col, end: col + w}
		col += w + 3 // 3-space gap between tabs
	}
	return zones
}

// tabAt maps a panel-local X column to a tab index. Columns on the leading
// pad, in an inter-tab gap, or past the last tab return (-1, false).
func (m *model) tabAt(x int) (int, bool) {
	for i, z := range m.tabHitZones() {
		if x >= z.start && x < z.end {
			return i, true
		}
	}
	return -1, false
}

// handlePanelClick implements tui.PanelClickMsg for the plugin. Y==0 is the
// tab-strip row; X is mapped to a tab index via tabAt. Y>0 falls in the
// viewport body, which has no per-row click targets (status rows are not
// selectable), so it is a no-op.
func (p *plugin) handlePanelClick(msg tui.PanelClickMsg) tea.Cmd {
	if msg.Panel != panelMain || msg.Y != 0 {
		return nil
	}
	if idx, ok := p.m.tabAt(msg.X); ok {
		p.m.setActiveTab(idx)
	}
	return nil
}

// wheelViewportStep is the number of lines scrolled per mouse-wheel notch,
// mirroring docstui's viewport wheel step.
const wheelViewportStep = 2

// handleWheel implements tui.WheelMsg for the plugin: scrolls the viewport by
// Delta notches. A wheel turn never changes focus — the surface has a single
// panel, so there is nothing else to focus.
func (p *plugin) handleWheel(msg tui.WheelMsg) tea.Cmd {
	if msg.Panel != panelMain {
		return nil
	}
	switch delta := msg.Delta * wheelViewportStep; {
	case delta < 0:
		p.m.viewport.ScrollUp(-delta)
	case delta > 0:
		p.m.viewport.ScrollDown(delta)
	}
	return nil
}
