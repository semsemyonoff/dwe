package statustui

import (
	"context"
	"strings"
	"testing"
	"time"

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
	if err := p.Actions(nil); err != nil {
		t.Errorf("Actions() = %v, want nil", err)
	}
	cmd, handled := p.HandleAction(tui.Action("unused"))
	if cmd != nil || handled {
		t.Errorf("HandleAction() = (%v, %v), want (nil, false)", cmd, handled)
	}

	// Resize is void; just confirm it does not panic.
	p.Resize(tui.Region{Width: 80, Height: 24})
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
