package statustui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// Status-dashboard-custom action identifiers registered by the plugin. IDs are
// dot-separated so the derived help key (tui.help.action.<id>) reads
// naturally; see the i18n coverage keys added alongside this file.
const (
	actionSectionPrev tui.Action = "section.prev"
	actionSectionNext tui.Action = "section.next"
	actionTabPrev     tui.Action = "tab.prev"
	actionTabNext     tui.Action = "tab.next"
	actionTab1        tui.Action = "tab.1"
	actionTab2        tui.Action = "tab.2"
	actionTab3        tui.Action = "tab.3"
	actionTab4        tui.Action = "tab.4"
	actionTab5        tui.Action = "tab.5"
)

// Help-modal section labels. sectionNav groups the within-tab table jumps
// (tab / shift+tab); sectionTabs groups the tab-switch actions (←/→, 1–5).
const (
	sectionNav  = "Navigation"
	sectionTabs = "Tabs"
)

// Actions implements tui.Plugin. It registers reload (stdlib), the tab switch on
// tab / shift+tab (freed because the Frame strips its focus built-ins on this
// single-panel surface — see tui.Registry.DisableFocusNav), the within-tab
// table-jump on ] / [, and the plugin's own Tabs section. Reload moves to ctrl+r
// (stdlib ActionReload), replacing the legacy "r" binding.
func (p *plugin) Actions(reg *tui.Registry) error {
	if err := tui.RegisterStandard(reg, tui.ActionReload); err != nil {
		return err
	}

	for _, spec := range []struct {
		a tui.Action
		b tui.Binding
	}{
		{actionSectionNext, tui.Binding{Keys: []string{"]"}, Desc: "Next table", Section: sectionNav}},
		{actionSectionPrev, tui.Binding{Keys: []string{"["}, Desc: "Previous table", Section: sectionNav}},
		{actionTabPrev, tui.Binding{Keys: []string{"shift+tab", "left", "h"}, Desc: "Previous tab", Section: sectionTabs}},
		{actionTabNext, tui.Binding{Keys: []string{"tab", "right", "l"}, Desc: "Next tab", Section: sectionTabs}},
		{actionTab1, tui.Binding{Keys: []string{"1"}, Desc: "Services", Section: sectionTabs}},
		{actionTab2, tui.Binding{Keys: []string{"2"}, Desc: "Deploy", Section: sectionTabs}},
		{actionTab3, tui.Binding{Keys: []string{"3"}, Desc: "Topology", Section: sectionTabs}},
		{actionTab4, tui.Binding{Keys: []string{"4"}, Desc: "Git", Section: sectionTabs}},
		{actionTab5, tui.Binding{Keys: []string{"5"}, Desc: "Daemons", Section: sectionTabs}},
	} {
		if err := reg.Register(spec.a, spec.b); err != nil {
			return err
		}
	}
	return nil
}

// HandleAction implements tui.Plugin. Tab-switch actions are no-ops until the
// first load completes (mirrors the legacy guard against navigating before
// m.tabs is populated — also avoids a modulo-by-zero on prev/next). Reload
// preserves the existing loadGen/reloadGen/reloadActive/reloadYOffset state
// machine verbatim.
func (p *plugin) HandleAction(a tui.Action) (tea.Cmd, bool) {
	m := p.m
	switch a {
	case actionSectionNext:
		m.jumpSection(+1)
		return nil, true
	case actionSectionPrev:
		m.jumpSection(-1)
		return nil, true
	case actionTabPrev:
		if len(m.tabs) == 0 {
			return nil, true
		}
		m.setActiveTab((m.active - 1 + len(m.tabs)) % len(m.tabs))
		return nil, true
	case actionTabNext:
		if len(m.tabs) == 0 {
			return nil, true
		}
		m.setActiveTab((m.active + 1) % len(m.tabs))
		return nil, true
	case actionTab1:
		m.setActiveTab(0)
		return nil, true
	case actionTab2:
		m.setActiveTab(1)
		return nil, true
	case actionTab3:
		m.setActiveTab(2)
		return nil, true
	case actionTab4:
		m.setActiveTab(3)
		return nil, true
	case actionTab5:
		m.setActiveTab(4)
		return nil, true
	case tui.ActionReload:
		if len(m.tabs) == 0 {
			return nil, true
		}
		m.loadGen++
		m.reloadActive = m.active
		m.reloadYOffset = m.viewport.YOffset()
		m.reloadGen = m.loadGen
		m.reloading = true
		return buildTabsCmd(m.ctx, m.deps, m.loadGen), true
	default:
		return nil, false
	}
}
