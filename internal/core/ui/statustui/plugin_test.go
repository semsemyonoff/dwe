package statustui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// newTestPlugin builds a minimal plugin for unit tests: a model over an empty
// Deps plus a cancelable context, mirroring newTestBrowser in docstui.
func newTestPlugin(t *testing.T) (*plugin, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m := newModel(Deps{}, ctx, 80, 24)
	return newPlugin(m, cancel), ctx
}

func TestPlugin_SatisfiesPluginInterface(t *testing.T) {
	var _ tui.Plugin = (*plugin)(nil)
}

func TestPlugin_PanelsShapeAndWeight(t *testing.T) {
	p, _ := newTestPlugin(t)
	panels := p.Panels()
	if len(panels) != 1 {
		t.Fatalf("Panels() len=%d, want 1", len(panels))
	}
	if panels[0].ID != panelMain {
		t.Errorf("panels[0].ID = %q, want %q", panels[0].ID, panelMain)
	}
	if panels[0].Weight != 1 {
		t.Errorf("panels[0].Weight = %d, want 1", panels[0].Weight)
	}
}

func TestPlugin_ResultIsNil(t *testing.T) {
	p, _ := newTestPlugin(t)
	if got := p.Result(); got != nil {
		t.Errorf("Result() = %v, want nil", got)
	}
}

func TestPlugin_PendingOverlayNone(t *testing.T) {
	p, _ := newTestPlugin(t)
	overlay, ok := p.PendingOverlay()
	if ok {
		t.Errorf("PendingOverlay() ok = true, want false")
	}
	if overlay != (tui.Overlay{}) {
		t.Errorf("PendingOverlay() overlay = %+v, want zero value", overlay)
	}
}

func TestPlugin_CapturingInputFalse(t *testing.T) {
	p, _ := newTestPlugin(t)
	if p.CapturingInput() {
		t.Errorf("CapturingInput() = true, want false")
	}
}

