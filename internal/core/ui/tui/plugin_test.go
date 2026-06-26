package tui

import (
	"errors"
	"testing"
)

// compile-time proof the stub satisfies the contract.
var _ Plugin = (*stubPlugin)(nil)

func TestStubPlugin_SatisfiesContract(t *testing.T) {
	// The var _ Plugin assertion above is the real proof; this test documents it
	// and exercises a couple of trivial accessors so the stub stays buildable.
	p := newStubPlugin()
	if got := p.StatusContext(); got != "stub: ready" {
		t.Fatalf("StatusContext() = %q, want %q", got, "stub: ready")
	}
	res, ok := p.Result().(stubResult)
	if !ok {
		t.Fatalf("Result() type = %T, want stubResult", p.Result())
	}
	if res.Selected != "none" {
		t.Fatalf("Result().Selected = %q, want %q", res.Selected, "none")
	}
}

func TestStubPlugin_LifecycleOrdering(t *testing.T) {
	p := newStubPlugin()

	if cmd := p.Init(); cmd != nil {
		t.Fatalf("Init() cmd = %v, want nil", cmd)
	}
	p.Update(stubMsg{payload: "a"})
	p.Update(stubMsg{payload: "b"})
	if err := p.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	want := []string{"init", "update", "update", "close"}
	if len(p.callOrder) != len(want) {
		t.Fatalf("callOrder = %v, want %v", p.callOrder, want)
	}
	for i := range want {
		if p.callOrder[i] != want[i] {
			t.Fatalf("callOrder[%d] = %q, want %q (full %v)", i, p.callOrder[i], want[i], p.callOrder)
		}
	}
	if !p.initCalled || !p.closeCalled {
		t.Fatalf("initCalled=%v closeCalled=%v, both want true", p.initCalled, p.closeCalled)
	}
}

func TestStubPlugin_CloseSurfacesError(t *testing.T) {
	p := newStubPlugin()
	sentinel := errors.New("boom")
	p.closeErr = sentinel
	if err := p.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("Close() = %v, want %v", err, sentinel)
	}
}

func TestStubPlugin_RecordsUpdateMessages(t *testing.T) {
	p := newStubPlugin()
	p.Update(stubMsg{payload: "x"})
	p.Update(stubMsg{payload: "y"})
	if len(p.gotMsgs) != 2 {
		t.Fatalf("gotMsgs len = %d, want 2", len(p.gotMsgs))
	}
	m, ok := p.gotMsgs[0].(stubMsg)
	if !ok || m.payload != "x" {
		t.Fatalf("gotMsgs[0] = %#v, want stubMsg{x}", p.gotMsgs[0])
	}
}

func TestStubPlugin_ActionsPopulateRegistry(t *testing.T) {
	p := newStubPlugin()
	reg := NewRegistry()
	if err := p.Actions(reg); err != nil {
		t.Fatalf("Actions() = %v, want nil", err)
	}

	// Both stub keys must resolve to the right actions through Match.
	if a, ok := reg.Match("o"); !ok || a != stubActionOpen {
		t.Fatalf("Match(o) = (%q, %v), want (%q, true)", a, ok, stubActionOpen)
	}
	if a, ok := reg.Match("r"); !ok || a != stubActionReload {
		t.Fatalf("Match(r) = (%q, %v), want (%q, true)", a, ok, stubActionReload)
	}
}

func TestStubPlugin_MatchedActionRoutesThroughHandleAction(t *testing.T) {
	p := newStubPlugin()
	reg := NewRegistry()
	if err := p.Actions(reg); err != nil {
		t.Fatalf("Actions() = %v", err)
	}

	a, ok := reg.Match("o")
	if !ok {
		t.Fatalf("Match(o) not found")
	}
	cmd, handled := p.HandleAction(a)
	if !handled {
		t.Fatalf("HandleAction(%q) handled = false, want true", a)
	}
	if cmd == nil {
		t.Fatalf("HandleAction(%q) cmd = nil, want non-nil sentinel", a)
	}
	if p.handledAction != stubActionOpen {
		t.Fatalf("handledAction = %q, want %q", p.handledAction, stubActionOpen)
	}
}

func TestStubPlugin_UnhandledActionReportsFalse(t *testing.T) {
	p := newStubPlugin()
	if cmd, handled := p.HandleAction("nope.unknown"); handled || cmd != nil {
		t.Fatalf("HandleAction(unknown) = (%v, %v), want (nil, false)", cmd, handled)
	}
}

func TestStubPlugin_PendingOverlayDrains(t *testing.T) {
	p := newStubPlugin()
	if _, ok := p.PendingOverlay(); ok {
		t.Fatalf("PendingOverlay() ok = true on fresh plugin, want false")
	}
	p.pending = &Overlay{Content: "hi", Width: 2, Height: 1}
	ov, ok := p.PendingOverlay()
	if !ok || ov.Content != "hi" {
		t.Fatalf("PendingOverlay() = (%#v, %v), want ({hi 2 1}, true)", ov, ok)
	}
	// Draining is one-shot.
	if _, ok := p.PendingOverlay(); ok {
		t.Fatalf("PendingOverlay() ok = true after drain, want false")
	}
}

func TestStubPlugin_PanelsDeclaredWeighted(t *testing.T) {
	p := newStubPlugin()
	panels := p.Panels()
	if len(panels) != 2 {
		t.Fatalf("Panels() len = %d, want 2", len(panels))
	}
	if panels[0].ID != stubPanelLeft || panels[1].ID != stubPanelRight {
		t.Fatalf("panel ids = %q,%q, want %q,%q", panels[0].ID, panels[1].ID, stubPanelLeft, stubPanelRight)
	}
	if panels[0].Weight != 1 || panels[1].Weight != 2 {
		t.Fatalf("weights = %d,%d, want 1,2", panels[0].Weight, panels[1].Weight)
	}
}

func TestStubPlugin_ViewPanelFillsInner(t *testing.T) {
	p := newStubPlugin()
	got := p.ViewPanel(stubPanelLeft, Region{Width: 10, Height: 4})
	want := "[left 10x4]"
	if got != want {
		t.Fatalf("ViewPanel = %q, want %q", got, want)
	}
}

func TestStubPlugin_ResizeRecordsBody(t *testing.T) {
	p := newStubPlugin()
	body := Region{X: 2, Y: 1, Width: 40, Height: 12}
	p.Resize(body)
	if p.lastResize != body {
		t.Fatalf("lastResize = %#v, want %#v", p.lastResize, body)
	}
}
