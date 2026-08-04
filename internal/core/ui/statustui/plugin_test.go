package statustui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// newTestPlugin builds a minimal plugin for unit tests: a model over an empty
// Deps plus a cancelable context, mirroring newTestBrowser in docstui.
func newTestPlugin(t *testing.T) (*plugin, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m := newModel(Deps{}, ctx)
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
	p := newPlugin(newModel(Deps{}, context.Background()), nil)
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
	p.m.loaded = true
	p.m.sectionAnchors = make([][]int, len(tabTitles))

	orig := renderTabFn
	t.Cleanup(func() { renderTabFn = orig })
	renderTabFn = func(_ tabSnapshot, _, _ int) (string, []int) { return "service content", nil }

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
	p.m.loaded = true
	p.m.sectionAnchors = make([][]int, len(tabTitles))

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
	p.m.loaded = true
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
	p.m.loaded = true
	p.m.healthIndicator = "● healthy"
	p.m.reloadAt = time.Now()

	if got := p.StatusContext(); got != "" {
		t.Errorf("StatusContext() with nil Cfg = %q, want empty", got)
	}

	p.m.deps.Cfg = &config.DweConfig{}
	p.m.loaded = false
	if got := p.StatusContext(); got != "" {
		t.Errorf("StatusContext() with no tabs = %q, want empty", got)
	}
}