func TestPlugin_CloseCancelsContext(t *testing.T) {
	p, ctx := newTestPlugin(t)
	if err := ctx.Err(); err != nil {
		t.Fatalf("ctx.Err() before Close() = %v, want nil", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if err := ctx.Err(); err != context.Canceled {
		t.Errorf("ctx.Err() after Close() = %v, want context.Canceled", err)
	}
}

func TestPlugin_CloseNilCancelIsNoop(t *testing.T) {
	p := newPlugin(newModel(Deps{}, context.Background(), 80, 24), nil)
	if err := p.Close(); err != nil {
		t.Errorf("Close() with nil cancel = %v, want nil", err)
	}
}

func TestPlugin_InitReturnsCmd(t *testing.T) {
	p, _ := newTestPlugin(t)
	if p.m.loadGen != 0 {
		t.Fatalf("loadGen before Init() = %d, want 0", p.m.loadGen)
	}
	cmd := p.Init()
	if cmd == nil {
		t.Fatalf("Init() cmd = nil, want non-nil")
	}
	if p.m.loadGen != 1 {
		t.Errorf("loadGen after Init() = %d, want 1", p.m.loadGen)
	}
}

func TestPlugin_StubMethodsZeroValues(t *testing.T) {
	p, _ := newTestPlugin(t)

	if got := p.Update(nil); got != nil {
		t.Errorf("Update() = %v, want nil", got)
	}

	// Resize is void; just confirm it does not panic.
	p.Resize(tui.Region{Width: 80, Height: 24})
}

func TestPlugin_HandleAction_UnknownIsUnhandled(t *testing.T) {
	p, _ := newTestPlugin(t)
	cmd, handled := p.HandleAction(tui.Action("unused"))
	if cmd != nil || handled {
		t.Errorf("HandleAction(unused) = (%v, %v), want (nil, false)", cmd, handled)
	}
}

func TestPlugin_ViewPanel_UnknownPanelIsEmpty(t *testing.T) {
	p, _ := newTestPlugin(t)
	if got := p.ViewPanel(tui.PanelID("other"), tui.Region{Width: 80, Height: 24}); got != "" {
		t.Errorf("ViewPanel(unknown) = %q, want empty", got)
	}
}

func TestPlugin_ViewPanel_LoadingShowsSpinnerNoTabStrip(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = true

	got := p.ViewPanel(panelMain, tui.Region{Width: 80, Height: 24})
	if got == "" {
		t.Fatalf("ViewPanel(loading) = empty, want spinner content")
	}
	if strings.Contains(got, "Services") {
		t.Errorf("ViewPanel(loading) = %q, must not contain the tab strip", got)
	}
}

func TestPlugin_ViewPanel_NormalShowsTabStripAndViewport(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.tabs = []tab{
		{"Services", "service content"},
		{"Deploy", "deploy content"},
	}
	p.m.viewport.SetContent(p.m.tabs[0].content)

	got := p.ViewPanel(panelMain, tui.Region{Width: 80, Height: 24})
	if !strings.Contains(got, "Services") || !strings.Contains(got, "Deploy") {
		t.Errorf("ViewPanel(normal) = %q, want both tab titles in the strip", got)
	}
	if !strings.Contains(got, "service content") {
		t.Errorf("ViewPanel(normal) = %q, want active tab content in the viewport", got)
	}
}

func TestPlugin_ViewPanel_SizesViewportToInnerMinusChrome(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.tabs = []tab{{"Services", "content"}}
	p.m.viewport.SetContent(p.m.tabs[0].content)

	p.ViewPanel(panelMain, tui.Region{Width: 40, Height: 10})

	if got := p.m.viewport.Width(); got != 40 {
		t.Errorf("viewport width = %d, want 40", got)
	}
	if got := p.m.viewport.Height(); got != 10-panelChromeRows {
		t.Errorf("viewport height = %d, want %d", got, 10-panelChromeRows)
	}
}

func TestPlugin_StatusContext_Loading(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = true

	got := p.StatusContext()
	if !strings.Contains(got, "loading…") {
		t.Errorf("StatusContext() = %q, want to contain %q", got, "loading…")
	}
}

func TestPlugin_StatusContext_Reloading(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.reloading = true

	got := p.StatusContext()
	if !strings.Contains(got, "reloading…") {
		t.Errorf("StatusContext() = %q, want to contain %q", got, "reloading…")
	}
}

func TestPlugin_StatusContext_LoadedWithTimestamp(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.reloading = false
	p.m.deps.Cfg = &config.DweConfig{}
	p.m.tabs = []tab{{"Services", "content"}}
	p.m.healthIndicator = "● healthy"
	p.m.reloadAt = time.Now().Add(-5 * time.Second)

	got := p.StatusContext()
	if !strings.Contains(got, "● healthy") {
		t.Errorf("StatusContext() = %q, want to contain health indicator", got)
	}
	if !strings.Contains(got, "loaded") || !strings.Contains(got, "ago") {
		t.Errorf("StatusContext() = %q, want to contain %q", got, "loaded ... ago")
	}
}

func TestPlugin_StatusContext_EmptyWhenNilCfgOrNoTabs(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.reloading = false
	p.m.deps.Cfg = nil
	p.m.tabs = []tab{{"Services", "content"}}
	p.m.healthIndicator = "● healthy"
	p.m.reloadAt = time.Now()

	if got := p.StatusContext(); got != "" {
		t.Errorf("StatusContext() with nil Cfg = %q, want empty", got)
	}

	p.m.deps.Cfg = &config.DweConfig{}
	p.m.tabs = nil
	if got := p.StatusContext(); got != "" {
		t.Errorf("StatusContext() with no tabs = %q, want empty", got)
	}
}

func TestPlugin_StatusContext_LoadedWithoutTimestamp(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.reloading = false
	p.m.deps.Cfg = &config.DweConfig{}
	p.m.tabs = []tab{{"Services", "content"}}
	p.m.healthIndicator = "● healthy"
	p.m.reloadAt = time.Time{}

	got := p.StatusContext()
	if !strings.Contains(got, "● healthy") {
		t.Errorf("StatusContext() = %q, want to contain health indicator", got)
	}
	if strings.Contains(got, "loaded") {
		t.Errorf("StatusContext() = %q, want no 'loaded' text when reloadAt is zero", got)
	}
}

// --- Task 5: reload + YOffset preservation through Plugin.Update ---

func TestPlugin_Update_CurrentTabsLoadedMsgApplied(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loadGen = 5
	p.m.tabs = []tab{{"Old", "old content"}}
	p.m.loading = true

	newTabs := []tab{
		{"Services", "services content"},
		{"Deploy", "deploy content"},
	}
	cmd := p.Update(tabsLoadedMsg{gen: 5, tabs: newTabs, loadedAt: time.Now()})

	if cmd != nil {
		t.Errorf("Update(tabsLoadedMsg) cmd = %v, want nil", cmd)
	}
	if len(p.m.tabs) != 2 || p.m.tabs[0].title != "Services" || p.m.tabs[1].title != "Deploy" {
		t.Errorf("tabs after Update = %+v, want Services/Deploy", p.m.tabs)
	}
	if p.m.loading {
		t.Errorf("loading after Update = true, want false")
	}
	if p.m.reloading {
		t.Errorf("reloading after Update = true, want false")
	}
}

func TestPlugin_Update_StaleTabsLoadedMsgIgnored(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loadGen = 5
	p.m.tabs = []tab{{"Old", "old content"}}

	p.Update(tabsLoadedMsg{gen: 2, tabs: []tab{{"New", "new content"}}, loadedAt: time.Now()})

	if len(p.m.tabs) != 1 || p.m.tabs[0].title != "Old" {
		t.Errorf("tabs after stale Update = %+v, want unchanged [Old]", p.m.tabs)
	}
}

func TestPlugin_Update_PreservesYOffsetOnReload_SameTab(t *testing.T) {
	p, _ := newTestPlugin(t)
	longContent := strings.Repeat("line\n", 100)
	p.m.tabs = []tab{
		{"Services", longContent},
		{"Deploy", "deploy content"},
	}
	p.m.active = 0
	p.m.loading = false
	p.m.loadGen = 1
	p.m.viewport.SetContent(longContent)
	p.m.viewport.SetHeight(10)
	p.m.viewport.SetYOffset(5)

	// Trigger a reload via HandleAction — captures active=0, yOffset=5,
	// reloadGen=loadGen (same machinery the mouse/keyboard path drives).
	cmd, handled := p.HandleAction(tui.ActionReload)
	if !handled || cmd == nil {
		t.Fatalf("HandleAction(ActionReload) = (%v, %v), want (non-nil, true)", cmd, handled)
	}
	savedGen := p.m.loadGen

	p.Update(tabsLoadedMsg{gen: savedGen, tabs: p.m.tabs, loadedAt: time.Now()})

	if got := p.m.viewport.YOffset(); got != 5 {
		t.Errorf("YOffset after same-tab reload = %d, want 5 (restored)", got)
	}
}

func TestPlugin_Update_ResetsYOffsetOnTabSwitch(t *testing.T) {
	p, _ := newTestPlugin(t)
	longContent := strings.Repeat("line\n", 100)
	p.m.tabs = []tab{
		{"Services", longContent},
		{"Deploy", "deploy content"},
	}
	p.m.active = 0
	p.m.loading = false
	p.m.loadGen = 1
	p.m.viewport.SetContent(longContent)
	p.m.viewport.SetHeight(10)
	p.m.viewport.SetYOffset(10)

	p.m.setActiveTab(1)

	if p.m.active != 1 {
		t.Fatalf("active after setActiveTab(1) = %d, want 1", p.m.active)
	}
	if got := p.m.viewport.YOffset(); got != 0 {
		t.Errorf("YOffset after tab switch = %d, want 0 (reset to top)", got)
	}
}

func TestPlugin_Update_TabSwitchInvalidatesPendingReloadRestore(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.tabs = []tab{
		{"Services", "services content"},
		{"Deploy", "deploy content"},
	}
	p.m.active = 0
	p.m.loadGen = 1
	p.m.loading = false

	cmd, handled := p.HandleAction(tui.ActionReload)
	if !handled || cmd == nil {
		t.Fatalf("HandleAction(ActionReload) = (%v, %v), want (non-nil, true)", cmd, handled)
	}
	savedReloadGen := p.m.reloadGen
	if savedReloadGen == 0 {
		t.Fatalf("reloadGen after reload = 0, want non-zero")
	}

	// Switching tabs clears reloadGen (setActiveTab, verbatim).
	p.m.setActiveTab(1)
	if p.m.reloadGen != 0 {
		t.Errorf("reloadGen after tab switch = %d, want 0", p.m.reloadGen)
	}

	// The old reload's result now arrives. Its gen matches m.loadGen (both
	// bumped by the same reload), so it is NOT dropped as stale — but since
	// reloadGen was cleared by the tab switch, the offset restore condition
	// is false and GotoTop() runs instead.
	p.Update(tabsLoadedMsg{gen: savedReloadGen, tabs: p.m.tabs, loadedAt: time.Now()})

	if p.m.active != 1 {
		t.Errorf("active after stale-reload delivery = %d, want 1 (unchanged)", p.m.active)
	}
	if got := p.m.viewport.YOffset(); got != 0 {
		t.Errorf("YOffset after stale-reload delivery = %d, want 0 (reset, not restored)", got)
	}
}

func TestPlugin_Update_MultipleReloads_DropsOlderResult(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.tabs = []tab{{"Services", "original"}}
	p.m.active = 0
	p.m.loading = false
	p.m.loadGen = 1

	if _, handled := p.HandleAction(tui.ActionReload); !handled {
		t.Fatalf("first HandleAction(ActionReload) not handled")
	}
	firstGen := p.m.loadGen

	if _, handled := p.HandleAction(tui.ActionReload); !handled {
		t.Fatalf("second HandleAction(ActionReload) not handled")
	}
	secondGen := p.m.loadGen

	if secondGen <= firstGen {
		t.Fatalf("secondGen=%d, want > firstGen=%d", secondGen, firstGen)
	}

	// Stale (first) reload's result is dropped.
	p.Update(tabsLoadedMsg{gen: firstGen, tabs: []tab{{"Services", "from first reload"}}, loadedAt: time.Now()})
	if p.m.tabs[0].content != "original" {
		t.Errorf("content after stale reload = %q, want %q (dropped)", p.m.tabs[0].content, "original")
	}

	// Current (second) reload's result is applied.
	p.Update(tabsLoadedMsg{gen: secondGen, tabs: []tab{{"Services", "from second reload"}}, loadedAt: time.Now()})
	if p.m.tabs[0].content != "from second reload" {
		t.Errorf("content after current reload = %q, want %q", p.m.tabs[0].content, "from second reload")
	}
}

func TestPlugin_Update_SpinnerTickAdvances(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = true

	cmd := p.Update(spinner.TickMsg{ID: p.m.spinner.ID()})
	if cmd == nil {
		t.Errorf("Update(spinner.TickMsg) cmd = nil, want a continuation command")
	}
}

func TestPlugin_Update_WindowSizeMsgDoesNotSizeViewport(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.viewport.SetWidth(80)
	p.m.viewport.SetHeight(24)

	cmd := p.Update(tea.WindowSizeMsg{Width: 200, Height: 60})

	if cmd != nil {
		t.Errorf("Update(tea.WindowSizeMsg) cmd = %v, want nil", cmd)
	}
	// Sizing is owned by Resize/ViewPanel, not Update — confirm Update left
	// the viewport dimensions untouched.
	if got := p.m.viewport.Width(); got != 80 {
		t.Errorf("viewport width after WindowSizeMsg = %d, want unchanged 80", got)
	}
	if got := p.m.viewport.Height(); got != 24 {
		t.Errorf("viewport height after WindowSizeMsg = %d, want unchanged 24", got)
	}
}

func TestPlugin_Update_UnmatchedKeyDelegatesToViewportScroll(t *testing.T) {
	p, _ := newTestPlugin(t)
	longContent := strings.Repeat("line\n", 100)
	p.m.tabs = []tab{{"Services", longContent}}
	p.m.loading = false
	p.m.viewport.SetContent(longContent)
	p.m.viewport.SetHeight(10)
	p.m.viewport.SetYOffset(0)

	p.Update(tea.KeyPressMsg{Code: tea.KeyDown, Text: ""})

	if got := p.m.viewport.YOffset(); got != 1 {
		t.Errorf("YOffset after unmatched down key = %d, want 1 (delegated to viewport)", got)
	}
}