func TestPlugin_StatusContext_LoadedWithoutTimestamp(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.reloading = false
	p.m.deps.Cfg = &config.DweConfig{}
	p.m.loaded = true
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
	p.m.loading = true

	newSnap := tabSnapshot{daemonErrs: 2}
	cmd := p.Update(tabsLoadedMsg{gen: 5, snap: newSnap, loadedAt: time.Now()})

	if cmd != nil {
		t.Errorf("Update(tabsLoadedMsg) cmd = %v, want nil", cmd)
	}
	if !p.m.loaded {
		t.Errorf("loaded after Update = false, want true")
	}
	if p.m.snap.daemonErrs != newSnap.daemonErrs {
		t.Errorf("snap after Update = %+v, want %+v", p.m.snap, newSnap)
	}
	if len(p.m.sectionAnchors) != len(tabTitles) {
		t.Errorf("sectionAnchors after Update len = %d, want %d", len(p.m.sectionAnchors), len(tabTitles))
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
	p.m.loaded = true
	p.m.snap = tabSnapshot{daemonErrs: 9}

	p.Update(tabsLoadedMsg{gen: 2, snap: tabSnapshot{daemonErrs: 1}, loadedAt: time.Now()})

	if p.m.snap.daemonErrs != 9 {
		t.Errorf("snap after stale Update = %+v, want unchanged (daemonErrs=9)", p.m.snap)
	}
}

func TestPlugin_Update_PreservesYOffsetOnReload_SameTab(t *testing.T) {
	p, _ := newTestPlugin(t)
	longContent := strings.Repeat("line\n", 100)
	p.m.loaded = true
	p.m.sectionAnchors = make([][]int, len(tabTitles))
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

	p.Update(tabsLoadedMsg{gen: savedGen, snap: tabSnapshot{}, loadedAt: time.Now()})

	if got := p.m.viewport.YOffset(); got != 5 {
		t.Errorf("YOffset after same-tab reload = %d, want 5 (restored)", got)
	}
}

func TestPlugin_Update_ResetsYOffsetOnTabSwitch(t *testing.T) {
	p, _ := newTestPlugin(t)
	longContent := strings.Repeat("line\n", 100)
	p.m.loaded = true
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
	p.m.loaded = true
	p.m.sectionAnchors = make([][]int, len(tabTitles))
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
	p.Update(tabsLoadedMsg{gen: savedReloadGen, snap: tabSnapshot{}, loadedAt: time.Now()})

	if p.m.active != 1 {
		t.Errorf("active after stale-reload delivery = %d, want 1 (unchanged)", p.m.active)
	}
	if got := p.m.viewport.YOffset(); got != 0 {
		t.Errorf("YOffset after stale-reload delivery = %d, want 0 (reset, not restored)", got)
	}
}

func TestPlugin_Update_MultipleReloads_DropsOlderResult(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.snap = tabSnapshot{daemonErrs: 0} // stands in for "original"
	p.m.loaded = true
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
	p.Update(tabsLoadedMsg{gen: firstGen, snap: tabSnapshot{daemonErrs: 1}, loadedAt: time.Now()})
	if p.m.snap.daemonErrs != 0 {
		t.Errorf("snap after stale reload = %+v, want unchanged (daemonErrs=0, dropped)", p.m.snap)
	}

	// Current (second) reload's result is applied.
	p.Update(tabsLoadedMsg{gen: secondGen, snap: tabSnapshot{daemonErrs: 2}, loadedAt: time.Now()})
	if p.m.snap.daemonErrs != 2 {
		t.Errorf("snap after current reload = %+v, want daemonErrs=2", p.m.snap)
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

// --- Task 6: mouse — tab clicks & wheel scroll ---

func TestPlugin_PanelClick_TabStripSelectsCorrectTab(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.loaded = true
	p.m.active = 0

	// Zones are measured once while active==0, matching the strip actually
	// on screen before each click below (active is reset to 0 before every
	// click so the rendered positions stay consistent with the zones).
	zones := p.m.tabHitZones()
	if len(zones) != len(tabTitles) {
		t.Fatalf("tabHitZones() len = %d, want %d", len(zones), len(tabTitles))
	}

	for i, z := range zones {
		p.m.active = 0
		mid := (z.start + z.end - 1) / 2
		cmd := p.handlePanelClick(tui.PanelClickMsg{Panel: panelMain, X: mid, Y: 0})
		if cmd != nil {
			t.Errorf("tab %d click: cmd = %v, want nil", i, cmd)
		}
		if p.m.active != i {
			t.Errorf("tab %d click at X=%d: active = %d, want %d", i, mid, p.m.active, i)
		}
	}
}

func TestPlugin_PanelClick_PastLastTabIsNoop(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.loaded = true
	p.m.active = 0

	zones := p.m.tabHitZones()
	last := zones[len(zones)-1]
	p.handlePanelClick(tui.PanelClickMsg{Panel: panelMain, X: last.end + 5, Y: 0})
	if p.m.active != 0 {
		t.Errorf("click past last tab: active = %d, want unchanged 0", p.m.active)
	}
}

func TestPlugin_PanelClick_LeadingPadIsNoop(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.loaded = true
	p.m.active = 1

	p.handlePanelClick(tui.PanelClickMsg{Panel: panelMain, X: 0, Y: 0})
	if p.m.active != 1 {
		t.Errorf("click on leading pad: active = %d, want unchanged 1", p.m.active)
	}
}

func TestPlugin_PanelClick_GapIsNoop(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.loaded = true
	p.m.active = 0

	zones := p.m.tabHitZones()
	gapX := zones[0].end // first column of the gap after tab 0
	p.handlePanelClick(tui.PanelClickMsg{Panel: panelMain, X: gapX, Y: 0})
	if p.m.active != 0 {
		t.Errorf("click in gap: active = %d, want unchanged 0", p.m.active)
	}
}

func TestPlugin_PanelClick_ViewportRowIsNoop(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.loaded = true
	p.m.active = 0

	p.handlePanelClick(tui.PanelClickMsg{Panel: panelMain, X: 5, Y: 1})
	if p.m.active != 0 {
		t.Errorf("click on viewport row: active = %d, want unchanged 0", p.m.active)
	}
}

func TestPlugin_PanelClick_WrongPanelIsNoop(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loaded = true
	p.m.active = 0

	p.handlePanelClick(tui.PanelClickMsg{Panel: tui.PanelID("other"), X: 5, Y: 0})
	if p.m.active != 0 {
		t.Errorf("click on wrong panel: active = %d, want unchanged 0", p.m.active)
	}
}

func TestPlugin_Wheel_ScrollsViewportByDeltaTimesStep(t *testing.T) {
	tests := []struct {
		name  string
		delta int
	}{
		{"down one notch", 1},
		{"down two notches", 2},
		{"up one notch", -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newTestPlugin(t)
			longContent := strings.Repeat("line\n", 100)
			p.m.loading = false
			p.m.viewport.SetContent(longContent)
			p.m.viewport.SetHeight(10)
			p.m.viewport.SetYOffset(20)
			before := p.m.viewport.YOffset()

			cmd := p.handleWheel(tui.WheelMsg{Panel: panelMain, Delta: tc.delta})
			if cmd != nil {
				t.Errorf("handleWheel cmd = %v, want nil", cmd)
			}
			want := before + tc.delta*wheelViewportStep
			if got := p.m.viewport.YOffset(); got != want {
				t.Errorf("YOffset after wheel delta=%d = %d, want %d", tc.delta, got, want)
			}
		})
	}
}

func TestPlugin_Wheel_WrongPanelIsNoop(t *testing.T) {
	p, _ := newTestPlugin(t)
	longContent := strings.Repeat("line\n", 100)
	p.m.loading = false
	p.m.viewport.SetContent(longContent)
	p.m.viewport.SetHeight(10)
	p.m.viewport.SetYOffset(20)

	p.handleWheel(tui.WheelMsg{Panel: tui.PanelID("other"), Delta: 1})
	if got := p.m.viewport.YOffset(); got != 20 {
		t.Errorf("YOffset after wrong-panel wheel = %d, want unchanged 20", got)
	}
}

func TestPlugin_Wheel_NeverChangesActivePanel(t *testing.T) {
	p, _ := newTestPlugin(t)
	longContent := strings.Repeat("line\n", 100)
	p.m.active = 0
	p.m.loading = false
	p.m.viewport.SetContent(longContent)
	p.m.viewport.SetHeight(10)

	p.handleWheel(tui.WheelMsg{Panel: panelMain, Delta: 1})
	if p.m.active != 0 {
		t.Errorf("active after wheel = %d, want unchanged 0", p.m.active)
	}
}

func TestPlugin_Update_RoutesPanelClickMsgToTabStrip(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.loaded = true
	p.m.active = 0

	zones := p.m.tabHitZones()
	mid := (zones[1].start + zones[1].end - 1) / 2
	if cmd := p.Update(tui.PanelClickMsg{Panel: panelMain, X: mid, Y: 0}); cmd != nil {
		t.Errorf("Update(PanelClickMsg) cmd = %v, want nil", cmd)
	}
	if p.m.active != 1 {
		t.Errorf("Update(PanelClickMsg) active = %d, want 1", p.m.active)
	}
}

func TestPlugin_Update_RoutesWheelMsgToViewport(t *testing.T) {
	p, _ := newTestPlugin(t)
	longContent := strings.Repeat("line\n", 100)
	p.m.loading = false
	p.m.viewport.SetContent(longContent)
	p.m.viewport.SetHeight(10)
	p.m.viewport.SetYOffset(0)

	p.Update(tui.WheelMsg{Panel: panelMain, Delta: 1})
	if got := p.m.viewport.YOffset(); got != wheelViewportStep {
		t.Errorf("Update(WheelMsg) YOffset = %d, want %d", got, wheelViewportStep)
	}
}

func TestPlugin_Update_FocusChangedMsgIsNoop(t *testing.T) {
	p, _ := newTestPlugin(t)
	if cmd := p.Update(tui.FocusChangedMsg{Panel: panelMain}); cmd != nil {
		t.Errorf("Update(FocusChangedMsg) cmd = %v, want nil", cmd)
	}
}

// --- Task 11: renderActiveTab memoisation on (loadGen, active, width) ---

// TestPlugin_RenderActiveTab_MemoisationContract puts a call-count spy on
// renderTabFn and drives it through the four invalidation triggers the plan
// calls out directly, rather than inferring memoisation from inspecting
// cache fields: two consecutive identical View() calls render once; a width
// change, a tab switch, and a reload each force exactly one more render.
func TestPlugin_RenderActiveTab_MemoisationContract(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.loaded = true
	p.m.sectionAnchors = make([][]int, len(tabTitles))

	calls := 0
	orig := renderTabFn
	t.Cleanup(func() { renderTabFn = orig })
	renderTabFn = func(_ tabSnapshot, _, _ int) (string, []int) {
		calls++
		return "body", nil
	}

	p.ViewPanel(panelMain, tui.Region{Width: 80, Height: 24})
	p.ViewPanel(panelMain, tui.Region{Width: 80, Height: 24})
	if calls != 1 {
		t.Fatalf("renderTabFn calls after two identical View() calls = %d, want 1", calls)
	}

	p.ViewPanel(panelMain, tui.Region{Width: 90, Height: 24})
	if calls != 2 {
		t.Fatalf("renderTabFn calls after width change = %d, want 2", calls)
	}
	p.ViewPanel(panelMain, tui.Region{Width: 90, Height: 24})
	if calls != 2 {
		t.Fatalf("renderTabFn calls after a repeated call at the new width = %d, want still 2", calls)
	}

	p.m.setActiveTab(1)
	p.ViewPanel(panelMain, tui.Region{Width: 90, Height: 24})
	if calls != 3 {
		t.Fatalf("renderTabFn calls after tab switch = %d, want 3", calls)
	}

	cmd, handled := p.HandleAction(tui.ActionReload)
	if !handled || cmd == nil {
		t.Fatalf("HandleAction(ActionReload) = (%v, %v), want (non-nil, true)", cmd, handled)
	}
	p.ViewPanel(panelMain, tui.Region{Width: 90, Height: 24})
	if calls != 4 {
		t.Fatalf("renderTabFn calls after reload = %d, want 4", calls)
	}
}

// TestPlugin_RenderActiveTab_ReloadedSnapshotReachesTheScreen covers the step
// the memoisation contract test above stops one short of: delivering the
// tabsLoadedMsg that the reload produced, and re-rendering.
//
// The (loadGen, active, width) key alone is NOT enough here. loadGen is
// bumped when the reload *starts*, and the spinner keeps ticking during the
// load, so at least one frame renders the pre-reload snapshot and caches it
// under the already-bumped gen. Without an explicit invalidation on
// tabsLoadedMsg the reloaded body would never reach the screen until a tab
// switch or a resize.
func TestPlugin_RenderActiveTab_ReloadedSnapshotReachesTheScreen(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.m.loading = false
	p.m.loaded = true
	p.m.sectionAnchors = make([][]int, len(tabTitles))

	// Render from whatever the snapshot's git rows say, so the body is a pure
	// function of the snapshot rather than a constant.
	orig := renderTabFn
	t.Cleanup(func() { renderTabFn = orig })
	renderTabFn = func(snap tabSnapshot, _, _ int) (string, []int) {
		if len(snap.gitRows) == 0 {
			return "EMPTY", nil
		}
		return snap.gitRows[0].Service, nil
	}

	p.m.snap = tabSnapshot{gitRows: []statusview.GitWorkspaceRow{{Service: "SNAPSHOT-A"}}}
	region := tui.Region{Width: 90, Height: 24}
	if got := p.ViewPanel(panelMain, region); !strings.Contains(got, "SNAPSHOT-A") {
		t.Fatalf("initial ViewPanel = %q, want it to contain SNAPSHOT-A", got)
	}

	cmd, handled := p.HandleAction(tui.ActionReload)
	if !handled || cmd == nil {
		t.Fatalf("HandleAction(ActionReload) = (%v, %v), want (non-nil, true)", cmd, handled)
	}
	// The in-flight frame: still snapshot A, but now cached under the bumped
	// loadGen. This is what a spinner tick renders mid-reload.
	if got := p.ViewPanel(panelMain, region); !strings.Contains(got, "SNAPSHOT-A") {
		t.Fatalf("in-flight ViewPanel = %q, want it to still contain SNAPSHOT-A", got)
	}

	p.Update(tabsLoadedMsg{
		gen:      p.m.loadGen,
		snap:     tabSnapshot{gitRows: []statusview.GitWorkspaceRow{{Service: "SNAPSHOT-B"}}},
		loadedAt: time.Now(),
	})

	got := p.ViewPanel(panelMain, region)
	if !strings.Contains(got, "SNAPSHOT-B") {
		t.Errorf("ViewPanel after the reload landed = %q, want it to contain SNAPSHOT-B (stale render cache)", got)
	}
}

func TestPlugin_Update_UnmatchedKeyDelegatesToViewportScroll(t *testing.T) {
	p, _ := newTestPlugin(t)
	longContent := strings.Repeat("line\n", 100)
	p.m.loading = false
	p.m.viewport.SetContent(longContent)
	p.m.viewport.SetHeight(10)
	p.m.viewport.SetYOffset(0)

	p.Update(tea.KeyPressMsg{Code: tea.KeyDown, Text: ""})

	if got := p.m.viewport.YOffset(); got != 1 {
		t.Errorf("YOffset after unmatched down key = %d, want 1 (delegated to viewport)", got)
	}
}
